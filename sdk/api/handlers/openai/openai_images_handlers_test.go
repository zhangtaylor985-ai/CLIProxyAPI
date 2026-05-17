package openai

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func performImagesEndpointRequest(t *testing.T, endpointPath string, contentType string, body io.Reader, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST(endpointPath, handler)

	req := httptest.NewRequest(http.MethodPost, endpointPath, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	return resp
}

func assertUnsupportedImagesModelResponse(t *testing.T, resp *httptest.ResponseRecorder, model string) {
	t.Helper()

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}

	message := gjson.GetBytes(resp.Body.Bytes(), "error.message").String()
	expectedMessage := "Model " + model + " is not supported on " + imagesGenerationsPath + " or " + imagesEditsPath + ". Use " + defaultImagesToolModel + "."
	if message != expectedMessage {
		t.Fatalf("error message = %q, want %q", message, expectedMessage)
	}
	if errorType := gjson.GetBytes(resp.Body.Bytes(), "error.type").String(); errorType != "invalid_request_error" {
		t.Fatalf("error type = %q, want invalid_request_error", errorType)
	}
}

func TestImagesModelValidationAllowsGPTImage2WithOptionalPrefix(t *testing.T) {
	for _, model := range []string{"gpt-image-2", "codex/gpt-image-2"} {
		if !isSupportedImagesModel(model) {
			t.Fatalf("expected %s to be supported", model)
		}
	}
	if isSupportedImagesModel("gpt-5.4-mini") {
		t.Fatal("expected gpt-5.4-mini to be rejected")
	}
}

func TestBuildImagesResponsesRequestPreservesModelPrefix(t *testing.T) {
	req := buildImagesResponsesRequest("draw a square", nil, []byte(`{"type":"image_generation","model":"codex/gpt-image-2"}`))

	if got := gjson.GetBytes(req, "tool_choice").String(); got != "required" {
		t.Fatalf("tool_choice = %q, want required", got)
	}
	if got := gjson.GetBytes(req, "model").String(); got != "codex/gpt-5.4-mini" {
		t.Fatalf("main model = %q, want codex/gpt-5.4-mini", got)
	}
	if got := gjson.GetBytes(req, "tools.0.model").String(); got != "codex/gpt-image-2" {
		t.Fatalf("tool model = %q, want codex/gpt-image-2", got)
	}
}

func TestBuildImagesResponsesRequestDefaultsUnprefixedModel(t *testing.T) {
	req := buildImagesResponsesRequest("draw a square", nil, []byte(`{"type":"image_generation","model":"gpt-image-2"}`))

	if got := gjson.GetBytes(req, "model").String(); got != defaultImagesMainModel {
		t.Fatalf("main model = %q, want %s", got, defaultImagesMainModel)
	}
}

func TestCollectImagesFromResponsesStreamBuildsImagesResponse(t *testing.T) {
	data := make(chan []byte, 1)
	data <- []byte(`data: {"type":"response.completed","response":{"created_at":1775555723,"output":[{"type":"image_generation_call","output_format":"png","result":"aGVsbG8=","revised_prompt":"draw a square"}],"tool_usage":{"image_gen":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}}` + "\n\n")
	close(data)

	out, errMsg := collectImagesFromResponsesStream(context.Background(), data, nil, "b64_json")
	if errMsg != nil {
		t.Fatalf("collectImagesFromResponsesStream error = %v", errMsg.Error)
	}
	if got := gjson.GetBytes(out, "data.0.b64_json").String(); got != "aGVsbG8=" {
		t.Fatalf("b64_json = %q, want image payload; body=%s", got, string(out))
	}
	if got := gjson.GetBytes(out, "data.0.revised_prompt").String(); got != "draw a square" {
		t.Fatalf("revised_prompt = %q, want draw a square", got)
	}
	if got := gjson.GetBytes(out, "usage.total_tokens").Int(); got != 3 {
		t.Fatalf("usage.total_tokens = %d, want 3; body=%s", got, string(out))
	}
}

func TestCollectImagesFromResponsesStreamUsesResponseUsageFallback(t *testing.T) {
	data := make(chan []byte, 1)
	data <- []byte(`data: {"type":"response.completed","response":{"created_at":1775555723,"output":[{"type":"image_generation_call","output_format":"png","result":"aGVsbG8="}],"usage":{"input_tokens":4,"output_tokens":5,"total_tokens":9}}}` + "\n\n")
	close(data)

	out, errMsg := collectImagesFromResponsesStream(context.Background(), data, nil, "b64_json")
	if errMsg != nil {
		t.Fatalf("collectImagesFromResponsesStream error = %v", errMsg.Error)
	}
	if got := gjson.GetBytes(out, "usage.total_tokens").Int(); got != 9 {
		t.Fatalf("usage.total_tokens = %d, want 9; body=%s", got, string(out))
	}
}

func TestImagesGenerationsRejectsUnsupportedModel(t *testing.T) {
	handler := &OpenAIAPIHandler{}
	body := strings.NewReader(`{"model":"gpt-5.4-mini","prompt":"draw a square"}`)

	resp := performImagesEndpointRequest(t, imagesGenerationsPath, "application/json", body, handler.ImagesGenerations)

	assertUnsupportedImagesModelResponse(t, resp, "gpt-5.4-mini")
}

func TestImagesEditsJSONRejectsUnsupportedModel(t *testing.T) {
	handler := &OpenAIAPIHandler{}
	body := strings.NewReader(`{"model":"gpt-5.4-mini","prompt":"edit this","images":[{"image_url":"data:image/png;base64,AA=="}]}`)

	resp := performImagesEndpointRequest(t, imagesEditsPath, "application/json", body, handler.ImagesEdits)

	assertUnsupportedImagesModelResponse(t, resp, "gpt-5.4-mini")
}

func TestImagesEditsMultipartRejectsUnsupportedModel(t *testing.T) {
	handler := &OpenAIAPIHandler{}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", "gpt-5.4-mini"); err != nil {
		t.Fatalf("write model field: %v", err)
	}
	if err := writer.WriteField("prompt", "edit this"); err != nil {
		t.Fatalf("write prompt field: %v", err)
	}
	if errClose := writer.Close(); errClose != nil {
		t.Fatalf("close multipart writer: %v", errClose)
	}

	resp := performImagesEndpointRequest(t, imagesEditsPath, writer.FormDataContentType(), &body, handler.ImagesEdits)

	assertUnsupportedImagesModelResponse(t, resp, "gpt-5.4-mini")
}
