package executor

import (
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func normalizeOpenAICompatThinkingToolCalls(body []byte) ([]byte, error) {
	if !openAICompatReasoningEnabled(body) {
		return body, nil
	}
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return body, nil
	}

	out := body
	latestReasoning := ""
	hasLatestReasoning := false
	patched := 0

	for msgIdx, msg := range messages.Array() {
		if strings.TrimSpace(msg.Get("role").String()) != "assistant" {
			continue
		}
		reasoning := msg.Get("reasoning_content")
		if reasoning.Exists() && strings.TrimSpace(reasoning.String()) != "" {
			latestReasoning = reasoning.String()
			hasLatestReasoning = true
		}
		toolCalls := msg.Get("tool_calls")
		if !toolCalls.Exists() || !toolCalls.IsArray() || len(toolCalls.Array()) == 0 {
			continue
		}
		if reasoning.Exists() && strings.TrimSpace(reasoning.String()) != "" {
			continue
		}
		reasoningText := fallbackAssistantReasoning(msg, hasLatestReasoning, latestReasoning)
		next, err := sjson.SetBytes(out, fmt.Sprintf("messages.%d.reasoning_content", msgIdx), reasoningText)
		if err != nil {
			return body, fmt.Errorf("openai compat executor: failed to set assistant reasoning_content: %w", err)
		}
		out = next
		patched++
	}
	if patched > 0 {
		log.WithField("patched_reasoning_messages", patched).Debug("openai compat executor: normalized assistant tool-call reasoning")
	}
	return out, nil
}

func openAICompatReasoningEnabled(body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	effort := strings.TrimSpace(gjson.GetBytes(body, "reasoning_effort").String())
	if effort != "" && !strings.EqualFold(effort, "none") && !strings.EqualFold(effort, "disabled") {
		return true
	}
	reasoning := gjson.GetBytes(body, "reasoning")
	if reasoning.Exists() && reasoning.IsObject() {
		if disabled := strings.TrimSpace(reasoning.Get("type").String()); strings.EqualFold(disabled, "disabled") {
			return false
		}
		return true
	}
	return false
}
