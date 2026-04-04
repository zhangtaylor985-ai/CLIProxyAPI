package sessiontrajectory

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
)

var compositeSessionPattern = regexp.MustCompile(`(^|[^A-Za-z0-9])session[_:-]([A-Za-z0-9][A-Za-z0-9._-]*)([^A-Za-z0-9._-]|$)`)

type usageSummary struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
	CachedTokens    int64 `json:"cached_tokens"`
	TotalTokens     int64 `json:"total_tokens"`
}

type normalizedConversation struct {
	UserID               string          `json:"user_id"`
	Source               string          `json:"source"`
	CallType             string          `json:"call_type"`
	Provider             string          `json:"provider"`
	Model                string          `json:"model"`
	CanonicalModelFamily string          `json:"canonical_model_family"`
	UserAgent            string          `json:"user_agent"`
	ProviderSessionID    string          `json:"provider_session_id,omitempty"`
	ProviderRequestID    string          `json:"provider_request_id,omitempty"`
	UpstreamLogID        string          `json:"upstream_log_id,omitempty"`
	System               json.RawMessage `json:"system,omitempty"`
	Tools                json.RawMessage `json:"tools,omitempty"`
	Messages             json.RawMessage `json:"messages,omitempty"`
	SystemHash           string          `json:"system_hash"`
	ToolsHash            string          `json:"tools_hash"`
	MessagesHash         string          `json:"messages_hash"`
	MessageCount         int             `json:"message_count"`
	Usage                usageSummary    `json:"usage"`
}

func normalizeCompletedRequest(record *CompletedRequest) (*normalizedConversation, []byte, []byte, []byte, error) {
	if record == nil {
		return nil, nil, nil, nil, nil
	}
	requestJSON := pickPrimaryJSON(record.RequestBody, record.APIRequestBody)
	if len(requestJSON) == 0 {
		return nil, requestJSON, nil, nil, nil
	}

	callType, provider := deriveCallTypeAndProvider(record.RequestURL)
	if callType == "unknown" {
		return nil, requestJSON, nil, nil, nil
	}
	responseJSON := normalizeResponsePayload(callType, record.ResponseBody, record.APIResponseBody)

	requestRoot := gjson.ParseBytes(requestJSON)
	responseRoot := gjson.ParseBytes(responseJSON)

	system := normalizeSystem(callType, requestRoot)
	tools := normalizeTools(callType, requestRoot)
	messages := normalizeMessages(callType, requestRoot)

	result := &normalizedConversation{
		Source:               deriveSource(record.RequestHeaders, record.RequestURL),
		CallType:             callType,
		Provider:             provider,
		Model:                strings.TrimSpace(firstNonEmptyJSON(requestRoot, responseRoot, "model", "response.model", "message.model")),
		CanonicalModelFamily: canonicalModelFamily(strings.TrimSpace(firstNonEmptyJSON(requestRoot, responseRoot, "model", "response.model", "message.model"))),
		UserAgent:            strings.TrimSpace(firstHeader(record.RequestHeaders, "User-Agent")),
		ProviderSessionID:    extractProviderSessionID(requestRoot, responseRoot),
		ProviderRequestID:    strings.TrimSpace(firstNonEmptyJSON(responseRoot, requestRoot, "id", "request_id", "response.id", "message.id")),
		UpstreamLogID:        strings.TrimSpace(firstNonEmptyHeader(record.ResponseHeaders, "X-Request-Id", "Request-Id", "Openai-Request-Id")),
		System:               cloneJSON(system),
		Tools:                cloneJSON(tools),
		Messages:             cloneJSON(messages),
		SystemHash:           hashBytes(system),
		ToolsHash:            hashBytes(tools),
		MessagesHash:         hashBytes(messages),
		MessageCount:         countJSONArray(messages),
		Usage:                extractUsage(responseRoot),
	}
	if result.Model == "" {
		result.Model = "unknown"
	}
	if result.CanonicalModelFamily == "" {
		result.CanonicalModelFamily = canonicalModelFamily(result.Provider)
	}

	normalizedJSON, err := json.Marshal(result)
	if err != nil {
		return nil, requestJSON, responseJSON, nil, err
	}
	return result, requestJSON, responseJSON, normalizedJSON, nil
}

func pickPrimaryJSON(values ...[]byte) []byte {
	for _, value := range values {
		if compacted := compactJSON(value); len(compacted) > 0 {
			return compacted
		}
	}
	return nil
}

func normalizeResponsePayload(callType string, values ...[]byte) []byte {
	for _, value := range values {
		if compacted := compactJSON(value); len(compacted) > 0 {
			return compacted
		}
		if compacted := compactStreamResponse(callType, value); len(compacted) > 0 {
			return compacted
		}
	}
	return nil
}

func compactJSON(value []byte) []byte {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || !gjson.ValidBytes(trimmed) {
		return nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, trimmed); err != nil {
		return append([]byte(nil), trimmed...)
	}
	return buf.Bytes()
}

func normalizeSystem(callType string, root gjson.Result) json.RawMessage {
	switch callType {
	case "anthropic_messages":
		return normalizePromptSystem(root.Get("system"))
	case "openai_responses":
		return normalizePromptSystem(root.Get("instructions"))
	default:
		return nil
	}
}

func normalizePromptSystem(value gjson.Result) json.RawMessage {
	if !value.Exists() {
		return nil
	}
	if value.Type == gjson.String {
		text := strings.TrimSpace(value.String())
		if text == "" {
			return nil
		}
		raw, _ := json.Marshal([]map[string]any{
			{
				"type": "text",
				"text": text,
			},
		})
		return raw
	}
	if compacted := compactJSON([]byte(value.Raw)); len(compacted) > 0 {
		return compacted
	}
	return nil
}

func normalizeTools(callType string, root gjson.Result) json.RawMessage {
	switch callType {
	case "anthropic_messages", "openai_chat_completions", "openai_responses":
		if tools := compactJSON([]byte(root.Get("tools").Raw)); len(tools) > 0 {
			return tools
		}
	}
	return nil
}

func normalizeMessages(callType string, root gjson.Result) json.RawMessage {
	switch callType {
	case "anthropic_messages", "openai_chat_completions":
		if messages := compactJSON([]byte(root.Get("messages").Raw)); len(messages) > 0 {
			return messages
		}
	case "openai_responses":
		if messages := compactJSON([]byte(root.Get("input").Raw)); len(messages) > 0 {
			return messages
		}
	case "openai_completions":
		prompt := strings.TrimSpace(root.Get("prompt").String())
		if prompt == "" {
			return nil
		}
		raw, _ := json.Marshal([]map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "text",
						"text": prompt,
					},
				},
			},
		})
		return raw
	}
	return nil
}

func deriveCallTypeAndProvider(requestURL string) (string, string) {
	path := strings.TrimSpace(requestURL)
	if index := strings.Index(path, "?"); index >= 0 {
		path = path[:index]
	}
	switch path {
	case "/v1/messages":
		return "anthropic_messages", "anthropic"
	case "/v1/chat/completions":
		return "openai_chat_completions", "openai"
	case "/v1/completions":
		return "openai_completions", "openai"
	case "/v1/responses":
		return "openai_responses", "openai"
	default:
		return "unknown", "unknown"
	}
}

func deriveSource(headers map[string][]string, requestURL string) string {
	userAgent := strings.ToLower(strings.TrimSpace(firstHeader(headers, "User-Agent")))
	path := strings.TrimSpace(requestURL)
	if index := strings.Index(path, "?"); index >= 0 {
		path = path[:index]
	}
	switch {
	case strings.Contains(userAgent, "claude") && strings.Contains(userAgent, "vscode"):
		return "claude-vscode"
	case strings.Contains(userAgent, "claude"):
		return "claude-cli"
	case strings.Contains(userAgent, "openai"):
		if path == "/v1/responses" {
			return "openai-responses"
		}
		return "openai-sdk"
	default:
		return "server-live-capture"
	}
}

func canonicalModelFamily(model string) string {
	key := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(key, "claude"):
		return "claude"
	case strings.HasPrefix(key, "gpt"), strings.HasPrefix(key, "chatgpt"), strings.HasPrefix(key, "o1"), strings.HasPrefix(key, "o3"), strings.HasPrefix(key, "o4"):
		return "gpt"
	case strings.HasPrefix(key, "gemini"):
		return "gemini"
	case strings.HasPrefix(key, "openai"):
		return "gpt"
	case strings.HasPrefix(key, "anthropic"):
		return "claude"
	default:
		return "unknown"
	}
}

func extractProviderSessionID(requestRoot, responseRoot gjson.Result) string {
	candidates := []string{
		strings.TrimSpace(firstNonEmptyJSON(requestRoot, responseRoot, "session_id", "metadata.session_id", "request.sessionId", "response.session_id", "message.session_id")),
		strings.TrimSpace(extractCompositeSessionToken(firstNonEmptyJSON(requestRoot, responseRoot, "metadata.user_id", "metadata.userId"))),
	}
	for _, candidate := range candidates {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func extractCompositeSessionToken(value string) string {
	if structured := extractStructuredSessionID(value); structured != "" {
		return structured
	}
	for _, candidate := range collectCompositeSessionCandidates(value) {
		matches := compositeSessionPattern.FindStringSubmatch(candidate)
		if len(matches) == 4 {
			return matches[2]
		}
	}
	return ""
}

func extractStructuredSessionID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || !gjson.Valid(trimmed) {
		return ""
	}
	return findStructuredSessionID(gjson.Parse(trimmed))
}

func findStructuredSessionID(root gjson.Result) string {
	if !root.Exists() {
		return ""
	}
	switch root.Type {
	case gjson.JSON:
		if root.IsArray() {
			for _, item := range root.Array() {
				if found := findStructuredSessionID(item); found != "" {
					return found
				}
			}
			return ""
		}
		if value := strings.TrimSpace(root.Get("session_id").String()); value != "" {
			return value
		}
		if value := strings.TrimSpace(root.Get("sessionId").String()); value != "" {
			return value
		}
		var found string
		root.ForEach(func(_, value gjson.Result) bool {
			found = findStructuredSessionID(value)
			return found == ""
		})
		return found
	default:
		return ""
	}
}

func collectCompositeSessionCandidates(value string) []string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if !gjson.Valid(trimmed) {
		return []string{trimmed}
	}
	candidates := make([]string, 0, 4)
	root := gjson.Parse(trimmed)
	collectStringValues(root, &candidates)
	return candidates
}

func collectStringValues(root gjson.Result, out *[]string) {
	if out == nil || !root.Exists() {
		return
	}
	switch root.Type {
	case gjson.String:
		if value := strings.TrimSpace(root.String()); value != "" {
			*out = append(*out, value)
		}
	case gjson.JSON:
		if root.IsArray() {
			for _, item := range root.Array() {
				collectStringValues(item, out)
			}
			return
		}
		root.ForEach(func(_, value gjson.Result) bool {
			collectStringValues(value, out)
			return true
		})
	}
}

func compactStreamResponse(callType string, value []byte) []byte {
	switch callType {
	case "anthropic_messages":
		return compactAnthropicStreamResponse(value)
	default:
		return nil
	}
}

type anthropicContentAccumulator struct {
	Type        string
	ID          string
	Name        string
	Text        string
	Thinking    string
	Signature   string
	PartialJSON string
	Input       json.RawMessage
}

func compactAnthropicStreamResponse(value []byte) []byte {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 {
		return nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)

	message := map[string]any{
		"type":    "message",
		"role":    "assistant",
		"content": []any{},
	}
	content := make(map[int]*anthropicContentAccumulator)
	usage := map[string]int64{}
	var found bool

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) || !gjson.ValidBytes(payload) {
			continue
		}
		found = true

		root := gjson.ParseBytes(payload)
		switch root.Get("type").String() {
		case "message_start":
			messageNode := root.Get("message")
			if messageNode.Exists() {
				var parsed map[string]any
				if err := json.Unmarshal([]byte(messageNode.Raw), &parsed); err == nil {
					message = parsed
				}
			}
			mergeUsageMap(usage, root.Get("message.usage"))
		case "content_block_start":
			index := int(root.Get("index").Int())
			content[index] = decodeAnthropicContentAccumulator(root.Get("content_block"))
		case "content_block_delta":
			index := int(root.Get("index").Int())
			block := content[index]
			if block == nil {
				block = &anthropicContentAccumulator{}
				content[index] = block
			}
			applyAnthropicContentDelta(block, root.Get("delta"))
		case "message_delta":
			if stopReason := strings.TrimSpace(root.Get("delta.stop_reason").String()); stopReason != "" {
				message["stop_reason"] = stopReason
			}
			stopSequence := root.Get("delta.stop_sequence")
			if stopSequence.Exists() && stopSequence.Type != gjson.Null {
				message["stop_sequence"] = stopSequence.Value()
			}
			mergeUsageMap(usage, root.Get("usage"))
		}
	}
	if !found {
		return nil
	}

	indexes := make([]int, 0, len(content))
	for index := range content {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	items := make([]any, 0, len(indexes))
	for _, index := range indexes {
		block := content[index]
		if block == nil {
			continue
		}
		if len(block.Input) == 0 && strings.TrimSpace(block.PartialJSON) != "" && gjson.Valid(block.PartialJSON) {
			block.Input = append(json.RawMessage(nil), block.PartialJSON...)
		}
		items = append(items, buildAnthropicContentItem(block))
	}
	message["content"] = items
	if len(usage) > 0 {
		message["usage"] = usage
	}
	raw, err := json.Marshal(message)
	if err != nil {
		return nil
	}
	return compactJSON(raw)
}

func decodeAnthropicContentAccumulator(node gjson.Result) *anthropicContentAccumulator {
	if !node.Exists() {
		return &anthropicContentAccumulator{}
	}
	item := &anthropicContentAccumulator{
		Type:      strings.TrimSpace(node.Get("type").String()),
		ID:        strings.TrimSpace(node.Get("id").String()),
		Name:      strings.TrimSpace(node.Get("name").String()),
		Text:      node.Get("text").String(),
		Thinking:  node.Get("thinking").String(),
		Signature: strings.TrimSpace(node.Get("signature").String()),
	}
	if input := compactJSON([]byte(node.Get("input").Raw)); len(input) > 0 {
		item.Input = input
	}
	return item
}

func applyAnthropicContentDelta(block *anthropicContentAccumulator, delta gjson.Result) {
	if block == nil || !delta.Exists() {
		return
	}
	switch deltaType := strings.TrimSpace(delta.Get("type").String()); deltaType {
	case "text_delta":
		if block.Type == "" {
			block.Type = "text"
		}
		block.Text += delta.Get("text").String()
	case "thinking_delta":
		if block.Type == "" {
			block.Type = "thinking"
		}
		block.Thinking += delta.Get("thinking").String()
	case "signature_delta":
		block.Signature = strings.TrimSpace(delta.Get("signature").String())
	case "input_json_delta":
		if block.Type == "" {
			block.Type = "tool_use"
		}
		block.PartialJSON += delta.Get("partial_json").String()
	}
}

func buildAnthropicContentItem(block *anthropicContentAccumulator) map[string]any {
	item := map[string]any{
		"type": firstNonEmpty(block.Type, "text"),
	}
	switch item["type"] {
	case "thinking":
		item["thinking"] = block.Thinking
		if block.Signature != "" {
			item["signature"] = block.Signature
		}
	case "tool_use":
		item["id"] = block.ID
		item["name"] = block.Name
		if len(block.Input) > 0 {
			var input any
			if err := json.Unmarshal(block.Input, &input); err == nil {
				item["input"] = input
			} else {
				item["input"] = map[string]any{}
			}
		} else {
			item["input"] = map[string]any{}
		}
	default:
		item["text"] = block.Text
	}
	return item
}

func mergeUsageMap(target map[string]int64, node gjson.Result) {
	if target == nil || !node.Exists() {
		return
	}
	for _, key := range []string{
		"input_tokens",
		"output_tokens",
		"total_tokens",
		"cache_read_input_tokens",
		"cache_creation_input_tokens",
	} {
		if value := node.Get(key); value.Exists() {
			target[key] = value.Int()
		}
	}
}

func extractUsage(root gjson.Result) usageSummary {
	if !root.Exists() {
		return usageSummary{}
	}

	input := firstExistingInt(root,
		"usage.input_tokens",
		"usage.prompt_tokens",
		"response.usage.input_tokens",
		"response.usage.prompt_tokens",
	)
	output := firstExistingInt(root,
		"usage.output_tokens",
		"usage.completion_tokens",
		"response.usage.output_tokens",
		"response.usage.completion_tokens",
	)
	reasoning := firstExistingInt(root,
		"usage.thinking_tokens",
		"usage.output_tokens_details.reasoning_tokens",
		"usage.completion_tokens_details.reasoning_tokens",
		"response.usage.thinking_tokens",
		"response.usage.output_tokens_details.reasoning_tokens",
		"response.usage.completion_tokens_details.reasoning_tokens",
	)
	cached := firstExistingInt(root,
		"usage.cache_read_input_tokens",
		"usage.prompt_tokens_details.cached_tokens",
		"usage.input_tokens_details.cached_tokens",
		"response.usage.cache_read_input_tokens",
		"response.usage.prompt_tokens_details.cached_tokens",
		"response.usage.input_tokens_details.cached_tokens",
	)
	total := firstExistingInt(root,
		"usage.total_tokens",
		"response.usage.total_tokens",
	)
	if total == 0 {
		total = input + output
	}

	return usageSummary{
		InputTokens:     input,
		OutputTokens:    output,
		ReasoningTokens: reasoning,
		CachedTokens:    cached,
		TotalTokens:     total,
	}
}

func firstExistingInt(root gjson.Result, paths ...string) int64 {
	for _, path := range paths {
		value := root.Get(path)
		if value.Exists() {
			return value.Int()
		}
	}
	return 0
}

func firstNonEmptyJSON(primary, secondary gjson.Result, paths ...string) string {
	for _, root := range []gjson.Result{primary, secondary} {
		if !root.Exists() {
			continue
		}
		for _, path := range paths {
			value := strings.TrimSpace(root.Get(path).String())
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func firstNonEmptyHeader(headers map[string][]string, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(firstHeader(headers, name)); value != "" {
			return value
		}
	}
	return ""
}

func firstHeader(headers map[string][]string, name string) string {
	for key, values := range headers {
		if !strings.EqualFold(strings.TrimSpace(key), name) {
			continue
		}
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				return value
			}
		}
	}
	return ""
}

func countJSONArray(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		return 0
	}
	return len(items)
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func cloneJSON(data []byte) json.RawMessage {
	if len(data) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), data...)
}

func messagesExactlyMatch(a, b []byte) bool {
	return bytes.Equal(canonicalJSON(a), canonicalJSON(b))
}

func messagesPrefixMatch(candidate, current []byte) bool {
	candidateItems, ok := decodeJSONArray(candidate)
	if !ok || len(candidateItems) == 0 {
		return false
	}
	currentItems, ok := decodeJSONArray(current)
	if !ok || len(currentItems) < len(candidateItems) {
		return false
	}
	for index := range candidateItems {
		if candidateItems[index] != currentItems[index] {
			return false
		}
	}
	return true
}

func appendAssistantResponseToMessages(callType string, messages, response []byte) []byte {
	messageItems, ok := decodeJSONArrayRaw(messages)
	if !ok || len(messageItems) == 0 {
		return nil
	}
	assistantMessage := assistantMessageFromResponse(callType, response)
	if len(assistantMessage) == 0 {
		return nil
	}
	messageItems = append(messageItems, assistantMessage)
	combined, err := json.Marshal(messageItems)
	if err != nil {
		return nil
	}
	return compactJSON(combined)
}

func assistantMessageFromResponse(callType string, response []byte) json.RawMessage {
	root := gjson.ParseBytes(compactJSON(response))
	if !root.Exists() {
		return nil
	}

	switch callType {
	case "anthropic_messages":
		content := compactJSON([]byte(root.Get("content").Raw))
		if len(content) == 0 {
			return nil
		}
		payload, _ := json.Marshal(map[string]any{
			"role":    firstNonEmpty(strings.TrimSpace(root.Get("role").String()), "assistant"),
			"content": decodeJSONOrDefault(content, []any{}),
		})
		return compactJSON(payload)
	case "openai_chat_completions":
		message := compactJSON([]byte(root.Get("choices.0.message").Raw))
		if len(message) > 0 {
			return message
		}
	case "openai_responses":
		output := compactJSON([]byte(root.Get("output").Raw))
		if len(output) == 0 {
			return nil
		}
		payload, _ := json.Marshal(map[string]any{
			"role":    "assistant",
			"content": decodeJSONOrDefault(output, []any{}),
		})
		return compactJSON(payload)
	}

	return nil
}

func decodeJSONArray(data []byte) ([]string, bool) {
	rawItems, ok := decodeJSONArrayRaw(data)
	if !ok {
		return nil, false
	}
	items := make([]string, 0, len(rawItems))
	for _, item := range rawItems {
		canonical := canonicalJSON(item)
		if len(canonical) == 0 {
			items = append(items, string(item))
			continue
		}
		items = append(items, string(canonical))
	}
	return items, true
}

func decodeJSONArrayRaw(data []byte) ([]json.RawMessage, bool) {
	compacted := compactJSON(data)
	if len(compacted) == 0 {
		return nil, false
	}
	var rawItems []json.RawMessage
	if err := json.Unmarshal(compacted, &rawItems); err != nil {
		return nil, false
	}
	return rawItems, true
}

func canonicalJSON(data []byte) []byte {
	compacted := compactJSON(data)
	if len(compacted) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(compacted, &value); err != nil {
		return compacted
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return compacted
	}
	return normalized
}
