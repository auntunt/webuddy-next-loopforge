package builtinagent

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	OpenRouterBaseURL = "https://openrouter.ai/api/v1"
	catalogTTL        = 6 * time.Hour
)

type FreeModelInfo struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	ContextLength int     `json:"contextLength"`
	ParamBillions float64 `json:"paramBillions,omitempty"`
	Created       int64   `json:"created,omitempty"`
}

type OpenRouterCatalog struct {
	mu        sync.RWMutex
	models    []FreeModelInfo
	fetchedAt time.Time
}

func NewOpenRouterCatalog() *OpenRouterCatalog {
	return &OpenRouterCatalog{}
}

// TopFreeModels returns the ranked list of newest, largest-parameter free text
// models on OpenRouter, cached for 6 hours. Falls back to openrouter/free if
// the catalog endpoint is temporarily unreachable.
func (c *OpenRouterCatalog) TopFreeModels(ctx context.Context, limit int) []FreeModelInfo {
	if limit <= 0 {
		limit = 5
	}
	c.mu.RLock()
	if len(c.models) > 0 && time.Since(c.fetchedAt) < catalogTTL {
		out := sliceCopy(c.models, limit)
		c.mu.RUnlock()
		return out
	}
	c.mu.RUnlock()

	fetched, err := fetchOpenRouterFreeModels(ctx)
	if err == nil && len(fetched) > 0 {
		c.mu.Lock()
		c.models = fetched
		c.fetchedAt = time.Now()
		c.mu.Unlock()
		return sliceCopy(fetched, limit)
	}

	c.mu.RLock()
	if len(c.models) > 0 {
		out := sliceCopy(c.models, limit)
		c.mu.RUnlock()
		return out
	}
	c.mu.RUnlock()

	return []FreeModelInfo{
		{ID: "openrouter/free", Name: "OpenRouter Free Auto-Router", ContextLength: 128000},
	}
}

func sliceCopy(in []FreeModelInfo, limit int) []FreeModelInfo {
	if len(in) > limit {
		in = in[:limit]
	}
	out := make([]FreeModelInfo, len(in))
	copy(out, in)
	return out
}

type orModelsPayload struct {
	Data []struct {
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
	} `json:"data"`
}

func fetchOpenRouterFreeModels(ctx context.Context) ([]FreeModelInfo, error) {
	tctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(tctx, http.MethodGet, OpenRouterBaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, err
	}
	var payload orModelsPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return rankFreeModels(payload), nil
}

var paramSizeRe = regexp.MustCompile(`(?i)(?:^|[-_/\s:])(\d+(?:\.\d+)?)\s*b(?:\b|[-_/])`)

func parseParamBillions(id, name string) float64 {
	best := 0.0
	for _, s := range []string{id, name} {
		for _, m := range paramSizeRe.FindAllStringSubmatch(s, -1) {
			if len(m) == 2 {
				if v, err := strconv.ParseFloat(m[1], 64); err == nil && v > best && v <= 2000 {
					best = v
				}
			}
		}
	}
	return best
}

func rankFreeModels(payload orModelsPayload) []FreeModelInfo {
	var out []FreeModelInfo
	for _, m := range payload.Data {
		isFree := strings.HasSuffix(m.ID, ":free") || (m.Pricing.Prompt == "0" && m.Pricing.Completion == "0")
		if !isFree {
			continue
		}
		// Skip non-text output models (audio/image generators like lyria)
		mod := strings.ToLower(m.Architecture.Modality)
		if mod != "" && !strings.Contains(mod, "->text") {
			continue
		}
		lowerID := strings.ToLower(m.ID)
		if strings.Contains(lowerID, "lyria") || strings.Contains(lowerID, "whisper") || strings.Contains(lowerID, "tts") {
			continue
		}
		out = append(out, FreeModelInfo{
			ID:            m.ID,
			Name:          m.Name,
			ContextLength: m.ContextLength,
			ParamBillions: parseParamBillions(m.ID, m.Name),
			Created:       m.Created,
		})
	}

	score := func(m FreeModelInfo) float64 {
		s := 0.0
		// Strongly prefer larger parameter counts (e.g. 550B > 70B > 31B > 1.7B)
		if m.ParamBillions >= 100 {
			s += 1000 + m.ParamBillions
		} else if m.ParamBillions >= 25 {
			s += 600 + m.ParamBillions*4
		} else if m.ParamBillions >= 7 {
			s += 300 + m.ParamBillions*5
		} else if m.ParamBillions > 0 {
			// Tiny models (1B-3B) ranked lower
			s += m.ParamBillions * 10
		} else {
			// Unlabeled flagship models (e.g. ernie-5.5-flash, nemotron-3.5-super, ling-3.2)
			s += 550
		}
		lower := strings.ToLower(m.ID + " " + m.Name)
		if strings.Contains(lower, "ultra") || strings.Contains(lower, "super") || strings.Contains(lower, "pro") {
			s += 180
		}
		if strings.Contains(lower, "deepseek") || strings.Contains(lower, "qwen") || strings.Contains(lower, "nemotron") || strings.Contains(lower, "ernie") {
			s += 120
		}
		if strings.Contains(lower, "nano") || strings.Contains(lower, "mini") || strings.Contains(lower, "1.7b") {
			s -= 150
		}
		if m.ContextLength >= 500000 {
			s += 90
		} else if m.ContextLength >= 128000 {
			s += 50
		}
		// Recency tie-breaker (normalized to ~0-60 range)
		if m.Created > 1700000000 {
			s += float64(m.Created-1700000000) / 2000000.0
		}
		return s
	}

	sort.Slice(out, func(i, j int) bool {
		return score(out[i]) > score(out[j])
	})

	// Ensure openrouter/free auto-router is available as a final fallback
	hasAuto := false
	for _, m := range out {
		if m.ID == "openrouter/free" {
			hasAuto = true
			break
		}
	}
	if !hasAuto {
		out = append(out, FreeModelInfo{
			ID:            "openrouter/free",
			Name:          "OpenRouter Free Auto-Router",
			ContextLength: 128000,
		})
	}
	return out
}
