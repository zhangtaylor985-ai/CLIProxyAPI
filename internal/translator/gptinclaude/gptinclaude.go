package gptinclaude

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/thinking"
	"github.com/tidwall/gjson"
)

type ClientKind string

const (
	ClientUnknown      ClientKind = "unknown"
	ClientClaudeCLI    ClientKind = "claude-cli"
	ClientClaudeVSCode ClientKind = "claude_vscode"
	ClientCodexVSCode  ClientKind = "codex_exec_vscode"
)

var (
	webSearchIntentMatchers = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bweb\s*search\b`),
		regexp.MustCompile(`(?i)\bsearch(?:\s+the)?\s+web\b`),
		regexp.MustCompile(`(?i)\bwebsearch\b`),
		regexp.MustCompile(`网页搜索|联网搜索|网上搜索|用网搜索`),
		regexp.MustCompile(`(?:请|帮我|麻烦|去)?搜索(?:一下)?`),
		regexp.MustCompile(`搜一下|查一下`),
	}
	webSearchNegationMatchers = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(?:do\s+not|don't|dont|without|avoid|skip|no|not)\b`),
		regexp.MustCompile(`不要|不用|别|无需|不需要|不用再|别再`),
	}
)

type headerGetter interface {
	GetHeader(string) string
}

// DetectClientKind classifies the downstream client from request headers stored
// in context. This keeps client-specific compatibility behavior isolated to the
// Claude -> GPT compatibility path without mutating request payloads.
func DetectClientKind(ctx context.Context) ClientKind {
	if ctx == nil {
		return ClientUnknown
	}

	getter, _ := ctx.Value("gin").(headerGetter)
	if getter == nil {
		return ClientUnknown
	}

	userAgent := strings.ToLower(strings.TrimSpace(getter.GetHeader("User-Agent")))
	originator := strings.ToLower(strings.TrimSpace(getter.GetHeader("Originator")))

	switch {
	case strings.Contains(userAgent, "claude-vscode"):
		return ClientClaudeVSCode
	case strings.HasPrefix(userAgent, "claude-cli/"):
		return ClientClaudeCLI
	case strings.Contains(userAgent, "vscode/"), originator == "codex_exec":
		return ClientCodexVSCode
	default:
		return ClientUnknown
	}
}

// ShouldEmitSyntheticWebSearchTag limits synthetic Claude-style websearch text
// blocks to the real Claude CLI path. VSCode/Codex clients have their own
// rendering expectations and should not receive the same synthetic text.
func ShouldEmitSyntheticWebSearchTag(kind ClientKind) bool {
	return kind == ClientClaudeCLI
}

// ShouldSurfaceReasoningSummaryAsThinking controls whether Codex reasoning
// summaries should be exposed as Claude "thinking" blocks. VSCode clients render
// these summaries as if they were live thoughts, which is misleading because the
// summaries usually arrive after the actual search work has already happened.
func ShouldSurfaceReasoningSummaryAsThinking(kind ClientKind) bool {
	switch kind {
	case ClientClaudeCLI, ClientClaudeVSCode, ClientCodexVSCode:
		return false
	default:
		return true
	}
}

// ShouldEmitVSCodeWebSearchProgress reports whether the client should receive a
// compact progress hint tied to real web_search_call events instead of verbose
// post-hoc reasoning summaries.
func ShouldEmitVSCodeWebSearchProgress(kind ClientKind) bool {
	switch kind {
	case ClientClaudeVSCode, ClientCodexVSCode:
		return true
	default:
		return false
	}
}

// ShouldPreEmitBuiltinWebSearchProgress restricts early synthetic progress to
// prompts that explicitly ask for a web search. Claude Code often advertises
// WebSearch as an available tool even for normal coding tasks, and eagerly
// showing "Searching the web." on response.created causes false positives in
// the CLI experience.
func ShouldPreEmitBuiltinWebSearchProgress(rawJSON []byte) bool {
	if !HasBuiltinWebSearch(rawJSON) {
		return false
	}

	return hasExplicitBuiltinWebSearchIntent(extractLatestUserText(rawJSON))
}

// BuildVSCodeWebSearchProgressThinking renders a short, factual progress message
// for VSCode-style clients. Unlike synthetic <tool_call> text, this keeps the
// UI concise and avoids leaking implementation details into the chat transcript.
func BuildVSCodeWebSearchProgressThinking(action gjson.Result, fallbackQuery string) string {
	switch strings.ToLower(strings.TrimSpace(action.Get("type").String())) {
	case "search":
		query := strings.TrimSpace(action.Get("query").String())
		if query == "" {
			query = strings.TrimSpace(fallbackQuery)
		}
		if query != "" {
			return "Searching the web for: " + query
		}
	}

	if query := strings.TrimSpace(fallbackQuery); query != "" {
		return "Searching the web for: " + query
	}
	return "Searching the web."
}

// HasBuiltinWebSearch reports whether the Claude request declared a search tool
// that should be routed to Codex built-in web_search on the Claude -> GPT
// compatibility path. This includes Anthropic's built-in web_search tool and
// Claude Code's generic WebSearch function tool.
func HasBuiltinWebSearch(rawJSON []byte) bool {
	tools := gjson.GetBytes(rawJSON, "tools")
	if !tools.IsArray() {
		return false
	}

	for _, tool := range tools.Array() {
		if IsCodexBuiltinWebSearchTool(tool) {
			return true
		}
	}
	return false
}

// IsCodexBuiltinWebSearchTool reports whether a Claude tool declaration should
// be translated to the Codex built-in web_search tool.
func IsCodexBuiltinWebSearchTool(tool gjson.Result) bool {
	if !tool.Exists() {
		return false
	}

	if strings.EqualFold(tool.Get("type").String(), "web_search_20250305") {
		return true
	}
	if strings.EqualFold(tool.Get("name").String(), "web_search") && tool.Get("type").Exists() {
		return true
	}

	if !strings.EqualFold(strings.TrimSpace(tool.Get("name").String()), "WebSearch") {
		return false
	}

	schema := tool.Get("input_schema")
	if !schema.Exists() {
		return true
	}

	query := schema.Get("properties.query")
	if query.Exists() {
		return true
	}
	if required := schema.Get("required"); required.IsArray() {
		for _, item := range required.Array() {
			if strings.EqualFold(strings.TrimSpace(item.String()), "query") {
				return true
			}
		}
	}
	return false
}

// ClampReasoningEffort caps search-heavy Claude->GPT requests at medium effort so
// the model reaches the first search call faster instead of overthinking before
// invoking the built-in search tool.
func ClampReasoningEffort(effort string, hasBuiltinWebSearch bool) string {
	normalized := strings.ToLower(strings.TrimSpace(effort))
	if normalized == "" {
		normalized = "medium"
	}
	if !hasBuiltinWebSearch {
		return normalized
	}

	switch normalized {
	case "max", "xhigh", "high":
		return "medium"
	default:
		return normalized
	}
}

// ClampTargetModelForBuiltinWebSearch downgrades Claude->GPT routed search
// requests to medium reasoning at the model-suffix layer so later suffix-based
// thinking application does not overwrite the earlier request-body clamp.
func ClampTargetModelForBuiltinWebSearch(model string, hasBuiltinWebSearch bool) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" || !hasBuiltinWebSearch {
		return trimmed
	}

	parsed := thinking.ParseSuffix(trimmed)
	base := strings.TrimSpace(parsed.ModelName)
	if base == "" {
		return trimmed
	}

	baseLower := strings.ToLower(base)
	if !strings.HasPrefix(baseLower, "gpt-") &&
		!strings.HasPrefix(baseLower, "chatgpt-") &&
		!strings.HasPrefix(baseLower, "o1") &&
		!strings.HasPrefix(baseLower, "o3") &&
		!strings.HasPrefix(baseLower, "o4") {
		return trimmed
	}

	suffix := strings.ToLower(strings.TrimSpace(parsed.RawSuffix))
	switch suffix {
	case "", "high", "xhigh", "max":
		return base + "(medium)"
	default:
		return trimmed
	}
}

// BuildSyntheticWebSearchToolCallText renders a Claude Code-compatible textual
// <tool_call> marker for Codex built-in web search events. Native Claude Code
// providers in this project already surface built-in web_search calls via text
// blocks, so we mirror that shape on the GPT-in-Claude compatibility path.
func BuildSyntheticWebSearchToolCallText(action gjson.Result) string {
	if !action.Exists() {
		return ""
	}

	prefix := "Searching the web.\n\n"
	args := map[string]any{}
	switch strings.ToLower(strings.TrimSpace(action.Get("type").String())) {
	case "search":
		query := strings.TrimSpace(action.Get("query").String())
		if query != "" {
			prefix = "Searched: " + query + "\n\n"
			args["query"] = query
		}
		if queries := action.Get("queries"); queries.Exists() && queries.IsArray() {
			values := make([]string, 0, len(queries.Array()))
			for _, queryItem := range queries.Array() {
				queryText := strings.TrimSpace(queryItem.String())
				if queryText != "" {
					values = append(values, queryText)
				}
			}
			if len(values) > 0 {
				args["queries"] = values
			}
		}
	case "open_page":
		url := strings.TrimSpace(action.Get("url").String())
		if url != "" {
			args["url"] = url
		}
	default:
		if raw := strings.TrimSpace(action.Raw); raw != "" {
			args["action"] = json.RawMessage(raw)
		}
	}

	if len(args) == 0 {
		return ""
	}

	payload := map[string]any{
		"name":      "web_search",
		"arguments": args,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return prefix + "<tool_call>\n" + string(body) + "\n</tool_call>"
}

// InferBuiltinWebSearchQuery extracts a likely built-in web search query from the
// latest Claude user message. This is only used on the Claude -> GPT compatibility
// path to surface an early synthetic tool call before Codex emits the final
// web_search_call action payload.
func InferBuiltinWebSearchQuery(rawJSON []byte) string {
	if !HasBuiltinWebSearch(rawJSON) {
		return ""
	}

	if text := extractLatestUserText(rawJSON); text != "" {
		if query := normalizeLikelyWebSearchQuery(text); query != "" {
			return query
		}
	}

	return ""
}

func extractLatestUserText(rawJSON []byte) string {
	messages := gjson.GetBytes(rawJSON, "messages")
	if !messages.IsArray() {
		return ""
	}

	for i := len(messages.Array()) - 1; i >= 0; i-- {
		message := messages.Array()[i]
		if !strings.EqualFold(strings.TrimSpace(message.Get("role").String()), "user") {
			continue
		}

		content := message.Get("content")
		if content.Type == gjson.String {
			return content.String()
		}
		if content.IsArray() {
			parts := content.Array()
			for j := len(parts) - 1; j >= 0; j-- {
				part := parts[j]
				if strings.EqualFold(strings.TrimSpace(part.Get("type").String()), "text") {
					return part.Get("text").String()
				}
			}
		}
	}

	return ""
}

func hasExplicitBuiltinWebSearchIntent(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}

	if extractExplicitSearchQuery(text) != "" {
		return true
	}

	for _, matcher := range webSearchIntentMatchers {
		indexes := matcher.FindAllStringIndex(text, -1)
		for _, idx := range indexes {
			if hasNearbyWebSearchNegation(text, idx[0], idx[1]) {
				continue
			}
			return true
		}
	}

	return false
}

func hasNearbyWebSearchNegation(text string, start, end int) bool {
	window := snippetAround(text, start, end, 18)
	if window == "" {
		return false
	}
	for _, matcher := range webSearchNegationMatchers {
		if matcher.FindStringIndex(window) != nil {
			return true
		}
	}
	return false
}

func snippetAround(text string, start, end, radius int) string {
	if text == "" {
		return ""
	}
	runes := []rune(text)
	startRune := utf8.RuneCountInString(text[:clampByteIndex(text, start)])
	endRune := utf8.RuneCountInString(text[:clampByteIndex(text, end)])
	left := startRune - radius
	if left < 0 {
		left = 0
	}
	right := endRune + radius
	if right > len(runes) {
		right = len(runes)
	}
	return string(runes[left:right])
}

func clampByteIndex(text string, idx int) int {
	if idx < 0 {
		return 0
	}
	if idx > len(text) {
		return len(text)
	}
	return idx
}

// BuildSyntheticWebSearchToolCallTextFromRequest synthesizes a Claude-compatible
// textual web_search tool call from the original Claude request when Codex has
// not yet emitted the final action payload.
func BuildSyntheticWebSearchToolCallTextFromRequest(rawJSON []byte) string {
	query := InferBuiltinWebSearchQuery(rawJSON)
	if query == "" {
		return ""
	}

	payload := map[string]any{
		"name": "web_search",
		"arguments": map[string]any{
			"query": query,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return "Searching the web.\n\n<tool_call>\n" + string(body) + "\n</tool_call>"
}

func normalizeLikelyWebSearchQuery(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	if explicit := extractExplicitSearchQuery(text); explicit != "" {
		return explicit
	}

	prefixes := []string{
		"perform a web search for the query:",
		"perform web search for the query:",
		"perform a web search for:",
		"web search for the query:",
		"web search for:",
		"search for:",
		"搜索：",
		"搜索:",
		"请搜索：",
		"请搜索:",
	}

	lower := strings.ToLower(text)
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			text = strings.TrimSpace(text[len(prefix):])
			break
		}
	}

	return sanitizeLikelySearchQuery(text)
}

func extractExplicitSearchQuery(text string) string {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n")
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "arguments:"):
			return sanitizeLikelySearchQuery(strings.TrimSpace(line[len("arguments:"):]))
		case strings.HasPrefix(lower, "query:"):
			return sanitizeLikelySearchQuery(strings.TrimSpace(line[len("query:"):]))
		}
	}

	return ""
}

func sanitizeLikelySearchQuery(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	lines := strings.Split(text, "\n")
	parts := make([]string, 0, 2)
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			if len(parts) > 0 {
				break
			}
			continue
		}
		if looksLikeSearchQueryNoise(line) {
			break
		}
		parts = append(parts, line)
		if len(parts) >= 2 {
			break
		}
	}

	if len(parts) == 0 {
		return ""
	}

	query := strings.Join(parts, " ")
	query = strings.Join(strings.Fields(query), " ")
	query = strings.Trim(query, " \t\r\n\"'“”‘’")
	queryLower := strings.ToLower(query)
	switch {
	case strings.HasPrefix(queryLower, "web search "):
		query = strings.TrimSpace(query[len("web search "):])
	case strings.HasPrefix(queryLower, "search "):
		query = strings.TrimSpace(query[len("search "):])
	}
	if query == "" {
		return ""
	}
	if len(query) > 220 {
		return ""
	}
	if looksLikeSearchQueryNoise(query) {
		return ""
	}
	return query
}

func looksLikeSearchQueryNoise(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}

	lower := strings.ToLower(trimmed)
	noisePrefixes := []string{
		"<system-reminder>",
		"arguments:",
		"query:",
		"import ",
		"from ",
		"def ",
		"class ",
		"func ",
		"package ",
		"python3 ",
		"curl ",
		"go test ",
		"sed -n ",
		"rg -n ",
		"cases =",
		"results =",
		"with open(",
		"for prompt in",
		"p = subprocess",
		"event:",
		"data:",
		"```",
		"read the output of the terminal command",
		"then fix the error",
		"then rerun the command",
		"repeat this debugging process",
		"use context7",
		"use brave-search",
		"读终端命令的输出",
		"然后修复该错误",
		"接着在终端中重新运行该命令",
		"如果再次出现错误",
		"使用 context7",
		"使用 brave-search",
	}
	for _, prefix := range noisePrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	if strings.Contains(lower, "with open(") ||
		strings.Contains(lower, "subprocess.run(") ||
		strings.Contains(lower, "json.dump(") ||
		strings.Contains(lower, "capture_output=true") ||
		strings.Contains(lower, "<tool_call>") {
		return true
	}

	return false
}
