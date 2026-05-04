package claude

import (
	"encoding/base64"
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertClaudeRequestToCodex_SystemMessageScenarios(t *testing.T) {
	tests := []struct {
		name             string
		inputJSON        string
		wantHasDeveloper bool
		wantTexts        []string
	}{
		{
			name: "No system field",
			inputJSON: `{
				"model": "claude-3-opus",
				"messages": [{"role": "user", "content": "hello"}]
			}`,
			wantHasDeveloper: false,
		},
		{
			name: "Empty string system field",
			inputJSON: `{
				"model": "claude-3-opus",
				"system": "",
				"messages": [{"role": "user", "content": "hello"}]
			}`,
			wantHasDeveloper: false,
		},
		{
			name: "String system field",
			inputJSON: `{
				"model": "claude-3-opus",
				"system": "Be helpful",
				"messages": [{"role": "user", "content": "hello"}]
			}`,
			wantHasDeveloper: true,
			wantTexts:        []string{"Be helpful"},
		},
		{
			name: "Array system field with filtered billing header",
			inputJSON: `{
				"model": "claude-3-opus",
				"system": [
					{"type": "text", "text": "x-anthropic-billing-header: tenant-123"},
					{"type": "text", "text": "Block 1"},
					{"type": "text", "text": "Block 2"}
				],
				"messages": [{"role": "user", "content": "hello"}]
			}`,
			wantHasDeveloper: true,
			wantTexts:        []string{"Block 1", "Block 2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertClaudeRequestToCodex("test-model", []byte(tt.inputJSON), false)
			resultJSON := gjson.ParseBytes(result)
			inputs := resultJSON.Get("input").Array()

			hasDeveloper := len(inputs) > 0 && inputs[0].Get("role").String() == "developer"
			if hasDeveloper != tt.wantHasDeveloper {
				t.Fatalf("got hasDeveloper = %v, want %v. Output: %s", hasDeveloper, tt.wantHasDeveloper, resultJSON.Get("input").Raw)
			}

			if !tt.wantHasDeveloper {
				return
			}

			content := inputs[0].Get("content").Array()
			if len(content) != len(tt.wantTexts) {
				t.Fatalf("got %d system content items, want %d. Content: %s", len(content), len(tt.wantTexts), inputs[0].Get("content").Raw)
			}

			for i, wantText := range tt.wantTexts {
				if gotType := content[i].Get("type").String(); gotType != "input_text" {
					t.Fatalf("content[%d] type = %q, want %q", i, gotType, "input_text")
				}
				if gotText := content[i].Get("text").String(); gotText != wantText {
					t.Fatalf("content[%d] text = %q, want %q", i, gotText, wantText)
				}
			}
		})
	}
}

func TestConvertClaudeRequestToCodex_ParallelToolCalls(t *testing.T) {
	tests := []struct {
		name                  string
		inputJSON             string
		wantParallelToolCalls bool
	}{
		{
			name: "Default to true when tool_choice.disable_parallel_tool_use is absent",
			inputJSON: `{
				"model": "claude-3-opus",
				"messages": [{"role": "user", "content": "hello"}]
			}`,
			wantParallelToolCalls: true,
		},
		{
			name: "Disable parallel tool calls when client opts out",
			inputJSON: `{
				"model": "claude-3-opus",
				"tool_choice": {"disable_parallel_tool_use": true},
				"messages": [{"role": "user", "content": "hello"}]
			}`,
			wantParallelToolCalls: false,
		},
		{
			name: "Keep parallel tool calls enabled when client explicitly allows them",
			inputJSON: `{
				"model": "claude-3-opus",
				"tool_choice": {"disable_parallel_tool_use": false},
				"messages": [{"role": "user", "content": "hello"}]
			}`,
			wantParallelToolCalls: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertClaudeRequestToCodex("test-model", []byte(tt.inputJSON), false)
			resultJSON := gjson.ParseBytes(result)

			if got := resultJSON.Get("parallel_tool_calls").Bool(); got != tt.wantParallelToolCalls {
				t.Fatalf("parallel_tool_calls = %v, want %v. Output: %s", got, tt.wantParallelToolCalls, string(result))
			}
		})
	}
}

func TestConvertClaudeRequestToCodex_PreservesBuiltinWebSearchReasoningEffort(t *testing.T) {
	input := `{
		"model": "claude-opus-4-6",
		"system": [{"type":"text","text":"You are an assistant for performing a web search tool use"}],
		"tools": [{"type":"web_search_20250305","name":"web_search","max_uses":8}],
		"thinking": {"type":"adaptive"},
		"output_config": {"effort":"max"},
		"messages": [{"role":"user","content":[{"type":"text","text":"Perform a web search for the query: 张雪峰 去世 辟谣"}]}]
	}`

	result := ConvertClaudeRequestToCodex("gpt-5.4", []byte(input), true)
	if got := gjson.GetBytes(result, "reasoning.effort").String(); got != "high" {
		t.Fatalf("reasoning.effort = %q, want %q; output=%s", got, "high", string(result))
	}
}

func TestConvertClaudeRequestToCodex_DoesNotClampBuiltinWebSearchWithoutSearchIntent(t *testing.T) {
	input := `{
		"model": "claude-opus-4-6",
		"tools": [{"name":"WebSearch","input_schema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}],
		"thinking": {"type":"adaptive"},
		"output_config": {"effort":"high"},
		"messages": [{"role":"user","content":"Reply with exactly HIGH_SEQ_20260413"}]
	}`

	result := ConvertClaudeRequestToCodex("gpt-5.4", []byte(input), true)
	if got := gjson.GetBytes(result, "reasoning.effort").String(); got != "high" {
		t.Fatalf("reasoning.effort = %q, want %q; output=%s", got, "high", string(result))
	}
}

func TestConvertClaudeRequestToCodex_EnabledThinkingExplicitEffortWinsOverBudget(t *testing.T) {
	input := `{
		"model": "claude-opus-4-6",
		"messages": [{"role":"user","content":"hi"}],
		"thinking": {"type":"enabled","budget_tokens":1024},
		"output_config": {"effort":"high"}
	}`

	result := ConvertClaudeRequestToCodex("gpt-5.4", []byte(input), false)
	if got := gjson.GetBytes(result, "reasoning.effort").String(); got != "high" {
		t.Fatalf("reasoning.effort = %q, want %q; output=%s", got, "high", string(result))
	}
}

func TestConvertClaudeRequestToCodex_UnknownExplicitEffortFallsBackToBudget(t *testing.T) {
	input := `{
		"model": "claude-opus-4-6",
		"messages": [{"role":"user","content":"hi"}],
		"thinking": {"type":"enabled","budget_tokens":1024},
		"output_config": {"effort":"weird"}
	}`

	result := ConvertClaudeRequestToCodex("gpt-5.4", []byte(input), false)
	if got := gjson.GetBytes(result, "reasoning.effort").String(); got != "low" {
		t.Fatalf("reasoning.effort = %q, want %q; output=%s", got, "low", string(result))
	}
}

func TestConvertClaudeRequestToCodex_MapsClaudeCodeWebSearchToolToBuiltinSearch(t *testing.T) {
	input := `{
		"model": "claude-opus-4-6",
		"tools": [{
			"name": "WebSearch",
			"description": "Allows Claude to search the web and use the results to inform responses",
			"input_schema": {
				"type": "object",
				"properties": {
					"query": {"type": "string"},
					"allowed_domains": {"type": "array", "items": {"type": "string"}}
				},
				"required": ["query"]
			}
		}],
		"thinking": {"type":"adaptive"},
		"output_config": {"effort":"max"},
		"messages": [{"role":"user","content":"2026 张雪峰去世你知道吗，他是怎么死的，为什么？"}]
	}`

	result := ConvertClaudeRequestToCodex("gpt-5.4", []byte(input), true)
	if got := gjson.GetBytes(result, "tools.0.type").String(); got != "web_search" {
		t.Fatalf("tools.0.type = %q, want %q; output=%s", got, "web_search", string(result))
	}
	if got := gjson.GetBytes(result, "reasoning.effort").String(); got != "high" {
		t.Fatalf("reasoning.effort = %q, want %q; output=%s", got, "high", string(result))
	}
}

func TestConvertClaudeRequestToCodex_PreservesUnknownToolResultContentBlocksAsInputText(t *testing.T) {
	input := `{
		"model": "claude-opus-4-6",
		"messages": [{
			"role": "user",
			"content": [{
				"type": "tool_result",
				"tool_use_id": "toolu_123",
				"content": [
					{"type": "text", "text": "updated"},
					{"type": "tool_progress", "status": "done", "step": "finalize"}
				]
			}]
		}]
	}`

	result := ConvertClaudeRequestToCodex("gpt-5.4", []byte(input), true)
	output := gjson.GetBytes(result, "input.0")

	if got := output.Get("type").String(); got != "function_call_output" {
		t.Fatalf("input.0.type = %q, want %q; output=%s", got, "function_call_output", string(result))
	}
	if got := output.Get("output.0.type").String(); got != "input_text" {
		t.Fatalf("output.0.type = %q, want %q; output=%s", got, "input_text", string(result))
	}
	if got := output.Get("output.0.text").String(); got != "updated" {
		t.Fatalf("output.0.text = %q, want %q; output=%s", got, "updated", string(result))
	}
	if got := output.Get("output.1.type").String(); got != "input_text" {
		t.Fatalf("output.1.type = %q, want %q; output=%s", got, "input_text", string(result))
	}
	rawFallback := output.Get("output.1.text").String()
	fallbackJSON := gjson.Parse(rawFallback)
	if got := fallbackJSON.Get("type").String(); got != "tool_progress" {
		t.Fatalf("output.1.text.type = %q, want %q; output=%s", got, "tool_progress", string(result))
	}
	if got := fallbackJSON.Get("status").String(); got != "done" {
		t.Fatalf("output.1.text.status = %q, want %q; output=%s", got, "done", string(result))
	}
	if got := fallbackJSON.Get("step").String(); got != "finalize" {
		t.Fatalf("output.1.text.step = %q, want %q; output=%s", got, "finalize", string(result))
	}
}

func TestConvertClaudeRequestToCodex_MapsDocumentBlocksToInputFile(t *testing.T) {
	input := `{
		"model": "claude-opus-4-6",
		"messages": [{
			"role": "user",
			"content": [
				{"type": "text", "text": "please read this"},
				{"type": "document", "title": "report.pdf", "source": {"type": "base64", "media_type": "application/pdf", "data": "JVBERi0xLjQ="}}
			]
		}]
	}`

	result := ConvertClaudeRequestToCodex("gpt-5.4", []byte(input), true)
	message := gjson.GetBytes(result, "input.0")

	if got := message.Get("content.1.type").String(); got != "input_file" {
		t.Fatalf("content.1.type = %q, want %q; output=%s", got, "input_file", string(result))
	}
	if got := message.Get("content.1.file_data").String(); got != "data:application/pdf;base64,JVBERi0xLjQ=" {
		t.Fatalf("content.1.file_data = %q; output=%s", got, string(result))
	}
	if got := message.Get("content.1.filename").String(); got != "report.pdf" {
		t.Fatalf("content.1.filename = %q, want report.pdf; output=%s", got, string(result))
	}
}

func TestConvertClaudeRequestToCodex_EncodesTextDocumentBlocksToInputFile(t *testing.T) {
	input := `{
		"model": "claude-opus-4-6",
		"messages": [{
			"role": "user",
			"content": [
				{"type": "text", "text": "please read this markdown"},
				{"type": "document", "title": "notes.md", "source": {"type": "text", "media_type": "text/plain", "data": "# Title\nhello"}}
			]
		}]
	}`

	result := ConvertClaudeRequestToCodex("gpt-5.4", []byte(input), true)
	message := gjson.GetBytes(result, "input.0")
	wantData := "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("# Title\nhello"))

	if got := message.Get("content.1.type").String(); got != "input_file" {
		t.Fatalf("content.1.type = %q, want %q; output=%s", got, "input_file", string(result))
	}
	if got := message.Get("content.1.file_data").String(); got != wantData {
		t.Fatalf("content.1.file_data = %q, want %q; output=%s", got, wantData, string(result))
	}
	if got := message.Get("content.1.filename").String(); got != "notes.md" {
		t.Fatalf("content.1.filename = %q, want notes.md; output=%s", got, string(result))
	}
}

func TestConvertClaudeRequestToCodex_MapsToolResultDocumentBlocksToInputFile(t *testing.T) {
	input := `{
		"model": "claude-opus-4-6",
		"messages": [{
			"role": "user",
			"content": [{
				"type": "tool_result",
				"tool_use_id": "toolu_123",
				"content": [
					{"type": "text", "text": "generated"},
					{"type": "document", "title": "artifact.pdf", "source": {"type": "base64", "media_type": "application/pdf", "data": "JVBERi0xLjQ="}}
				]
			}]
		}]
	}`

	result := ConvertClaudeRequestToCodex("gpt-5.4", []byte(input), true)
	output := gjson.GetBytes(result, "input.0")

	if got := output.Get("type").String(); got != "function_call_output" {
		t.Fatalf("input.0.type = %q, want %q; output=%s", got, "function_call_output", string(result))
	}
	if got := output.Get("output.1.type").String(); got != "input_file" {
		t.Fatalf("output.1.type = %q, want %q; output=%s", got, "input_file", string(result))
	}
	if got := output.Get("output.1.file_data").String(); got != "data:application/pdf;base64,JVBERi0xLjQ=" {
		t.Fatalf("output.1.file_data = %q; output=%s", got, string(result))
	}
	if got := output.Get("output.1.filename").String(); got != "artifact.pdf" {
		t.Fatalf("output.1.filename = %q, want artifact.pdf; output=%s", got, string(result))
	}
}

func TestConvertClaudeRequestToCodex_EncodesToolResultTextDocumentBlocksToInputFile(t *testing.T) {
	input := `{
		"model": "claude-opus-4-6",
		"messages": [{
			"role": "user",
			"content": [{
				"type": "tool_result",
				"tool_use_id": "toolu_123",
				"content": [
					{"type": "text", "text": "generated"},
					{"type": "document", "title": "artifact.md", "source": {"type": "text", "media_type": "text/markdown", "data": "# Done\ncontent"}}
				]
			}]
		}]
	}`

	result := ConvertClaudeRequestToCodex("gpt-5.4", []byte(input), true)
	output := gjson.GetBytes(result, "input.0")
	wantData := "data:text/markdown;base64," + base64.StdEncoding.EncodeToString([]byte("# Done\ncontent"))

	if got := output.Get("output.1.type").String(); got != "input_file" {
		t.Fatalf("output.1.type = %q, want %q; output=%s", got, "input_file", string(result))
	}
	if got := output.Get("output.1.file_data").String(); got != wantData {
		t.Fatalf("output.1.file_data = %q, want %q; output=%s", got, wantData, string(result))
	}
	if got := output.Get("output.1.filename").String(); got != "artifact.md" {
		t.Fatalf("output.1.filename = %q, want artifact.md; output=%s", got, string(result))
	}
}

func TestConvertClaudeRequestToCodex_PreservesPlainStringToolResultOutput(t *testing.T) {
	input := `{
		"model": "claude-opus-4-6",
		"messages": [{
			"role": "user",
			"content": [{
				"type": "tool_result",
				"tool_use_id": "toolu_123",
				"content": "Updated task #9 status"
			}]
		}]
	}`

	result := ConvertClaudeRequestToCodex("gpt-5.4", []byte(input), true)
	if got := gjson.GetBytes(result, "input.0.output").String(); got != "Updated task #9 status" {
		t.Fatalf("input.0.output = %q, want %q; output=%s", got, "Updated task #9 status", string(result))
	}
}
