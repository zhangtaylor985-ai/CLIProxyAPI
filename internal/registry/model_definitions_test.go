package registry

import "testing"

func TestCodexGPT55DefinitionsMatchCurrentMetadata(t *testing.T) {
	for name, models := range map[string][]*ModelInfo{
		"plus": GetCodexPlusModels(),
		"pro":  GetCodexProModels(),
		"team": GetCodexTeamModels(),
	} {
		model := findModelInfo(models, "gpt-5.5")
		if model == nil {
			t.Fatalf("expected gpt-5.5 in codex-%s models", name)
		}
		if model.Created != 1776902400 {
			t.Fatalf("codex-%s gpt-5.5 created = %d, want 1776902400", name, model.Created)
		}
		if model.Description != "Frontier model for complex coding, research, and real-world work." {
			t.Fatalf("codex-%s gpt-5.5 description = %q", name, model.Description)
		}
		if model.ContextLength != 272000 {
			t.Fatalf("codex-%s gpt-5.5 context_length = %d, want 272000", name, model.ContextLength)
		}
	}
}

func TestCodexBuiltinsIncludeGPTImage2(t *testing.T) {
	for name, models := range map[string][]*ModelInfo{
		"free": GetCodexFreeModels(),
		"plus": GetCodexPlusModels(),
		"pro":  GetCodexProModels(),
		"team": GetCodexTeamModels(),
	} {
		model := findModelInfo(models, "gpt-image-2")
		if model == nil {
			t.Fatalf("expected gpt-image-2 in codex-%s models", name)
		}
		if model.DisplayName != "GPT Image 2" {
			t.Fatalf("codex-%s gpt-image-2 display name = %q", name, model.DisplayName)
		}
	}
}

func findModelInfo(models []*ModelInfo, id string) *ModelInfo {
	for _, model := range models {
		if model != nil && model.ID == id {
			return model
		}
	}
	return nil
}
