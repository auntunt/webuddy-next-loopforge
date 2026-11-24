package builtinagent

import (
	"testing"
)

func TestRankFreeModelsPrefersLargeParamsAndExcludesNonText(t *testing.T) {
	var p orModelsPayload
	p.Data = []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Created       int64  `json:"created"`
		ContextLength int    `json:"context_length"`
		Architecture  struct {
			Modality string `json:"modality"`
		} `json:"architecture"`
		Pricing struct {
			Prompt     string `json:"prompt"`
			Completion string `json:"completion"`
		} `json:"pricing"`
	}{
		{
			ID: "liquid/lfm-3-1.7b:free", Name: "LFM 1.7B (free)", Created: 1795000000, ContextLength: 262144,
			Architecture: struct {
				Modality string `json:"modality"`
			}{Modality: "text->text"},
		},
		{
			ID: "google/lyria-3-pro-preview", Name: "Lyria Audio", Created: 1795100000, ContextLength: 1000000,
			Architecture: struct {
				Modality string `json:"modality"`
			}{Modality: "text->audio"},
			Pricing: struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			}{Prompt: "0", Completion: "0"},
		},
		{
			ID: "nvidia/nemotron-3-ultra-550b-a55b:free", Name: "NVIDIA Nemotron 3 Ultra (free)", Created: 1794000000, ContextLength: 1000000,
			Architecture: struct {
				Modality string `json:"modality"`
			}{Modality: "text->text"},
		},
		{
			ID: "google/gemma-4-31b-it:free", Name: "Google Gemma 4 31B (free)", Created: 1794500000, ContextLength: 262144,
			Architecture: struct {
				Modality string `json:"modality"`
			}{Modality: "text->text"},
		},
	}

	ranked := rankFreeModels(p)
	for _, m := range ranked {
		if m.ID == "google/lyria-3-pro-preview" {
			t.Fatalf("expected non-text audio model to be excluded")
		}
	}
	if len(ranked) < 3 {
		t.Fatalf("expected at least 3 ranked text models, got %d", len(ranked))
	}
	if ranked[0].ID != "nvidia/nemotron-3-ultra-550b-a55b:free" {
		t.Fatalf("expected 550B ultra model ranked #1, got %q", ranked[0].ID)
	}
	if ranked[0].ParamBillions != 550 {
		t.Fatalf("expected ParamBillions=550, got %v", ranked[0].ParamBillions)
	}
}
