package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestBuildOpenAIResponsesStreamErrorChunk(t *testing.T) {
	chunk := BuildOpenAIResponsesStreamErrorChunk(http.StatusInternalServerError, "unexpected EOF", 0)
	var payload map[string]any
	if err := json.Unmarshal(chunk, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["type"] != "error" {
		t.Fatalf("type = %v, want %q", payload["type"], "error")
	}
	if payload["code"] != "internal_server_error" {
		t.Fatalf("code = %v, want %q", payload["code"], "internal_server_error")
	}
	if payload["message"] != "unexpected EOF" {
		t.Fatalf("message = %v, want %q", payload["message"], "unexpected EOF")
	}
	if payload["sequence_number"] != float64(0) {
		t.Fatalf("sequence_number = %v, want %v", payload["sequence_number"], 0)
	}
}

func TestBuildOpenAIResponsesStreamErrorChunkExtractsHTTPErrorBody(t *testing.T) {
	chunk := BuildOpenAIResponsesStreamErrorChunk(
		http.StatusInternalServerError,
		`{"error":{"message":"oops","type":"server_error","code":"internal_server_error"}}`,
		0,
	)
	var payload map[string]any
	if err := json.Unmarshal(chunk, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["type"] != "error" {
		t.Fatalf("type = %v, want %q", payload["type"], "error")
	}
	if payload["code"] != "internal_server_error" {
		t.Fatalf("code = %v, want %q", payload["code"], "internal_server_error")
	}
	if payload["message"] != "oops" {
		t.Fatalf("message = %v, want %q", payload["message"], "oops")
	}
}

func TestBuildOpenAIResponsesStreamErrorChunkSanitizesUnknownProviderLeak(t *testing.T) {
	chunk := BuildOpenAIResponsesStreamErrorChunk(
		http.StatusBadGateway,
		`{"error":{"message":"unknown provider for model gpt-5.4(medium)","type":"server_error","code":"internal_server_error"}}`,
		0,
	)
	var payload map[string]any
	if err := json.Unmarshal(chunk, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["message"] != GenericSensitiveClientErrorMessage {
		t.Fatalf("message = %v", payload["message"])
	}
	if payload["code"] != "internal_server_error" {
		t.Fatalf("code = %v", payload["code"])
	}
	assertNoClientInternalLeak(t, chunk)
}

func TestBuildOpenAIResponsesStreamErrorChunkSanitizesCodexAuthLeak(t *testing.T) {
	chunk := BuildOpenAIResponsesStreamErrorChunk(
		http.StatusForbidden,
		`{"error":{"message":"Codex Provider API rejected auth file for gpt-5.5","type":"authentication_error","code":"codex_auth_file_failed"}}`,
		0,
	)
	var payload map[string]any
	if err := json.Unmarshal(chunk, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["message"] != GenericSensitiveClientErrorMessage {
		t.Fatalf("message = %v", payload["message"])
	}
	if payload["code"] != "internal_server_error" {
		t.Fatalf("code = %v", payload["code"])
	}
	assertNoClientInternalLeak(t, chunk)
}
