package responses

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertOpenAIResponsesRequestToClaude_MapsJSONSchemaResponseFormat(t *testing.T) {
	in := []byte(`{
		"model":"gpt-5.4",
		"stream":true,
		"response_format":{
			"type":"json_schema",
			"json_schema":{
				"name":"code_reviewer_result",
				"strict":true,
				"schema":{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"]}
			}
		},
		"text":{"verbosity":"low"},
		"input":[{"role":"user","content":[{"type":"input_text","text":"review this diff"}]}]
	}`)

	out := ConvertOpenAIResponsesRequestToClaude("claude-opus-4-7", in, true)

	if got := gjson.GetBytes(out, "text.format.type").String(); got != "json_schema" {
		t.Fatalf("text.format.type = %q, want json_schema; out=%s", got, string(out))
	}
	if got := gjson.GetBytes(out, "text.format.name").String(); got != "code_reviewer_result" {
		t.Fatalf("text.format.name = %q, want code_reviewer_result; out=%s", got, string(out))
	}
	if !gjson.GetBytes(out, "text.format.strict").Bool() {
		t.Fatalf("text.format.strict = false, want true; out=%s", string(out))
	}
	if got := gjson.GetBytes(out, "text.format.schema.required.0").String(); got != "summary" {
		t.Fatalf("text.format.schema.required.0 = %q, want summary; out=%s", got, string(out))
	}
	if got := gjson.GetBytes(out, "text.verbosity").String(); got != "low" {
		t.Fatalf("text.verbosity = %q, want low; out=%s", got, string(out))
	}
}

func TestConvertOpenAIResponsesRequestToClaude_MapsTextOnlyVerbosity(t *testing.T) {
	in := []byte(`{
		"model":"gpt-5.4",
		"text":{"verbosity":"high"},
		"input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]
	}`)

	out := ConvertOpenAIResponsesRequestToClaude("claude-opus-4-7", in, false)

	if got := gjson.GetBytes(out, "text.verbosity").String(); got != "high" {
		t.Fatalf("text.verbosity = %q, want high; out=%s", got, string(out))
	}
	if gjson.GetBytes(out, "text.format").Exists() {
		t.Fatalf("unexpected text.format when response_format missing; out=%s", string(out))
	}
}
