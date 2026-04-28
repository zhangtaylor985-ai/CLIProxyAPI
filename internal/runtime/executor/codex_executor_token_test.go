package executor

import (
	"strings"
	"testing"

	"github.com/tiktoken-go/tokenizer"
)

func TestCountCodexInputTokensSkipsFunctionOutputFileData(t *testing.T) {
	enc, err := tokenizer.ForModel(tokenizer.GPT5)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"model":"gpt-5.4","input":[{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_text","text":"generated"},{"type":"input_file","file_data":"data:application/pdf;base64,` + strings.Repeat("A", 200000) + `","filename":"artifact.pdf"}]}]}`)

	count, err := countCodexInputTokens(enc, body)
	if err != nil {
		t.Fatal(err)
	}
	if count <= 0 {
		t.Fatalf("count=%d, want positive text tokens", count)
	}
	if count > 20 {
		t.Fatalf("count=%d, file_data should not be counted as prompt text", count)
	}
}
