package executor

import (
	"testing"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

func TestPayloadOriginalRequestedModel(t *testing.T) {
	opts := cliproxyexecutor.Options{Metadata: map[string]any{
		cliproxyexecutor.RequestedModelMetadataKey:         "gpt-5.5(medium)",
		cliproxyexecutor.OriginalRequestedModelMetadataKey: "claude-opus-4-8[1m]",
	}}

	if got := payloadRequestedModel(opts, "fallback"); got != "gpt-5.5(medium)" {
		t.Fatalf("payloadRequestedModel = %q", got)
	}
	if got := payloadOriginalRequestedModel(opts, "fallback"); got != "claude-opus-4-8[1m]" {
		t.Fatalf("payloadOriginalRequestedModel = %q", got)
	}
}

func TestPayloadOriginalRequestedModelFallsBack(t *testing.T) {
	opts := cliproxyexecutor.Options{Metadata: map[string]any{
		cliproxyexecutor.RequestedModelMetadataKey: "gpt-5.5(medium)",
	}}

	if got := payloadOriginalRequestedModel(opts, "gpt-5.5(medium)"); got != "gpt-5.5(medium)" {
		t.Fatalf("payloadOriginalRequestedModel fallback = %q", got)
	}
}
