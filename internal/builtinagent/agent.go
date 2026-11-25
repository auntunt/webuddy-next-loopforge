package builtinagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agentflow/internal/envprobe"
)

type CustomConfig struct {
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey,omitempty"`
	Model   string `json:"model"`
}

type PublicStatus struct {
	Ready        bool            `json:"ready"`
	Mode         string          `json:"mode"` // "custom" | "openrouter_free" | "local_gateway" | "unconfigured"
	ModeLabel    string          `json:"modeLabel"`
	ActiveModel  string          `json:"activeModel,omitempty"`
	FreeModels   []FreeModelInfo `json:"freeModels,omitempty"`
	HasOpenRouter bool           `json:"hasOpenRouter"`
	Custom       CustomConfig    `json:"custom"`
	CustomHasKey bool            `json:"customHasKey"`
}

type CompletionRequest struct {
	SystemPrompt string
	UserPrompt   string
	MaxTokens    int
	Temperature  float64
	Timeout      time.Duration
}

type CompletionResult struct {
	Text        string   `json:"text"`
	Model       string   `json:"model"`
	Provider    string   `json:"provider"` // "custom" | "openrouter_free" | "local_gateway" | "codex_cli"
	LatencyMs   int64    `json:"latencyMs"`
	TriedModels []string `json:"triedModels,omitempty"`
	Error       string   `json:"error,omitempty"`
}

type Manager struct {
	catalog    *OpenRouterCatalog
	configPath string
	mu         sync.RWMutex
	custom     CustomConfig
}

var (
	defaultOnce sync.Once
	defaultMgr  *Manager
)

func Default() *Manager {
	defaultOnce.Do(func() {
		home, _ := os.UserHomeDir()
		cfgPath := ""
		if home != "" {
			cfgPath = filepath.Join(home, ".webuddy-builtin-llm.json")
		}
		defaultMgr = NewManager(cfgPath)
	})
	return defaultMgr
}

func NewManager(configPath string) *Manager {
	m := &Manager{
		catalog:    NewOpenRouterCatalog(),
		configPath: configPath,
	}
	m.loadCustomFromDisk()
	return m
}

func (m *Manager) loadCustomFromDisk() {
	if m.configPath == "" {
		return
	}
	raw, err := os.ReadFile(m.configPath)
	if err != nil {
		return
	}
	var cfg CustomConfig
	if json.Unmarshal(raw, &cfg) == nil {
		m.mu.Lock()
		m.custom = CustomConfig{
			BaseURL: strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
			APIKey:  strings.TrimSpace(cfg.APIKey),
			Model:   strings.TrimSpace(cfg.Model),
		}
		m.mu.Unlock()
	}
}

func (m *Manager) SaveCustomConfig(cfg CustomConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	clean := CustomConfig{
		BaseURL: strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		APIKey:  strings.TrimSpace(cfg.APIKey),
		Model:   strings.TrimSpace(cfg.Model),
	}
	// Preserve existing key if user left apiKey blank while keeping same baseUrl
	if clean.APIKey == "" && clean.BaseURL != "" && clean.BaseURL == m.custom.BaseURL {
		clean.APIKey = m.custom.APIKey
	}
	// If baseUrl is cleared, clear everything (revert to default OpenRouter free mode)
	if clean.BaseURL == "" {
		clean = CustomConfig{}
	}

	if m.configPath != "" {
		raw, err := json.MarshalIndent(clean, "", "  ")
		if err != nil {
			return err
		}
		tmp := m.configPath + ".tmp"
		if err := os.WriteFile(tmp, raw, 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, m.configPath); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	m.custom = clean
	return nil
}

func (m *Manager) effectiveCustomConfig() (CustomConfig, string) {
	m.mu.RLock()
	custom := m.custom
	m.mu.RUnlock()
	if custom.BaseURL != "" && custom.APIKey != "" {
		return custom, "ui"
	}
	envURL := strings.TrimRight(strings.TrimSpace(os.Getenv("WEBUDDY_LLM_BASE_URL")), "/")
	envKey := strings.TrimSpace(os.Getenv("WEBUDDY_LLM_API_KEY"))
	if envURL != "" && envKey != "" {
		return CustomConfig{
			BaseURL: envURL,
			APIKey:  envKey,
			Model:   strings.TrimSpace(os.Getenv("WEBUDDY_LLM_MODEL")),
		}, "env"
	}
	return CustomConfig{}, ""
}

func (m *Manager) Status(ctx context.Context) PublicStatus {
	m.mu.RLock()
	custom := m.custom
	m.mu.RUnlock()

	orKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	freeModels := m.catalog.TopFreeModels(ctx, 5)
	st := PublicStatus{
		FreeModels:    freeModels,
		HasOpenRouter: orKey != "",
		Custom: CustomConfig{
			BaseURL: custom.BaseURL,
			Model:   custom.Model,
		},
		CustomHasKey: custom.APIKey != "",
	}

	if eff, src := m.effectiveCustomConfig(); eff.BaseURL != "" && eff.APIKey != "" {
		st.Ready = true
		st.Mode = "custom"
		if src == "env" {
			st.ModeLabel = "已接入自定义模型（环境变量）"
		} else {
			st.ModeLabel = "已接入自定义模型"
		}
		st.ActiveModel = eff.Model
		return st
	}
	if orKey != "" {
		st.Ready = true
		st.Mode = "openrouter_free"
		st.ModeLabel = "OpenRouter 最新大参数免费模型（自动防限流切换）"
		if len(freeModels) > 0 {
			st.ActiveModel = freeModels[0].ID
		}
		return st
	}
	if local := loadLocalCodexGateway(); local.BaseURL != "" && local.APIKey != "" {
		st.Ready = true
		st.Mode = "local_gateway"
		st.ModeLabel = "本机已配置网关兜底"
		st.ActiveModel = local.Model
		return st
	}
	if envprobe.LookupBinary("codex") != "" {
		st.Ready = true
		st.Mode = "codex_cli"
		st.ModeLabel = "本机 Codex CLI 兜底"
		st.ActiveModel = "codex-cli"
		return st
	}
	st.Ready = false
	st.Mode = "unconfigured"
	st.ModeLabel = "未配置模型"
	return st
}

// Complete runs an LLM completion following the 3-tier priority:
//  1. User/Server Custom Model (UI config or WEBUDDY_LLM_* env)
//  2. OpenRouter Free Large-Parameter Pool (OPENROUTER_API_KEY, auto-failover up to 3 models)
//  3. Local ~/.codex/config.toml gateway or OPENAI_API_KEY or codex exec CLI
func (m *Manager) Complete(ctx context.Context, req CompletionRequest) CompletionResult {
	start := time.Now()
	if req.MaxTokens <= 0 {
		req.MaxTokens = 400
	}
	if req.Temperature <= 0 {
		req.Temperature = 0.2
	}

	totalTimeout := req.Timeout
	if totalTimeout <= 0 {
		totalTimeout = 28 * time.Second
	}
	perModelTimeout := 12 * time.Second
	if totalTimeout > 30*time.Second {
		perModelTimeout = 25 * time.Second
	}
	tctx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()

	var tried []string
	var lastErr error

	// Tier 1: Custom Model (UI saved or WEBUDDY_LLM_* env)
	if custom, _ := m.effectiveCustomConfig(); custom.BaseURL != "" && custom.APIKey != "" {
		model := custom.Model
		if model == "" {
			model = "gpt-4o-mini"
		}
		tried = append(tried, model)
		if txt, err := callOpenAICompatible(tctx, custom.BaseURL, custom.APIKey, model, req, nil); err == nil && txt != "" {
			return CompletionResult{
				Text:        txt,
				Model:       model,
				Provider:    "custom",
				LatencyMs:   time.Since(start).Milliseconds(),
				TriedModels: tried,
			}
		} else {
			lastErr = err
		}
	}

	// Tier 2: OpenRouter Free Large-Parameter Pool with automatic failover
	// Always reserve the final attempt for openrouter/free auto-router if top models fail.
	if orKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")); orKey != "" {
		rawCands := m.catalog.TopFreeModels(tctx, 3)
		tryList := make([]string, 0, 3)
		hasAuto := false
		for _, c := range rawCands {
			if len(tryList) >= 2 && c.ID != "openrouter/free" {
				continue
			}
			if c.ID == "openrouter/free" {
				hasAuto = true
			}
			tryList = append(tryList, c.ID)
		}
		if !hasAuto {
			tryList = append(tryList, "openrouter/free")
		}
		headers := map[string]string{
			"HTTP-Referer": "https://webuddy.bbstack.date",
			"X-Title":      "Webuddy Built-in Agent",
		}
		for _, modelID := range tryList {
			tried = append(tried, modelID)
			perModelCtx, perCancel := context.WithTimeout(tctx, perModelTimeout)
			txt, err := callChatCompletions(perModelCtx, OpenRouterBaseURL, orKey, modelID, req, headers)
			perCancel()
			if err == nil && strings.TrimSpace(txt) != "" {
				return CompletionResult{
					Text:        strings.TrimSpace(txt),
					Model:       modelID,
					Provider:    "openrouter_free",
					LatencyMs:   time.Since(start).Milliseconds(),
					TriedModels: tried,
				}
			}
			lastErr = err
		}
	}

	// Tier 3: Local ~/.codex/config.toml gateway or OPENAI_API_KEY
	if local := loadLocalCodexGateway(); local.BaseURL != "" && local.APIKey != "" {
		tried = append(tried, local.Model)
		if txt, err := callOpenAICompatible(tctx, local.BaseURL, local.APIKey, local.Model, req, nil); err == nil && txt != "" {
			return CompletionResult{
				Text:        txt,
				Model:       local.Model,
				Provider:    "local_gateway",
				LatencyMs:   time.Since(start).Milliseconds(),
				TriedModels: tried,
			}
		} else {
			lastErr = err
		}
	}

	// Tier 4: codex exec CLI fallback
	if txt, err := callCodexExecFallback(tctx, req.SystemPrompt+"\n\n"+req.UserPrompt); err == nil && txt != "" {
		tried = append(tried, "codex-cli")
		return CompletionResult{
			Text:        txt,
			Model:       "codex-cli",
			Provider:    "codex_cli",
			LatencyMs:   time.Since(start).Milliseconds(),
			TriedModels: tried,
		}
	} else if lastErr == nil {
		lastErr = err
	}

	errMsg := "AI 暂不可用"
	if lastErr != nil {
		errMsg = lastErr.Error()
		if len(errMsg) > 160 {
			errMsg = errMsg[:160] + "..."
		}
	}
	return CompletionResult{
		LatencyMs:   time.Since(start).Milliseconds(),
		TriedModels: tried,
		Error:       errMsg,
	}
}

func loadLocalCodexGateway() CustomConfig {
	home, _ := os.UserHomeDir()
	if home != "" {
		if raw, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml")); err == nil {
			var defaultModel, activeProvider, section string
			providers := make(map[string]CustomConfig)
			for _, rawLine := range strings.Split(string(raw), "\n") {
				line := strings.TrimSpace(rawLine)
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
				key := strings.TrimSpace(line[:eq])
				val := strings.TrimSpace(line[eq+1:])
				if idx := strings.IndexByte(val, '#'); idx >= 0 && !strings.Contains(val[:idx], "://") {
					val = strings.TrimSpace(val[:idx])
				}
				val = strings.Trim(val, `"'`)
				if section == "" {
					if key == "model" && val != "" {
						defaultModel = val
					} else if key == "model_provider" && val != "" {
						activeProvider = val
					}
				} else if strings.HasPrefix(section, "model_providers.") {
					pname := strings.Trim(strings.TrimPrefix(section, "model_providers."), `"' `)
					if strings.Contains(pname, ".") || pname == "" {
						continue
					}
					p := providers[pname]
					if key == "base_url" && val != "" {
						p.BaseURL = strings.TrimRight(val, "/")
					} else if key == "experimental_bearer_token" && val != "" {
						p.APIKey = val
					}
					providers[pname] = p
				}
			}
			pick := func(p CustomConfig) CustomConfig {
				p.Model = defaultModel
				if p.Model == "" {
					p.Model = "gpt-6-luna"
				}
				return p
			}
			if activeProvider != "" {
				if p, ok := providers[activeProvider]; ok && p.BaseURL != "" && p.APIKey != "" {
					return pick(p)
				}
			} else {
				for _, p := range providers {
					if p.BaseURL != "" && p.APIKey != "" {
						return pick(p)
					}
				}
			}
		}
	}
	if key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); key != "" {
		base := strings.TrimRight(strings.TrimSpace(os.Getenv("OPENAI_BASE_URL")), "/")
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		model := strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
		if model == "" {
			model = "gpt-4o-mini"
		}
		return CustomConfig{BaseURL: base, APIKey: key, Model: model}
	}
	return CustomConfig{}
}

func callOpenAICompatible(ctx context.Context, baseURL, token, model string, req CompletionRequest, extraHeaders map[string]string) (string, error) {
	// Try standard /chat/completions first, then /responses (for Codex gateways)
	txt, err := callChatCompletions(ctx, baseURL, token, model, req, extraHeaders)
	if err == nil && strings.TrimSpace(txt) != "" {
		return strings.TrimSpace(txt), nil
	}
	respBody := map[string]any{
		"model":             model,
		"instructions":      req.SystemPrompt,
		"max_output_tokens": req.MaxTokens,
		"input": []map[string]any{
			{"role": "user", "content": req.UserPrompt},
		},
		"stream": false,
	}
	txt2, err2 := postJSON(ctx, strings.TrimRight(baseURL, "/")+"/responses", token, respBody, extraHeaders)
	if err2 == nil && strings.TrimSpace(txt2) != "" {
		return strings.TrimSpace(txt2), nil
	}
	if err != nil && err2 != nil {
		return "", fmt.Errorf("chat: %v; responses: %v", err, err2)
	}
	if err2 != nil {
		return "", err2
	}
	if err != nil {
		return "", err
	}
	return "", fmt.Errorf("模型返回空内容")
}

func callChatCompletions(ctx context.Context, baseURL, token, model string, req CompletionRequest, extraHeaders map[string]string) (string, error) {
	chatBody := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": req.SystemPrompt},
			{"role": "user", "content": req.UserPrompt},
		},
		"temperature": req.Temperature,
		"max_tokens":  req.MaxTokens,
	}
	if strings.Contains(strings.ToLower(baseURL), "openrouter.ai") {
		chatBody["reasoning"] = map[string]any{"effort": "none"}
	}
	return postJSON(ctx, strings.TrimRight(baseURL, "/")+"/chat/completions", token, chatBody, extraHeaders)
}

func postJSON(ctx context.Context, endpoint, token string, body any, extraHeaders map[string]string) (string, error) {
	rawReq, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(rawReq))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	for k, v := range extraHeaders {
		httpReq.Header.Set(k, v)
	}
	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "text/event-stream") {
		return parseSSE(resp.Body), nil
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return "", err
	}
	return extractText(payload), nil
}

func parseSSE(r io.Reader) string {
	var sb strings.Builder
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var ev struct {
			Delta string `json:"delta"`
		}
		if json.Unmarshal([]byte(data), &ev) == nil && ev.Delta != "" {
			sb.WriteString(ev.Delta)
		}
	}
	return strings.TrimSpace(sb.String())
}

func extractText(payload []byte) string {
	var parsed struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				Reasoning string `json:"reasoning"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return ""
	}
	if s := strings.TrimSpace(parsed.OutputText); s != "" {
		return s
	}
	for _, out := range parsed.Output {
		for _, c := range out.Content {
			if s := strings.TrimSpace(c.Text); s != "" {
				return s
			}
		}
	}
	if len(parsed.Choices) > 0 {
		if s := strings.TrimSpace(parsed.Choices[0].Message.Content); s != "" {
			return s
		}
	}
	return ""
}

func callCodexExecFallback(ctx context.Context, prompt string) (string, error) {
	bin := envprobe.LookupBinary("codex")
	if bin == "" {
		return "", fmt.Errorf("codex CLI 未安装")
	}
	outFile := filepath.Join(os.TempDir(), fmt.Sprintf("wb-builtin-%d.txt", time.Now().UnixNano()))
	defer os.Remove(outFile)
	cmd := exec.CommandContext(ctx, bin, "exec", "--sandbox", "read-only", "--skip-git-repo-check", "-o", outFile, prompt)
	cmd.Env = envprobe.ExpandedEnv()
	out, err := cmd.CombinedOutput()
	if raw, rerr := os.ReadFile(outFile); rerr == nil && strings.TrimSpace(string(raw)) != "" {
		return strings.TrimSpace(string(raw)), nil
	}
	if err != nil {
		return "", fmt.Errorf("codex exec: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
