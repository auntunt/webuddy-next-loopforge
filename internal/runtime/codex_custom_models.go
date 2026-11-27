package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"agentflow/internal/domain"
)

// ParseCodexConfigDetails extracts active provider, base_url, bearer token, default model,
// and reasoning effort from a Codex config.toml payload.
func ParseCodexConfigDetails(raw string) (providerName, baseURL, token, defaultModel, effort string, hasCustomAuth bool) {
	hasCustomAuth = codexConfigHasCustomAuth([]byte(raw))
	section := ""
	type provInfo struct {
		baseURL string
		token   string
	}
	providers := map[string]provInfo{}
	for _, ln := range strings.Split(raw, "\n") {
		line := strings.TrimSpace(ln)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		if idx := strings.Index(v, " #"); idx >= 0 {
			v = strings.TrimSpace(v[:idx])
		}
		v = strings.Trim(v, `"'`)
		if section == "" {
			switch k {
			case "model_provider":
				providerName = v
			case "model":
				defaultModel = v
			case "model_reasoning_effort":
				effort = v
			}
		} else if strings.HasPrefix(section, "model_providers.") {
			pname := strings.Trim(strings.TrimPrefix(section, "model_providers."), `"'`)
			info := providers[pname]
			switch k {
			case "base_url":
				info.baseURL = strings.TrimRight(v, "/")
			case "experimental_bearer_token":
				info.token = v
			}
			providers[pname] = info
		}
	}
	if providerName != "" {
		if info, ok := providers[providerName]; ok {
			return providerName, info.baseURL, info.token, defaultModel, effort, hasCustomAuth
		}
	}
	for pname, info := range providers {
		if info.baseURL != "" && info.token != "" {
			return pname, info.baseURL, info.token, defaultModel, effort, hasCustomAuth
		}
	}
	return providerName, "", "", defaultModel, effort, hasCustomAuth
}

func discoverCodexCustomConfigModels(ctx context.Context, configPath string) []domain.CLIModel {
	rawBytes, err := os.ReadFile(configPath)
	if err != nil {
		return nil
	}
	_, baseURL, token, defaultModel, _, hasCustom := ParseCodexConfigDetails(string(rawBytes))
	if !hasCustom && baseURL == "" {
		return nil
	}
	efforts := []string{"low", "medium", "high", "xhigh", "max", "ultra"}
	makeModel := func(id, name string) domain.CLIModel {
		if strings.TrimSpace(name) == "" {
			name = id
		}
		cp := append([]string(nil), efforts...)
		return domain.CLIModel{
			ID:                         id,
			Name:                       name,
			DiscoveredReasoningEfforts: &cp,
		}
	}
	var out []domain.CLIModel
	seen := map[string]bool{}
	if dm := strings.TrimSpace(defaultModel); dm != "" {
		seen[dm] = true
		out = append(out, makeModel(dm, dm))
	}
	if baseURL != "" && token != "" {
		qctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
		req, reqErr := http.NewRequestWithContext(qctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
		if reqErr == nil {
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Accept", "application/json")
			if resp, doErr := http.DefaultClient.Do(req); doErr == nil {
				defer resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					var payload struct {
						Data []struct {
							ID          string `json:"id"`
							DisplayName string `json:"display_name"`
							Name        string `json:"name"`
						} `json:"data"`
					}
					if body, rErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20)); rErr == nil && json.Unmarshal(body, &payload) == nil {
						for _, item := range payload.Data {
							id := strings.TrimSpace(item.ID)
							if id == "" || strings.HasPrefix(strings.ToLower(id), "gpt-image") || seen[id] || strings.ContainsAny(id, " \t\r\n") {
								continue
							}
							seen[id] = true
							label := strings.TrimSpace(item.DisplayName)
							if label == "" {
								label = strings.TrimSpace(item.Name)
							}
							out = append(out, makeModel(id, label))
						}
					}
				}
			}
		}
	}
	return out
}

func discoverClaudeCustomSettingsModels(ctx context.Context, settingsPath string) []domain.CLIModel {
	rawBytes, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil
	}
	var doc struct {
		Model string            `json:"model"`
		Env   map[string]string `json:"env"`
	}
	if json.Unmarshal(rawBytes, &doc) != nil {
		return nil
	}
	token := strings.TrimSpace(doc.Env["ANTHROPIC_AUTH_TOKEN"])
	if token == "" {
		token = strings.TrimSpace(doc.Env["ANTHROPIC_API_KEY"])
	}
	baseURL := strings.TrimRight(strings.TrimSpace(doc.Env["ANTHROPIC_BASE_URL"]), "/")
	defaultModel := strings.TrimSpace(doc.Model)
	if defaultModel == "" {
		defaultModel = strings.TrimSpace(doc.Env["ANTHROPIC_MODEL"])
	}
	if token == "" && defaultModel == "" {
		return nil
	}
	efforts := []string{"low", "medium", "high", "max"}
	makeModel := func(id, name string) domain.CLIModel {
		if strings.TrimSpace(name) == "" {
			name = id
		}
		cp := append([]string(nil), efforts...)
		return domain.CLIModel{ID: id, Name: name, DiscoveredReasoningEfforts: &cp}
	}
	var out []domain.CLIModel
	seen := map[string]bool{}
	if defaultModel != "" {
		seen[defaultModel] = true
		out = append(out, makeModel(defaultModel, defaultModel))
	}
	if baseURL != "" && token != "" {
		endpoint := baseURL
		if !strings.HasSuffix(endpoint, "/v1") {
			endpoint += "/v1"
		}
		endpoint += "/models"
		qctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
		req, reqErr := http.NewRequestWithContext(qctx, http.MethodGet, endpoint, nil)
		if reqErr == nil {
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("x-api-key", token)
			req.Header.Set("anthropic-version", "2023-06-01")
			if resp, doErr := http.DefaultClient.Do(req); doErr == nil {
				defer resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					var payload struct {
						Data []struct {
							ID          string `json:"id"`
							DisplayName string `json:"display_name"`
						} `json:"data"`
					}
					if body, rErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20)); rErr == nil && json.Unmarshal(body, &payload) == nil {
						for _, item := range payload.Data {
							id := strings.TrimSpace(item.ID)
							if id == "" || seen[id] || strings.ContainsAny(id, " \t\r\n") {
								continue
							}
							seen[id] = true
							out = append(out, makeModel(id, item.DisplayName))
						}
					}
				}
			}
		}
	}
	return out
}
