package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"agentflow/internal/domain"
)

var inlineTOMLHeaderKVRe = regexp.MustCompile(`"([^"]+)"\s*=\s*"([^"]*)"`)

// ParseCodexConfigDetails extracts active provider, base_url, bearer token, default model,
// and reasoning effort from a Codex config.toml payload.
func ParseCodexConfigDetails(raw string) (providerName, baseURL, token, defaultModel, effort string, hasCustomAuth bool) {
	providerName, baseURL, token, defaultModel, effort, _, _, hasCustomAuth = ParseCodexConfigFull(raw)
	return
}

// ParseCodexConfigFull extracts all custom provider fields generically (including any custom
// http_headers or extra TOML directives inside [model_providers.<name>]) without coupling to
// any specific gateway vendor.
func ParseCodexConfigFull(raw string) (providerName, baseURL, token, defaultModel, effort, extraProviderTOML string, headers map[string]string, hasCustomAuth bool) {
	hasCustomAuth = codexConfigHasCustomAuth([]byte(raw))
	section := ""
	type provInfo struct {
		baseURL   string
		token     string
		extra     []string
		headerMap map[string]string
	}
	providers := map[string]*provInfo{}
	getProv := func(name string) *provInfo {
		p := providers[name]
		if p == nil {
			p = &provInfo{headerMap: map[string]string{}}
			providers[name] = p
		}
		return p
	}
	managedProviderKeys := map[string]bool{
		"name":                      true,
		"base_url":                  true,
		"wire_api":                  true,
		"requires_openai_auth":      true,
		"experimental_bearer_token": true,
		"supports_websockets":       true,
	}
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
		rawVal := strings.TrimSpace(line[eq+1:])
		v := rawVal
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
			info := getProv(pname)
			switch k {
			case "base_url":
				info.baseURL = strings.TrimRight(v, "/")
			case "experimental_bearer_token":
				info.token = v
			default:
				if !managedProviderKeys[k] {
					info.extra = append(info.extra, line)
				}
				if k == "http_headers" {
					for _, m := range inlineTOMLHeaderKVRe.FindAllStringSubmatch(rawVal, -1) {
						if len(m) == 3 {
							info.headerMap[m[1]] = m[2]
						}
					}
				}
			}
		}
	}
	pick := func(pname string, info *provInfo) (string, string, string, string, string, string, map[string]string, bool) {
		return pname, info.baseURL, info.token, defaultModel, effort, strings.Join(info.extra, "\n"), info.headerMap, hasCustomAuth
	}
	if providerName != "" {
		if info, ok := providers[providerName]; ok {
			return pick(providerName, info)
		}
	}
	for pname, info := range providers {
		if info.baseURL != "" && info.token != "" {
			return pick(pname, info)
		}
	}
	return providerName, "", "", defaultModel, effort, "", nil, hasCustomAuth
}

type modelAccessibilityCacheEntry struct {
	Key        string            `json:"key"`
	Accessible map[string]bool   `json:"accessible"`
	CheckedAt  time.Time         `json:"checkedAt"`
}

var (
	modelAccessibilityMemMu sync.Mutex
	modelAccessibilityMem   = map[string]modelAccessibilityCacheEntry{}
)

const modelAccessibilityTTL = 30 * time.Minute

func isUnaccessibleGatewayModelError(statusCode int, body string) bool {
	if statusCode != http.StatusBadRequest && statusCode != http.StatusNotFound && statusCode != http.StatusServiceUnavailable {
		return false
	}
	low := strings.ToLower(body)
	for _, marker := range []string{
		"model_not_found",
		"model_not_in_accessible_catalog",
		"is not supported when using",
		"does not exist",
		"no auth available",
		"auth_unavailable",
		"no available channel",
		"unsupported model",
	} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}

// FilterAccessibleModelIDs probes an OpenAI Responses-compatible gateway once per
// (baseURL, token, modelList) signature (cached for 30m in memory and optionally on disk)
// and removes static /models entries whose upstream channel returns model_not_found / no_auth.
// Local/loopback mock test servers without a /responses handler (returning plain 404 without
// model error JSON) keep all models untouched so unit tests remain unaffected.
func FilterAccessibleModelIDs(ctx context.Context, cacheDir, baseURL, token string, headers map[string]string, ids []string) []string {
	if len(ids) == 0 || strings.TrimSpace(baseURL) == "" || strings.TrimSpace(token) == "" {
		return ids
	}
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	sigRaw := strings.TrimRight(baseURL, "/") + "\x00" + token + "\x00" + strings.Join(sorted, ",")
	sum := sha256.Sum256([]byte(sigRaw))
	cacheKey := hex.EncodeToString(sum[:16])

	now := time.Now()
	modelAccessibilityMemMu.Lock()
	if entry, ok := modelAccessibilityMem[cacheKey]; ok && now.Sub(entry.CheckedAt) < modelAccessibilityTTL {
		modelAccessibilityMemMu.Unlock()
		return applyAccessibilityMap(ids, entry.Accessible)
	}
	modelAccessibilityMemMu.Unlock()

	diskPath := ""
	if strings.TrimSpace(cacheDir) != "" {
		diskPath = filepath.Join(cacheDir, ".model-accessibility-cache.json")
		if raw, err := os.ReadFile(diskPath); err == nil {
			var entry modelAccessibilityCacheEntry
			if json.Unmarshal(raw, &entry) == nil && entry.Key == cacheKey && now.Sub(entry.CheckedAt) < modelAccessibilityTTL {
				modelAccessibilityMemMu.Lock()
				modelAccessibilityMem[cacheKey] = entry
				modelAccessibilityMemMu.Unlock()
				return applyAccessibilityMap(ids, entry.Accessible)
			}
		}
	}

	endpoint := strings.TrimRight(baseURL, "/") + "/responses"
	accessible := make(map[string]bool, len(ids))
	var mu sync.Mutex
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup

	for _, id := range ids {
		wg.Add(1)
		go func(modelID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			pctx, cancel := context.WithTimeout(ctx, 4500*time.Millisecond)
			defer cancel()
			payload, _ := json.Marshal(map[string]any{
				"model":             modelID,
				"input":             "ok",
				"max_output_tokens": 1,
				"stream":            true,
			})
			req, err := http.NewRequestWithContext(pctx, http.MethodPost, endpoint, bytes.NewReader(payload))
			if err != nil {
				mu.Lock()
				accessible[modelID] = true
				mu.Unlock()
				return
			}
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			for hk, hv := range headers {
				req.Header.Set(hk, hv)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				// Keep model on transient network timeout so valid models are never dropped.
				mu.Lock()
				accessible[modelID] = true
				mu.Unlock()
				return
			}
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				// Close stream immediately upon 200 headers to avoid token generation overhead.
				_ = resp.Body.Close()
				mu.Lock()
				accessible[modelID] = true
				mu.Unlock()
				return
			}
			errBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			unusable := isUnaccessibleGatewayModelError(resp.StatusCode, string(errBytes))
			mu.Lock()
			accessible[modelID] = !unusable
			mu.Unlock()
		}(id)
	}
	wg.Wait()

	filtered := applyAccessibilityMap(ids, accessible)
	if len(filtered) == 0 {
		// Safety net: if every model was rejected (e.g. non-responses endpoint), keep original list.
		return ids
	}

	entry := modelAccessibilityCacheEntry{
		Key:        cacheKey,
		Accessible: accessible,
		CheckedAt:  now,
	}
	modelAccessibilityMemMu.Lock()
	modelAccessibilityMem[cacheKey] = entry
	modelAccessibilityMemMu.Unlock()
	if diskPath != "" {
		if encoded, err := json.Marshal(entry); err == nil {
			_ = os.WriteFile(diskPath, encoded, 0o600)
		}
	}
	return filtered
}

func applyAccessibilityMap(ids []string, accessible map[string]bool) []string {
	if len(accessible) == 0 {
		return ids
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if ok, exists := accessible[id]; !exists || ok {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return ids
	}
	return out
}

func discoverCodexCustomConfigModels(ctx context.Context, configPath string) []domain.CLIModel {
	rawBytes, err := os.ReadFile(configPath)
	if err != nil {
		return nil
	}
	_, baseURL, token, defaultModel, _, _, headers, hasCustom := ParseCodexConfigFull(string(rawBytes))
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
			for hk, hv := range headers {
				req.Header.Set(hk, hv)
			}
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
		if len(out) > 1 {
			ids := make([]string, 0, len(out))
			byID := make(map[string]domain.CLIModel, len(out))
			for _, m := range out {
				ids = append(ids, m.ID)
				byID[m.ID] = m
			}
			validIDs := FilterAccessibleModelIDs(ctx, filepath.Dir(configPath), baseURL, token, headers, ids)
			filtered := make([]domain.CLIModel, 0, len(validIDs))
			for _, id := range validIDs {
				if m, ok := byID[id]; ok {
					filtered = append(filtered, m)
				}
			}
			if len(filtered) > 0 {
				out = filtered
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
