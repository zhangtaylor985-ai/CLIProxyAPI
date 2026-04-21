package chat_completions

import (
	"context"
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertCodexResponseToOpenAI_StreamSetsModelFromResponseCreated(t *testing.T) {
	ctx := context.Background()
	var param any

	modelName := "gpt-5.3-codex"

	out := ConvertCodexResponseToOpenAI(ctx, modelName, nil, nil, []byte(`data: {"type":"response.created","response":{"id":"resp_123","created_at":1700000000,"model":"gpt-5.3-codex"}}`), &param)
	if len(out) != 0 {
		t.Fatalf("expected no output for response.created, got %d chunks", len(out))
	}

	out = ConvertCodexResponseToOpenAI(ctx, modelName, nil, nil, []byte(`data: {"type":"response.output_text.delta","delta":"hello"}`), &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(out))
	}

	gotModel := gjson.GetBytes(out[0], "model").String()
	if gotModel != modelName {
		t.Fatalf("expected model %q, got %q", modelName, gotModel)
	}
}

func TestConvertCodexResponseToOpenAI_FirstChunkUsesRequestModelName(t *testing.T) {
	ctx := context.Background()
	var param any

	modelName := "gpt-5.3-codex"

	out := ConvertCodexResponseToOpenAI(ctx, modelName, nil, nil, []byte(`data: {"type":"response.output_text.delta","delta":"hello"}`), &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(out))
	}

	gotModel := gjson.GetBytes(out[0], "model").String()
	if gotModel != modelName {
		t.Fatalf("expected model %q, got %q", modelName, gotModel)
	}
}

func TestConvertCodexResponseToOpenAINonStream_AggregatesSSETranscriptContent(t *testing.T) {
	raw := []byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_123\",\"created_at\":1700000000,\"model\":\"gpt-5.4\"}}\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"你好\"}\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"，世界。\"}\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_123\",\"created_at\":1700000000,\"model\":\"gpt-5.4\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":4,\"total_tokens\":14}}}\n" +
		"data: [DONE]\n")

	out := ConvertCodexResponseToOpenAINonStream(context.Background(), "gpt-5.4", nil, nil, raw, nil)
	if len(out) == 0 {
		t.Fatal("expected non-empty response")
	}

	if got := gjson.GetBytes(out, "choices.0.message.content").String(); got != "你好，世界。" {
		t.Fatalf("expected aggregated content, got %q", got)
	}
	if got := gjson.GetBytes(out, "choices.0.finish_reason").String(); got != "stop" {
		t.Fatalf("expected finish_reason stop, got %q", got)
	}
	if got := gjson.GetBytes(out, "usage.prompt_tokens").Int(); got != 10 {
		t.Fatalf("expected prompt_tokens 10, got %d", got)
	}
}
