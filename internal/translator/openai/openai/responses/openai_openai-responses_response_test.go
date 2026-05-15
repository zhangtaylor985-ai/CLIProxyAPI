package responses

import (
	"context"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func parseOpenAIResponsesSSEEvent(t *testing.T, chunk []byte) (string, gjson.Result) {
	t.Helper()

	lines := strings.Split(string(chunk), "\n")
	if len(lines) < 2 {
		t.Fatalf("unexpected SSE chunk: %q", chunk)
	}

	event := strings.TrimSpace(strings.TrimPrefix(lines[0], "event:"))
	dataLine := strings.TrimSpace(strings.TrimPrefix(lines[1], "data:"))
	if !gjson.Valid(dataLine) {
		t.Fatalf("invalid SSE data JSON: %q", dataLine)
	}
	return event, gjson.Parse(dataLine)
}

func TestConvertOpenAIChatCompletionsResponseToOpenAIResponses_MultipleToolCallsRemainSeparate(t *testing.T) {
	in := []string{
		`data: {"id":"resp_test","object":"chat.completion.chunk","created":1773896263,"model":"model","choices":[{"index":0,"delta":{"role":"assistant","content":null,"reasoning_content":null,"tool_calls":[{"index":0,"id":"call_list","type":"function","function":{"name":"shell_command","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"resp_test","object":"chat.completion.chunk","created":1773896263,"model":"model","choices":[{"index":0,"delta":{"role":null,"content":null,"reasoning_content":null,"tool_calls":[{"index":0,"function":{"arguments":"{\"command\":\"Get-ChildItem -Force\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"resp_test","object":"chat.completion.chunk","created":1773896263,"model":"model","choices":[{"index":0,"delta":{"role":"assistant","content":null,"reasoning_content":null,"tool_calls":[{"index":1,"id":"call_rg","type":"function","function":{"name":"shell_command","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"resp_test","object":"chat.completion.chunk","created":1773896263,"model":"model","choices":[{"index":0,"delta":{"role":null,"content":null,"reasoning_content":null,"tool_calls":[{"index":1,"function":{"arguments":"{\"command\":\"rg --files\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"resp_test","object":"chat.completion.chunk","created":1773896263,"model":"model","choices":[{"index":0,"delta":{"role":null,"content":null,"reasoning_content":null,"tool_calls":null},"finish_reason":"tool_calls"}],"usage":{"completion_tokens":10,"total_tokens":20,"prompt_tokens":10}}`,
		`data: [DONE]`,
	}

	request := []byte(`{"model":"gpt-5.5","tool_choice":"auto","parallel_tool_calls":true}`)

	var param any
	var out [][]byte
	for _, line := range in {
		out = append(out, ConvertOpenAIChatCompletionsResponseToOpenAIResponses(context.Background(), "model", request, request, []byte(line), &param)...)
	}

	doneArgs := map[string]string{}
	outputItems := map[string]gjson.Result{}

	for _, chunk := range out {
		ev, data := parseOpenAIResponsesSSEEvent(t, chunk)
		switch ev {
		case "response.output_item.done":
			if data.Get("item.type").String() != "function_call" {
				continue
			}
			doneArgs[data.Get("item.call_id").String()] = data.Get("item.arguments").String()
		case "response.completed":
			for _, item := range data.Get("response.output").Array() {
				if item.Get("type").String() == "function_call" {
					outputItems[item.Get("call_id").String()] = item
				}
			}
		}
	}

	if len(doneArgs) != 2 {
		t.Fatalf("expected 2 function_call done events, got %d", len(doneArgs))
	}
	for callID, args := range doneArgs {
		if !gjson.Valid(args) {
			t.Fatalf("invalid JSON args for %s: %q", callID, args)
		}
		if strings.Contains(args, "}{") {
			t.Fatalf("arguments for %s were concatenated: %q", callID, args)
		}
	}
	if got := gjson.Get(doneArgs["call_list"], "command").String(); got != "Get-ChildItem -Force" {
		t.Fatalf("unexpected command for call_list: %q", got)
	}
	if got := gjson.Get(doneArgs["call_rg"], "command").String(); got != "rg --files" {
		t.Fatalf("unexpected command for call_rg: %q", got)
	}

	if len(outputItems) != 2 {
		t.Fatalf("expected 2 function_call items in response.output, got %d", len(outputItems))
	}
	if outputItems["call_list"].Get("arguments").String() == outputItems["call_rg"].Get("arguments").String() {
		t.Fatalf("expected distinct completed arguments, got %q", outputItems["call_list"].Get("arguments").String())
	}
}
