package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"agentflow/internal/domain"
	"agentflow/internal/webuddykb"

	"github.com/gin-gonic/gin"
)

type readinessCheckItem struct {
	Ready  bool   `json:"ready"`
	ID     string `json:"id,omitempty"`
	Name   string `json:"name,omitempty"`
	Driver string `json:"driver,omitempty"`
	Detail string `json:"detail"`
}

type projectReadinessResult struct {
	ProjectID    string             `json:"projectId"`
	ProjectName  string             `json:"projectName"`
	OverallReady bool               `json:"overallReady"`
	VerifiedAt   time.Time          `json:"verifiedAt"`
	Sandbox      readinessCheckItem `json:"sandbox"`
	KB           readinessCheckItem `json:"kb"`
	Runner       readinessCheckItem `json:"runner"`
}

func (d *deps) computeProjectReadiness(userID int64, projectID string) projectReadinessResult {
	pid := strings.TrimSpace(projectID)
	var projName string
	if pid == "" {
		if projects := d.repo.Projects(); len(projects) > 0 {
			pid = projects[0].ID
			projName = projects[0].Name
		}
	} else if p, ok := d.repo.Project(pid); ok {
		projName = p.Name
	}

	res := projectReadinessResult{
		ProjectID:   pid,
		ProjectName: projName,
		VerifiedAt:  time.Now().UTC(),
	}

	// 1. Check conversation-capable sandboxes (native or docker)
	if pid != "" {
		sbs := d.repo.ConversationSandboxes(pid)
		for _, sb := range sbs {
			if domain.ConversationSandboxDriver(sb.Driver) {
				res.Sandbox = readinessCheckItem{
					Ready:  true,
					ID:     sb.ID,
					Name:   sb.Name,
					Driver: sb.Driver,
					Detail: fmt.Sprintf("已配置 %s 沙箱（%s）", sb.Driver, sb.Name),
				}
				break
			}
		}
	}
	if !res.Sandbox.Ready {
		res.Sandbox = readinessCheckItem{
			Ready:  false,
			Detail: "当前仅有 local 沙箱，尚未配置工作区可用的 native / docker 沙箱",
		}
	}

	// 2. Check repository & .tmh Knowledge Base
	var matchedRepo *domain.Repository
	for _, r := range d.repo.Repositories() {
		if (pid == "" || r.ProjectID == pid) && strings.TrimSpace(r.LocalPath) != "" {
			cp := r
			matchedRepo = &cp
			break
		}
	}
	if matchedRepo != nil {
		ex, hg, hkb, _ := inspectLocalRepo(matchedRepo.LocalPath)
		if ex && (hg || hkb) {
			res.KB = readinessCheckItem{
				Ready:  true,
				ID:     matchedRepo.ID,
				Name:   matchedRepo.Name,
				Detail: "已挂载代码图谱与知识库（可直接问答与查影响）",
			}
		} else if ex {
			res.KB = readinessCheckItem{
				Ready:  false,
				ID:     matchedRepo.ID,
				Name:   matchedRepo.Name,
				Detail: "已挂载仓库目录，尚未构建 .tmh 知识图谱",
			}
		} else {
			res.KB = readinessCheckItem{
				Ready:  false,
				ID:     matchedRepo.ID,
				Name:   matchedRepo.Name,
				Detail: "仓库路径在控制面不可访问",
			}
		}
	} else {
		res.KB = readinessCheckItem{
			Ready:  false,
			Detail: "该项目尚未关联本地代码仓库",
		}
	}

	// 3. Check online Runner / default Runner
	var defRunnerID string
	if userID > 0 {
		defRunnerID = d.repo.DefaultRunner(userID)
	}
	var chosenRunner *domain.Runner
	for _, r := range d.repo.Runners() {
		if r.Status != domain.RunnerStatusOnline {
			continue
		}
		if userID > 0 && r.OwnerUserID != 0 && !domain.CanUserUseRunner(r, userID) {
			continue
		}
		if r.ID == defRunnerID {
			cp := r
			chosenRunner = &cp
			break
		}
		if chosenRunner == nil {
			cp := r
			chosenRunner = &cp
		}
	}
	if chosenRunner != nil {
		res.Runner = readinessCheckItem{
			Ready:  true,
			ID:     chosenRunner.ID,
			Name:   chosenRunner.Name,
			Detail: fmt.Sprintf("在线执行机：%s", chosenRunner.Name),
		}
	} else {
		res.Runner = readinessCheckItem{
			Ready:  false,
			Detail: "暂无在线执行机器",
		}
	}

	res.OverallReady = res.Sandbox.Ready && res.Runner.Ready
	return res
}

func (d *deps) autoConfigureProjectEnv(userID int64, projectID string) (projectReadinessResult, []string, error) {
	pid := strings.TrimSpace(projectID)
	if pid == "" {
		if projects := d.repo.Projects(); len(projects) > 0 {
			pid = projects[0].ID
		}
	}
	if pid == "" {
		return projectReadinessResult{}, nil, fmt.Errorf("未找到目标项目")
	}

	var actions []string
	before := d.computeProjectReadiness(userID, pid)

	// 1. Auto-create a ready-to-use native workspace sandbox if missing
	if !before.Sandbox.Ready {
		sbID := "sandbox-native-" + sanitizeRepoSlug(pid)
		if sbID == "sandbox-native-" {
			sbID = "sandbox-native-" + randomID()[:6]
		}
		sb := domain.SandboxProfile{
			ID:        sbID,
			Name:      "轻量工作区沙箱 (Native)",
			ProjectID: pid,
			Driver:    domain.SandboxDriverNative,
		}
		if err := d.repo.PutSandbox(sb); err != nil {
			return before, actions, fmt.Errorf("创建 native 沙箱失败: %w", err)
		}
		actions = append(actions, fmt.Sprintf("已为项目创建并启用「%s」（ID: %s）", sb.Name, sb.ID))
	} else {
		actions = append(actions, fmt.Sprintf("工作区沙箱已就绪（%s · %s）", before.Sandbox.Name, before.Sandbox.Driver))
	}

	// 2. Auto-bind default online runner for the user if unset
	if userID > 0 && d.repo.DefaultRunner(userID) == "" {
		for _, r := range d.repo.Runners() {
			if r.Status != domain.RunnerStatusOnline {
				continue
			}
			if r.OwnerUserID != 0 && !domain.CanUserUseRunner(r, userID) {
				continue
			}
			if r.DefaultSelectionBlocker() == "" {
				if err := d.repo.SetDefaultRunner(userID, r.ID, time.Now().UTC()); err == nil {
					actions = append(actions, fmt.Sprintf("已自动设为默认执行机器：%s", r.Name))
				}
				break
			}
		}
	}

	after := d.computeProjectReadiness(userID, pid)
	return after, actions, nil
}

// GET /api/webuddy/env/project-readiness?projectId=...
func (h *webuddyEnvHandlers) projectReadiness(c *gin.Context) {
	pid := strings.TrimSpace(c.Query("projectId"))
	if pid == "" {
		c.JSON(http.StatusOK, projectReadinessResult{
			OverallReady: false,
			Sandbox:      readinessCheckItem{Ready: false, Detail: "尚未选择项目"},
			Runner:       readinessCheckItem{Ready: false, Detail: "尚未选择项目"},
		})
		return
	}
	if authRequired() {
		if !h.requireProjectReader(c, pid) {
			return
		}
	}
	var userID int64
	if u, ok := h.requestUser(c.Request, true); ok {
		userID = u.ID
	}
	res := h.computeProjectReadiness(userID, pid)
	c.JSON(http.StatusOK, res)
}

// POST /api/webuddy/env/auto-configure
func (h *webuddyEnvHandlers) autoConfigure(c *gin.Context) {
	var input struct {
		ProjectID string `json:"projectId"`
	}
	if !decode(c, &input) {
		return
	}
	pid := strings.TrimSpace(input.ProjectID)
	if authRequired() {
		if pid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请指定 projectId"})
			return
		}
		if !h.requireProjectWriter(c, pid) {
			return
		}
	}
	var userID int64
	if u, ok := h.requestUser(c.Request, true); ok {
		userID = u.ID
	}
	after, actions, err := h.autoConfigureProjectEnv(userID, pid)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "readiness": after})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":        true,
		"actions":   actions,
		"readiness": after,
	})
}

// POST /api/webuddy/sop/quick-create
// Allows 1-click standardized SOP creation from AI chat confirmation cards,
// automatically generating codex-backed stage agents and a published WorkflowDefinition.
func (h *webuddyEnvHandlers) sopQuickCreate(c *gin.Context) {
	var input struct {
		ProjectID   string `json:"projectId"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Generic     bool   `json:"generic"`
		Steps       []struct {
			Name     string `json:"name"`
			Prompt   string `json:"prompt"`
			Artifact string `json:"artifact"`
		} `json:"steps"`
	}
	if !decode(c, &input) {
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SOP 名称不能为空"})
		return
	}
	pid := strings.TrimSpace(input.ProjectID)
	isPlatformAdmin := h.isPlatformAdmin(c.Request)
	if authRequired() && !isPlatformAdmin {
		if input.Generic {
			c.JSON(http.StatusForbidden, gin.H{"error": "只有平台管理员可以发布平台级通用 SOP，取消勾选通用即可发布为当前租户专属 SOP"})
			return
		}
		if pid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请先选择所属项目再构建 SOP"})
			return
		}
		if !h.requireProjectWriter(c, pid) {
			return
		}
	}
	if len(input.Steps) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "至少需要 1 个执行步骤"})
		return
	}

	actor := "平台"
	if u, ok := h.requestUser(c.Request, true); ok && u != nil && strings.TrimSpace(u.LoginName) != "" {
		actor = strings.TrimSpace(u.LoginName)
	}
	slug := sanitizeRepoSlug(name)
	if slug == "" {
		slug = "custom"
	}
	suffix := randomID()
	if len(suffix) > 6 {
		suffix = suffix[:6]
	}
	wfID := "sop-" + slug + "-" + suffix
	now := time.Now().UTC()

	nodes := make([]domain.WorkflowNode, 0, len(input.Steps))
	edges := make([]domain.WorkflowEdge, 0, len(input.Steps))
	for i, st := range input.Steps {
		stepName := strings.TrimSpace(st.Name)
		if stepName == "" {
			stepName = fmt.Sprintf("阶段 %d", i+1)
		}
		stepPrompt := strings.TrimSpace(st.Prompt)
		if stepPrompt == "" {
			stepPrompt = fmt.Sprintf("完成「%s」阶段工作并输出清晰结论与验证依据。", stepName)
		}
		artifact := strings.TrimSpace(st.Artifact)
		if artifact == "" {
			artifact = fmt.Sprintf("step-%d-report.md", i+1)
		}
		nodeID := fmt.Sprintf("step-%d", i+1)
		agentID := fmt.Sprintf("%s-agent-%d", wfID, i+1)
		ag := domain.AgentDefinition{
			ID:            agentID,
			Name:          fmt.Sprintf("%s · %s", name, stepName),
			Description:   stepName,
			ProjectID:     pid,
			Version:       1,
			ExecutorID:    "codex",
			KnowledgeBase: true,
			PlatformMCP:   true,
			Prompt:        stepPrompt + "\n\n任务输入：{{input}}",
		}
		if err := h.repo.PutAgent(ag); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("保存阶段 Agent 失败: %v", err)})
			return
		}
		nodes = append(nodes, domain.WorkflowNode{
			ID:          nodeID,
			Name:        stepName,
			Type:        "agent",
			AgentID:     agentID,
			MaxAttempts: 2,
			ArtifactOutputs: []domain.ArtifactContract{
				{
					Name:      nodeID + "-report",
					Path:      artifact,
					MediaType: "text/markdown",
					Required:  true,
				},
				{
					Name:      nodeID + "-knowledge",
					Path:      "knowledge.md",
					MediaType: "text/markdown",
					Required:  false,
				},
			},
			Position: domain.Position{X: float64(100 + i*240), Y: 200},
		})
		if i > 0 {
			edges = append(edges, domain.WorkflowEdge{
				ID:     fmt.Sprintf("e%d", i),
				Source: fmt.Sprintf("step-%d", i),
				Target: nodeID,
				When:   "success",
			})
		}
	}

	wf := domain.WorkflowDefinition{
		ID:          wfID,
		Name:        name,
		Description: strings.TrimSpace(input.Description),
		ProjectID:   pid,
		Version:     1,
		Revision:    1,
		Status:      "published",
		Owner:       actor,
		Generic:     input.Generic || pid == "",
		UpdatedAt:   now,
		Nodes:       nodes,
		Edges:       edges,
	}
	if err := h.repo.PutWorkflow(wf); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("保存 SOP 失败: %v", err)})
		return
	}
	if !wf.Generic {
		_ = defaultTenantStore.bindWorkflowTenant(wf.ID, h.currentActiveTenant(c.Request))
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"workflow": wf,
	})
}

// ---------- Direct Webuddy Tools inside Assistant Chat ----------

func webuddyAssistantToolSpecs() []map[string]any {
	return []map[string]any{
		{
			"name":        "webuddy_kb_overview",
			"description": "免沙箱秒查当前项目的代码图谱总览（业务域、业务流程、数据表与知识页列表），回答项目是干什么的、有哪些模块时优先调用。",
			"inputSchema": json.RawMessage(`{"type":"object","properties":{"projectId":{"type":"string","description":"可选项目ID，默认当前会话项目"}}}`),
		},
		{
			"name":        "webuddy_kb_search",
			"description": "免沙箱秒搜当前项目的代码图谱、外部平台接入、接口、数据表与知识库页面，无需打开 Docker 工作区即可直接查询。",
			"inputSchema": json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"搜索关键词"},"limit":{"type":"integer","description":"返回条数，默认20"}},"required":["query"]}`),
		},
		{
			"name":        "webuddy_kb_chain",
			"description": "免沙箱追溯当前项目的业务证据链（业务域->流程->数据表->源码行号），适合回答「有哪些外部平台接入」「怎么校验输入」等业务问题。",
			"inputSchema": json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"业务问题或关键词"},"flows":{"type":"integer","description":"最多返回流程数，默认6"}},"required":["query"]}`),
		},
		{
			"name":        "webuddy_ensure_project_env",
			"description": "一键为当前项目自动配置并实时核验工作区环境（自动创建 native 轻量工作区沙箱、检查知识库与在线执行机），返回真实核验回执。配置完成后请在回复中输出一个 webuddy-card 代码块让用户直观看到生效状态。",
			"inputSchema": json.RawMessage(`{"type":"object","properties":{"projectId":{"type":"string","description":"可选项目ID，默认当前会话项目"}}}`),
		},
		{
			"name":        "webuddy_kb_update",
			"description": "直接在服务端调用本地源码仓与内置大模型，批量增量分析并更新当前项目的“未分析/过期”知识库页面（每页强制通过真实源码 file:line 溯源校验与 Git Commit 盖章），无需在沙箱里 git clone。完成后请在回复末尾输出一个语言为 webuddy-card 的 JSON 代码块（type 为 kb_update），让用户直观看到覆盖率进度条与源码溯源清单。",
			"inputSchema": json.RawMessage(`{"type":"object","properties":{"projectId":{"type":"string","description":"可选项目ID，默认当前会话项目"},"limit":{"type":"integer","description":"本次增量更新页数，默认3，最大5"},"query":{"type":"string","description":"可选聚焦关键词（如 审批、用户、订单），优先更新匹配页面"}}}`),
		},
		{
			"name":        "webuddy_preview_start",
			"description": "直接在平台服务端为当前项目一键启动 Web 服务并打穿 HTTPS 预览通道（/app-preview/<projectId>/），自动安装缺失依赖并分配本地端口。当用户想查看页面内容、打开后台、预览界面或体验部署效果时直接调用。完成后请在回复末尾输出语言为 webuddy-card 的 JSON 代码块（type 为 live_preview），前端会自动内嵌实时可交互页面预览。",
			"inputSchema": json.RawMessage(`{"type":"object","properties":{"projectId":{"type":"string","description":"可选项目ID，默认当前会话项目"}}}`),
		},
		{
			"name":        "webuddy_list_sops",
			"description": "免沙箱秒查当前项目及平台内置的所有已发布 SOP 流程（含 pstack 拆分的 4 条通用 SOP），在识别到用户需求或需要推荐/使用 SOP 时调用，用于准确填写 issue_confirm / sop_confirm 卡片里的 SOP ID。",
			"inputSchema": json.RawMessage(`{"type":"object","properties":{"projectId":{"type":"string","description":"可选项目ID，默认当前会话项目"}}}`),
		},
		{
			"name":        "webuddy_kb_shared_search",
			"description": "搜索当前租户的跨项目团队共享知识库（包含各项目同步的架构知识、代码审查沉淀的避坑经验与团队规范），回答跨项目约定、历史踩坑或复用方案时优先调用。",
			"inputSchema": json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"搜索关键词（留空返回最近共享条目）"}}}`),
		},
		{
			"name":        "webuddy_runners_manage",
			"description": "工作台执行机/测试机管控工具：无需进终端，支持查看测试机列表(list)、生成单行接入新测试机命令(add)、切换默认执行机(use)、一键同步主控网关配置并清理冲突登录态(sync)、远程修改测试机 CLI 模型或网关(config)、以及远程 OTA 升级 Runner 守护进程(upgrade)。",
			"inputSchema": json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["list","add","use","sync","config","upgrade"],"description":"操作类型"},"runner":{"type":"string","description":"目标测试机名称或 ID（sync/upgrade 支持传 all）"},"provider":{"type":"string","enum":["codex","claude-code"],"description":"CLI 类型，默认 codex"},"model":{"type":"string","description":"可选默认模型（如 gpt-6.2-sol、gpt-6-luna）"},"baseUrl":{"type":"string","description":"可选网关 Base URL"},"apiKey":{"type":"string","description":"可选网关令牌"},"effort":{"type":"string","description":"可选思考强度（low/medium/high/xhigh）"}},"required":["action"]}`),
		},
		{
			"name":        "webuddy_autotest_manage",
			"description": "AI 自动化测试与五步自愈工具：支持列出当前项目固化回归用例(list)、用自然语言需求直接在服务端启动 Headless Chrome 真机探索并固化确定性脚本(generate)、或零 Token 极速跑回归并遇 UI 改版自动执行五步诊断与 >=0.80 置信度自愈(run)。",
			"inputSchema": json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["list","generate","run"],"description":"操作类型"},"projectId":{"type":"string","description":"可选项目ID，默认当前会话项目"},"title":{"type":"string","description":"generate 时的用例标题"},"requirement":{"type":"string","description":"generate 时的自然语言验收场景描述"},"baseUrl":{"type":"string","description":"generate 时的目标站点地址（如 https://earn-demo.bbstack.date）"},"preconditionUrl":{"type":"string","description":"generate 时的可选前置登录态注入路径（如 /auth/dev?id=1&to=admin）"},"caseId":{"type":"string","description":"run 时的用例 ID 或关键词，留空跑全量"}},"required":["action"]}`),
		},
	}
}

func isWebuddyAssistantTool(name string) bool {
	switch name {
	case "webuddy_kb_overview", "webuddy_kb_search", "webuddy_kb_chain", "webuddy_ensure_project_env", "webuddy_kb_update", "webuddy_preview_start", "webuddy_list_sops", "webuddy_kb_shared_search", "webuddy_runners_manage", "webuddy_autotest_manage":
		return true
	default:
		return false
	}
}

func (h *agentToolGateway) callWebuddyAssistantTool(ctx context.Context, session *AgentToolSession, name string, rawArgs json.RawMessage) (map[string]any, error) {
	if h.deps == nil || h.repo == nil {
		return mcpToolTextResult("Webuddy 服务未初始化", true), nil
	}
	var args map[string]any
	_ = json.Unmarshal(rawArgs, &args)
	strArg := func(k string) string {
		if v, ok := args[k].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	intArg := func(k string, def int) int {
		if v, ok := args[k].(float64); ok && int(v) > 0 {
			return int(v)
		}
		return def
	}

	pid := strArg("projectId")
	if pid == "" && session != nil {
		pid = strings.TrimSpace(session.ProjectID)
	}
	isAdmin := session != nil && strings.TrimSpace(session.User.LoginName) != "" && h.repo.IsPlatformAdmin(session.User.LoginName)
	checkProjectPerm := func(targetPID string, write bool) (string, bool) {
		if targetPID == "" {
			return "请先选择当前会话所属的项目后再执行此操作。", false
		}
		if !authRequired() || isAdmin {
			return "", true
		}
		var uid int64
		if session != nil {
			uid = session.User.ID
		}
		p, ok := h.repo.Project(targetPID)
		if !ok {
			return "未找到目标项目或无权访问。", false
		}
		activeTID := defaultTenantStore.activeTenantForUser(uid, false)
		if defaultTenantStore.projectTenant(targetPID) != activeTID {
			return "无权跨团队空间访问该项目。", false
		}
		if p.OwnerID == uid {
			return "", true
		}
		for _, m := range p.Members {
			if m.UserID == uid {
				if write && strings.EqualFold(strings.TrimSpace(m.Role), "viewer") {
					return "当前账号为只读成员，无权执行写操作。", false
				}
				return "", true
			}
		}
		// Allow tenant members to read tenant projects, and non-viewer tenant members to write
		if role, in := defaultTenantStore.userTenantRole(activeTID, uid); in {
			if write && role == "viewer" {
				return "当前团队角色为只读访客，无权执行写操作。", false
			}
			return "", true
		}
		return "无权访问该项目。", false
	}

	if name == "webuddy_runners_manage" {
		return h.runWebuddyRunnersManageTool(ctx, session, args)
	}

	if name == "webuddy_autotest_manage" {
		if strArg("action") == "generate" || strArg("action") == "run" {
			if msg, ok := checkProjectPerm(pid, true); !ok {
				return mcpToolTextResult(msg, true), nil
			}
		}
		return h.runWebuddyAutoTestManageTool(ctx, session, pid, args)
	}

	if name == "webuddy_ensure_project_env" {
		if msg, ok := checkProjectPerm(pid, true); !ok {
			return mcpToolTextResult(msg, true), nil
		}
		after, actions, err := h.autoConfigureProjectEnv(session.User.ID, pid)
		if err != nil {
			return mcpToolTextResult(err.Error(), true), nil
		}
		out, _ := json.Marshal(map[string]any{
			"ok":        true,
			"actions":   actions,
			"readiness": after,
			"uiHint":    fmt.Sprintf("请在最终回复中附带一个 ```webuddy-card 代码块，内容为 {\"type\":\"env_confirm\",\"projectId\":%q,\"title\":\"工作区环境已自动配置并核验\",\"summary\":\"已启用轻量工作区沙箱并核验通过\"}，让用户在对话内直接看到实时绿色生效卡片。", after.ProjectID),
		})
		return mcpToolTextResult(string(out), false), nil
	}

	if name == "webuddy_kb_update" {
		if msg, ok := checkProjectPerm(pid, true); !ok {
			return mcpToolTextResult(msg, true), nil
		}
		actor := strings.TrimSpace(session.User.LoginName)
		if actor == "" && session.User.ID > 0 {
			actor = fmt.Sprintf("user-%d", session.User.ID)
		}
		if actor == "" {
			actor = "webuddy-ai"
		}
		receipt, err := h.deps.runWebuddyKBBatchUpdate(ctx, actor, pid, intArg("limit", 3), strArg("query"))
		if err != nil {
			return mcpToolTextResult(err.Error(), true), nil
		}
		out, _ := json.Marshal(map[string]any{
			"ok":      receipt.OK,
			"receipt": receipt,
			"uiHint":  fmt.Sprintf("请在最终回复末尾附带一个语言为 webuddy-card 的 fenced JSON 代码块：{\"type\":\"kb_update\",\"projectId\":%q,\"title\":\"知识库已完成增量更新与源码溯源核验\"}，前端会自动渲染实时覆盖率进度条、源码 file:line 溯源清单以及「继续智能更新」按钮。", receipt.ProjectID),
		})
		return mcpToolTextResult(string(out), false), nil
	}

	if name == "webuddy_kb_shared_search" {
		isAdmin := session != nil && strings.TrimSpace(session.User.LoginName) != "" && h.repo.IsPlatformAdmin(session.User.LoginName)
		activeTID := DefaultTenantID
		if session != nil && session.User.ID > 0 {
			activeTID = defaultTenantStore.activeTenantForUser(session.User.ID, isAdmin)
		}
		items := defaultSharedKBStore.list(activeTID, strArg("query"), "")
		if len(items) > 12 {
			items = items[:12]
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "tenantId": activeTID, "items": items})
		return mcpToolTextResult(string(out), false), nil
	}

	if name == "webuddy_list_sops" {
		type sopBrief struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Generic     bool   `json:"generic"`
			Steps       int    `json:"steps"`
		}
		isAdmin := session != nil && strings.TrimSpace(session.User.LoginName) != "" && h.repo.IsPlatformAdmin(session.User.LoginName)
		activeTID := DefaultTenantID
		if session != nil && session.User.ID > 0 {
			activeTID = defaultTenantStore.activeTenantForUser(session.User.ID, isAdmin)
		}
		var list []sopBrief
		for _, wf := range h.repo.Workflows() {
			if wf.Status != "published" && wf.Status != "" {
				continue
			}
			if wf.Generic || isCanonicalPresetWorkflowID(wf.ID) {
				if !defaultTenantStore.presetSOPEnabledInTenant(activeTID, wf.ID) {
					continue
				}
			} else if defaultTenantStore.workflowTenant(wf) != activeTID {
				continue
			}
			list = append(list, sopBrief{
				ID:          wf.ID,
				Name:        wf.Name,
				Description: wf.Description,
				Generic:     wf.Generic || wf.Shared,
				Steps:       len(wf.Nodes),
			})
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "sops": list})
		return mcpToolTextResult(string(out), false), nil
	}

	if name == "webuddy_preview_start" {
		if msg, ok := checkProjectPerm(pid, true); !ok {
			return mcpToolTextResult(msg, true), nil
		}
		eh := &webuddyEnvHandlers{deps: h.deps}
		resolvedPID, projName, repoPath := eh.resolveProjectAndRepoPath(pid)
		if repoPath == "" {
			return mcpToolTextResult("当前项目尚未挂载本地代码仓库，无法启动实时预览。", true), nil
		}
		dto, err := defaultPreviewMgr.start(ctx, resolvedPID, projName, repoPath)
		if err != nil {
			out, _ := json.Marshal(map[string]any{
				"ok":    false,
				"error": err.Error(),
				"logs":  dto.Logs,
			})
			return mcpToolTextResult(string(out), true), nil
		}
		out, _ := json.Marshal(map[string]any{
			"ok":      true,
			"preview": dto,
			"uiHint":  fmt.Sprintf("服务已启动并打穿端口预览（%s），请在最终回复末尾附带一个语言为 webuddy-card 的 fenced JSON 代码块：{\"type\":\"live_preview\",\"projectId\":%q,\"title\":\"实时应用预览已打穿端口并就绪\"}，前端会直接内嵌可交互页面预览与快捷入口。", dto.PreviewURL, dto.ProjectID),
		})
		return mcpToolTextResult(string(out), false), nil
	}

	// Resolve repoPath strictly for the authorized session project
	if msg, ok := checkProjectPerm(pid, false); !ok {
		return mcpToolTextResult(msg, true), nil
	}
	repoPath := ""
	for _, r := range h.repo.Repositories() {
		if r.ProjectID == pid && strings.TrimSpace(r.LocalPath) != "" {
			repoPath = strings.TrimSpace(r.LocalPath)
			break
		}
	}
	if repoPath == "" {
		return mcpToolTextResult("当前项目未挂载本地知识库目录，可先在环境接入页一键挂载。", true), nil
	}

	svc := webuddykb.NewService()
	var raw json.RawMessage
	var err error
	switch name {
	case "webuddy_kb_overview":
		raw, err = svc.Overview(ctx, repoPath)
	case "webuddy_kb_search":
		raw, err = svc.Search(ctx, repoPath, strArg("query"), intArg("limit", 20))
	case "webuddy_kb_chain":
		raw, err = svc.Chain(ctx, repoPath, strArg("query"), intArg("flows", 6))
	}
	if err != nil {
		return mcpToolTextResult(err.Error(), true), nil
	}
	return mcpToolTextResult(string(raw), false), nil
}
