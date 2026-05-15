package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
	"github.com/tidwall/gjson"
)

type recordingModelExecutor struct {
	provider string

	mu           sync.Mutex
	seenModels   []string
	seenPayloads []string
}

func (e *recordingModelExecutor) Identifier() string {
	return e.provider
}

func (e *recordingModelExecutor) Execute(_ context.Context, _ *coreauth.Auth, req coreexecutor.Request, _ coreexecutor.Options) (coreexecutor.Response, error) {
	e.mu.Lock()
	e.seenModels = append(e.seenModels, req.Model)
	e.seenPayloads = append(e.seenPayloads, string(req.Payload))
	e.mu.Unlock()
	return coreexecutor.Response{Payload: []byte(`{"model":"` + req.Model + `"}`)}, nil
}

func (e *recordingModelExecutor) ExecuteStream(_ context.Context, _ *coreauth.Auth, req coreexecutor.Request, _ coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	e.mu.Lock()
	e.seenModels = append(e.seenModels, req.Model)
	e.seenPayloads = append(e.seenPayloads, string(req.Payload))
	e.mu.Unlock()

	ch := make(chan coreexecutor.StreamChunk, 1)
	ch <- coreexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.completed","response":{"output":[]}}`)}
	close(ch)
	return &coreexecutor.StreamResult{Chunks: ch}, nil
}

func (e *recordingModelExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *recordingModelExecutor) CountTokens(_ context.Context, _ *coreauth.Auth, req coreexecutor.Request, _ coreexecutor.Options) (coreexecutor.Response, error) {
	e.mu.Lock()
	e.seenModels = append(e.seenModels, req.Model)
	e.seenPayloads = append(e.seenPayloads, string(req.Payload))
	e.mu.Unlock()
	return coreexecutor.Response{Payload: []byte(`{"model":"` + req.Model + `"}`)}, nil
}

func (e *recordingModelExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, &coreauth.Error{Code: "not_implemented", Message: "HttpRequest not implemented"}
}

func (e *recordingModelExecutor) lastModel() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.seenModels) == 0 {
		return ""
	}
	return e.seenModels[len(e.seenModels)-1]
}

func (e *recordingModelExecutor) lastPayloadModel() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.seenPayloads) == 0 {
		return ""
	}
	return gjson.Get(e.seenPayloads[len(e.seenPayloads)-1], "model").String()
}

func TestAPIKeyModelRoutingDoesNotRewriteOpenAIResponsesRequests(t *testing.T) {
	handler, executor, ctx := newModelRoutingTestHandler(t, "codex", []string{"gpt-5.5-routing-preserve", "gpt-5.4-routing-target"}, "gpt-5.5-routing-preserve", "gpt-5.4-routing-target")

	_, _, errMsg := handler.ExecuteWithAuthManager(ctx, "openai-response", "gpt-5.5-routing-preserve", []byte(`{"model":"gpt-5.5-routing-preserve","input":[]}`), "")
	if errMsg != nil {
		t.Fatalf("ExecuteWithAuthManager error = %v", errMsg.Error)
	}
	if got := executor.lastModel(); got != "gpt-5.5-routing-preserve" {
		t.Fatalf("executor model = %q, want direct Codex/OpenAI Responses model preserved", got)
	}
	if got := executor.lastPayloadModel(); got != "gpt-5.5-routing-preserve" {
		t.Fatalf("payload model = %q, want direct Codex/OpenAI Responses model preserved", got)
	}
}

func TestAPIKeyModelRoutingDoesNotRewriteOpenAIResponsesStreams(t *testing.T) {
	handler, executor, ctx := newModelRoutingTestHandler(t, "codex", []string{"gpt-5.5-routing-stream", "gpt-5.4-routing-target"}, "gpt-5.5-routing-stream", "gpt-5.4-routing-target")

	dataChan, _, errChan := handler.ExecuteStreamWithAuthManager(ctx, "openai-response", "gpt-5.5-routing-stream", []byte(`{"model":"gpt-5.5-routing-stream","input":[]}`), "")
	for dataChan != nil || errChan != nil {
		select {
		case _, ok := <-dataChan:
			if !ok {
				dataChan = nil
			}
		case errMsg, ok := <-errChan:
			if !ok {
				errChan = nil
				continue
			}
			if errMsg != nil {
				t.Fatalf("ExecuteStreamWithAuthManager error = %v", errMsg.Error)
			}
		}
	}

	if got := executor.lastModel(); got != "gpt-5.5-routing-stream" {
		t.Fatalf("executor stream model = %q, want direct Codex/OpenAI Responses model preserved", got)
	}
	if got := executor.lastPayloadModel(); got != "gpt-5.5-routing-stream" {
		t.Fatalf("stream payload model = %q, want direct Codex/OpenAI Responses model preserved", got)
	}
}

func TestAPIKeyModelRoutingStillAppliesToClaudeRequests(t *testing.T) {
	handler, executor, ctx := newModelRoutingTestHandler(t, "codex", []string{"gpt-5.4-routing-target"}, "claude-opus-routing-source", "gpt-5.4-routing-target")

	_, _, errMsg := handler.ExecuteWithAuthManager(ctx, "claude", "claude-opus-routing-source", []byte(`{"model":"claude-opus-routing-source","messages":[]}`), "")
	if errMsg != nil {
		t.Fatalf("ExecuteWithAuthManager error = %v", errMsg.Error)
	}
	if got := executor.lastModel(); got != "gpt-5.4-routing-target" {
		t.Fatalf("executor model = %q, want Claude model-routing target", got)
	}
	if got := executor.lastPayloadModel(); got != "gpt-5.4-routing-target" {
		t.Fatalf("payload model = %q, want Claude model-routing target", got)
	}
}

func newModelRoutingTestHandler(t *testing.T, provider string, models []string, fromModel string, targetModel string) (*BaseAPIHandler, *recordingModelExecutor, context.Context) {
	t.Helper()

	manager := coreauth.NewManager(nil, nil, nil)
	executor := &recordingModelExecutor{provider: provider}
	manager.RegisterExecutor(executor)

	auth := &coreauth.Auth{ID: provider + "-routing-test-auth", Provider: provider}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("manager.Register: %v", err)
	}

	modelInfos := make([]*registry.ModelInfo, 0, len(models))
	for _, model := range models {
		modelInfos = append(modelInfos, &registry.ModelInfo{ID: model})
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, modelInfos)

	enabled := true
	policy := &internalconfig.APIKeyPolicy{
		APIKey: "test-api-key",
		ModelRouting: internalconfig.APIKeyModelRoutingPolicy{Rules: []internalconfig.ModelRoutingRule{
			{
				Enabled:             &enabled,
				FromModel:           fromModel,
				TargetModel:         targetModel,
				TargetPercent:       100,
				StickyWindowSeconds: 3600,
			},
		}},
	}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	ginCtx.Set("apiKey", "test-api-key")
	ginCtx.Set("apiKeyPolicy", policy)

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	return handler, executor, context.WithValue(context.Background(), "gin", ginCtx)
}
