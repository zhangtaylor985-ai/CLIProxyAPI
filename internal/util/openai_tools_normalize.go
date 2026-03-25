package util

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// NormalizeOpenAIToolsPayload removes/normalizes provider-specific tool wrappers so that
// OpenAI-compatible backends won't reject the request with unknown parameters such as
// "tools.defer_loading".
//
// It is intentionally best-effort and schema-tolerant:
//   - If "tools" is an object wrapper like {"defer_loading":true,"tools":[...]}, it unwraps it.
//   - If "tool_choice" is nested under tools, it hoists it to top-level when absent.
//   - If "tools" is an array, it removes "defer_loading" fields from tool entries.
//   - If "tools" remains an object/invalid type after normalization, it drops "tools" entirely.
func NormalizeOpenAIToolsPayload(payload []byte) []byte {
	if len(payload) == 0 || !gjson.ValidBytes(payload) {
		return payload
	}

	tools := gjson.GetBytes(payload, "tools")
	if !tools.Exists() {
		return sanitizeOpenAIToolSelection(payload)
	}

	out := payload

	switch {
	case tools.IsArray():
		arr := tools.Array()
		for i := 0; i < len(arr); i++ {
			if !arr[i].IsObject() {
				continue
			}
			if arr[i].Get("defer_loading").Exists() {
				if updated, err := sjson.DeleteBytes(out, fmt.Sprintf("tools.%d.defer_loading", i)); err == nil {
					out = updated
				}
			}
			if arr[i].Get("function.defer_loading").Exists() {
				if updated, err := sjson.DeleteBytes(out, fmt.Sprintf("tools.%d.function.defer_loading", i)); err == nil {
					out = updated
				}
			}
		}
		return sanitizeOpenAIToolSelection(out)

	case tools.IsObject():
		if updated, err := sjson.DeleteBytes(out, "tools.defer_loading"); err == nil {
			out = updated
		}

		if !gjson.GetBytes(out, "tool_choice").Exists() {
			if nestedChoice := gjson.GetBytes(out, "tools.tool_choice"); nestedChoice.Exists() {
				if updated, err := sjson.SetRawBytes(out, "tool_choice", []byte(nestedChoice.Raw)); err == nil {
					out = updated
				}
			}
		}

		if nested := gjson.GetBytes(out, "tools.tools"); nested.Exists() && nested.IsArray() {
			if updated, err := sjson.SetRawBytes(out, "tools", []byte(nested.Raw)); err == nil {
				out = updated
			}
			return NormalizeOpenAIToolsPayload(out)
		}

		// Still an object (unknown schema). OpenAI expects tools to be an array; drop it to avoid 400s.
		if gjson.GetBytes(out, "tools").IsObject() {
			if updated, err := sjson.DeleteBytes(out, "tools"); err == nil {
				out = updated
			}
		}
		return sanitizeOpenAIToolSelection(out)

	default:
		if updated, err := sjson.DeleteBytes(out, "tools"); err == nil {
			out = updated
		}
		return sanitizeOpenAIToolSelection(out)
	}
}

func sanitizeOpenAIToolSelection(payload []byte) []byte {
	if len(payload) == 0 || !gjson.ValidBytes(payload) {
		return payload
	}

	out := payload
	hasDeclaredTools := false
	functionNames := make(map[string]struct{})

	if tools := gjson.GetBytes(out, "tools"); tools.Exists() && tools.IsArray() {
		arr := tools.Array()
		for i := 0; i < len(arr); i++ {
			if !arr[i].IsObject() {
				continue
			}
			hasDeclaredTools = true
			if name := strings.TrimSpace(arr[i].Get("function.name").String()); name != "" {
				functionNames[name] = struct{}{}
				continue
			}
			if name := strings.TrimSpace(arr[i].Get("name").String()); name != "" {
				functionNames[name] = struct{}{}
			}
		}
	}

	if functions := gjson.GetBytes(out, "functions"); functions.Exists() && functions.IsArray() {
		arr := functions.Array()
		for i := 0; i < len(arr); i++ {
			if !arr[i].IsObject() {
				continue
			}
			hasDeclaredTools = true
			if name := strings.TrimSpace(arr[i].Get("name").String()); name != "" {
				functionNames[name] = struct{}{}
			}
		}
	}

	if !hasDeclaredTools {
		for _, path := range []string{"tool_choice", "parallel_tool_calls", "function_call"} {
			if updated, err := sjson.DeleteBytes(out, path); err == nil {
				out = updated
			}
		}
		return out
	}

	if toolChoice := gjson.GetBytes(out, "tool_choice"); toolChoice.Exists() && toolChoice.IsObject() {
		if strings.EqualFold(strings.TrimSpace(toolChoice.Get("type").String()), "function") {
			name := strings.TrimSpace(toolChoice.Get("function.name").String())
			if name == "" {
				name = strings.TrimSpace(toolChoice.Get("name").String())
			}
			if name == "" {
				if updated, err := sjson.DeleteBytes(out, "tool_choice"); err == nil {
					out = updated
				}
			} else if _, ok := functionNames[name]; !ok {
				if updated, err := sjson.DeleteBytes(out, "tool_choice"); err == nil {
					out = updated
				}
			}
		}
	}

	if functionCall := gjson.GetBytes(out, "function_call"); functionCall.Exists() && functionCall.IsObject() {
		name := strings.TrimSpace(functionCall.Get("name").String())
		if name != "" {
			if _, ok := functionNames[name]; !ok {
				if updated, err := sjson.DeleteBytes(out, "function_call"); err == nil {
					out = updated
				}
			}
		}
	}

	return out
}

// StripOpenAIToolsForImageInputs removes tool-related fields from multimodal OpenAI payloads.
// Some OpenAI-compatible backends reject top-level tool parameters when image inputs are present,
// so this keeps Claude -> GPT failover working for image uploads.
func StripOpenAIToolsForImageInputs(payload []byte) []byte {
	if len(payload) == 0 || !gjson.ValidBytes(payload) || !hasOpenAIImageInput(payload) {
		return payload
	}

	out := payload
	for _, path := range []string{"tools", "tool_choice", "parallel_tool_calls", "functions", "function_call"} {
		if updated, err := sjson.DeleteBytes(out, path); err == nil {
			out = updated
		}
	}
	return out
}

func hasOpenAIImageInput(payload []byte) bool {
	hasImagePart := func(content gjson.Result) bool {
		switch {
		case content.IsArray():
			parts := content.Array()
			for i := 0; i < len(parts); i++ {
				partType := parts[i].Get("type").String()
				switch partType {
				case "image_url", "input_image", "image":
					return true
				}
			}
		case content.IsObject():
			partType := content.Get("type").String()
			switch partType {
			case "image_url", "input_image", "image":
				return true
			}
		}
		return false
	}

	if messages := gjson.GetBytes(payload, "messages"); messages.IsArray() {
		arr := messages.Array()
		for i := 0; i < len(arr); i++ {
			if hasImagePart(arr[i].Get("content")) {
				return true
			}
		}
	}

	if input := gjson.GetBytes(payload, "input"); input.IsArray() {
		arr := input.Array()
		for i := 0; i < len(arr); i++ {
			item := arr[i]
			if item.Get("type").String() == "input_image" {
				return true
			}
			if hasImagePart(item.Get("content")) {
				return true
			}
		}
	}

	return false
}
