package claude

import (
	"context"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

type fakeGinHeaders map[string]string

func (f fakeGinHeaders) GetHeader(name string) string { return f[name] }

func claudeCLICtx() context.Context {
	return context.WithValue(context.Background(), "gin", fakeGinHeaders{
		"User-Agent": "claude-cli/2.1.76 (external, sdk-cli)",
	})
}

func vscodeCtx() context.Context {
	return context.WithValue(context.Background(), "gin", fakeGinHeaders{
		"User-Agent": "codex_exec/0.98.0 (Mac OS 26.3.0; arm64) vscode/1.112.0",
		"Originator": "codex_exec",
	})
}

func claudeVSCodeCtx() context.Context {
	return context.WithValue(context.Background(), "gin", fakeGinHeaders{
		"User-Agent": "claude-cli/2.1.76 (external, claude-vscode, agent-sdk/0.2.76)",
	})
}

func TestConvertCodexResponseToClaude_WebSearchCallDoneEmitsSyntheticToolCallText(t *testing.T) {
	raw := []byte(`data: {"type":"response.output_item.done","item":{"id":"ws_123","type":"web_search_call","status":"completed","action":{"type":"search","query":"张雪峰 去世 辟谣","queries":["张雪峰 去世 辟谣","site:weibo.com 张雪峰"]}},"output_index":1,"sequence_number":8}`)

	var param any
	out := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", nil, nil, raw, &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 output chunk, got %d", len(out))
	}

	chunk := string(out[0])
	if !strings.Contains(chunk, "event: content_block_start") {
		t.Fatalf("expected content_block_start event, got %q", chunk)
	}
	if !strings.Contains(chunk, "event: content_block_delta") {
		t.Fatalf("expected content_block_delta event, got %q", chunk)
	}
	if !strings.Contains(chunk, "Searched: 张雪峰 去世 辟谣") {
		t.Fatalf("expected visible searched progress text, got %q", chunk)
	}
	if !strings.Contains(chunk, "\\u003ctool_call\\u003e") {
		t.Fatalf("expected synthetic <tool_call> marker, got %q", chunk)
	}
	if !strings.Contains(chunk, "\\\"name\\\":\\\"web_search\\\"") {
		t.Fatalf("expected web_search tool name, got %q", chunk)
	}
	if !strings.Contains(chunk, "\\\"query\\\":\\\"张雪峰 去世 辟谣\\\"") {
		t.Fatalf("expected synthetic query payload, got %q", chunk)
	}

	if got := param.(*ConvertCodexResponseToClaudeParams).BlockIndex; got != 1 {
		t.Fatalf("expected block index to advance to 1, got %d", got)
	}
}

func TestConvertCodexResponseToClaude_WebSearchCallAddedEmitsEarlySyntheticToolCallText(t *testing.T) {
	originalRequest := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":[{"type":"text","text":"Perform a web search for the query: 2026 张雪峰 去世 怎么死的 辟谣"}]}],"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":8}]}`)
	raw := []byte(`data: {"type":"response.output_item.added","item":{"id":"ws_123","type":"web_search_call","status":"in_progress"},"output_index":1,"sequence_number":4}`)

	var param any
	out := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", originalRequest, nil, raw, &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 output chunk, got %d", len(out))
	}

	chunk := string(out[0])
	if !strings.Contains(chunk, "Searching the web.") {
		t.Fatalf("expected visible searching progress text, got %q", chunk)
	}
	if !strings.Contains(chunk, "\\u003ctool_call\\u003e") {
		t.Fatalf("expected early synthetic <tool_call> marker, got %q", chunk)
	}
	if !strings.Contains(chunk, "\\\"query\\\":\\\"2026 张雪峰 去世 怎么死的 辟谣\\\"") {
		t.Fatalf("expected inferred web search query, got %q", chunk)
	}
	if _, ok := param.(*ConvertCodexResponseToClaudeParams).EmittedSyntheticWebSearchStarts["ws_123"]; !ok {
		t.Fatalf("expected synthetic web search call marker to be tracked")
	}
}

func TestConvertCodexResponseToClaude_ResponseCreatedPreEmitsSyntheticWebSearchStart(t *testing.T) {
	originalRequest := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":[{"type":"text","text":"Perform a web search for the query: 2026 张雪峰 去世 怎么死的 辟谣"}]}],"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":8}]}`)
	raw := []byte(`data: {"type":"response.created","response":{"id":"resp_123","model":"gpt-5.4","status":"in_progress"}}`)

	var param any
	out := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", originalRequest, nil, raw, &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 output chunk, got %d", len(out))
	}

	chunk := string(out[0])
	if !strings.Contains(chunk, "event: message_start") {
		t.Fatalf("expected message_start event, got %q", chunk)
	}
	if !strings.Contains(chunk, "Searching the web.") {
		t.Fatalf("expected pre-emitted searching progress text, got %q", chunk)
	}
	if !strings.Contains(chunk, "\\u003ctool_call\\u003e") {
		t.Fatalf("expected early synthetic <tool_call> marker, got %q", chunk)
	}
	if got := param.(*ConvertCodexResponseToClaudeParams).BlockIndex; got != 1 {
		t.Fatalf("expected block index to advance to 1 after pre-emit, got %d", got)
	}
	if !param.(*ConvertCodexResponseToClaudeParams).SkipNextSyntheticWebSearchStart {
		t.Fatalf("expected next synthetic web search start to be skipped")
	}
}

func TestConvertCodexResponseToClaude_FirstAddedAfterPreEmitDoesNotDuplicate(t *testing.T) {
	originalRequest := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":[{"type":"text","text":"Perform a web search for the query: 2026 张雪峰 去世 怎么死的 辟谣"}]}],"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":8}]}`)
	created := []byte(`data: {"type":"response.created","response":{"id":"resp_123","model":"gpt-5.4","status":"in_progress"}}`)
	added := []byte(`data: {"type":"response.output_item.added","item":{"id":"ws_123","type":"web_search_call","status":"in_progress"},"output_index":1,"sequence_number":4}`)

	var param any
	_ = ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", originalRequest, nil, created, &param)
	out := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", originalRequest, nil, added, &param)

	if len(out) != 1 {
		t.Fatalf("expected 1 output chunk, got %d", len(out))
	}
	if got := string(out[0]); strings.TrimSpace(got) != "" {
		t.Fatalf("expected first added event after pre-emit to be suppressed, got %q", got)
	}
	if _, ok := param.(*ConvertCodexResponseToClaudeParams).EmittedSyntheticWebSearchStarts["ws_123"]; !ok {
		t.Fatalf("expected first real web search start to be tracked after suppression")
	}
	if param.(*ConvertCodexResponseToClaudeParams).SkipNextSyntheticWebSearchStart {
		t.Fatalf("expected skip flag to be cleared after first real start")
	}
}

func TestConvertCodexResponseToClaude_ResponseCreatedPreEmitsVSCodeSearchProgress(t *testing.T) {
	originalRequest := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"用 websearch 搜索今天的新闻"}],"tools":[{"name":"WebSearch","input_schema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}]}`)
	raw := []byte(`data: {"type":"response.created","response":{"id":"resp_123","model":"gpt-5.4","status":"in_progress"}}`)

	var param any
	out := ConvertCodexResponseToClaude(claudeVSCodeCtx(), "gpt-5.4", originalRequest, nil, raw, &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 output chunk, got %d", len(out))
	}

	chunk := string(out[0])
	if !strings.Contains(chunk, "event: message_start") {
		t.Fatalf("expected message_start event, got %q", chunk)
	}
	if !strings.Contains(chunk, "Searching the web for: 用 websearch 搜索今天的新闻") {
		t.Fatalf("expected early VSCode search progress thinking, got %q", chunk)
	}
}

func TestConvertCodexResponseToClaude_ResponseCreatedDoesNotPreEmitSearchForCodeAnalysis(t *testing.T) {
	originalRequest := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"看一下当前的项目代码，帮我简单分析一下即可"}],"tools":[{"name":"WebSearch","input_schema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}]}`)
	raw := []byte(`data: {"type":"response.created","response":{"id":"resp_123","model":"gpt-5.4","status":"in_progress"}}`)

	var param any
	out := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", originalRequest, nil, raw, &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 output chunk, got %d", len(out))
	}

	chunk := string(out[0])
	if !strings.Contains(chunk, "event: message_start") {
		t.Fatalf("expected message_start event, got %q", chunk)
	}
	if strings.Contains(chunk, "Searching the web.") {
		t.Fatalf("expected non-search prompt to skip early websearch text, got %q", chunk)
	}
}

func TestConvertCodexResponseToClaude_WebSearchCallAddedEmitsEarlySyntheticToolCallTextForClaudeCodeTool(t *testing.T) {
	originalRequest := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"2026 张雪峰去世你知道吗，他是怎么死的，为什么？"}],"tools":[{"name":"WebSearch","input_schema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}]}`)
	raw := []byte(`data: {"type":"response.output_item.added","item":{"id":"ws_123","type":"web_search_call","status":"in_progress"},"output_index":1,"sequence_number":4}`)

	var param any
	out := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", originalRequest, nil, raw, &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 output chunk, got %d", len(out))
	}

	chunk := string(out[0])
	if !strings.Contains(chunk, "Searching the web.") {
		t.Fatalf("expected visible searching progress text, got %q", chunk)
	}
	if !strings.Contains(chunk, "\\u003ctool_call\\u003e") {
		t.Fatalf("expected early synthetic <tool_call> marker, got %q", chunk)
	}
	if !strings.Contains(chunk, "\\\"query\\\":\\\"2026 张雪峰去世你知道吗，他是怎么死的，为什么？\\\"") {
		t.Fatalf("expected inferred search query from generic WebSearch tool, got %q", chunk)
	}
}

func TestConvertCodexResponseToClaude_WebSearchCallDoneStillEmitsCompletedProgressAfterEarlyEmit(t *testing.T) {
	originalRequest := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":[{"type":"text","text":"Perform a web search for the query: 2026 张雪峰 去世 怎么死的 辟谣"}]}],"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":8}]}`)
	added := []byte(`data: {"type":"response.output_item.added","item":{"id":"ws_123","type":"web_search_call","status":"in_progress"},"output_index":1,"sequence_number":4}`)
	done := []byte(`data: {"type":"response.output_item.done","item":{"id":"ws_123","type":"web_search_call","status":"completed","action":{"type":"search","query":"2026 张雪峰 去世 怎么死的 辟谣"}},"output_index":1,"sequence_number":8}`)

	var param any
	_ = ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", originalRequest, nil, added, &param)
	out := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", originalRequest, nil, done, &param)

	if len(out) != 1 {
		t.Fatalf("expected 1 output chunk, got %d", len(out))
	}
	chunk := string(out[0])
	if !strings.Contains(chunk, "Searched: 2026 张雪峰 去世 怎么死的 辟谣") {
		t.Fatalf("expected completed search progress to be emitted, got %q", chunk)
	}
}

func TestConvertCodexResponseToClaudeNonStream_WebSearchCallBecomesTextBlock(t *testing.T) {
	raw := []byte(`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.4","usage":{"input_tokens":10,"output_tokens":20},"output":[{"type":"web_search_call","status":"completed","action":{"type":"search","query":"2026 张雪峰 去世 怎么死的"}}]}}`)

	out := ConvertCodexResponseToClaudeNonStream(claudeCLICtx(), "gpt-5.4", nil, nil, raw, nil)
	if got := gjson.GetBytes(out, "content.0.type").String(); got != "text" {
		t.Fatalf("content.0.type = %q, want %q; out=%s", got, "text", string(out))
	}
	text := gjson.GetBytes(out, "content.0.text").String()
	if !strings.Contains(text, "<tool_call>") {
		t.Fatalf("expected synthetic <tool_call> marker, got %q", text)
	}
	if !strings.Contains(text, "\"query\":\"2026 张雪峰 去世 怎么死的\"") {
		t.Fatalf("expected query text in synthetic tool call, got %q", text)
	}
}

func TestConvertCodexResponseToClaude_WebSearchSyntheticTagSuppressedForVSCode(t *testing.T) {
	originalRequest := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"OpenAI Codex app 最新官方信息是什么？给我来源。"}],"tools":[{"name":"WebSearch","input_schema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}]}`)
	raw := []byte(`data: {"type":"response.output_item.added","item":{"id":"ws_123","type":"web_search_call","status":"in_progress"},"output_index":1,"sequence_number":4}`)

	var param any
	out := ConvertCodexResponseToClaude(vscodeCtx(), "gpt-5.4", originalRequest, nil, raw, &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 output chunk, got %d", len(out))
	}
	chunk := string(out[0])
	if strings.Contains(chunk, "\\u003ctool_call\\u003e") {
		t.Fatalf("expected VSCode path to suppress synthetic tool tag, got %q", chunk)
	}
	if !strings.Contains(chunk, "Searching the web for: OpenAI Codex app 最新官方信息是什么？给我来源。") {
		t.Fatalf("expected VSCode path to emit concise progress thinking, got %q", chunk)
	}
}

func TestConvertCodexResponseToClaude_WebSearchSyntheticTagSuppressedForClaudeVSCode(t *testing.T) {
	originalRequest := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"OpenAI Codex app 最新官方信息是什么？给我来源。"}],"tools":[{"name":"WebSearch","input_schema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}]}`)
	raw := []byte(`data: {"type":"response.output_item.added","item":{"id":"ws_123","type":"web_search_call","status":"in_progress"},"output_index":1,"sequence_number":4}`)

	var param any
	out := ConvertCodexResponseToClaude(claudeVSCodeCtx(), "gpt-5.4", originalRequest, nil, raw, &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 output chunk, got %d", len(out))
	}
	chunk := string(out[0])
	if strings.Contains(chunk, "\\u003ctool_call\\u003e") {
		t.Fatalf("expected Claude VSCode path to suppress synthetic tool tag, got %q", chunk)
	}
	if !strings.Contains(chunk, "Searching the web for: OpenAI Codex app 最新官方信息是什么？给我来源。") {
		t.Fatalf("expected Claude VSCode path to emit concise progress thinking, got %q", chunk)
	}
}

func TestConvertCodexResponseToClaude_ReasoningSummarySuppressedForClaudeVSCode(t *testing.T) {
	raw := []byte(`data: {"type":"response.reasoning_summary_text.delta","delta":"**Considering news sources**","item_id":"rs_1","output_index":1,"sequence_number":11,"summary_index":0}`)

	var param any
	out := ConvertCodexResponseToClaude(claudeVSCodeCtx(), "gpt-5.4", nil, nil, raw, &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 output chunk, got %d", len(out))
	}
	if got := strings.TrimSpace(string(out[0])); got != "" {
		t.Fatalf("expected Claude VSCode path to suppress reasoning summary thinking, got %q", got)
	}
}
