package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"agentflow/internal/domain"
	"agentflow/internal/runtime"
)

func maskSecretToken(tok string) string {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return ""
	}
	if len(tok) <= 8 {
		return "****"
	}
	return tok[:4] + "****" + tok[len(tok)-4:]
}

func resolveHostCLIConfigPaths(provider string) (configDir, configFile, oauthFile string, err error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", "", "", fmt.Errorf("无法定位执行机用户主目录")
	}
	switch provider {
	case "codex":
		dir := strings.TrimSpace(os.Getenv("CODEX_HOME"))
		if dir == "" {
			dir = filepath.Join(home, ".codex")
		}
		return dir, filepath.Join(dir, "config.toml"), filepath.Join(dir, "auth.json"), nil
	case "claude-code":
		dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
		if dir == "" {
			dir = filepath.Join(home, ".claude")
		}
		return dir, filepath.Join(dir, "settings.json"), filepath.Join(dir, ".credentials.json"), nil
	default:
		return "", "", "", fmt.Errorf("暂不支持远程配置该 CLI: %s", provider)
	}
}

func restoreLatestOAuthBackupIfMissing(oauthFile string) bool {
	if st, err := os.Stat(oauthFile); err == nil && st.Mode().IsRegular() {
		return true
	}
	matches, err := filepath.Glob(oauthFile + ".bak-*")
	if err != nil || len(matches) == 0 {
		return false
	}
	slices.Sort(matches)
	latest := matches[len(matches)-1]
	return os.Rename(latest, oauthFile) == nil
}

func switchHostToOfficialLogin(provider, configFile, oauthFile string) error {
	switch provider {
	case "codex":
		existing, err := os.ReadFile(configFile)
		if err == nil {
			_ = os.WriteFile(configFile+".custom-bak-"+time.Now().Format("20060102150405"), existing, 0o600)
			projects := extractCodexProjectsBlocks(string(existing))
			if projects != "" {
				if wErr := writeConfigAtomic(configFile, []byte(projects)); wErr != nil {
					return wErr
				}
			} else {
				_ = os.Remove(configFile)
			}
		}
		restoreLatestOAuthBackupIfMissing(oauthFile)
		return nil
	case "claude-code":
		existing, err := os.ReadFile(configFile)
		if err == nil {
			_ = os.WriteFile(configFile+".custom-bak-"+time.Now().Format("20060102150405"), existing, 0o600)
			doc := map[string]any{}
			if json.Unmarshal(existing, &doc) == nil {
				if envMap, ok := doc["env"].(map[string]any); ok {
					delete(envMap, "ANTHROPIC_AUTH_TOKEN")
					delete(envMap, "ANTHROPIC_API_KEY")
					delete(envMap, "ANTHROPIC_BASE_URL")
					delete(envMap, "ANTHROPIC_MODEL")
					if len(envMap) == 0 {
						delete(doc, "env")
					}
				}
				delete(doc, "model")
				if len(doc) > 0 {
					if enc, mErr := json.MarshalIndent(doc, "", "  "); mErr == nil {
						_ = writeConfigAtomic(configFile, append(enc, '\n'))
					}
				} else {
					_ = os.Remove(configFile)
				}
			}
		}
		restoreLatestOAuthBackupIfMissing(oauthFile)
		return nil
	default:
		return fmt.Errorf("不支持切换该 CLI 的登录模式: %s", provider)
	}
}

func (d *Daemon) handleRunnerCLIConfig(ctx context.Context, req *domain.RunnerCLIConfigRequest) domain.RunnerWorkspaceResponse {
	if req == nil {
		return domain.RunnerWorkspaceResponse{Status: http.StatusBadRequest, Error: "缺少 CLI 配置请求"}
	}
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if provider == "claude" {
		provider = "claude-code"
	}
	configDir, configFile, oauthFile, err := resolveHostCLIConfigPaths(provider)
	if err != nil {
		return domain.RunnerWorkspaceResponse{Status: http.StatusBadRequest, Error: err.Error()}
	}

	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		action = "get"
	}

	var clearedBackup string
	if action == "set" {
		if err := os.MkdirAll(configDir, 0o700); err != nil {
			return domain.RunnerWorkspaceResponse{Status: http.StatusInternalServerError, Error: "创建 CLI 配置目录失败: " + err.Error()}
		}
		if req.UseOfficialLogin {
			if err := switchHostToOfficialLogin(provider, configFile, oauthFile); err != nil {
				return domain.RunnerWorkspaceResponse{Status: http.StatusInternalServerError, Error: "切回官方账号登录模式失败: " + err.Error()}
			}
		} else {
			hasConfigFields := strings.TrimSpace(req.RawConfigTOML) != "" ||
				strings.TrimSpace(req.GatewayName) != "" ||
				strings.TrimSpace(req.BaseURL) != "" ||
				strings.TrimSpace(req.APIKey) != "" ||
				strings.TrimSpace(req.Model) != "" ||
				strings.TrimSpace(req.Effort) != ""
			if hasConfigFields || !req.ClearConflictingAuth {
				switch provider {
				case "codex":
					if err := applyCodexHostConfig(configFile, req); err != nil {
						return domain.RunnerWorkspaceResponse{Status: http.StatusBadRequest, Error: err.Error()}
					}
				case "claude-code":
					if err := applyClaudeHostConfig(configFile, req); err != nil {
						return domain.RunnerWorkspaceResponse{Status: http.StatusBadRequest, Error: err.Error()}
					}
				}
			}
			if req.ClearConflictingAuth {
				if st, statErr := os.Stat(oauthFile); statErr == nil && st.Mode().IsRegular() {
					backup := oauthFile + ".bak-" + time.Now().Format("20060102150405")
					if renameErr := os.Rename(oauthFile, backup); renameErr == nil {
						clearedBackup = backup
					}
				}
			}
		}
	}

	status := inspectHostCLIConfig(ctx, d, provider, configFile, oauthFile)
	status.ClearedAuthBackup = clearedBackup
	return runnerWorkspacePayload(status)
}

func inspectHostCLIConfig(ctx context.Context, d *Daemon, provider, configFile, oauthFile string) domain.RunnerCLIConfigStatus {
	st := domain.RunnerCLIConfigStatus{
		Provider:   provider,
		AuthSource: "none",
		ConfigPath: configFile,
	}
	if info, err := os.Stat(oauthFile); err == nil && info.Mode().IsRegular() {
		st.HasOAuthFile = true
		st.AuthSource = "official"
	}
	rawBytes, err := os.ReadFile(configFile)
	if err == nil {
		st.Exists = true
		if info, statErr := os.Stat(configFile); statErr == nil {
			st.UpdatedAt = info.ModTime().UTC().Format(time.RFC3339)
		}
		switch provider {
		case "codex":
			provName, baseURL, token, model, effort, hasCustom := runtime.ParseCodexConfigDetails(string(rawBytes))
			st.GatewayName = provName
			st.BaseURL = baseURL
			st.TokenMasked = maskSecretToken(token)
			st.Model = model
			st.Effort = effort
			st.HasCustomAuth = hasCustom
			st.ConflictingAuth = hasCustom && st.HasOAuthFile
			if hasCustom {
				st.AuthSource = "custom_gateway"
			}
		case "claude-code":
			var doc struct {
				Model string            `json:"model"`
				Env   map[string]string `json:"env"`
			}
			if json.Unmarshal(rawBytes, &doc) == nil {
				tok := strings.TrimSpace(doc.Env["ANTHROPIC_AUTH_TOKEN"])
				if tok == "" {
					tok = strings.TrimSpace(doc.Env["ANTHROPIC_API_KEY"])
				}
				st.BaseURL = strings.TrimSpace(doc.Env["ANTHROPIC_BASE_URL"])
				st.TokenMasked = maskSecretToken(tok)
				st.Model = strings.TrimSpace(doc.Model)
				if st.Model == "" {
					st.Model = strings.TrimSpace(doc.Env["ANTHROPIC_MODEL"])
				}
				st.HasCustomAuth = tok != ""
				st.ConflictingAuth = st.HasCustomAuth && st.HasOAuthFile
				if st.HasCustomAuth {
					st.AuthSource = "custom_gateway"
				}
			}
		}
	}

	pctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	binPath := ""
	if d != nil {
		for _, rt := range d.currentRuntimes() {
			if rt.Provider == provider && strings.TrimSpace(rt.BinaryPath) != "" {
				binPath = strings.TrimSpace(rt.BinaryPath)
				break
			}
		}
	}
	if binPath == "" {
		binPath = findProviderBinaryQuick(provider)
	}
	if binPath != "" {
		if list, mErr := runtime.DiscoverRunnerLoginModels(pctx, provider, binPath); mErr == nil {
			for _, m := range list {
				if m.ID != "" && m.ID != "default" {
					st.DiscoveredModels = append(st.DiscoveredModels, m.ID)
				}
			}
		}
	} else if provider == "codex" && st.HasCustomAuth {
		if list, mErr := runtime.DiscoverRunnerLoginModels(pctx, provider, "codex"); mErr == nil {
			for _, m := range list {
				if m.ID != "" && m.ID != "default" {
					st.DiscoveredModels = append(st.DiscoveredModels, m.ID)
				}
			}
		}
	}
	return st
}

func findProviderBinaryQuick(provider string) string {
	name := "codex"
	if provider == "claude-code" {
		name = "claude"
	}
	for _, dir := range strings.Split(os.Getenv("PATH"), string(os.PathListSeparator)) {
		if dir == "" {
			continue
		}
		cand := filepath.Join(dir, name)
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		for _, sub := range []string{".npm-global/bin", ".local/bin"} {
			cand := filepath.Join(home, sub, name)
			if info, err := os.Stat(cand); err == nil && !info.IsDir() {
				return cand
			}
		}
	}
	return ""
}

func applyCodexHostConfig(configFile string, req *domain.RunnerCLIConfigRequest) error {
	if raw := strings.TrimSpace(req.RawConfigTOML); raw != "" {
		return writeConfigAtomic(configFile, []byte(raw+"\n"))
	}
	existingBytes, _ := os.ReadFile(configFile)
	oldProv, oldURL, oldTok, oldModel, oldEffort, oldExtra, _, _ := runtime.ParseCodexConfigFull(string(existingBytes))

	gw := strings.TrimSpace(req.GatewayName)
	if gw == "" {
		if oldProv != "" {
			gw = oldProv
		} else {
			gw = "custom_gateway"
		}
	}
	for _, ch := range gw {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-') {
			return fmt.Errorf("网关标识仅允许字母、数字、下划线或连字符")
		}
	}
	baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	if baseURL == "" {
		baseURL = oldURL
	}
	if baseURL == "" {
		return fmt.Errorf("请填写 Codex 网关 Base URL（例如 https://api.example.com/v1）")
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		apiKey = oldTok
	}
	if apiKey == "" {
		return fmt.Errorf("请填写网关访问令牌（Bearer Token / API Key）")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = oldModel
	}
	effort := strings.TrimSpace(req.Effort)
	if effort == "" {
		if oldEffort != "" {
			effort = oldEffort
		} else {
			effort = "medium"
		}
	}
	extraTOML := strings.TrimSpace(req.ExtraProviderTOML)
	if extraTOML == "" && oldURL == baseURL {
		extraTOML = strings.TrimSpace(oldExtra)
	}

	preservedProjects := extractCodexProjectsBlocks(string(existingBytes))

	var b strings.Builder
	fmt.Fprintf(&b, "model_provider = %s\n", strconv.Quote(gw))
	if model != "" {
		fmt.Fprintf(&b, "model = %s\n", strconv.Quote(model))
	}
	fmt.Fprintf(&b, "model_reasoning_effort = %s\n", strconv.Quote(effort))
	b.WriteString("service_tier = \"default\"\n\n")
	fmt.Fprintf(&b, "[model_providers.%s]\n", gw)
	fmt.Fprintf(&b, "name = %s\n", strconv.Quote(gw))
	fmt.Fprintf(&b, "base_url = %s\n", strconv.Quote(baseURL))
	b.WriteString("wire_api = \"responses\"\n")
	b.WriteString("requires_openai_auth = false\n")
	fmt.Fprintf(&b, "experimental_bearer_token = %s\n", strconv.Quote(apiKey))
	b.WriteString("supports_websockets = false\n")
	if extraTOML != "" {
		b.WriteString(extraTOML + "\n")
	}
	if preservedProjects != "" {
		b.WriteString("\n")
		b.WriteString(preservedProjects)
	}
	// Clear cached accessibility probe so new config re-verifies models cleanly.
	_ = os.Remove(filepath.Join(filepath.Dir(configFile), ".model-accessibility-cache.json"))
	return writeConfigAtomic(configFile, []byte(b.String()))
}

func extractCodexProjectsBlocks(raw string) string {
	var keep []string
	inProjects := false
	for _, ln := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			sec := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			inProjects = strings.HasPrefix(sec, "projects.")
		}
		if inProjects {
			keep = append(keep, ln)
		}
	}
	if len(keep) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join(keep, "\n")) + "\n"
}

func applyClaudeHostConfig(configFile string, req *domain.RunnerCLIConfigRequest) error {
	doc := map[string]any{}
	if existing, err := os.ReadFile(configFile); err == nil {
		_ = json.Unmarshal(existing, &doc)
	}
	envMap, _ := doc["env"].(map[string]any)
	if envMap == nil {
		envMap = map[string]any{}
	}
	if u := strings.TrimSpace(req.BaseURL); u != "" {
		envMap["ANTHROPIC_BASE_URL"] = u
	}
	if k := strings.TrimSpace(req.APIKey); k != "" {
		envMap["ANTHROPIC_AUTH_TOKEN"] = k
	}
	if m := strings.TrimSpace(req.Model); m != "" {
		doc["model"] = m
		envMap["ANTHROPIC_MODEL"] = m
	}
	doc["env"] = envMap
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return writeConfigAtomic(configFile, append(encoded, '\n'))
}

func writeConfigAtomic(path string, content []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".wb-cli-cfg-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
