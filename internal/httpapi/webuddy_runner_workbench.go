package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agentflow/internal/domain"
	"agentflow/internal/runtime"

	"github.com/gin-gonic/gin"
)

// ---------- Target-Runner Model Discovery Cache ----------

type targetRunnerModelsCacheEntry struct {
	models  []domain.CLIModel
	expires time.Time
}

var targetRunnerModelsCache sync.Map // key: runnerID + "|" + provider

func invalidateTargetRunnerModelCache(runnerID string) {
	runnerID = strings.TrimSpace(runnerID)
	if runnerID == "" {
		return
	}
	targetRunnerModelsCache.Range(func(key, _ any) bool {
		if ks, ok := key.(string); ok && strings.HasPrefix(ks, runnerID+"|") {
			targetRunnerModelsCache.Delete(key)
		}
		return true
	})
}

func (d *deps) resolveUserTargetRunner(userID int64, explicitRunnerID, provider string) domain.Runner {
	if d == nil || d.repo == nil || userID <= 0 {
		return domain.Runner{}
	}
	explicitRunnerID = strings.TrimSpace(explicitRunnerID)
	if explicitRunnerID != "" {
		if r, ok := d.repo.Runner(explicitRunnerID); ok && domain.CanUserUseRunner(r, userID) && r.Status == domain.RunnerStatusOnline && (provider == "" || r.SupportsProvider(provider)) {
			return r
		}
	}
	if defID := strings.TrimSpace(d.repo.DefaultRunner(userID)); defID != "" {
		if r, ok := d.repo.Runner(defID); ok && domain.CanUserUseRunner(r, userID) && r.Status == domain.RunnerStatusOnline && (provider == "" || r.SupportsProvider(provider)) {
			return r
		}
	}
	if provider != "" {
		return scheduleLoginRunner(d.repo, userID, provider)
	}
	return domain.Runner{}
}

func (d *deps) discoverTargetRunnerModels(ctx context.Context, runner domain.Runner, provider string) []domain.CLIModel {
	if d == nil || d.runnerControls == nil || runner.ID == "" || runner.Status != domain.RunnerStatusOnline || !runner.SupportsCLISettings() {
		return nil
	}
	if !runnerUsesLocalLogin(runner, provider) {
		return nil
	}
	cacheKey := runner.ID + "|" + provider
	now := time.Now()
	if raw, ok := targetRunnerModelsCache.Load(cacheKey); ok {
		entry := raw.(targetRunnerModelsCacheEntry)
		if now.Before(entry.expires) {
			return entry.models
		}
	}
	qctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	status, res := runnerLoginModelCatalog(qctx, d.runnerControls, runner, provider, nil)
	if status != http.StatusOK || len(res.Models) == 0 {
		targetRunnerModelsCache.Store(cacheKey, targetRunnerModelsCacheEntry{models: nil, expires: now.Add(15 * time.Second)})
		return nil
	}
	targetRunnerModelsCache.Store(cacheKey, targetRunnerModelsCacheEntry{models: res.Models, expires: now.Add(2 * time.Minute)})
	return res.Models
}

// ---------- Mint One-Time Runner Bind Code ----------

func (h *runnersHandlers) mintBindCodeForUser(userID int64) (string, time.Time, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("生成绑定码失败")
	}
	code := runnerBindCodePrefix + base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now().UTC()
	exp := now.Add(runnerBindCodeTTL)
	h.runnerBindMu.Lock()
	if h.runnerBindCodes == nil {
		h.runnerBindCodes = map[string]runnerBindCode{}
	}
	for key, entry := range h.runnerBindCodes {
		if !entry.expiresAt.After(now) {
			delete(h.runnerBindCodes, key)
		}
	}
	h.runnerBindCodes[code] = runnerBindCode{userID: userID, expiresAt: exp}
	h.runnerBindMu.Unlock()
	return code, exp, nil
}

func resolvePublicOriginHost(r *http.Request) string {
	if r != nil {
		if fwd := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); fwd != "" {
			if idx := strings.IndexByte(fwd, ','); idx >= 0 {
				fwd = strings.TrimSpace(fwd[:idx])
			}
			if fwd != "" {
				return fwd
			}
		}
		if host := strings.TrimSpace(r.Host); host != "" && !strings.HasPrefix(host, "127.0.0.1") && !strings.HasPrefix(host, "localhost") {
			return host
		}
	}
	if pub := strings.TrimSpace(os.Getenv("AGENTFLOW_PUBLIC_URL")); pub != "" {
		pub = strings.TrimPrefix(strings.TrimPrefix(pub, "https://"), "http://")
		if idx := strings.IndexByte(pub, '/'); idx >= 0 {
			pub = pub[:idx]
		}
		if pub != "" {
			return pub
		}
	}
	if r != nil && strings.TrimSpace(r.Host) != "" {
		return strings.TrimSpace(r.Host)
	}
	return "webuddy.bbstack.date"
}

func buildRunnerInstallCommand(host, code string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		host = "webuddy.bbstack.date"
	}
	return fmt.Sprintf("(curl -fsSL --connect-timeout 5 https://%s/runners/install.sh || curl -fsSL http://%s/runners/install.sh) | bash -s -- --server %s --token %s --service", host, host, host, code)
}

// ---------- Control-Plane Host Gateway Template ----------

type hostGatewaySummary struct {
	Available   bool   `json:"available"`
	Provider    string `json:"provider"`
	GatewayName string `json:"gatewayName,omitempty"`
	BaseURL     string `json:"baseUrl,omitempty"`
	Model       string `json:"model,omitempty"`
	Effort      string `json:"effort,omitempty"`
}

func readControlPlaneHostCLIConfig(provider string) (domain.RunnerCLIConfigRequest, hostGatewaySummary, bool) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "claude" {
		provider = "claude-code"
	}
	home, _ := os.UserHomeDir()
	switch provider {
	case "codex":
		dir := strings.TrimSpace(os.Getenv("CODEX_HOME"))
		if dir == "" && home != "" {
			dir = filepath.Join(home, ".codex")
		}
		if dir == "" {
			return domain.RunnerCLIConfigRequest{}, hostGatewaySummary{Provider: provider}, false
		}
		raw, err := os.ReadFile(filepath.Join(dir, "config.toml"))
		if err != nil {
			return domain.RunnerCLIConfigRequest{}, hostGatewaySummary{Provider: provider}, false
		}
		gw, baseURL, token, model, effort, extraTOML, _, hasCustom := runtime.ParseCodexConfigFull(string(raw))
		if !hasCustom || baseURL == "" || token == "" {
			return domain.RunnerCLIConfigRequest{}, hostGatewaySummary{Provider: provider}, false
		}
		req := domain.RunnerCLIConfigRequest{
			Provider:             "codex",
			Action:               "set",
			GatewayName:          gw,
			BaseURL:              baseURL,
			APIKey:               token,
			Model:                model,
			Effort:               effort,
			ExtraProviderTOML:    extraTOML,
			ClearConflictingAuth: true,
		}
		return req, hostGatewaySummary{
			Available:   true,
			Provider:    "codex",
			GatewayName: gw,
			BaseURL:     baseURL,
			Model:       model,
			Effort:      effort,
		}, true
	case "claude-code":
		dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
		if dir == "" && home != "" {
			dir = filepath.Join(home, ".claude")
		}
		if dir == "" {
			return domain.RunnerCLIConfigRequest{}, hostGatewaySummary{Provider: provider}, false
		}
		raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
		if err != nil {
			return domain.RunnerCLIConfigRequest{}, hostGatewaySummary{Provider: provider}, false
		}
		var doc struct {
			Model string            `json:"model"`
			Env   map[string]string `json:"env"`
		}
		if json.Unmarshal(raw, &doc) != nil {
			return domain.RunnerCLIConfigRequest{}, hostGatewaySummary{Provider: provider}, false
		}
		tok := strings.TrimSpace(doc.Env["ANTHROPIC_AUTH_TOKEN"])
		if tok == "" {
			tok = strings.TrimSpace(doc.Env["ANTHROPIC_API_KEY"])
		}
		baseURL := strings.TrimSpace(doc.Env["ANTHROPIC_BASE_URL"])
		model := strings.TrimSpace(doc.Model)
		if model == "" {
			model = strings.TrimSpace(doc.Env["ANTHROPIC_MODEL"])
		}
		if tok == "" {
			return domain.RunnerCLIConfigRequest{}, hostGatewaySummary{Provider: provider}, false
		}
		req := domain.RunnerCLIConfigRequest{
			Provider:             "claude-code",
			Action:               "set",
			BaseURL:              baseURL,
			APIKey:               tok,
			Model:                model,
			ClearConflictingAuth: true,
		}
		return req, hostGatewaySummary{
			Available: true,
			Provider:  "claude-code",
			BaseURL:   baseURL,
			Model:     model,
		}, true
	default:
		return domain.RunnerCLIConfigRequest{}, hostGatewaySummary{Provider: provider}, false
	}
}

// ---------- HTTP API: GET / PUT /api/runners/:id/cli-config ----------

type runnerCLIConfigHTTPInput struct {
	Provider             string `json:"provider"`
	UseOfficialLogin     bool   `json:"useOfficialLogin"`
	SyncFromHost         bool   `json:"syncFromHost"`
	GatewayName          string `json:"gatewayName"`
	BaseURL              string `json:"baseUrl"`
	APIKey               string `json:"apiKey"`
	Model                string `json:"model"`
	Effort               string `json:"effort"`
	RawConfigTOML        string `json:"rawConfigToml"`
	ClearConflictingAuth *bool  `json:"clearConflictingAuth"`
}

func (d *deps) canManageRunnerConfig(user domain.User, runner domain.Runner) bool {
	if runner.OwnerUserID == user.ID && user.ID > 0 {
		return true
	}
	if d != nil && d.repo != nil && strings.TrimSpace(user.LoginName) != "" && d.repo.IsPlatformAdmin(user.LoginName) {
		return runner.OwnerUserID == 0 || runner.OwnerUserID == user.ID
	}
	return false
}

func (d *deps) requestRunnerCLIConfig(ctx context.Context, user domain.User, runner domain.Runner, action, provider string, input runnerCLIConfigHTTPInput) (domain.RunnerCLIConfigStatus, hostGatewaySummary, int, string) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = "codex"
	}
	if provider == "claude" {
		provider = "claude-code"
	}
	hostReq, hostSummary, canSync := readControlPlaneHostCLIConfig(provider)
	if !d.canManageRunnerConfig(user, runner) {
		return domain.RunnerCLIConfigStatus{}, hostSummary, http.StatusForbidden, "只能查看或配置自己名下的执行机器"
	}
	if runner.Status != domain.RunnerStatusOnline || d.runnerControls == nil {
		return domain.RunnerCLIConfigStatus{}, hostSummary, http.StatusServiceUnavailable, "执行机器当前不在线，请确认机器已连接后再试"
	}
	if !runner.SupportsCLISettings() {
		return domain.RunnerCLIConfigStatus{}, hostSummary, http.StatusConflict, fmt.Sprintf("远程网关配置需要 Runner %s 及以上版本，请先点击「检查更新」或输入 /runner upgrade 升级机器", domain.RunnerVersionCLISettings)
	}

	rpcReq := domain.RunnerCLIConfigRequest{
		Provider: provider,
		Action:   action,
	}
	if action == "set" {
		clearAuth := true
		if input.ClearConflictingAuth != nil {
			clearAuth = *input.ClearConflictingAuth
		}
		if input.UseOfficialLogin {
			rpcReq.UseOfficialLogin = true
		} else if input.SyncFromHost {
			if !canSync {
				return domain.RunnerCLIConfigStatus{}, hostSummary, http.StatusBadRequest, "主控机尚未配置可同步的网关模板，请直接填写网关地址与访问令牌"
			}
			rpcReq = hostReq
			if m := strings.TrimSpace(input.Model); m != "" {
				rpcReq.Model = m
			}
			if ef := strings.TrimSpace(input.Effort); ef != "" {
				rpcReq.Effort = ef
			}
			rpcReq.ClearConflictingAuth = clearAuth
		} else {
			rpcReq.GatewayName = strings.TrimSpace(input.GatewayName)
			rpcReq.BaseURL = strings.TrimSpace(input.BaseURL)
			rpcReq.APIKey = strings.TrimSpace(input.APIKey)
			rpcReq.Model = strings.TrimSpace(input.Model)
			rpcReq.Effort = strings.TrimSpace(input.Effort)
			rpcReq.RawConfigTOML = input.RawConfigTOML
			rpcReq.ClearConflictingAuth = clearAuth
		}
	}

	rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := d.runnerControls.requestWorkspace(rctx, runner.ID, domain.RunnerWorkspaceRequest{
		Operation: domain.RunnerWorkspaceOperationCLIConfig,
		CLIConfig: &rpcReq,
	})
	if err != nil {
		return domain.RunnerCLIConfigStatus{}, hostSummary, http.StatusServiceUnavailable, "无法通过控制通道连接该测试机，若机器刚启动或版本较旧，请先执行一次 OTA 升级（/runner upgrade）"
	}
	if resp.Error != "" {
		code := resp.Status
		if code < 400 || code > 599 {
			code = http.StatusBadGateway
		}
		errMsg := resp.Error
		if strings.Contains(errMsg, "unsupported workspace operation") || strings.Contains(errMsg, "invalid run id") {
			errMsg = "该测试机上的 Runner 守护进程尚未加载远程配置模块，请先执行 `/runner upgrade`、点击「检查更新」或重启该机器上的 Runner 服务"
		}
		return domain.RunnerCLIConfigStatus{}, hostSummary, code, errMsg
	}
	var status domain.RunnerCLIConfigStatus
	if err := json.Unmarshal(resp.Payload, &status); err != nil {
		return domain.RunnerCLIConfigStatus{}, hostSummary, http.StatusBadGateway, "解析测试机返回的 CLI 配置状态失败"
	}
	if action == "set" {
		invalidateTargetRunnerModelCache(runner.ID)
	}
	return status, hostSummary, http.StatusOK, ""
}

func (h *runnersHandlers) runnerCLIConfig(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	current, ok := h.requestUser(c.Request, true)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录后配置执行机器"})
		return
	}
	user, _ := h.repo.User(current.ID)
	if user.ID == 0 {
		user = domain.User{ID: current.ID, LoginName: current.LoginName}
	}
	runner, found := h.repo.Runner(strings.TrimSpace(c.Param("id")))
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "执行机器不存在"})
		return
	}
	if c.Request.Method == http.MethodGet {
		provider := strings.TrimSpace(c.Query("provider"))
		st, hostSummary, code, errMsg := h.deps.requestRunnerCLIConfig(c.Request.Context(), user, runner, "get", provider, runnerCLIConfigHTTPInput{})
		if errMsg != "" {
			c.JSON(code, gin.H{"error": errMsg, "hostGateway": hostSummary})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "status": st, "hostGateway": hostSummary})
		return
	}
	var input runnerCLIConfigHTTPInput
	if !decode(c, &input) {
		return
	}
	provider := strings.TrimSpace(input.Provider)
	if provider == "" {
		provider = strings.TrimSpace(c.Query("provider"))
	}
	st, hostSummary, code, errMsg := h.deps.requestRunnerCLIConfig(c.Request.Context(), user, runner, "set", provider, input)
	if errMsg != "" {
		c.JSON(code, gin.H{"error": errMsg, "hostGateway": hostSummary})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "status": st, "hostGateway": hostSummary})
}

// ---------- Eta-Inspired Zero-Token Workbench Slash Commands ----------

func isWorkbenchSlashCommand(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/") {
		return false
	}
	firstLine := trimmed
	if nl := strings.IndexAny(firstLine, "\r\n"); nl >= 0 {
		firstLine = strings.TrimSpace(firstLine[:nl])
	}
	fields := strings.Fields(firstLine)
	if len(fields) == 0 {
		return false
	}
	switch strings.ToLower(fields[0]) {
	case "/runner", "/runners", "/machine", "/machines", "/测试机", "/执行机", "/test", "/autotest", "/测试":
		return true
	default:
		return false
	}
}

func (d *deps) visibleUserRunners(user domain.User) []domain.Runner {
	if d == nil || d.repo == nil {
		return nil
	}
	all := domain.PublicRunners(d.repo.Runners())
	out := make([]domain.Runner, 0, len(all))
	for _, r := range all {
		if r.OwnerUserID == user.ID || domain.IsSharedHostRunner(r) {
			out = append(out, r)
		}
	}
	return out
}

func matchUserRunners(list []domain.Runner, selector string, onlyOwned bool, user domain.User, isAdmin bool) []domain.Runner {
	sel := strings.TrimSpace(selector)
	isAll := sel == "" || strings.EqualFold(sel, "all") || sel == "全部" || sel == "*"
	var matched []domain.Runner
	for _, r := range list {
		if onlyOwned && r.OwnerUserID != user.ID && !(r.OwnerUserID == 0 && isAdmin) {
			continue
		}
		if isAll {
			matched = append(matched, r)
			continue
		}
		if strings.EqualFold(r.ID, sel) || strings.EqualFold(r.Name, sel) {
			return []domain.Runner{r}
		}
		if strings.Contains(strings.ToLower(r.Name), strings.ToLower(sel)) || strings.Contains(strings.ToLower(r.ID), strings.ToLower(sel)) {
			matched = append(matched, r)
		}
	}
	return matched
}

func (d *deps) executeWorkbenchRunnerOp(ctx context.Context, user domain.User, originHost, subcmd string, args []string) string {
	if d == nil || d.repo == nil {
		return "执行机管理服务未就绪。"
	}
	isAdmin := strings.TrimSpace(user.LoginName) != "" && d.repo.IsPlatformAdmin(user.LoginName)
	runners := d.visibleUserRunners(user)
	defaultID := d.repo.DefaultRunner(user.ID)

	sub := strings.ToLower(strings.TrimSpace(subcmd))
	switch sub {
	case "", "list", "ls", "status", "状态", "列表":
		var b strings.Builder
		b.WriteString("### 🖥️ 执行机 / 测试机实时状态（零 Token 瞬时指令）\n\n")
		if len(runners) == 0 {
			b.WriteString("当前账号下还没有接入任何测试机。发送 `/runner add` 可立即生成 10 分钟有效的单行接入命令。\n")
		} else {
			b.WriteString("| 机器名称 | 状态 | 默认 | 系统 / Runner | 已启用 CLI（认证方式） |\n")
			b.WriteString("| :--- | :--- | :--- | :--- | :--- |\n")
			for _, r := range runners {
				st := "🟢 在线"
				if r.Status != domain.RunnerStatusOnline {
					st = "⚪ 离线"
				}
				defMark := "—"
				if r.ID == defaultID {
					defMark = "★ 默认"
				}
				var clis []string
				for _, rt := range r.Runtimes {
					if rt.Status == "disabled" {
						continue
					}
					mode := "Key"
					if runnerUsesLocalLogin(r, rt.Provider) {
						mode = "本机配置"
					}
					clis = append(clis, fmt.Sprintf("`%s`(%s)", rt.Provider, mode))
				}
				cliText := strings.Join(clis, ", ")
				if cliText == "" {
					cliText = "—"
				}
				ver := r.RunnerVersion
				if ver == "" {
					ver = "未知"
				}
				fmt.Fprintf(&b, "| **%s** (`%s`) | %s | %s | %s/%s · `v%s` | %s |\n",
					r.Name, r.ID, st, defMark, r.OS, r.Arch, ver, cliText)
			}
		}
		b.WriteString("\n**快捷管理指令（直接发送，0 Token 毫秒级生效）：**\n")
		b.WriteString("- `/runner add` — 生成接入新测试机（Linux / macOS / WSL2）的一键安装命令\n")
		b.WriteString("- `/runner sync all`（或 `/runner sync WSL2`）— 一键将主控网关配置同步到测试机并自动清理冲突 `auth.json`\n")
		b.WriteString("- `/runner use <名称>`（如 `/runner use WSL2` 或 `/runner use cloud`）— 切换默认执行机器\n")
		b.WriteString("- `/runner config <名称> codex model=gpt-6.2-sol` — 远程查看或修改测试机 CLI 默认模型/网关\n")
		b.WriteString("- `/runner upgrade all` — 一键远程 OTA 升级在线测试机的 Runner 守护进程\n")
		return b.String()

	case "add", "bind", "new", "接入", "添加":
		if d.runners == nil {
			return "绑定码服务未初始化。"
		}
		code, exp, err := d.runners.mintBindCodeForUser(user.ID)
		if err != nil {
			return "❌ " + err.Error()
		}
		cmd := buildRunnerInstallCommand(originHost, code)
		var b strings.Builder
		b.WriteString("### ➕ 接入新测试机（Linux / macOS / Windows WSL2）\n\n")
		fmt.Fprintf(&b, "已为您签发一次性安全绑定码（有效期至 `%s`），在目标机器终端粘贴执行以下单行命令即可自动接入并注册后台守护服务：\n\n", exp.Local().Format("15:04:05"))
		fmt.Fprintf(&b, "```bash\n%s\n```\n\n", cmd)
		b.WriteString("**接入后无需再手工改机器上的配置文件：**\n")
		b.WriteString("1. 机器上线后直接在这里发送 `/runner sync <机器名>`，即可一键下发与主控相同的专用网关配置并清理冲突登录态；\n")
		b.WriteString("2. 或前往 **平台底座 → 执行机器** 页面，点击对应 CLI 行的 **「配置网关」** 可视化修改网关地址、令牌与默认模型。")
		return b.String()

	case "use", "default", "switch", "切换":
		target := strings.TrimSpace(strings.Join(args, " "))
		if target == "" {
			return "请指定要切换的机器名称，例如：`/runner use WSL2`、`/runner use 43` 或 `/runner use cloud`。"
		}
		if strings.EqualFold(target, "cloud") || target == "云端" || target == "none" {
			if err := d.repo.SetDefaultRunner(user.ID, "", time.Now().UTC()); err != nil {
				return "❌ 切回云端失败：" + err.Error()
			}
			return "✅ 已将默认执行目标切回 **平台云端执行池**。"
		}
		matched := matchUserRunners(runners, target, false, user, isAdmin)
		if len(matched) == 0 {
			return fmt.Sprintf("❌ 未找到匹配 `%s` 的执行机器，请发送 `/runner` 查看可用列表。", target)
		}
		chosen := matched[0]
		if blocker := chosen.DefaultSelectionBlocker(); blocker != "" {
			return fmt.Sprintf("❌ 无法将 **%s** 设为默认机器：%s", chosen.Name, blocker)
		}
		if err := d.repo.SetDefaultRunner(user.ID, chosen.ID, time.Now().UTC()); err != nil {
			return "❌ 切换默认机器失败：" + err.Error()
		}
		return fmt.Sprintf("✅ 已将默认执行机器切换为 **%s** (`%s`)，后续新建对话与 SOP 将优先在该机器上运行。", chosen.Name, chosen.ID)

	case "sync", "同步":
		selector := "all"
		provider := "codex"
		for _, a := range args {
			low := strings.ToLower(strings.TrimSpace(a))
			if low == "codex" || low == "claude" || low == "claude-code" {
				if low == "claude" {
					provider = "claude-code"
				} else {
					provider = low
				}
			} else if strings.TrimSpace(a) != "" {
				selector = a
			}
		}
		matched := matchUserRunners(runners, selector, true, user, isAdmin)
		var online []domain.Runner
		for _, r := range matched {
			if r.Status == domain.RunnerStatusOnline {
				online = append(online, r)
			}
		}
		if len(online) == 0 {
			return fmt.Sprintf("❌ 未找到匹配 `%s` 的在线自有测试机，请先确认机器在线（发送 `/runner` 查看）。", selector)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "### ⚡ 远程同步 `%s` 网关配置结果\n\n", provider)
		for _, r := range online {
			st, _, _, errMsg := d.requestRunnerCLIConfig(ctx, user, r, "set", provider, runnerCLIConfigHTTPInput{
				Provider:     provider,
				SyncFromHost: true,
			})
			if errMsg != "" {
				fmt.Fprintf(&b, "- ❌ **%s** (`%s`)：%s\n", r.Name, r.ID, errMsg)
				continue
			}
			authNote := "无冲突 OAuth 文件"
			if st.ClearedAuthBackup != "" {
				authNote = "已自动隔离备份冲突登录态 `" + filepath.Base(st.ClearedAuthBackup) + "`"
			}
			previewModels := strings.Join(st.DiscoveredModels, ", ")
			if len(st.DiscoveredModels) > 8 {
				previewModels = strings.Join(st.DiscoveredModels[:8], ", ") + fmt.Sprintf(" 等共 %d 个模型", len(st.DiscoveredModels))
			}
			if previewModels == "" {
				previewModels = st.Model
			}
			fmt.Fprintf(&b, "- ✅ **%s** (`%s`)：网关 `%s` (`%s`) · 默认模型 `%s` · %s · 可用模型：%s\n",
				r.Name, r.ID, firstNonEmpty(st.GatewayName, "custom"), st.BaseURL, st.Model, authNote, previewModels)
		}
		return b.String()

	case "config", "cfg", "配置":
		if len(args) == 0 {
			return "用法示例：\n- 查看配置：`/runner config WSL2 codex`\n- 修改默认模型或网关：`/runner config WSL2 codex model=gpt-6.2-sol effort=high`"
		}
		selector := args[0]
		provider := "codex"
		input := runnerCLIConfigHTTPInput{}
		hasMutation := false
		for _, tok := range args[1:] {
			low := strings.ToLower(strings.TrimSpace(tok))
			if low == "codex" || low == "claude" || low == "claude-code" {
				if low == "claude" {
					provider = "claude-code"
				} else {
					provider = low
				}
				continue
			}
			k, v, ok := strings.Cut(tok, "=")
			if !ok {
				continue
			}
			v = strings.Trim(strings.TrimSpace(v), `"'`)
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "model", "m":
				input.Model = v
				hasMutation = true
			case "url", "baseurl", "base_url":
				input.BaseURL = v
				hasMutation = true
			case "key", "token", "apikey", "api_key":
				input.APIKey = v
				hasMutation = true
			case "effort", "reasoning":
				input.Effort = v
				hasMutation = true
			case "gateway", "provider_name":
				input.GatewayName = v
				hasMutation = true
			case "mode", "auth":
				if strings.EqualFold(v, "official") || strings.EqualFold(v, "oauth") || v == "官方" {
					input.UseOfficialLogin = true
					hasMutation = true
				}
			case "sync":
				if v == "1" || strings.EqualFold(v, "true") {
					input.SyncFromHost = true
					hasMutation = true
				}
			case "clear-auth", "clear_auth":
				bv := v == "1" || strings.EqualFold(v, "true")
				input.ClearConflictingAuth = &bv
				hasMutation = true
			}
		}
		input.Provider = provider
		matched := matchUserRunners(runners, selector, true, user, isAdmin)
		if len(matched) == 0 {
			return fmt.Sprintf("❌ 未找到匹配 `%s` 的自有执行机器。", selector)
		}
		r := matched[0]
		action := "get"
		if hasMutation {
			action = "set"
		}
		st, _, _, errMsg := d.requestRunnerCLIConfig(ctx, user, r, action, provider, input)
		if errMsg != "" {
			return fmt.Sprintf("❌ **%s** (`%s`)：%s", r.Name, r.ID, errMsg)
		}
		verb := "当前配置"
		if hasMutation {
			verb = "配置已更新"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "### 🔧 **%s** (`%s`) · `%s` %s\n\n", r.Name, r.ID, provider, verb)
		fmt.Fprintf(&b, "- **配置文件**：`%s`（存在：`%v`）\n", st.ConfigPath, st.Exists)
		fmt.Fprintf(&b, "- **网关标识 / 地址**：`%s` · `%s`\n", firstNonEmpty(st.GatewayName, "—"), firstNonEmpty(st.BaseURL, "官方默认"))
		fmt.Fprintf(&b, "- **令牌掩码**：`%s`\n", firstNonEmpty(st.TokenMasked, "未配置"))
		fmt.Fprintf(&b, "- **默认模型 / 强度**：`%s` / `%s`\n", firstNonEmpty(st.Model, "默认"), firstNonEmpty(st.Effort, "默认"))
		if st.ConflictingAuth {
			b.WriteString("- ⚠️ **检测到同目录残留 OAuth 登录文件**（工作台运行已自动隔离忽略；发送 `/runner sync " + r.Name + "` 可永久备份清理）\n")
		} else if st.ClearedAuthBackup != "" {
			fmt.Fprintf(&b, "- 🧹 **已隔离冲突登录态**：`%s`\n", st.ClearedAuthBackup)
		}
		if len(st.DiscoveredModels) > 0 {
			fmt.Fprintf(&b, "- **实时探测可用模型（%d 个）**：`%s`\n", len(st.DiscoveredModels), strings.Join(st.DiscoveredModels, "`, `"))
		}
		return b.String()

	case "upgrade", "update", "升级":
		selector := "all"
		if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
			selector = strings.TrimSpace(args[0])
		}
		matched := matchUserRunners(runners, selector, true, user, isAdmin)
		var online []domain.Runner
		for _, r := range matched {
			if r.Status == domain.RunnerStatusOnline {
				online = append(online, r)
			}
		}
		if len(online) == 0 {
			return fmt.Sprintf("❌ 未找到匹配 `%s` 的在线自有测试机。", selector)
		}
		var b strings.Builder
		b.WriteString("### 🚀 测试机 Runner 远程 OTA 升级\n\n")
		for _, r := range online {
			if d.runnerControls == nil || !r.SupportsCLISettings() {
				fmt.Fprintf(&b, "- ⚠️ **%s** (`%s`)：当前版本 `%s` 不支持远程升级\n", r.Name, r.ID, r.RunnerVersion)
				continue
			}
			uctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			resp, err := d.runnerControls.requestWorkspace(uctx, r.ID, domain.RunnerWorkspaceRequest{
				Operation:   domain.RunnerWorkspaceOperationMaintenance,
				Maintenance: &domain.RunnerMaintenanceRequest{Apply: true},
			})
			cancel()
			if err != nil {
				fmt.Fprintf(&b, "- ❌ **%s** (`%s`)：连接失败（%v）\n", r.Name, r.ID, err)
				continue
			}
			if resp.Error != "" {
				fmt.Fprintf(&b, "- ❌ **%s** (`%s`)：%s\n", r.Name, r.ID, resp.Error)
				continue
			}
			var mres struct {
				Status  string `json:"status"`
				Current string `json:"current"`
				Message string `json:"message"`
			}
			_ = json.Unmarshal(resp.Payload, &mres)
			fmt.Fprintf(&b, "- ✅ **%s** (`%s`)：状态 `%s`（当前 `v%s`）%s\n",
				r.Name, r.ID, firstNonEmpty(mres.Status, "updating"), firstNonEmpty(mres.Current, r.RunnerVersion),
				func() string {
					if mres.Message != "" {
						return " — " + mres.Message
					}
					return " — 正在后台拉取最新二进制并自动热重启（约 5~10 秒重连上线）"
				}())
		}
		return b.String()

	default:
		return "未知子命令 `" + subcmd + "`。支持：`/runner`、`/runner add`、`/runner sync [机器名|all]`、`/runner use <机器名>`、`/runner config <机器名> codex model=...`、`/runner upgrade [机器名|all]`。"
	}
}

func (h *assistantHandlers) executeWorkbenchSlashCommand(ctx context.Context, user domain.User, r *http.Request, content string) string {
	trimmed := strings.TrimSpace(content)
	if nl := strings.IndexAny(trimmed, "\r\n"); nl >= 0 {
		trimmed = strings.TrimSpace(trimmed[:nl])
	}
	fields := strings.Fields(trimmed)
	cmd := ""
	if len(fields) > 0 {
		cmd = strings.ToLower(fields[0])
	}
	subcmd := ""
	var args []string
	if len(fields) > 1 {
		subcmd = fields[1]
		args = fields[2:]
	}
	if cmd == "/test" || cmd == "/autotest" || cmd == "/测试" {
		pid := ""
		if r != nil {
			pid = strings.TrimSpace(r.URL.Query().Get("projectId"))
		}
		return h.deps.executeWorkbenchAutoTestOp(ctx, user, pid, subcmd, args)
	}
	return h.deps.executeWorkbenchRunnerOp(ctx, user, resolvePublicOriginHost(r), subcmd, args)
}

// ---------- Natural-Language MCP Tool: webuddy_runners_manage ----------

func (h *agentToolGateway) runWebuddyRunnersManageTool(ctx context.Context, session *AgentToolSession, args map[string]any) (map[string]any, error) {
	if session == nil || session.User.ID <= 0 {
		return mcpToolTextResult("请先登录后管理执行机器", true), nil
	}
	strArg := func(k string) string {
		if v, ok := args[k].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	action := strings.ToLower(strArg("action"))
	if action == "" {
		action = "list"
	}
	var cmdArgs []string
	if r := strArg("runner"); r != "" {
		cmdArgs = append(cmdArgs, r)
	}
	if p := strArg("provider"); p != "" {
		cmdArgs = append(cmdArgs, p)
	}
	if m := strArg("model"); m != "" {
		cmdArgs = append(cmdArgs, "model="+m)
	}
	if u := strArg("baseUrl"); u != "" {
		cmdArgs = append(cmdArgs, "url="+u)
	}
	if k := strArg("apiKey"); k != "" {
		cmdArgs = append(cmdArgs, "key="+k)
	}
	if ef := strArg("effort"); ef != "" {
		cmdArgs = append(cmdArgs, "effort="+ef)
	}
	md := h.deps.executeWorkbenchRunnerOp(ctx, session.User, resolvePublicOriginHost(nil), action, cmdArgs)
	out, _ := json.Marshal(map[string]any{
		"ok":       !strings.HasPrefix(md, "❌"),
		"action":   action,
		"markdown": md,
	})
	return mcpToolTextResult(string(out), strings.HasPrefix(md, "❌")), nil
}
