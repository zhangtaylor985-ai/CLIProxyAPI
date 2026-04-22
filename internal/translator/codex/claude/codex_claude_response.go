// Package claude provides response translation functionality for Codex to Claude Code API compatibility.
// This package handles the conversion of Codex API responses into Claude Code-compatible
// Server-Sent Events (SSE) format, implementing a sophisticated state machine that manages
// different response types including text content, thinking processes, and function calls.
// The translation ensures proper sequencing of SSE events and maintains state across
// multiple response chunks to provide a seamless streaming experience.
package claude

import (
	"bytes"
	"context"
	"strings"

	translatorcommon "github.com/router-for-me/CLIProxyAPI/v6/internal/translator/common"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/translator/gptinclaude"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var (
	dataTag = []byte("data:")
)

// ConvertCodexResponseToClaudeParams holds parameters for response conversion.
type ConvertCodexResponseToClaudeParams struct {
	HasToolCall                        bool
	BlockIndex                         int
	HasReceivedArgumentsDelta          bool
	HasTextDelta                       bool
	TextBlockOpen                      bool
	ToolBlockOpen                      bool
	ToolStopPending                    bool
	LastBuiltinWebSearchQuery          string
	EmittedSyntheticWebSearchStarts    map[string]struct{}
	EmittedSyntheticWebSearchCompletes map[string]struct{}
}

// ConvertCodexResponseToClaude performs sophisticated streaming response format conversion.
// This function implements a complex state machine that translates Codex API responses
// into Claude Code-compatible Server-Sent Events (SSE) format. It manages different response types
// and handles state transitions between content blocks, thinking processes, and function calls.
//
// Response type states: 0=none, 1=content, 2=thinking, 3=function
// The function maintains state across multiple calls to ensure proper SSE event sequencing.
//
// Parameters:
//   - ctx: The context for the request, used for cancellation and timeout handling
//   - modelName: The name of the model being used for the response (unused in current implementation)
//   - rawJSON: The raw JSON response from the Codex API
//   - param: A pointer to a parameter object for maintaining state between calls
//
// Returns:
//   - [][]byte: A slice of Claude Code-compatible JSON responses
func ConvertCodexResponseToClaude(ctx context.Context, _ string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) [][]byte {
	if *param == nil {
		initialQuery := gptinclaude.InferBuiltinWebSearchQuery(originalRequestRawJSON)
		*param = &ConvertCodexResponseToClaudeParams{
			HasToolCall:                        false,
			BlockIndex:                         0,
			LastBuiltinWebSearchQuery:          initialQuery,
			EmittedSyntheticWebSearchStarts:    map[string]struct{}{},
			EmittedSyntheticWebSearchCompletes: map[string]struct{}{},
		}
	}

	// log.Debugf("rawJSON: %s", string(rawJSON))
	if !bytes.HasPrefix(rawJSON, dataTag) {
		return [][]byte{}
	}
	rawJSON = bytes.TrimSpace(rawJSON[5:])

	output := make([]byte, 0, 512)
	rootResult := gjson.ParseBytes(rawJSON)
	typeResult := rootResult.Get("type")
	typeStr := typeResult.String()
	clientKind := gptinclaude.DetectClientKind(ctx)
	var template []byte
	if typeStr == "response.created" {
		template = []byte(`{"type":"message_start","message":{"id":"","type":"message","role":"assistant","model":"claude-opus-4-1-20250805","stop_sequence":null,"usage":{},"content":[],"stop_reason":null}}`)
		template, _ = sjson.SetBytes(template, "message.model", rootResult.Get("response.model").String())
		template, _ = sjson.SetBytes(template, "message.id", rootResult.Get("response.id").String())
		template, _ = sjson.SetRawBytes(template, "message.usage", buildClaudeUsageDefaults())

		output = translatorcommon.AppendSSEEventBytes(output, "message_start", template, 2)
	} else if typeStr == "response.reasoning_summary_part.added" {
		if !gptinclaude.ShouldSurfaceReasoningSummaryAsThinking(clientKind) {
			return [][]byte{output}
		}
		template = []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`)
		template, _ = sjson.SetBytes(template, "index", (*param).(*ConvertCodexResponseToClaudeParams).BlockIndex)

		output = translatorcommon.AppendSSEEventBytes(output, "content_block_start", template, 2)
	} else if typeStr == "response.reasoning_summary_text.delta" {
		if !gptinclaude.ShouldSurfaceReasoningSummaryAsThinking(clientKind) {
			return [][]byte{output}
		}
		template = []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":""}}`)
		template, _ = sjson.SetBytes(template, "index", (*param).(*ConvertCodexResponseToClaudeParams).BlockIndex)
		template, _ = sjson.SetBytes(template, "delta.thinking", rootResult.Get("delta").String())

		output = translatorcommon.AppendSSEEventBytes(output, "content_block_delta", template, 2)
	} else if typeStr == "response.reasoning_summary_part.done" {
		if !gptinclaude.ShouldSurfaceReasoningSummaryAsThinking(clientKind) {
			return [][]byte{output}
		}
		template = []byte(`{"type":"content_block_stop","index":0}`)
		template, _ = sjson.SetBytes(template, "index", (*param).(*ConvertCodexResponseToClaudeParams).BlockIndex)
		(*param).(*ConvertCodexResponseToClaudeParams).BlockIndex++

		output = translatorcommon.AppendSSEEventBytes(output, "content_block_stop", template, 2)

	} else if typeStr == "response.content_part.added" {
		template = []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		params := (*param).(*ConvertCodexResponseToClaudeParams)
		template, _ = sjson.SetBytes(template, "index", params.BlockIndex)
		params.TextBlockOpen = true

		output = translatorcommon.AppendSSEEventBytes(output, "content_block_start", template, 2)
	} else if typeStr == "response.output_text.delta" {
		params := (*param).(*ConvertCodexResponseToClaudeParams)
		params.HasTextDelta = true
		template = []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":""}}`)
		template, _ = sjson.SetBytes(template, "index", params.BlockIndex)
		template, _ = sjson.SetBytes(template, "delta.text", rootResult.Get("delta").String())

		output = translatorcommon.AppendSSEEventBytes(output, "content_block_delta", template, 2)
	} else if typeStr == "response.content_part.done" {
		params := (*param).(*ConvertCodexResponseToClaudeParams)
		template = []byte(`{"type":"content_block_stop","index":0}`)
		template, _ = sjson.SetBytes(template, "index", params.BlockIndex)
		params.BlockIndex++
		params.TextBlockOpen = false

		output = translatorcommon.AppendSSEEventBytes(output, "content_block_stop", template, 2)
	} else if typeStr == "response.completed" {
		params := (*param).(*ConvertCodexResponseToClaudeParams)
		if params.ToolBlockOpen {
			output = append(output, finalizeCodexToolUseBlock(params)...)
		}
		template = []byte(`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{}}`)
		p := params.HasToolCall
		stopReason := rootResult.Get("response.stop_reason").String()
		if p {
			template, _ = sjson.SetBytes(template, "delta.stop_reason", "tool_use")
		} else if stopReason == "max_tokens" || stopReason == "stop" {
			template, _ = sjson.SetBytes(template, "delta.stop_reason", stopReason)
		} else {
			template, _ = sjson.SetBytes(template, "delta.stop_reason", "end_turn")
		}
		template, _ = sjson.SetRawBytes(template, "usage", buildClaudeUsageDefaults())
		inputTokens, outputTokens, cachedTokens := extractResponsesUsage(rootResult.Get("response.usage"))
		template, _ = sjson.SetBytes(template, "usage.input_tokens", inputTokens)
		template, _ = sjson.SetBytes(template, "usage.output_tokens", outputTokens)
		template, _ = sjson.SetBytes(template, "usage.cache_read_input_tokens", cachedTokens)
		if serviceTier := strings.TrimSpace(rootResult.Get("response.service_tier").String()); serviceTier != "" {
			template, _ = sjson.SetBytes(template, "usage.service_tier", serviceTier)
		}

		output = translatorcommon.AppendSSEEventBytes(output, "message_delta", template, 2)
		output = translatorcommon.AppendSSEEventBytes(output, "message_stop", []byte(`{"type":"message_stop"}`), 2)
	} else if typeStr == "response.output_item.added" {
		itemResult := rootResult.Get("item")
		itemType := itemResult.Get("type").String()
		if itemType == "function_call" {
			params := (*param).(*ConvertCodexResponseToClaudeParams)
			params.HasToolCall = true
			params.HasReceivedArgumentsDelta = false
			params.ToolStopPending = false
			params.ToolBlockOpen = true
			toolName := itemResult.Get("name").String()
			{
				// Restore original tool name if shortened
				rev := buildReverseMapFromClaudeOriginalShortToOriginal(originalRequestRawJSON)
				if orig, ok := rev[toolName]; ok {
					toolName = orig
				}
			}
			template = []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"","name":"","input":{}}}`)
			template, _ = sjson.SetBytes(template, "index", params.BlockIndex)
			template, _ = sjson.SetBytes(template, "content_block.id", util.SanitizeClaudeToolID(itemResult.Get("call_id").String()))
			template, _ = sjson.SetBytes(template, "content_block.name", toolName)

			output = translatorcommon.AppendSSEEventBytes(output, "content_block_start", template, 2)
		} else if itemType == "web_search_call" {
			itemID := strings.TrimSpace(itemResult.Get("id").String())
			if itemID != "" {
				if _, exists := (*param).(*ConvertCodexResponseToClaudeParams).EmittedSyntheticWebSearchStarts[itemID]; exists {
					return [][]byte{output}
				}
			}
			if query := strings.TrimSpace(itemResult.Get("action.query").String()); query != "" {
				(*param).(*ConvertCodexResponseToClaudeParams).LastBuiltinWebSearchQuery = query
			}
			if gptinclaude.ShouldEmitSyntheticWebSearchTag(clientKind) {
				syntheticText := gptinclaude.BuildSyntheticWebSearchToolCallText(gjson.Result{})
				if syntheticText != "" {
					blockIndex := (*param).(*ConvertCodexResponseToClaudeParams).BlockIndex

					template = []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
					template, _ = sjson.SetBytes(template, "index", blockIndex)
					output = translatorcommon.AppendSSEEventBytes(output, "content_block_start", template, 2)

					template = []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":""}}`)
					template, _ = sjson.SetBytes(template, "index", blockIndex)
					template, _ = sjson.SetBytes(template, "delta.text", syntheticText)
					output = translatorcommon.AppendSSEEventBytes(output, "content_block_delta", template, 2)

					template = []byte(`{"type":"content_block_stop","index":0}`)
					template, _ = sjson.SetBytes(template, "index", blockIndex)
					output = translatorcommon.AppendSSEEventBytes(output, "content_block_stop", template, 2)

					(*param).(*ConvertCodexResponseToClaudeParams).BlockIndex++
				}
				if itemID != "" {
					(*param).(*ConvertCodexResponseToClaudeParams).EmittedSyntheticWebSearchStarts[itemID] = struct{}{}
				}
			} else if gptinclaude.ShouldEmitVSCodeWebSearchProgress(clientKind) {
				blockIndex := (*param).(*ConvertCodexResponseToClaudeParams).BlockIndex
				progressThinking := gptinclaude.BuildVSCodeWebSearchProgressThinking(
					itemResult.Get("action"),
					(*param).(*ConvertCodexResponseToClaudeParams).LastBuiltinWebSearchQuery,
				)
				if progressThinking != "" {
					template = []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`)
					template, _ = sjson.SetBytes(template, "index", blockIndex)
					output = translatorcommon.AppendSSEEventBytes(output, "content_block_start", template, 2)

					template = []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":""}}`)
					template, _ = sjson.SetBytes(template, "index", blockIndex)
					template, _ = sjson.SetBytes(template, "delta.thinking", progressThinking)
					output = translatorcommon.AppendSSEEventBytes(output, "content_block_delta", template, 2)

					template = []byte(`{"type":"content_block_stop","index":0}`)
					template, _ = sjson.SetBytes(template, "index", blockIndex)
					output = translatorcommon.AppendSSEEventBytes(output, "content_block_stop", template, 2)

					(*param).(*ConvertCodexResponseToClaudeParams).BlockIndex++
				}
				if itemID != "" {
					(*param).(*ConvertCodexResponseToClaudeParams).EmittedSyntheticWebSearchStarts[itemID] = struct{}{}
				}
			}
		}
	} else if typeStr == "response.output_item.done" {
		itemResult := rootResult.Get("item")
		itemType := itemResult.Get("type").String()
		if itemType == "message" {
			params := (*param).(*ConvertCodexResponseToClaudeParams)
			if !params.HasTextDelta {
				output = append(output, emitFallbackMessageTextFromOutputItemDone(itemResult, params)...)
			}
		} else if itemType == "function_call" {
			params := (*param).(*ConvertCodexResponseToClaudeParams)
			if params.HasReceivedArgumentsDelta {
				output = append(output, finalizeCodexToolUseBlock(params)...)
			} else {
				params.ToolStopPending = true
			}
		} else if itemType == "web_search_call" {
			if query := strings.TrimSpace(itemResult.Get("action.query").String()); query != "" {
				(*param).(*ConvertCodexResponseToClaudeParams).LastBuiltinWebSearchQuery = query
			}
			itemID := strings.TrimSpace(itemResult.Get("id").String())
			if itemID != "" {
				if _, exists := (*param).(*ConvertCodexResponseToClaudeParams).EmittedSyntheticWebSearchCompletes[itemID]; exists {
					return [][]byte{output}
				}
			}
			syntheticText := ""
			if gptinclaude.ShouldEmitSyntheticWebSearchTag(clientKind) {
				syntheticText = gptinclaude.BuildSyntheticWebSearchToolCallText(itemResult.Get("action"))
			}
			if syntheticText != "" {
				blockIndex := (*param).(*ConvertCodexResponseToClaudeParams).BlockIndex

				template = []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
				template, _ = sjson.SetBytes(template, "index", blockIndex)
				output = translatorcommon.AppendSSEEventBytes(output, "content_block_start", template, 2)

				template = []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":""}}`)
				template, _ = sjson.SetBytes(template, "index", blockIndex)
				template, _ = sjson.SetBytes(template, "delta.text", syntheticText)
				output = translatorcommon.AppendSSEEventBytes(output, "content_block_delta", template, 2)

				template = []byte(`{"type":"content_block_stop","index":0}`)
				template, _ = sjson.SetBytes(template, "index", blockIndex)
				output = translatorcommon.AppendSSEEventBytes(output, "content_block_stop", template, 2)

				(*param).(*ConvertCodexResponseToClaudeParams).BlockIndex++
				if itemID != "" {
					(*param).(*ConvertCodexResponseToClaudeParams).EmittedSyntheticWebSearchCompletes[itemID] = struct{}{}
				}
			}
		}
	} else if typeStr == "response.function_call_arguments.delta" {
		params := (*param).(*ConvertCodexResponseToClaudeParams)
		params.HasReceivedArgumentsDelta = true
		template = []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":""}}`)
		template, _ = sjson.SetBytes(template, "index", params.BlockIndex)
		template, _ = sjson.SetBytes(template, "delta.partial_json", rootResult.Get("delta").String())

		output = translatorcommon.AppendSSEEventBytes(output, "content_block_delta", template, 2)
	} else if typeStr == "response.function_call_arguments.done" {
		// Some models (e.g. gpt-5.3-codex-spark) send function call arguments
		// in a single "done" event without preceding "delta" events.
		// Emit the full arguments as a single input_json_delta so the
		// downstream Claude client receives the complete tool input.
		// When delta events were already received, skip to avoid duplicating arguments.
		params := (*param).(*ConvertCodexResponseToClaudeParams)
		if !params.HasReceivedArgumentsDelta {
			if args := rootResult.Get("arguments").String(); args != "" {
				template = []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":""}}`)
				template, _ = sjson.SetBytes(template, "index", params.BlockIndex)
				template, _ = sjson.SetBytes(template, "delta.partial_json", args)

				output = translatorcommon.AppendSSEEventBytes(output, "content_block_delta", template, 2)
			}
		}
		params.HasReceivedArgumentsDelta = true
		if params.ToolStopPending {
			output = append(output, finalizeCodexToolUseBlock(params)...)
		}
	}

	return [][]byte{output}
}

// ConvertCodexResponseToClaudeNonStream converts a non-streaming Codex response to a non-streaming Claude Code response.
// This function processes the complete Codex response and transforms it into a single Claude Code-compatible
// JSON response. It handles message content, tool calls, reasoning content, and usage metadata, combining all
// the information into a single response that matches the Claude Code API format.
//
// Parameters:
//   - ctx: The context for the request, used for cancellation and timeout handling
//   - modelName: The name of the model being used for the response (unused in current implementation)
//   - rawJSON: The raw JSON response from the Codex API
//   - param: A pointer to a parameter object for the conversion (unused in current implementation)
//
// Returns:
//   - []byte: A Claude Code-compatible JSON response containing all message content and metadata
func ConvertCodexResponseToClaudeNonStream(ctx context.Context, _ string, originalRequestRawJSON, _ []byte, rawJSON []byte, _ *any) []byte {
	revNames := buildReverseMapFromClaudeOriginalShortToOriginal(originalRequestRawJSON)
	clientKind := gptinclaude.DetectClientKind(ctx)

	rootResult := gjson.ParseBytes(rawJSON)
	if rootResult.Get("type").String() != "response.completed" {
		return []byte{}
	}

	responseData := rootResult.Get("response")
	if !responseData.Exists() {
		return []byte{}
	}

	out := []byte(`{"id":"","type":"message","role":"assistant","model":"","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0}}`)
	out, _ = sjson.SetBytes(out, "id", responseData.Get("id").String())
	out, _ = sjson.SetBytes(out, "model", responseData.Get("model").String())
	inputTokens, outputTokens, cachedTokens := extractResponsesUsage(responseData.Get("usage"))
	out, _ = sjson.SetBytes(out, "usage.input_tokens", inputTokens)
	out, _ = sjson.SetBytes(out, "usage.output_tokens", outputTokens)
	if cachedTokens > 0 {
		out, _ = sjson.SetBytes(out, "usage.cache_read_input_tokens", cachedTokens)
	}

	hasToolCall := false

	if output := responseData.Get("output"); output.Exists() && output.IsArray() {
		output.ForEach(func(_, item gjson.Result) bool {
			switch item.Get("type").String() {
			case "reasoning":
				if !gptinclaude.ShouldSurfaceReasoningSummaryAsThinking(clientKind) {
					return true
				}
				thinkingBuilder := strings.Builder{}
				if summary := item.Get("summary"); summary.Exists() {
					if summary.IsArray() {
						summary.ForEach(func(_, part gjson.Result) bool {
							if txt := part.Get("text"); txt.Exists() {
								thinkingBuilder.WriteString(txt.String())
							} else {
								thinkingBuilder.WriteString(part.String())
							}
							return true
						})
					} else {
						thinkingBuilder.WriteString(summary.String())
					}
				}
				if thinkingBuilder.Len() == 0 {
					if content := item.Get("content"); content.Exists() {
						if content.IsArray() {
							content.ForEach(func(_, part gjson.Result) bool {
								if txt := part.Get("text"); txt.Exists() {
									thinkingBuilder.WriteString(txt.String())
								} else {
									thinkingBuilder.WriteString(part.String())
								}
								return true
							})
						} else {
							thinkingBuilder.WriteString(content.String())
						}
					}
				}
				if thinkingBuilder.Len() > 0 {
					block := []byte(`{"type":"thinking","thinking":""}`)
					block, _ = sjson.SetBytes(block, "thinking", thinkingBuilder.String())
					out, _ = sjson.SetRawBytes(out, "content.-1", block)
				}
			case "message":
				if content := item.Get("content"); content.Exists() {
					if content.IsArray() {
						content.ForEach(func(_, part gjson.Result) bool {
							if part.Get("type").String() == "output_text" {
								text := part.Get("text").String()
								if text != "" {
									block := []byte(`{"type":"text","text":""}`)
									block, _ = sjson.SetBytes(block, "text", text)
									out, _ = sjson.SetRawBytes(out, "content.-1", block)
								}
							}
							return true
						})
					} else {
						text := content.String()
						if text != "" {
							block := []byte(`{"type":"text","text":""}`)
							block, _ = sjson.SetBytes(block, "text", text)
							out, _ = sjson.SetRawBytes(out, "content.-1", block)
						}
					}
				}
			case "function_call":
				hasToolCall = true
				name := item.Get("name").String()
				if original, ok := revNames[name]; ok {
					name = original
				}

				toolBlock := []byte(`{"type":"tool_use","id":"","name":"","input":{}}`)
				toolBlock, _ = sjson.SetBytes(toolBlock, "id", util.SanitizeClaudeToolID(item.Get("call_id").String()))
				toolBlock, _ = sjson.SetBytes(toolBlock, "name", name)
				inputRaw := "{}"
				if argsStr := item.Get("arguments").String(); argsStr != "" && gjson.Valid(argsStr) {
					argsJSON := gjson.Parse(argsStr)
					if argsJSON.IsObject() {
						inputRaw = argsJSON.Raw
					}
				}
				toolBlock, _ = sjson.SetRawBytes(toolBlock, "input", []byte(inputRaw))
				out, _ = sjson.SetRawBytes(out, "content.-1", toolBlock)
			case "web_search_call":
				syntheticText := ""
				if gptinclaude.ShouldEmitSyntheticWebSearchTag(clientKind) {
					syntheticText = gptinclaude.BuildSyntheticWebSearchToolCallText(item.Get("action"))
				}
				if syntheticText != "" {
					block := []byte(`{"type":"text","text":""}`)
					block, _ = sjson.SetBytes(block, "text", syntheticText)
					out, _ = sjson.SetRawBytes(out, "content.-1", block)
				}
			}
			return true
		})
	}

	if stopReason := responseData.Get("stop_reason"); stopReason.Exists() && stopReason.String() != "" {
		out, _ = sjson.SetBytes(out, "stop_reason", stopReason.String())
	} else if hasToolCall {
		out, _ = sjson.SetBytes(out, "stop_reason", "tool_use")
	} else {
		out, _ = sjson.SetBytes(out, "stop_reason", "end_turn")
	}

	if stopSequence := responseData.Get("stop_sequence"); stopSequence.Exists() && stopSequence.String() != "" {
		out, _ = sjson.SetRawBytes(out, "stop_sequence", []byte(stopSequence.Raw))
	}

	return out
}

func extractResponsesUsage(usage gjson.Result) (int64, int64, int64) {
	if !usage.Exists() || usage.Type == gjson.Null {
		return 0, 0, 0
	}

	inputTokens := usage.Get("input_tokens").Int()
	outputTokens := usage.Get("output_tokens").Int()
	cachedTokens := usage.Get("input_tokens_details.cached_tokens").Int()

	if cachedTokens > 0 {
		if inputTokens >= cachedTokens {
			inputTokens -= cachedTokens
		} else {
			inputTokens = 0
		}
	}

	return inputTokens, outputTokens, cachedTokens
}

func buildClaudeUsageDefaults() []byte {
	return []byte(`{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":"standard","cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0},"inference_geo":"","iterations":[],"speed":"standard"}`)
}

func finalizeCodexToolUseBlock(params *ConvertCodexResponseToClaudeParams) []byte {
	if params == nil || !params.ToolBlockOpen {
		return nil
	}
	template := []byte(`{"type":"content_block_stop","index":0}`)
	template, _ = sjson.SetBytes(template, "index", params.BlockIndex)
	params.BlockIndex++
	params.ToolBlockOpen = false
	params.ToolStopPending = false
	params.HasReceivedArgumentsDelta = false
	return translatorcommon.AppendSSEEventBytes(nil, "content_block_stop", template, 2)
}

func emitFallbackMessageTextFromOutputItemDone(itemResult gjson.Result, params *ConvertCodexResponseToClaudeParams) []byte {
	if params == nil {
		return nil
	}
	contentResult := itemResult.Get("content")
	if !contentResult.Exists() || !contentResult.IsArray() {
		return nil
	}

	var textBuilder strings.Builder
	contentResult.ForEach(func(_, part gjson.Result) bool {
		if part.Get("type").String() != "output_text" {
			return true
		}
		if txt := part.Get("text").String(); txt != "" {
			textBuilder.WriteString(txt)
		}
		return true
	})

	text := textBuilder.String()
	if text == "" {
		return nil
	}

	var output []byte
	if !params.TextBlockOpen {
		template := []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		template, _ = sjson.SetBytes(template, "index", params.BlockIndex)
		params.TextBlockOpen = true
		output = translatorcommon.AppendSSEEventBytes(output, "content_block_start", template, 2)
	}

	template := []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":""}}`)
	template, _ = sjson.SetBytes(template, "index", params.BlockIndex)
	template, _ = sjson.SetBytes(template, "delta.text", text)
	output = translatorcommon.AppendSSEEventBytes(output, "content_block_delta", template, 2)

	if params.TextBlockOpen {
		template = []byte(`{"type":"content_block_stop","index":0}`)
		template, _ = sjson.SetBytes(template, "index", params.BlockIndex)
		output = translatorcommon.AppendSSEEventBytes(output, "content_block_stop", template, 2)
		params.TextBlockOpen = false
		params.BlockIndex++
	}
	params.HasTextDelta = true
	return output
}

// buildReverseMapFromClaudeOriginalShortToOriginal builds a map[short]original from original Claude request tools.
func buildReverseMapFromClaudeOriginalShortToOriginal(original []byte) map[string]string {
	tools := gjson.GetBytes(original, "tools")
	rev := map[string]string{}
	if !tools.IsArray() {
		return rev
	}
	var names []string
	arr := tools.Array()
	for i := 0; i < len(arr); i++ {
		n := arr[i].Get("name").String()
		if n != "" {
			names = append(names, n)
		}
	}
	if len(names) > 0 {
		m := buildShortNameMap(names)
		for orig, short := range m {
			rev[short] = orig
		}
	}
	return rev
}

func ClaudeTokenCount(ctx context.Context, count int64) []byte {
	return translatorcommon.ClaudeInputTokensJSON(count)
}
