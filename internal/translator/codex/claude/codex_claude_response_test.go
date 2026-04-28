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

func TestConvertCodexResponseToClaude_WebSearchCallAddedEmitsSyntheticToolCallTextForClaudeCLI(t *testing.T) {
	originalRequest := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":[{"type":"text","text":"Perform a web search for the query: 2026 张雪峰 去世 怎么死的 辟谣"}]}],"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":8}]}`)
	raw := []byte(`data: {"type":"response.output_item.added","item":{"id":"ws_123","type":"web_search_call","status":"in_progress"},"output_index":1,"sequence_number":4}`)

	var param any
	out := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", originalRequest, nil, raw, &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 output chunk, got %d", len(out))
	}

	chunk := string(out[0])
	if !strings.Contains(chunk, "Searching the web.") {
		t.Fatalf("expected claude cli added event to emit generic search progress, got %q", chunk)
	}
	if !strings.Contains(chunk, "\\u003ctool_call\\u003e") {
		t.Fatalf("expected claude cli added event to emit synthetic tool tag, got %q", chunk)
	}
	if _, ok := param.(*ConvertCodexResponseToClaudeParams).EmittedSyntheticWebSearchStarts["ws_123"]; !ok {
		t.Fatalf("expected web search start to be tracked")
	}
}

func TestConvertCodexResponseToClaude_ResponseCreatedDoesNotPreEmitSyntheticWebSearchStart(t *testing.T) {
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
	if strings.Contains(chunk, "Searching the web.") {
		t.Fatalf("expected response.created to avoid pre-emitting search progress, got %q", chunk)
	}
	if got := param.(*ConvertCodexResponseToClaudeParams).BlockIndex; got != 0 {
		t.Fatalf("expected block index to stay at 0 without pre-emit, got %d", got)
	}
}

func TestConvertCodexResponseToClaude_DuplicateAddedDoesNotDuplicateSyntheticWebSearchStart(t *testing.T) {
	originalRequest := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":[{"type":"text","text":"Perform a web search for the query: 2026 张雪峰 去世 怎么死的 辟谣"}]}],"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":8}]}`)
	added := []byte(`data: {"type":"response.output_item.added","item":{"id":"ws_123","type":"web_search_call","status":"in_progress"},"output_index":1,"sequence_number":4}`)

	var param any
	first := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", originalRequest, nil, added, &param)
	out := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", originalRequest, nil, added, &param)

	if len(first) != 1 {
		t.Fatalf("expected 1 first output chunk, got %d", len(first))
	}
	if !strings.Contains(string(first[0]), "Searching the web.") {
		t.Fatalf("expected first added event to emit generic search progress, got %q", string(first[0]))
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 second output chunk, got %d", len(out))
	}
	if got := string(out[0]); strings.TrimSpace(got) != "" {
		t.Fatalf("expected duplicate added event to be suppressed, got %q", got)
	}
	if _, ok := param.(*ConvertCodexResponseToClaudeParams).EmittedSyntheticWebSearchStarts["ws_123"]; !ok {
		t.Fatalf("expected first real web search start to be tracked")
	}
}

func TestConvertCodexResponseToClaude_ResponseCreatedDoesNotPreEmitVSCodeSearchProgress(t *testing.T) {
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
	if strings.Contains(chunk, "Searching the web") {
		t.Fatalf("expected response.created to avoid pre-emitting VSCode search progress, got %q", chunk)
	}
}

func TestConvertCodexResponseToClaude_WebSearchCallAddedEmitsGenericStartForClaudeCodeTool(t *testing.T) {
	originalRequest := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"2026 张雪峰去世你知道吗，他是怎么死的，为什么？"}],"tools":[{"name":"WebSearch","input_schema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}]}`)
	raw := []byte(`data: {"type":"response.output_item.added","item":{"id":"ws_123","type":"web_search_call","status":"in_progress"},"output_index":1,"sequence_number":4}`)

	var param any
	out := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", originalRequest, nil, raw, &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 output chunk, got %d", len(out))
	}

	chunk := string(out[0])
	if !strings.Contains(chunk, "Searching the web.") {
		t.Fatalf("expected claude cli added event to emit generic search progress for generic WebSearch tool, got %q", chunk)
	}
	if !strings.Contains(chunk, "\\u003ctool_call\\u003e") {
		t.Fatalf("expected claude cli added event to emit synthetic tool tag for generic WebSearch tool, got %q", chunk)
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

func TestConvertCodexResponseToClaude_ClaudeCLIMultiSearchKeepsSingleGenericStart(t *testing.T) {
	originalRequest := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":[{"type":"text","text":"Perform a web search for the query: today's top news"}]}],"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":8}]}`)
	events := [][]byte{
		[]byte(`data: {"type":"response.created","response":{"id":"resp_123","model":"gpt-5.4","status":"in_progress"}}`),
		[]byte(`data: {"type":"response.output_item.added","item":{"id":"ws_1","type":"web_search_call","status":"in_progress"},"output_index":1,"sequence_number":4}`),
		[]byte(`data: {"type":"response.output_item.done","item":{"id":"ws_1","type":"web_search_call","status":"completed","action":{"type":"search","query":"today top news Reuters"}},"output_index":1,"sequence_number":8}`),
		[]byte(`data: {"type":"response.output_item.added","item":{"id":"ws_2","type":"web_search_call","status":"in_progress"},"output_index":2,"sequence_number":9}`),
		[]byte(`data: {"type":"response.output_item.done","item":{"id":"ws_2","type":"web_search_call","status":"completed","action":{"type":"search","query":"site:reuters.com/world today top news"}},"output_index":2,"sequence_number":12}`),
	}

	var param any
	var transcript strings.Builder
	for _, event := range events {
		out := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", originalRequest, nil, event, &param)
		if len(out) != 1 {
			t.Fatalf("expected 1 output chunk, got %d", len(out))
		}
		transcript.WriteString(string(out[0]))
	}

	got := transcript.String()
	if count := strings.Count(got, "Searching the web."); count != 2 {
		t.Fatalf("expected one generic search start per real added event, got %d transcript=%q", count, got)
	}
	if count := strings.Count(got, "Searched: "); count != 2 {
		t.Fatalf("expected two concrete searched progress lines, got %d transcript=%q", count, got)
	}
}

func TestConvertCodexResponseToClaude_FunctionCallStreamingUnaffected(t *testing.T) {
	added := []byte(`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_123","name":"ReadFile"},"output_index":0,"sequence_number":1}`)
	argsDone := []byte(`data: {"type":"response.function_call_arguments.done","arguments":"{\"path\":\"README.md\"}","item_id":"call_123","output_index":0,"sequence_number":2}`)
	done := []byte(`data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_123","name":"ReadFile"},"output_index":0,"sequence_number":3}`)

	var param any
	addedOut := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", nil, nil, added, &param)
	if len(addedOut) != 1 {
		t.Fatalf("expected 1 added output chunk, got %d", len(addedOut))
	}
	addedChunk := string(addedOut[0])
	if !strings.Contains(addedChunk, "\"type\":\"tool_use\"") {
		t.Fatalf("expected function call to still become tool_use, got %q", addedChunk)
	}
	if !strings.Contains(addedChunk, "\"name\":\"ReadFile\"") {
		t.Fatalf("expected tool name to be preserved, got %q", addedChunk)
	}
	if strings.Contains(addedChunk, "\"type\":\"input_json_delta\"") {
		t.Fatalf("expected function call start to avoid empty input_json_delta, got %q", addedChunk)
	}

	argsOut := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", nil, nil, argsDone, &param)
	if len(argsOut) != 1 {
		t.Fatalf("expected 1 arguments output chunk, got %d", len(argsOut))
	}
	if !strings.Contains(string(argsOut[0]), "\\\"path\\\":\\\"README.md\\\"") {
		t.Fatalf("expected function call arguments delta to be preserved, got %q", string(argsOut[0]))
	}

	doneOut := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", nil, nil, done, &param)
	if len(doneOut) != 1 {
		t.Fatalf("expected 1 done output chunk, got %d", len(doneOut))
	}
	if !strings.Contains(string(doneOut[0]), "event: content_block_stop") {
		t.Fatalf("expected function call completion to emit content_block_stop, got %q", string(doneOut[0]))
	}
}

func TestConvertCodexResponseToClaude_FunctionCallDoneBeforeArgumentsWaitsForJSONCompletion(t *testing.T) {
	added := []byte(`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_123","name":"ReadFile"},"output_index":0,"sequence_number":1}`)
	done := []byte(`data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_123","name":"ReadFile"},"output_index":0,"sequence_number":2}`)
	argsDone := []byte(`data: {"type":"response.function_call_arguments.done","arguments":"{\"path\":\"README.md\"}","item_id":"call_123","output_index":0,"sequence_number":3}`)

	var param any
	_ = ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", nil, nil, added, &param)

	doneOut := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", nil, nil, done, &param)
	if len(doneOut) != 1 {
		t.Fatalf("expected 1 done output chunk, got %d", len(doneOut))
	}
	if got := strings.TrimSpace(string(doneOut[0])); got != "" {
		t.Fatalf("expected tool block stop to wait for arguments completion, got %q", got)
	}

	argsOut := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", nil, nil, argsDone, &param)
	if len(argsOut) != 1 {
		t.Fatalf("expected 1 arguments output chunk, got %d", len(argsOut))
	}
	chunk := string(argsOut[0])
	if !strings.Contains(chunk, "\\\"path\\\":\\\"README.md\\\"") {
		t.Fatalf("expected delayed function arguments to be emitted, got %q", chunk)
	}
	if !strings.Contains(chunk, "event: content_block_stop") {
		t.Fatalf("expected delayed function arguments to close tool block, got %q", chunk)
	}
}

func TestConvertCodexResponseToClaude_ResponseCompletedClosesPendingToolBlock(t *testing.T) {
	added := []byte(`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_123","name":"ReadFile"},"output_index":0,"sequence_number":1}`)
	done := []byte(`data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_123","name":"ReadFile"},"output_index":0,"sequence_number":2}`)
	completed := []byte(`data: {"type":"response.completed","response":{"id":"resp_123","model":"gpt-5.4","usage":{"input_tokens":12,"output_tokens":4}}}`)

	var param any
	_ = ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", nil, nil, added, &param)
	_ = ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", nil, nil, done, &param)

	out := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", nil, nil, completed, &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 completed output chunk, got %d", len(out))
	}
	chunk := string(out[0])
	if !strings.Contains(chunk, "event: content_block_stop") {
		t.Fatalf("expected response.completed to close any pending tool block, got %q", chunk)
	}
	if !strings.Contains(chunk, "event: message_delta") {
		t.Fatalf("expected response.completed to still emit message_delta, got %q", chunk)
	}
}

func TestConvertCodexResponseToClaude_ResponseDoneActsAsCompleted(t *testing.T) {
	done := []byte(`data: {"type":"response.done","response":{"id":"resp_123","model":"gpt-5.5","usage":{"input_tokens":12,"output_tokens":4}}}`)

	var param any
	out := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.5", nil, nil, done, &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 done output chunk, got %d", len(out))
	}
	chunk := string(out[0])
	for _, want := range []string{
		"event: message_delta",
		`"input_tokens":12`,
		`"output_tokens":4`,
		"event: message_stop",
	} {
		if !strings.Contains(chunk, want) {
			t.Fatalf("expected response.done to behave like completed and include %q, got %q", want, chunk)
		}
	}
}

func TestConvertCodexResponseToClaude_OutputItemDoneMessageFallsBackToTextEvents(t *testing.T) {
	var param any
	chunks := [][]byte{
		[]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.4"}}`),
		[]byte(`data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]},"output_index":0}`),
		[]byte(`data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.4","usage":{"input_tokens":1,"output_tokens":1}}}`),
	}

	var transcript strings.Builder
	for _, chunk := range chunks {
		out := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", nil, nil, chunk, &param)
		if len(out) != 1 {
			t.Fatalf("expected 1 output chunk, got %d", len(out))
		}
		transcript.Write(out[0])
	}

	got := transcript.String()
	if !strings.Contains(got, `event: content_block_start`) {
		t.Fatalf("expected fallback message to open a text block, got %q", got)
	}
	if !strings.Contains(got, `event: content_block_delta`) || !strings.Contains(got, `"text_delta","text":"ok"`) {
		t.Fatalf("expected fallback message text delta, got %q", got)
	}
	if !strings.Contains(got, `event: content_block_stop`) {
		t.Fatalf("expected fallback message to close the text block, got %q", got)
	}
}

func TestConvertCodexResponseToClaude_OutputItemDoneMessageDoesNotDuplicateDeltaStream(t *testing.T) {
	var param any
	chunks := [][]byte{
		[]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.4"}}`),
		[]byte(`data: {"type":"response.content_part.added","item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`),
		[]byte(`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"hello"}`),
		[]byte(`data: {"type":"response.content_part.done","item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"hello"}}`),
		[]byte(`data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]},"output_index":0}`),
	}

	var transcript strings.Builder
	for _, chunk := range chunks {
		out := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", nil, nil, chunk, &param)
		if len(out) != 1 {
			t.Fatalf("expected 1 output chunk, got %d", len(out))
		}
		transcript.Write(out[0])
	}

	if count := strings.Count(transcript.String(), `"text_delta","text":"hello"`); count != 1 {
		t.Fatalf("expected fallback path to avoid duplicating streamed text, got %d transcript=%q", count, transcript.String())
	}
}

func TestConvertCodexResponseToClaude_UsesCompatibleUsageDefaults(t *testing.T) {
	created := []byte(`data: {"type":"response.created","response":{"id":"resp_123","model":"gpt-5.4","status":"in_progress"}}`)
	completed := []byte(`data: {"type":"response.completed","response":{"id":"resp_123","model":"gpt-5.4","usage":{"input_tokens":12,"output_tokens":4}}}`)

	var param any
	createdOut := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", nil, nil, created, &param)
	if len(createdOut) != 1 {
		t.Fatalf("expected 1 created output chunk, got %d", len(createdOut))
	}
	createdChunk := string(createdOut[0])
	for _, needle := range []string{`"speed":"standard"`, `"service_tier":"standard"`, `"cache_creation_input_tokens":0`, `"cache_read_input_tokens":0`} {
		if !strings.Contains(createdChunk, needle) {
			t.Fatalf("expected message_start usage to include %s, got %q", needle, createdChunk)
		}
	}

	completedOut := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", nil, nil, completed, &param)
	if len(completedOut) != 1 {
		t.Fatalf("expected 1 completed output chunk, got %d", len(completedOut))
	}
	completedChunk := string(completedOut[0])
	for _, needle := range []string{`"speed":"standard"`, `"service_tier":"standard"`, `"cache_creation_input_tokens":0`, `"cache_read_input_tokens":0`} {
		if !strings.Contains(completedChunk, needle) {
			t.Fatalf("expected message_delta usage to include %s, got %q", needle, completedChunk)
		}
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

func TestConvertCodexResponseToClaudeNonStream_ResponseDoneActsAsCompleted(t *testing.T) {
	raw := []byte(`{"type":"response.done","response":{"id":"resp_1","model":"gpt-5.5","usage":{"input_tokens":10,"output_tokens":20},"output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}]}}`)

	out := ConvertCodexResponseToClaudeNonStream(claudeCLICtx(), "gpt-5.5", nil, nil, raw, nil)
	if got := gjson.GetBytes(out, "usage.input_tokens").Int(); got != 10 {
		t.Fatalf("usage.input_tokens = %d, want 10; out=%s", got, string(out))
	}
	if got := gjson.GetBytes(out, "usage.output_tokens").Int(); got != 20 {
		t.Fatalf("usage.output_tokens = %d, want 20; out=%s", got, string(out))
	}
	if got := gjson.GetBytes(out, "content.0.text").String(); got != "done" {
		t.Fatalf("content.0.text = %q, want done; out=%s", got, string(out))
	}
}

func TestConvertCodexResponseToClaudeNonStream_AcceptsSSETranscriptAndPatchesOutputItemDone(t *testing.T) {
	raw := []byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]},\"output_index\":0}\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5.4\",\"usage\":{\"input_tokens\":8,\"output_tokens\":2},\"output\":[]}}\n\n")

	out := ConvertCodexResponseToClaudeNonStream(claudeCLICtx(), "gpt-5.4", nil, nil, raw, nil)
	if got := gjson.GetBytes(out, "id").String(); got != "resp_1" {
		t.Fatalf("id = %q, want resp_1; out=%s", got, string(out))
	}
	if got := gjson.GetBytes(out, "content.0.type").String(); got != "text" {
		t.Fatalf("content.0.type = %q, want text; out=%s", got, string(out))
	}
	if got := gjson.GetBytes(out, "content.0.text").String(); got != "ok" {
		t.Fatalf("content.0.text = %q, want ok; out=%s", got, string(out))
	}
	if got := gjson.GetBytes(out, "usage.input_tokens").Int(); got != 8 {
		t.Fatalf("usage.input_tokens = %d, want 8; out=%s", got, string(out))
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

func TestConvertCodexResponseToClaude_ReasoningSummarySuppressedForClaudeCLI(t *testing.T) {
	raw := []byte(`data: {"type":"response.reasoning_summary_text.delta","delta":"Considering which files to inspect first","item_id":"rs_1","output_index":1,"sequence_number":11,"summary_index":0}`)

	var param any
	out := ConvertCodexResponseToClaude(claudeCLICtx(), "gpt-5.4", nil, nil, raw, &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 output chunk, got %d", len(out))
	}
	if got := strings.TrimSpace(string(out[0])); got != "" {
		t.Fatalf("expected Claude CLI path to suppress reasoning summary thinking, got %q", got)
	}
}

func TestConvertCodexResponseToClaudeNonStream_SuppressesReasoningForClaudeCLI(t *testing.T) {
	raw := []byte(`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.4","usage":{"input_tokens":10,"output_tokens":20},"output":[{"type":"reasoning","summary":[{"text":"Inspecting files and planning test command"}]},{"type":"message","content":[{"type":"output_text","text":"done"}]}]}}`)

	out := ConvertCodexResponseToClaudeNonStream(claudeCLICtx(), "gpt-5.4", nil, nil, raw, nil)
	if got := gjson.GetBytes(out, "content.#").Int(); got != 1 {
		t.Fatalf("expected only final text content block, got %d; out=%s", got, string(out))
	}
	if got := gjson.GetBytes(out, "content.0.type").String(); got != "text" {
		t.Fatalf("expected remaining content block to be text, got %q; out=%s", got, string(out))
	}
}
