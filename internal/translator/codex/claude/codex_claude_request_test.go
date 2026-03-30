package claude

import (
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

func TestConvertClaudeRequestToCodex_ClampBuiltinWebSearchReasoningEffort(t *testing.T) {
	input := `{
		"model": "claude-opus-4-6",
		"system": [{"type":"text","text":"You are an assistant for performing a web search tool use"}],
		"tools": [{"type":"web_search_20250305","name":"web_search","max_uses":8}],
		"thinking": {"type":"adaptive"},
		"output_config": {"effort":"max"},
		"messages": [{"role":"user","content":[{"type":"text","text":"Perform a web search for the query: 张雪峰 去世 辟谣"}]}]
	}`

	result := ConvertClaudeRequestToCodex("gpt-5.4", []byte(input), true)
	if got := gjson.GetBytes(result, "reasoning.effort").String(); got != "medium" {
		t.Fatalf("reasoning.effort = %q, want %q; output=%s", got, "medium", string(result))
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
	if got := gjson.GetBytes(result, "reasoning.effort").String(); got != "medium" {
		t.Fatalf("reasoning.effort = %q, want %q; output=%s", got, "medium", string(result))
	}
}
