package gptinclaude

import (
	"context"
	"fmt"
	"testing"

	"github.com/tidwall/gjson"
)

type fakeGinHeaders map[string]string

func (f fakeGinHeaders) GetHeader(name string) string { return f[name] }

func TestDetectClientKind(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want ClientKind
	}{
		{
			name: "claude cli",
			ctx: context.WithValue(context.Background(), "gin", fakeGinHeaders{
				"User-Agent": "claude-cli/2.1.76 (external, sdk-cli)",
			}),
			want: ClientClaudeCLI,
		},
		{
			name: "claude vscode",
			ctx: context.WithValue(context.Background(), "gin", fakeGinHeaders{
				"User-Agent": "claude-cli/2.1.76 (external, claude-vscode, agent-sdk/0.2.76)",
			}),
			want: ClientClaudeVSCode,
		},
		{
			name: "vscode codex",
			ctx: context.WithValue(context.Background(), "gin", fakeGinHeaders{
				"User-Agent": "codex_exec/0.98.0 (Mac OS 26.3.0; arm64) vscode/1.112.0",
				"Originator": "codex_exec",
			}),
			want: ClientCodexVSCode,
		},
		{
			name: "unknown",
			ctx:  context.Background(),
			want: ClientUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectClientKind(tc.ctx); got != tc.want {
				t.Fatalf("DetectClientKind() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInferBuiltinWebSearchQuery_StripsInjectedScriptNoise(t *testing.T) {
	raw := []byte(`{
		"messages": [{
			"role": "user",
			"content": "OpenAI Codex app 最新官方信息是什么？给我来源。\nimport subprocess, time, json\ncases = []\nwith open('/tmp/x', 'w') as f:\n    pass"
		}],
		"tools": [{
			"name": "WebSearch",
			"input_schema": {
				"type": "object",
				"properties": {"query": {"type": "string"}},
				"required": ["query"]
			}
		}]
	}`)

	if got := InferBuiltinWebSearchQuery(raw); got != "OpenAI Codex app 最新官方信息是什么？给我来源。" {
		t.Fatalf("InferBuiltinWebSearchQuery() = %q", got)
	}
}

func TestInferBuiltinWebSearchQuery_RejectsPureCodeNoise(t *testing.T) {
	raw := []byte(`{
		"messages": [{
			"role": "user",
			"content": "import subprocess\nresults = []\nwith open('/tmp/x', 'w') as f:\n    pass"
		}],
		"tools": [{
			"name": "WebSearch",
			"input_schema": {
				"type": "object",
				"properties": {"query": {"type": "string"}},
				"required": ["query"]
			}
		}]
	}`)

	if got := InferBuiltinWebSearchQuery(raw); got != "" {
		t.Fatalf("InferBuiltinWebSearchQuery() = %q, want empty", got)
	}
}

func TestInferBuiltinWebSearchQuery_HandlesPrefixedPrompt(t *testing.T) {
	raw := []byte(`{
		"messages": [{
			"role": "user",
			"content": [{"type":"text","text":"Perform a web search for the query: 2026 张雪峰 去世 怎么死的 辟谣"}]
		}],
		"tools": [{"type":"web_search_20250305","name":"web_search"}]
	}`)

	if got := InferBuiltinWebSearchQuery(raw); got != "2026 张雪峰 去世 怎么死的 辟谣" {
		t.Fatalf("InferBuiltinWebSearchQuery() = %q", got)
	}
}

func TestInferBuiltinWebSearchQuery_ExtractsArgumentsFromSkillWrappedPrompt(t *testing.T) {
	raw := []byte(`{
		"messages": [{
			"role": "user",
			"content": [{
				"type":"text",
				"text":"读终端命令的输出，以理解所显示的错误。\n然后修复该错误。使用 context7 和 brave-search MCPs 来帮助理解错误。\n接着在终端中重新运行该命令。如果再次出现错误，请重复此调试过程。\n\nARGUMENTS: web search latest headlines for today"
			}]
		}],
		"tools": [{"type":"web_search_20250305","name":"web_search"}]
	}`)

	if got := InferBuiltinWebSearchQuery(raw); got != "latest headlines for today" {
		t.Fatalf("InferBuiltinWebSearchQuery() = %q", got)
	}
}

func TestShouldPreEmitBuiltinWebSearchProgress_ExplicitSearchPrompt(t *testing.T) {
	raw := []byte(`{
		"messages": [
			{"role": "user", "content": "用 websearch 搜索今天的新闻"}
		],
		"tools": [
			{
				"name": "WebSearch",
				"input_schema": {
					"type": "object",
					"properties": {"query": {"type": "string"}},
					"required": ["query"]
				}
			}
		]
	}`)

	if !ShouldPreEmitBuiltinWebSearchProgress(raw) {
		t.Fatalf("ShouldPreEmitBuiltinWebSearchProgress() = false, want true")
	}
}

func TestShouldPreEmitBuiltinWebSearchProgress_CodeAnalysisPrompt(t *testing.T) {
	raw := []byte(`{
		"messages": [
			{"role": "user", "content": "看一下当前的项目代码，帮我简单分析一下即可"}
		],
		"tools": [
			{
				"name": "WebSearch",
				"input_schema": {
					"type": "object",
					"properties": {"query": {"type": "string"}},
					"required": ["query"]
				}
			}
		]
	}`)

	if ShouldPreEmitBuiltinWebSearchProgress(raw) {
		t.Fatalf("ShouldPreEmitBuiltinWebSearchProgress() = true, want false")
	}
}

func TestShouldPreEmitBuiltinWebSearchProgress_NegatedSearchPrompt(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
	}{
		{
			name:   "chinese negation",
			prompt: "看一下当前的项目代码，帮我简单分析一下即可。不要用 web search。",
		},
		{
			name:   "english negation",
			prompt: "Read AGENTS.md and inspect the repository. Keep it concise and do not use web search.",
		},
	}

	for _, tc := range tests {
		raw := []byte(fmt.Sprintf(`{
			"messages": [
				{"role": "user", "content": %q}
			],
			"tools": [
				{
					"name": "WebSearch",
					"input_schema": {
						"type": "object",
						"properties": {"query": {"type": "string"}},
						"required": ["query"]
					}
				}
			]
		}`, tc.prompt))

		if ShouldPreEmitBuiltinWebSearchProgress(raw) {
			t.Fatalf("%s: ShouldPreEmitBuiltinWebSearchProgress() = true, want false", tc.name)
		}
	}
}

func TestShouldPreEmitBuiltinWebSearchProgress_HandlesFlexibleSearchIntentWording(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		want   bool
	}{
		{
			name:   "english search verb",
			prompt: "Please search the web for today's OpenAI news.",
			want:   true,
		},
		{
			name:   "english negated web search",
			prompt: "Please search the repository structure, but do not search the web.",
			want:   false,
		},
		{
			name:   "chinese positive",
			prompt: "请帮我搜索一下今天的 AI 新闻。",
			want:   true,
		},
		{
			name:   "chinese negated nearby",
			prompt: "你分析一下项目，但别搜索一下网页资料。",
			want:   false,
		},
	}

	for _, tc := range tests {
		raw := []byte(fmt.Sprintf(`{
			"messages": [
				{"role": "user", "content": %q}
			],
			"tools": [
				{
					"name": "WebSearch",
					"input_schema": {
						"type": "object",
						"properties": {"query": {"type": "string"}},
						"required": ["query"]
					}
				}
			]
		}`, tc.prompt))

		if got := ShouldPreEmitBuiltinWebSearchProgress(raw); got != tc.want {
			t.Fatalf("%s: ShouldPreEmitBuiltinWebSearchProgress() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestShouldSurfaceReasoningSummaryAsThinking(t *testing.T) {
	tests := []struct {
		kind ClientKind
		want bool
	}{
		{kind: ClientClaudeCLI, want: false},
		{kind: ClientClaudeVSCode, want: false},
		{kind: ClientCodexVSCode, want: false},
		{kind: ClientUnknown, want: true},
	}

	for _, tc := range tests {
		if got := ShouldSurfaceReasoningSummaryAsThinking(tc.kind); got != tc.want {
			t.Fatalf("ShouldSurfaceReasoningSummaryAsThinking(%q) = %v, want %v", tc.kind, got, tc.want)
		}
	}
}

func TestBuildVSCodeWebSearchProgressThinking(t *testing.T) {
	tests := []struct {
		name     string
		action   string
		fallback string
		want     string
	}{
		{
			name:     "search action query",
			action:   `{"type":"search","query":"March 30 2026 top news Reuters world US"}`,
			fallback: "",
			want:     "Searching the web for: March 30 2026 top news Reuters world US",
		},
		{
			name:     "fallback query",
			action:   `{}`,
			fallback: "用 websearch 搜索今天的新闻",
			want:     "Searching the web for: 用 websearch 搜索今天的新闻",
		},
		{
			name:     "generic fallback",
			action:   `{}`,
			fallback: "",
			want:     "Searching the web.",
		},
	}

	for _, tc := range tests {
		if got := BuildVSCodeWebSearchProgressThinking(gjson.Parse(tc.action), tc.fallback); got != tc.want {
			t.Fatalf("%s: BuildVSCodeWebSearchProgressThinking() = %q, want %q", tc.name, got, tc.want)
		}
	}
}
