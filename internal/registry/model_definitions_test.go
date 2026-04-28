package registry

import "testing"

func TestCodexGPT54And55DefinitionsMatchCurrentMetadata(t *testing.T) {
	for name, models := range map[string][]*ModelInfo{
		"plus": GetCodexPlusModels(),
		"pro":  GetCodexProModels(),
		"team": GetCodexTeamModels(),
	} {
		for _, tt := range []struct {
			id            string
			created       int64
			description   string
			contextLength int
		}{
			{
				id:            "gpt-5.4",
				created:       1772668800,
				description:   "Stable version of GPT 5.4",
				contextLength: 272000,
			},
			{
				id:            "gpt-5.5",
				created:       1776902400,
				description:   "Frontier model for complex coding, research, and real-world work.",
				contextLength: 400000,
			},
		} {
			model := findModelInfo(models, tt.id)
			if model == nil {
				t.Fatalf("expected %s in codex-%s models", tt.id, name)
			}
			if model.Created != tt.created {
				t.Fatalf("codex-%s %s created = %d, want %d", name, tt.id, model.Created, tt.created)
			}
			if model.Description != tt.description {
				t.Fatalf("codex-%s %s description = %q", name, tt.id, model.Description)
			}
			if model.ContextLength != tt.contextLength {
				t.Fatalf("codex-%s %s context_length = %d, want %d", name, tt.id, model.ContextLength, tt.contextLength)
			}
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
