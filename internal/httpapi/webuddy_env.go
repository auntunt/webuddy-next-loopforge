package httpapi

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentflow/internal/domain"
	"agentflow/internal/envprobe"

	"github.com/gin-gonic/gin"
)

type webuddyEnvHandlers struct {
	*deps
}

type envRunnerItem struct {
	ID              string                 `json:"id"`
	Name            string                 `json:"name"`
	Status          string                 `json:"status"`
	IsDefault       bool                   `json:"isDefault"`
	OS              string                 `json:"os"`
	Arch            string                 `json:"arch"`
	Version         string                 `json:"version"`
	Runtimes        []domain.RunnerRuntime `json:"runtimes,omitempty"`
	LastHeartbeatAt *time.Time             `json:"lastHeartbeatAt,omitempty"`
	HostProfile     *envprobe.HostProfile  `json:"hostProfile,omitempty"`
}

// GET /api/webuddy/env/overview
// Returns control-plane HostProfile + all online/registered Runners with their
// reported HostProfiles, marking already-mounted KBs and already-imported MCPs.
func (h *webuddyEnvHandlers) overview(c *gin.Context) {
	ctx := c.Request.Context()
	if h.envProber != nil {
		// Kick off background live probe if TTL expired (non-blocking)
		h.envProber.TriggerProbeAsync(false)
	}
	var serverProf envprobe.HostProfile
	if h.envProber != nil {
		serverProf = h.envProber.Snapshot(ctx)
	} else {
		serverProf = envprobe.ScanHost(ctx)
	}

	importedPaths := make(map[string]bool)
	allRepos := h.repo.Repositories()
	for _, r := range allRepos {
		if lp := strings.TrimRight(strings.TrimSpace(r.LocalPath), "/"); lp != "" {
			importedPaths[lp] = true
		}
	}
	markImportedKBs(&serverProf, importedPaths)

	importedMCPNames := make(map[string]bool)
	for _, m := range h.repo.MCPServers() {
		importedMCPNames[strings.ToLower(strings.TrimSpace(m.Name))] = true
		importedMCPNames[strings.ToLower(strings.TrimSpace(m.ID))] = true
	}

	var defaultRunnerID string
	var currentUserID int64
	if user, ok := h.requestUser(c.Request, true); ok {
		currentUserID = user.ID
		defaultRunnerID = h.repo.DefaultRunner(user.ID)
		if defaultRunnerID == "" && domain.SharedHostRunnerID() != "" {
			defaultRunnerID = domain.SharedHostRunnerID()
		}
	}

	allRunners := h.repo.Runners()
	runnersOut := make([]envRunnerItem, 0, len(allRunners))
	for _, r := range allRunners {
		if currentUserID > 0 && r.OwnerUserID != 0 && !domain.CanUserUseRunner(r, currentUserID) {
			continue
		}
		item := envRunnerItem{
			ID:              r.ID,
			Name:            r.Name,
			Status:          r.Status,
			IsDefault:       r.ID == defaultRunnerID,
			OS:              r.OS,
			Arch:            r.Arch,
			Version:         r.RunnerVersion,
			Runtimes:        r.Runtimes,
			LastHeartbeatAt: r.LastSeenAt,
		}
		if h.runnerProfiles != nil {
			if raw, ok := h.runnerProfiles.Load(r.ID); ok {
				if prof, ok := raw.(envprobe.HostProfile); ok {
					markImportedKBs(&prof, importedPaths)
					item.HostProfile = &prof
				}
			}
		}
		runnersOut = append(runnersOut, item)
	}

	importedMCPList := make([]string, 0, len(importedMCPNames))
	for k := range importedMCPNames {
		importedMCPList = append(importedMCPList, k)
	}

	c.JSON(http.StatusOK, gin.H{
		"server":        serverProf,
		"runners":       runnersOut,
		"importedMcps":  importedMCPList,
		"importedRepos": len(allRepos),
	})
}

func markImportedKBs(prof *envprobe.HostProfile, importedPaths map[string]bool) {
	if prof == nil || len(prof.KnowledgeBases) == 0 {
		return
	}
	copied := make([]envprobe.DiscoveredKB, len(prof.KnowledgeBases))
	copy(copied, prof.KnowledgeBases)
	for i := range copied {
		p := strings.TrimRight(strings.TrimSpace(copied[i].Path), "/")
		copied[i].AlreadyImported = importedPaths[p]
	}
	prof.KnowledgeBases = copied
}

// POST /api/webuddy/env/probe
// Triggers a live 1-token CLI & Git connectivity probe on the control plane.
// Pass ?sync=1 to wait for fresh probe results; otherwise triggers asynchronously.
func (h *webuddyEnvHandlers) probe(c *gin.Context) {
	if h.envProber == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "环境探活服务未初始化"})
		return
	}
	if c.Query("sync") == "1" || c.Query("sync") == "true" {
		prof := h.envProber.TriggerProbe(c.Request.Context(), true)
		c.JSON(http.StatusOK, gin.H{"ok": true, "sync": true, "server": prof})
		return
	}
	h.envProber.TriggerProbeAsync(true)
	prof := h.envProber.Snapshot(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"ok": true, "sync": false, "server": prof})
}

// POST /api/webuddy/env/mount-kb
// One-click mounts a discovered .tmh knowledge base directory as a Project + Repository.
func (h *webuddyEnvHandlers) mountKB(c *gin.Context) {
	var input struct {
		LocalPath     string `json:"localPath"`
		Name          string `json:"name"`
		CloneURL      string `json:"cloneUrl"`
		DefaultBranch string `json:"defaultBranch"`
		AllowRemote   bool   `json:"allowRemote"`
	}
	if !decode(c, &input) {
		return
	}
	lp := strings.TrimRight(strings.TrimSpace(input.LocalPath), "/")
	if lp == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "localPath 不能为空"})
		return
	}
	if st, err := os.Stat(lp); err != nil || !st.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "控制面本机未找到该目录（暂仅支持挂载控制面可见目录）: " + lp})
		return
	}
	for _, r := range h.repo.Repositories() {
		if strings.TrimRight(strings.TrimSpace(r.LocalPath), "/") == lp {
			p, _ := h.repo.Project(r.ProjectID)
			c.JSON(http.StatusOK, gin.H{
				"reused":      true,
				"projectId":   r.ProjectID,
				"projectName": p.Name,
				"repoId":      r.ID,
				"localPath":   lp,
			})
			return
		}
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = filepath.Base(lp)
	}
	slug := sanitizeRepoSlug(name)
	if slug == "" {
		slug = "kb-" + randomID()[:6]
	}
	if _, exists := h.repo.Project(slug); exists {
		slug = slug + "-" + randomID()[:4]
	}
	rid := "repo-" + slug
	if _, exists := h.repo.Repository(rid); exists {
		rid = rid + "-" + randomID()[:4]
	}
	key := strings.ToUpper(slug)
	if len(key) > 4 {
		key = key[:4]
	}
	branch := strings.TrimSpace(input.DefaultBranch)
	if branch == "" {
		branch = "main"
	}
	proj := domain.Project{
		ID:                  slug,
		Key:                 key,
		Name:                name,
		Description:         "从环境与接入中心一键挂载：" + lp,
		Color:               "#2563eb",
		DefaultRepositoryID: rid,
	}
	if err := h.repo.PutProject(proj); err != nil {
		respond(c, err)
		return
	}
	repoObj := domain.Repository{
		ID:            rid,
		ProjectID:     slug,
		Name:          name,
		CloneURL:      strings.TrimSpace(input.CloneURL),
		LocalPath:     lp,
		DefaultBranch: branch,
	}
	if err := h.repo.PutRepository(repoObj); err != nil {
		respond(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"reused":      false,
		"projectId":   proj.ID,
		"projectName": proj.Name,
		"repoId":      repoObj.ID,
		"localPath":   lp,
	})
}

// POST /api/webuddy/env/import-mcp
// One-click imports a discovered external MCP server from ~/.codex/config.toml or
// ~/.claude/settings.json into the platform MCP registry.
func (h *webuddyEnvHandlers) importMCP(c *gin.Context) {
	var input struct {
		Name      string   `json:"name"`
		Transport string   `json:"transport"` // "http" | "stdio"
		Target    string   `json:"target"`    // URL or command
		Args      []string `json:"args"`
		Source    string   `json:"source"`
	}
	if !decode(c, &input) {
		return
	}
	name := strings.TrimSpace(input.Name)
	target := strings.TrimSpace(input.Target)
	transport := strings.ToLower(strings.TrimSpace(input.Transport))
	if name == "" || target == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name 与 target 不能为空"})
		return
	}
	if transport != "http" && transport != "stdio" {
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
			transport = "http"
		} else {
			transport = "stdio"
		}
	}
	slug := sanitizeRepoSlug(name)
	if slug == "" {
		slug = "mcp-" + randomID()[:6]
	}
	mcpID := "ext-" + slug
	var ownerID int64
	scope := ""
	if user, ok := h.requestUser(c.Request, true); ok {
		scope = "personal"
		ownerID = user.ID
		mcpID = fmt.Sprintf("ext-u%d-%s", user.ID, slug)
	} else if authRequired() {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录后再导入 MCP"})
		return
	}
	if existing, exists := h.repo.MCPServer(mcpID); exists && existing.Scope == "personal" && existing.OwnerUserID != ownerID {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权覆盖其他用户的个人 MCP"})
		return
	}
	def := domain.MCPServerDefinition{
		ID:          mcpID,
		Name:        name,
		Scope:       scope,
		OwnerUserID: ownerID,
		Description: "自动发现自 " + strings.TrimSpace(input.Source),
		Transport:   transport,
		TimeoutSec:  30,
		UpdatedAt:   time.Now().UTC(),
	}
	if transport == "http" {
		def.URL = target
	} else {
		def.Command = target
		def.Args = input.Args
	}
	if err := h.repo.PutMCPServer(def); err != nil {
		respond(c, err)
		return
	}
	h.configureSync(h.repo.SyncConfig())
	c.JSON(http.StatusOK, gin.H{"ok": true, "server": domain.PublicMCPServer(def)})
}

// POST /api/webuddy/env/set-default-runner
// One-click sets the signed-in user's default execution machine.
func (h *webuddyEnvHandlers) setDefaultRunner(c *gin.Context) {
	user, ok := h.requestUser(c.Request, true)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录后再设置默认执行机器"})
		return
	}
	var input struct {
		RunnerID string `json:"runnerId"`
	}
	if !decode(c, &input) {
		return
	}
	rid := strings.TrimSpace(input.RunnerID)
	if rid != "" {
		r, found := h.repo.Runner(rid)
		if !found {
			c.JSON(http.StatusNotFound, gin.H{"error": "执行机器不存在"})
			return
		}
		if r.OwnerUserID != 0 && !domain.CanUserUseRunner(r, user.ID) {
			c.JSON(http.StatusForbidden, gin.H{"error": "不能将其他用户的执行机器设为默认"})
			return
		}
		if blocker := r.DefaultSelectionBlocker(); blocker != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": blocker})
			return
		}
	}
	if err := h.repo.SetDefaultRunner(user.ID, rid, time.Now().UTC()); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "defaultRunnerId": rid})
}
