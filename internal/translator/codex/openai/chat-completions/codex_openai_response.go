// Package openai provides response translation functionality for Codex to OpenAI API compatibility.
// This package handles the conversion of Codex API responses into OpenAI Chat Completions-compatible
// JSON format, transforming streaming events and non-streaming responses into the format
// expected by OpenAI API clients. It supports both streaming and non-streaming modes,
// handling text content, tool calls, reasoning content, and usage metadata appropriately.
package chat_completions

import (
	"bytes"
	"context"
	"sort"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var (
	dataTag = []byte("data:")
)

// ConvertCliToOpenAIParams holds parameters for response conversion.
type ConvertCliToOpenAIParams struct {
	ResponseID                string
	CreatedAt                 int64
	Model                     string
	FunctionCallIndex         int
	HasReceivedArgumentsDelta bool
	HasToolCallAnnounced      bool
}

type toolCallAccumulator struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

// ConvertCodexResponseToOpenAI translates a single chunk of a streaming response from the
// Codex API format to the OpenAI Chat Completions streaming format.
// It processes various Codex event types and transforms them into OpenAI-compatible JSON responses.
// The function handles text content, tool calls, reasoning content, and usage metadata, outputting
// responses that match the OpenAI API format. It supports incremental updates for streaming responses.
//
// Parameters:
//   - ctx: The context for the request, used for cancellation and timeout handling
//   - modelName: The name of the model being used for the response
//   - rawJSON: The raw JSON response from the Codex API
//   - param: A pointer to a parameter object for maintaining state between calls
//
// Returns:
//   - [][]byte: A slice of OpenAI-compatible JSON responses
func ConvertCodexResponseToOpenAI(_ context.Context, modelName string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) [][]byte {
	if *param == nil {
		*param = &ConvertCliToOpenAIParams{
			Model:                     modelName,
			CreatedAt:                 0,
			ResponseID:                "",
			FunctionCallIndex:         -1,
			HasReceivedArgumentsDelta: false,
			HasToolCallAnnounced:      false,
		}
	}

	if !bytes.HasPrefix(rawJSON, dataTag) {
		return [][]byte{}
	}
	rawJSON = bytes.TrimSpace(rawJSON[5:])

	// Initialize the OpenAI SSE template.
	template := []byte(`{"id":"","object":"chat.completion.chunk","created":12345,"model":"model","choices":[{"index":0,"delta":{"role":null,"content":null,"reasoning_content":null,"tool_calls":null},"finish_reason":null,"native_finish_reason":null}]}`)

	rootResult := gjson.ParseBytes(rawJSON)

	typeResult := rootResult.Get("type")
	dataType := typeResult.String()
	if dataType == "response.created" {
		(*param).(*ConvertCliToOpenAIParams).ResponseID = rootResult.Get("response.id").String()
		(*param).(*ConvertCliToOpenAIParams).CreatedAt = rootResult.Get("response.created_at").Int()
		(*param).(*ConvertCliToOpenAIParams).Model = rootResult.Get("response.model").String()
		return [][]byte{}
	}

	// Extract and set the model version.
	cachedModel := (*param).(*ConvertCliToOpenAIParams).Model
	if modelResult := gjson.GetBytes(rawJSON, "model"); modelResult.Exists() {
		template, _ = sjson.SetBytes(template, "model", modelResult.String())
	} else if cachedModel != "" {
		template, _ = sjson.SetBytes(template, "model", cachedModel)
	} else if modelName != "" {
		template, _ = sjson.SetBytes(template, "model", modelName)
	}

	template, _ = sjson.SetBytes(template, "created", (*param).(*ConvertCliToOpenAIParams).CreatedAt)

	// Extract and set the response ID.
	template, _ = sjson.SetBytes(template, "id", (*param).(*ConvertCliToOpenAIParams).ResponseID)

	// Extract and set usage metadata (token counts).
	if usageResult := gjson.GetBytes(rawJSON, "response.usage"); usageResult.Exists() {
		if outputTokensResult := usageResult.Get("output_tokens"); outputTokensResult.Exists() {
			template, _ = sjson.SetBytes(template, "usage.completion_tokens", outputTokensResult.Int())
		}
		if totalTokensResult := usageResult.Get("total_tokens"); totalTokensResult.Exists() {
			template, _ = sjson.SetBytes(template, "usage.total_tokens", totalTokensResult.Int())
		}
		if inputTokensResult := usageResult.Get("input_tokens"); inputTokensResult.Exists() {
			template, _ = sjson.SetBytes(template, "usage.prompt_tokens", inputTokensResult.Int())
		}
		if cachedTokensResult := usageResult.Get("input_tokens_details.cached_tokens"); cachedTokensResult.Exists() {
			template, _ = sjson.SetBytes(template, "usage.prompt_tokens_details.cached_tokens", cachedTokensResult.Int())
		}
		if reasoningTokensResult := usageResult.Get("output_tokens_details.reasoning_tokens"); reasoningTokensResult.Exists() {
			template, _ = sjson.SetBytes(template, "usage.completion_tokens_details.reasoning_tokens", reasoningTokensResult.Int())
		}
	}

	if dataType == "response.reasoning_summary_text.delta" {
		if deltaResult := rootResult.Get("delta"); deltaResult.Exists() {
			template, _ = sjson.SetBytes(template, "choices.0.delta.role", "assistant")
			template, _ = sjson.SetBytes(template, "choices.0.delta.reasoning_content", deltaResult.String())
		}
	} else if dataType == "response.reasoning_summary_text.done" {
		template, _ = sjson.SetBytes(template, "choices.0.delta.role", "assistant")
		template, _ = sjson.SetBytes(template, "choices.0.delta.reasoning_content", "\n\n")
	} else if dataType == "response.output_text.delta" {
		if deltaResult := rootResult.Get("delta"); deltaResult.Exists() {
			template, _ = sjson.SetBytes(template, "choices.0.delta.role", "assistant")
			template, _ = sjson.SetBytes(template, "choices.0.delta.content", deltaResult.String())
		}
	} else if dataType == "response.completed" {
		finishReason := "stop"
		if (*param).(*ConvertCliToOpenAIParams).FunctionCallIndex != -1 {
			finishReason = "tool_calls"
		}
		template, _ = sjson.SetBytes(template, "choices.0.finish_reason", finishReason)
		template, _ = sjson.SetBytes(template, "choices.0.native_finish_reason", finishReason)
	} else if dataType == "response.output_item.added" {
		itemResult := rootResult.Get("item")
		if !itemResult.Exists() || itemResult.Get("type").String() != "function_call" {
			return [][]byte{}
		}

		// Increment index for this new function call item.
		(*param).(*ConvertCliToOpenAIParams).FunctionCallIndex++
		(*param).(*ConvertCliToOpenAIParams).HasReceivedArgumentsDelta = false
		(*param).(*ConvertCliToOpenAIParams).HasToolCallAnnounced = true

		functionCallItemTemplate := []byte(`{"index":0,"id":"","type":"function","function":{"name":"","arguments":""}}`)
		functionCallItemTemplate, _ = sjson.SetBytes(functionCallItemTemplate, "index", (*param).(*ConvertCliToOpenAIParams).FunctionCallIndex)
		functionCallItemTemplate, _ = sjson.SetBytes(functionCallItemTemplate, "id", itemResult.Get("call_id").String())

		// Restore original tool name if it was shortened.
		name := itemResult.Get("name").String()
		rev := buildReverseMapFromOriginalOpenAI(originalRequestRawJSON)
		if orig, ok := rev[name]; ok {
			name = orig
		}
		functionCallItemTemplate, _ = sjson.SetBytes(functionCallItemTemplate, "function.name", name)
		functionCallItemTemplate, _ = sjson.SetBytes(functionCallItemTemplate, "function.arguments", "")

		template, _ = sjson.SetBytes(template, "choices.0.delta.role", "assistant")
		template, _ = sjson.SetRawBytes(template, "choices.0.delta.tool_calls", []byte(`[]`))
		template, _ = sjson.SetRawBytes(template, "choices.0.delta.tool_calls.-1", functionCallItemTemplate)

	} else if dataType == "response.function_call_arguments.delta" {
		(*param).(*ConvertCliToOpenAIParams).HasReceivedArgumentsDelta = true

		deltaValue := rootResult.Get("delta").String()
		functionCallItemTemplate := []byte(`{"index":0,"function":{"arguments":""}}`)
		functionCallItemTemplate, _ = sjson.SetBytes(functionCallItemTemplate, "index", (*param).(*ConvertCliToOpenAIParams).FunctionCallIndex)
		functionCallItemTemplate, _ = sjson.SetBytes(functionCallItemTemplate, "function.arguments", deltaValue)

		template, _ = sjson.SetRawBytes(template, "choices.0.delta.tool_calls", []byte(`[]`))
		template, _ = sjson.SetRawBytes(template, "choices.0.delta.tool_calls.-1", functionCallItemTemplate)

	} else if dataType == "response.function_call_arguments.done" {
		if (*param).(*ConvertCliToOpenAIParams).HasReceivedArgumentsDelta {
			// Arguments were already streamed via delta events; nothing to emit.
			return [][]byte{}
		}

		// Fallback: no delta events were received, emit the full arguments as a single chunk.
		fullArgs := rootResult.Get("arguments").String()
		functionCallItemTemplate := []byte(`{"index":0,"function":{"arguments":""}}`)
		functionCallItemTemplate, _ = sjson.SetBytes(functionCallItemTemplate, "index", (*param).(*ConvertCliToOpenAIParams).FunctionCallIndex)
		functionCallItemTemplate, _ = sjson.SetBytes(functionCallItemTemplate, "function.arguments", fullArgs)

		template, _ = sjson.SetRawBytes(template, "choices.0.delta.tool_calls", []byte(`[]`))
		template, _ = sjson.SetRawBytes(template, "choices.0.delta.tool_calls.-1", functionCallItemTemplate)

	} else if dataType == "response.output_item.done" {
		itemResult := rootResult.Get("item")
		if !itemResult.Exists() || itemResult.Get("type").String() != "function_call" {
			return [][]byte{}
		}

		if (*param).(*ConvertCliToOpenAIParams).HasToolCallAnnounced {
			// Tool call was already announced via output_item.added; skip emission.
			(*param).(*ConvertCliToOpenAIParams).HasToolCallAnnounced = false
			return [][]byte{}
		}

		// Fallback path: model skipped output_item.added, so emit complete tool call now.
		(*param).(*ConvertCliToOpenAIParams).FunctionCallIndex++

		functionCallItemTemplate := []byte(`{"index":0,"id":"","type":"function","function":{"name":"","arguments":""}}`)
		functionCallItemTemplate, _ = sjson.SetBytes(functionCallItemTemplate, "index", (*param).(*ConvertCliToOpenAIParams).FunctionCallIndex)

		template, _ = sjson.SetRawBytes(template, "choices.0.delta.tool_calls", []byte(`[]`))
		functionCallItemTemplate, _ = sjson.SetBytes(functionCallItemTemplate, "id", itemResult.Get("call_id").String())

		// Restore original tool name if it was shortened.
		name := itemResult.Get("name").String()
		rev := buildReverseMapFromOriginalOpenAI(originalRequestRawJSON)
		if orig, ok := rev[name]; ok {
			name = orig
		}
		functionCallItemTemplate, _ = sjson.SetBytes(functionCallItemTemplate, "function.name", name)

		functionCallItemTemplate, _ = sjson.SetBytes(functionCallItemTemplate, "function.arguments", itemResult.Get("arguments").String())
		template, _ = sjson.SetBytes(template, "choices.0.delta.role", "assistant")
		template, _ = sjson.SetRawBytes(template, "choices.0.delta.tool_calls.-1", functionCallItemTemplate)

	} else {
		return [][]byte{}
	}

	return [][]byte{template}
}

// ConvertCodexResponseToOpenAINonStream converts a non-streaming Codex response to a non-streaming OpenAI response.
// This function processes the complete Codex response and transforms it into a single OpenAI-compatible
// JSON response. It handles message content, tool calls, reasoning content, and usage metadata, combining all
// the information into a single response that matches the OpenAI API format.
//
// Parameters:
//   - ctx: The context for the request, used for cancellation and timeout handling
//   - modelName: The name of the model being used for the response (unused in current implementation)
//   - rawJSON: The raw JSON response from the Codex API
//   - param: A pointer to a parameter object for the conversion (unused in current implementation)
//
// Returns:
//   - []byte: An OpenAI-compatible JSON response containing all message content and metadata
func ConvertCodexResponseToOpenAINonStream(_ context.Context, _ string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, _ *any) []byte {
	chunks := extractCodexSSEChunks(rawJSON)
	if len(chunks) > 0 {
		return convertCodexSSETranscriptToOpenAI(originalRequestRawJSON, chunks)
	}

	rootResult := gjson.ParseBytes(rawJSON)
	if rootResult.Get("type").String() != "response.completed" {
		return []byte{}
	}

	return convertCompletedCodexResponseToOpenAI(originalRequestRawJSON, rootResult.Get("response"))
}

func extractCodexSSEChunks(rawJSON []byte) [][]byte {
	lines := bytes.Split(rawJSON, []byte("\n"))
	chunks := make([][]byte, 0, len(lines))
	for _, line := range lines {
		if !bytes.HasPrefix(line, dataTag) {
			continue
		}
		line = bytes.TrimSpace(line[5:])
		if len(line) == 0 || bytes.Equal(line, []byte("[DONE]")) {
			continue
		}
		chunks = append(chunks, bytes.Clone(line))
	}
	return chunks
}

func convertCodexSSETranscriptToOpenAI(originalRequestRawJSON []byte, chunks [][]byte) []byte {
	var (
		responseID     string
		model          string
		createdAt      int64
		contentParts   []string
		reasoningParts []string
		usageResult    gjson.Result
		completedResp  gjson.Result
	)

	toolCalls := make(map[int]*toolCallAccumulator)

	for _, chunk := range chunks {
		rootResult := gjson.ParseBytes(chunk)
		switch rootResult.Get("type").String() {
		case "response.created":
			response := rootResult.Get("response")
			if id := response.Get("id").String(); id != "" {
				responseID = id
			}
			if m := response.Get("model").String(); m != "" {
				model = m
			}
			if created := response.Get("created_at").Int(); created > 0 {
				createdAt = created
			}

		case "response.output_text.delta":
			if delta := rootResult.Get("delta").String(); delta != "" {
				contentParts = append(contentParts, delta)
			}

		case "response.output_text.done":
			if len(contentParts) == 0 {
				if text := rootResult.Get("text").String(); text != "" {
					contentParts = append(contentParts, text)
				}
			}

		case "response.reasoning_summary_text.delta":
			if delta := rootResult.Get("delta").String(); delta != "" {
				reasoningParts = append(reasoningParts, delta)
			}

		case "response.reasoning_summary_text.done":
			if len(reasoningParts) == 0 {
				if text := rootResult.Get("text").String(); text != "" {
					reasoningParts = append(reasoningParts, text)
				}
			}

		case "response.output_item.added":
			if outputIndex := int(rootResult.Get("output_index").Int()); outputIndex >= 0 {
				item := rootResult.Get("item")
				if item.Get("type").String() == "function_call" {
					acc := toolCalls[outputIndex]
					if acc == nil {
						acc = &toolCallAccumulator{}
						toolCalls[outputIndex] = acc
					}
					if id := item.Get("call_id").String(); id != "" {
						acc.ID = id
					}
					if name := item.Get("name").String(); name != "" {
						acc.Name = name
					}
				}
			}

		case "response.function_call_arguments.delta":
			if outputIndex := int(rootResult.Get("output_index").Int()); outputIndex >= 0 {
				acc := toolCalls[outputIndex]
				if acc == nil {
					acc = &toolCallAccumulator{}
					toolCalls[outputIndex] = acc
				}
				if delta := rootResult.Get("delta").String(); delta != "" {
					acc.Arguments.WriteString(delta)
				}
			}

		case "response.function_call_arguments.done":
			if outputIndex := int(rootResult.Get("output_index").Int()); outputIndex >= 0 {
				acc := toolCalls[outputIndex]
				if acc == nil {
					acc = &toolCallAccumulator{}
					toolCalls[outputIndex] = acc
				}
				if acc.Arguments.Len() == 0 {
					if args := rootResult.Get("arguments").String(); args != "" {
						acc.Arguments.WriteString(args)
					}
				}
			}

		case "response.output_item.done":
			if outputIndex := int(rootResult.Get("output_index").Int()); outputIndex >= 0 {
				item := rootResult.Get("item")
				if item.Get("type").String() == "function_call" {
					acc := toolCalls[outputIndex]
					if acc == nil {
						acc = &toolCallAccumulator{}
						toolCalls[outputIndex] = acc
					}
					if id := item.Get("call_id").String(); id != "" {
						acc.ID = id
					}
					if name := item.Get("name").String(); name != "" {
						acc.Name = name
					}
					if acc.Arguments.Len() == 0 {
						if args := item.Get("arguments").String(); args != "" {
							acc.Arguments.WriteString(args)
						}
					}
				}
			}

		case "response.completed":
			completedResp = rootResult.Get("response")
			usageResult = completedResp.Get("usage")
			if id := completedResp.Get("id").String(); id != "" {
				responseID = id
			}
			if m := completedResp.Get("model").String(); m != "" {
				model = m
			}
			if created := completedResp.Get("created_at").Int(); created > 0 {
				createdAt = created
			}
		}
	}

	if completedResp.Exists() {
		adoptCompletedResponseOutput(completedResp, &contentParts, &reasoningParts, toolCalls)
	}

	return buildOpenAINonStreamResponse(originalRequestRawJSON, responseID, model, createdAt, strings.Join(contentParts, ""), strings.Join(reasoningParts, ""), toolCalls, usageResult)
}

func adoptCompletedResponseOutput(responseResult gjson.Result, contentParts, reasoningParts *[]string, toolCalls map[int]*toolCallAccumulator) {
	if !responseResult.Exists() {
		return
	}
	outputResult := responseResult.Get("output")
	if !outputResult.IsArray() {
		return
	}

	if len(*contentParts) > 0 && len(*reasoningParts) > 0 && len(toolCalls) > 0 {
		return
	}

	outputArray := outputResult.Array()
	for idx, outputItem := range outputArray {
		switch outputItem.Get("type").String() {
		case "reasoning":
			if len(*reasoningParts) > 0 {
				continue
			}
			if summaryResult := outputItem.Get("summary"); summaryResult.IsArray() {
				for _, summaryItem := range summaryResult.Array() {
					if summaryItem.Get("type").String() == "summary_text" {
						if text := summaryItem.Get("text").String(); text != "" {
							*reasoningParts = append(*reasoningParts, text)
						}
					}
				}
			}

		case "message":
			if len(*contentParts) > 0 {
				continue
			}
			if contentResult := outputItem.Get("content"); contentResult.IsArray() {
				for _, contentItem := range contentResult.Array() {
					if contentItem.Get("type").String() == "output_text" {
						if text := contentItem.Get("text").String(); text != "" {
							*contentParts = append(*contentParts, text)
						}
					}
				}
			}

		case "function_call":
			if len(toolCalls) > 0 {
				continue
			}
			acc := &toolCallAccumulator{}
			acc.ID = outputItem.Get("call_id").String()
			acc.Name = outputItem.Get("name").String()
			if args := outputItem.Get("arguments").String(); args != "" {
				acc.Arguments.WriteString(args)
			}
			toolCalls[idx] = acc
		}
	}
}

func buildOpenAINonStreamResponse(originalRequestRawJSON []byte, responseID, model string, createdAt int64, contentText, reasoningText string, toolCalls map[int]*toolCallAccumulator, usageResult gjson.Result) []byte {
	unixTimestamp := time.Now().Unix()

	template := []byte(`{"id":"","object":"chat.completion","created":123456,"model":"model","choices":[{"index":0,"message":{"role":"assistant","content":null,"reasoning_content":null,"tool_calls":null},"finish_reason":null,"native_finish_reason":null}]}`)

	// Extract and set the model version.
	if model != "" {
		template, _ = sjson.SetBytes(template, "model", model)
	}

	// Extract and set the creation timestamp.
	if createdAt > 0 {
		template, _ = sjson.SetBytes(template, "created", createdAt)
	} else {
		template, _ = sjson.SetBytes(template, "created", unixTimestamp)
	}

	// Extract and set the response ID.
	if responseID != "" {
		template, _ = sjson.SetBytes(template, "id", responseID)
	}

	// Extract and set usage metadata (token counts).
	if usageResult.Exists() {
		if outputTokensResult := usageResult.Get("output_tokens"); outputTokensResult.Exists() {
			template, _ = sjson.SetBytes(template, "usage.completion_tokens", outputTokensResult.Int())
		}
		if totalTokensResult := usageResult.Get("total_tokens"); totalTokensResult.Exists() {
			template, _ = sjson.SetBytes(template, "usage.total_tokens", totalTokensResult.Int())
		}
		if inputTokensResult := usageResult.Get("input_tokens"); inputTokensResult.Exists() {
			template, _ = sjson.SetBytes(template, "usage.prompt_tokens", inputTokensResult.Int())
		}
		if cachedTokensResult := usageResult.Get("input_tokens_details.cached_tokens"); cachedTokensResult.Exists() {
			template, _ = sjson.SetBytes(template, "usage.prompt_tokens_details.cached_tokens", cachedTokensResult.Int())
		}
		if reasoningTokensResult := usageResult.Get("output_tokens_details.reasoning_tokens"); reasoningTokensResult.Exists() {
			template, _ = sjson.SetBytes(template, "usage.completion_tokens_details.reasoning_tokens", reasoningTokensResult.Int())
		}
	}

	if contentText != "" {
		template, _ = sjson.SetBytes(template, "choices.0.message.content", contentText)
		template, _ = sjson.SetBytes(template, "choices.0.message.role", "assistant")
	}

	if reasoningText != "" {
		template, _ = sjson.SetBytes(template, "choices.0.message.reasoning_content", reasoningText)
		template, _ = sjson.SetBytes(template, "choices.0.message.role", "assistant")
	}

	if len(toolCalls) > 0 {
		template, _ = sjson.SetRawBytes(template, "choices.0.message.tool_calls", []byte(`[]`))
		for _, toolCall := range orderedToolCalls(toolCalls) {
			functionCallTemplate := []byte(`{"id":"","type":"function","function":{"name":"","arguments":""}}`)
			functionCallTemplate, _ = sjson.SetBytes(functionCallTemplate, "id", toolCall.ID)

			name := toolCall.Name
			rev := buildReverseMapFromOriginalOpenAI(originalRequestRawJSON)
			if orig, ok := rev[name]; ok {
				name = orig
			}
			functionCallTemplate, _ = sjson.SetBytes(functionCallTemplate, "function.name", name)
			functionCallTemplate, _ = sjson.SetBytes(functionCallTemplate, "function.arguments", toolCall.Arguments.String())
			template, _ = sjson.SetRawBytes(template, "choices.0.message.tool_calls.-1", functionCallTemplate)
		}
		template, _ = sjson.SetBytes(template, "choices.0.message.role", "assistant")
	}

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}
	template, _ = sjson.SetBytes(template, "choices.0.finish_reason", finishReason)
	template, _ = sjson.SetBytes(template, "choices.0.native_finish_reason", finishReason)

	return template
}

func convertCompletedCodexResponseToOpenAI(originalRequestRawJSON []byte, responseResult gjson.Result) []byte {
	var contentParts []string
	var reasoningParts []string
	toolCalls := make(map[int]*toolCallAccumulator)
	adoptCompletedResponseOutput(responseResult, &contentParts, &reasoningParts, toolCalls)

	return buildOpenAINonStreamResponse(
		originalRequestRawJSON,
		responseResult.Get("id").String(),
		responseResult.Get("model").String(),
		responseResult.Get("created_at").Int(),
		strings.Join(contentParts, ""),
		strings.Join(reasoningParts, ""),
		toolCalls,
		responseResult.Get("usage"),
	)
}

func orderedToolCalls(toolCalls map[int]*toolCallAccumulator) []*toolCallAccumulator {
	if len(toolCalls) == 0 {
		return nil
	}
	indices := make([]int, 0, len(toolCalls))
	for idx := range toolCalls {
		indices = append(indices, idx)
	}
	sort.Ints(indices)
	ordered := make([]*toolCallAccumulator, 0, len(indices))
	for _, idx := range indices {
		ordered = append(ordered, toolCalls[idx])
	}
	return ordered
}

// buildReverseMapFromOriginalOpenAI builds a map of shortened tool name -> original tool name
// from the original OpenAI-style request JSON using the same shortening logic.
func buildReverseMapFromOriginalOpenAI(original []byte) map[string]string {
	tools := gjson.GetBytes(original, "tools")
	rev := map[string]string{}
	if tools.IsArray() && len(tools.Array()) > 0 {
		var names []string
		arr := tools.Array()
		for i := 0; i < len(arr); i++ {
			t := arr[i]
			if t.Get("type").String() != "function" {
				continue
			}
			fn := t.Get("function")
			if !fn.Exists() {
				continue
			}
			if v := fn.Get("name"); v.Exists() {
				names = append(names, v.String())
			}
		}
		if len(names) > 0 {
			m := buildShortNameMap(names)
			for orig, short := range m {
				rev[short] = orig
			}
		}
	}
	return rev
}
