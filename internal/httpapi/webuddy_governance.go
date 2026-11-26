package httpapi

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentflow/internal/domain"
	"agentflow/internal/webuddykb"

	"github.com/gin-gonic/gin"
)

// PlatformPolicy controls whether loose project workflows (no mandatory Git/SOP)
// and direct code archive uploads are permitted across the platform.
type PlatformPolicy struct {
	AllowLooseProjects bool   `json:"allowLooseProjects"`
	AllowArchiveUpload bool   `json:"allowArchiveUpload"`
	DefaultUserRole    string `json:"defaultUserRole"` // member | project_creator | viewer
	UpdatedAt          string `json:"updatedAt,omitempty"`
	UpdatedBy          string `json:"updatedBy,omitempty"`
}

// UserRBACRecord stores extended per-user governance attributes managed by platform admins.
type UserRBACRecord struct {
	Role      string `json:"role"` // admin | project_creator | member | viewer
	Disabled  bool   `json:"disabled"`
	Note      string `json:"note,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	UpdatedBy string `json:"updatedBy,omitempty"`
}

type governanceStore struct {
	mu       sync.RWMutex
	policy   PlatformPolicy
	users    map[int64]UserRBACRecord
	loaded   bool
	baseDir  string
}

var defaultGovStore = &governanceStore{}

func (g *governanceStore) ensureLoaded() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.loaded {
		return
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		home = os.TempDir()
	}
	g.baseDir = home
	g.policy = PlatformPolicy{
		AllowLooseProjects: true,
		AllowArchiveUpload: true,
		DefaultUserRole:    "member",
	}
	g.users = make(map[int64]UserRBACRecord)

	if raw, err := os.ReadFile(filepath.Join(g.baseDir, ".webuddy-platform-policy.json")); err == nil {
		var p PlatformPolicy
		if json.Unmarshal(raw, &p) == nil {
			if p.DefaultUserRole == "" {
				p.DefaultUserRole = "member"
			}
			g.policy = p
		}
	}
	if raw, err := os.ReadFile(filepath.Join(g.baseDir, ".webuddy-user-rbac.json")); err == nil {
		var m map[int64]UserRBACRecord
		if json.Unmarshal(raw, &m) == nil && m != nil {
			g.users = m
		}
	}
	g.loaded = true
}

func (g *governanceStore) getPolicy() PlatformPolicy {
	g.ensureLoaded()
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.policy
}

func (g *governanceStore) savePolicy(p PlatformPolicy) error {
	g.ensureLoaded()
	if p.DefaultUserRole != "member" && p.DefaultUserRole != "project_creator" && p.DefaultUserRole != "viewer" {
		p.DefaultUserRole = "member"
	}
	g.mu.Lock()
	g.policy = p
	baseDir := g.baseDir
	g.mu.Unlock()
	return writeAtomicJSON(filepath.Join(baseDir, ".webuddy-platform-policy.json"), p)
}

func (g *governanceStore) getUserRecord(userID int64) (UserRBACRecord, bool) {
	g.ensureLoaded()
	g.mu.RLock()
	defer g.mu.RUnlock()
	rec, ok := g.users[userID]
	return rec, ok
}

func (g *governanceStore) setUserRecord(userID int64, rec UserRBACRecord) error {
	g.ensureLoaded()
	g.mu.Lock()
	g.users[userID] = rec
	snapshot := make(map[int64]UserRBACRecord, len(g.users))
	for k, v := range g.users {
		snapshot[k] = v
	}
	baseDir := g.baseDir
	g.mu.Unlock()
	return writeAtomicJSON(filepath.Join(baseDir, ".webuddy-user-rbac.json"), snapshot)
}

func writeAtomicJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (h *usersHandlers) platformPolicy(c *gin.Context) {
	if c.Request.Method == http.MethodGet {
		c.JSON(http.StatusOK, gin.H{
			"policy":        defaultGovStore.getPolicy(),
			"signupEnabled": signupEnabled(),
		})
		return
	}
	if !h.platformAdminWrite(c) {
		return
	}
	var input struct {
		AllowLooseProjects *bool  `json:"allowLooseProjects"`
		AllowArchiveUpload *bool  `json:"allowArchiveUpload"`
		DefaultUserRole    string `json:"defaultUserRole"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的策略配置"})
		return
	}
	cur := defaultGovStore.getPolicy()
	if input.AllowLooseProjects != nil {
		cur.AllowLooseProjects = *input.AllowLooseProjects
	}
	if input.AllowArchiveUpload != nil {
		cur.AllowArchiveUpload = *input.AllowArchiveUpload
	}
	if strings.TrimSpace(input.DefaultUserRole) != "" {
		cur.DefaultUserRole = strings.TrimSpace(input.DefaultUserRole)
	}
	if u, ok := h.requestUser(c.Request, true); ok {
		cur.UpdatedBy = u.LoginName
	}
	cur.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := defaultGovStore.savePolicy(cur); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "policy": cur})
}

type rbacProjectEntry struct {
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	Role        string `json:"role"` // owner | maintainer | member | viewer
}

type rbacUserSummary struct {
	UserID       int64              `json:"userId"`
	LoginName    string             `json:"loginName"`
	Admin        bool               `json:"admin"`
	Password     bool               `json:"password"`
	Role         string             `json:"role"` // admin | project_creator | member | viewer
	Disabled     bool               `json:"disabled"`
	Note         string             `json:"note,omitempty"`
	Projects     []rbacProjectEntry `json:"projects"`
	CreatedAt    *time.Time         `json:"createdAt,omitempty"`
}

func (h *usersHandlers) platformRBACUsers(c *gin.Context) {
	if !h.isPlatformAdmin(c.Request) {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有平台管理员可以管理用户与权限"})
		return
	}
	accounts, err := h.repo.ListPlatformAccounts()
	if err != nil {
		respond(c, err)
		return
	}
	projects := h.repo.Projects()
	pol := defaultGovStore.getPolicy()

	out := make([]rbacUserSummary, 0, len(accounts))
	for _, acc := range accounts {
		rec, hasRec := defaultGovStore.getUserRecord(acc.UserID)
		role := pol.DefaultUserRole
		if hasRec && rec.Role != "" {
			role = rec.Role
		}
		if acc.Admin {
			role = "admin"
		}
		var memberships []rbacProjectEntry
		for _, p := range projects {
			if p.OwnerID == acc.UserID {
				memberships = append(memberships, rbacProjectEntry{
					ProjectID:   p.ID,
					ProjectName: p.Name,
					Role:        "owner",
				})
				continue
			}
			for _, m := range p.Members {
				if m.UserID == acc.UserID {
					r := strings.TrimSpace(m.Role)
					if r == "" {
						r = "member"
					}
					memberships = append(memberships, rbacProjectEntry{
						ProjectID:   p.ID,
						ProjectName: p.Name,
						Role:        r,
					})
					break
				}
			}
		}
		if memberships == nil {
			memberships = []rbacProjectEntry{}
		}
		out = append(out, rbacUserSummary{
			UserID:    acc.UserID,
			LoginName: acc.LoginName,
			Admin:     acc.Admin,
			Password:  acc.Password,
			Role:      role,
			Disabled:  rec.Disabled && !acc.Admin,
			Note:      rec.Note,
			Projects:  memberships,
			CreatedAt: acc.CreatedAt,
		})
	}

	projCatalog := make([]map[string]string, 0, len(projects))
	for _, p := range projects {
		projCatalog = append(projCatalog, map[string]string{
			"id":   p.ID,
			"name": p.Name,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"users":         out,
		"projects":      projCatalog,
		"policy":        pol,
		"signupEnabled": signupEnabled(),
	})
}

func (h *usersHandlers) updatePlatformRBACUser(c *gin.Context) {
	if !h.platformAdminWrite(c) {
		return
	}
	targetID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || targetID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的用户 ID"})
		return
	}
	var input struct {
		Role     string             `json:"role"`
		Disabled *bool              `json:"disabled"`
		Note     *string            `json:"note"`
		Projects []rbacProjectEntry `json:"projects"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的权限更新请求"})
		return
	}
	actor, _ := h.requestUser(c.Request, true)
	acc := h.platformAccount(targetID)
	if acc.LoginName == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "账号不存在"})
		return
	}
	if actor != nil && actor.ID == targetID && input.Disabled != nil && *input.Disabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不能停用当前登录的管理员账号"})
		return
	}

	rec, _ := defaultGovStore.getUserRecord(targetID)
	if input.Role != "" {
		switch input.Role {
		case "admin", "project_creator", "member", "viewer":
			rec.Role = input.Role
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "不支持的角色类型: " + input.Role})
			return
		}
		// Sync platform admin list when promoting/demoting admin
		if input.Role == "admin" && !acc.Admin {
			_ = h.addPlatformAdminLogin(acc.LoginName)
		} else if input.Role != "admin" && acc.Admin {
			if actor != nil && actor.ID == targetID {
				c.JSON(http.StatusBadRequest, gin.H{"error": "不能取消自己的平台管理员身份"})
				return
			}
			_ = h.removePlatformAdminLogin(acc.LoginName)
		}
	}
	if input.Disabled != nil {
		rec.Disabled = *input.Disabled
	}
	if input.Note != nil {
		rec.Note = strings.TrimSpace(*input.Note)
	}
	if actor != nil {
		rec.UpdatedBy = actor.LoginName
	}
	rec.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := defaultGovStore.setUserRecord(targetID, rec); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Sync project memberships if provided
	if input.Projects != nil {
		desired := make(map[string]string, len(input.Projects))
		for _, item := range input.Projects {
			r := strings.TrimSpace(item.Role)
			if r == "maintainer" || r == "member" || r == "viewer" || r == "owner" {
				desired[strings.TrimSpace(item.ProjectID)] = r
			}
		}
		for _, proj := range h.repo.Projects() {
			if proj.OwnerID == targetID {
				// Keep owner intact
				continue
			}
			wantRole, want := desired[proj.ID]
			if wantRole == "owner" {
				wantRole = "maintainer"
			}
			changed := false
			nextMembers := make([]domain.ProjectMember, 0, len(proj.Members)+1)
			found := false
			for _, m := range proj.Members {
				if m.UserID == targetID {
					found = true
					if !want {
						changed = true
						continue
					}
					if m.Role != wantRole {
						m.Role = wantRole
						changed = true
					}
				}
				nextMembers = append(nextMembers, m)
			}
			if want && !found {
				nextMembers = append(nextMembers, domain.ProjectMember{UserID: targetID, Role: wantRole})
				changed = true
			}
			if changed {
				proj.Members = nextMembers
				proj.UpdatedAt = time.Now().UTC()
				_ = h.repo.PutProject(proj)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "userId": targetID, "record": rec})
}

func (h *usersHandlers) addPlatformAdminLogin(login string) error {
	_, err := h.repo.GrantPlatformAdmin(login)
	return err
}

func (h *usersHandlers) removePlatformAdminLogin(login string) error {
	return h.repo.RevokePlatformAdmin(login)
}

// ---------- Direct Code Archive (.zip / .tar.gz) Upload & Auto-Setup ----------

const (
	maxArchiveUploadBytes     = 100 << 20 // 100 MB upload limit
	maxArchiveExtractBytes    = 300 << 20 // 300 MB uncompressed guard
	maxArchiveExtractFiles    = 12000
)

// resolveUploadedReposDir returns the persistent host-backed root directory for
// uploaded code archives. It honors WEBUDDY_UPLOADED_REPOS_DIR first, then
// $HOME/.webuddy-uploaded-repos, and intentionally avoids os.Getwd() so
// containerized runs with WORKDIR=/app never write into the ephemeral image layer.
func resolveUploadedReposDir() string {
	if envDir := strings.TrimSpace(os.Getenv("WEBUDDY_UPLOADED_REPOS_DIR")); envDir != "" {
		return envDir
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".webuddy-uploaded-repos")
	}
	return filepath.Join(os.TempDir(), "webuddy-uploaded-repos")
}

func (h *webuddyEnvHandlers) uploadProjectArchive(c *gin.Context) {
	pol := defaultGovStore.getPolicy()
	if !pol.AllowArchiveUpload {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "平台管理员已关闭「压缩包直接导入」功能，请通过标准 Git 仓库接入或联系管理员开启松散项目管理。",
		})
		return
	}

	var userID int64
	if u, ok := h.requestUser(c.Request, true); ok {
		userID = u.ID
		if rec, ok := defaultGovStore.getUserRecord(u.ID); ok && rec.Disabled && !h.isPlatformAdmin(c.Request) {
			c.JSON(http.StatusForbidden, gin.H{"error": "该账号已被管理员停用"})
			return
		}
	}

	pid := strings.TrimSpace(c.Param("id"))
	if pid == "" {
		pid = strings.TrimSpace(c.Query("projectId"))
	}
	if pid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请指定目标项目 ID"})
		return
	}
	proj, ok := h.repo.Project(pid)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "项目不存在: " + pid})
		return
	}
	if authRequired() && !h.requireProjectWriter(c, pid) {
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxArchiveUploadBytes)
	fh, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传 .zip 或 .tar.gz 代码压缩包: " + err.Error()})
		return
	}
	fname := strings.ToLower(strings.TrimSpace(fh.Filename))
	isZip := strings.HasSuffix(fname, ".zip")
	isTarGz := strings.HasSuffix(fname, ".tar.gz") || strings.HasSuffix(fname, ".tgz")
	if !isZip && !isTarGz {
		c.JSON(http.StatusBadRequest, gin.H{"error": "仅支持 .zip、.tar.gz 或 .tgz 格式的代码压缩包"})
		return
	}

	tmpFile, err := os.CreateTemp("", "webuddy-archive-*")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	src, err := fh.Open()
	if err != nil {
		tmpFile.Close()
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if _, err := io.Copy(tmpFile, src); err != nil {
		src.Close()
		tmpFile.Close()
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取上传文件失败: " + err.Error()})
		return
	}
	src.Close()
	tmpFile.Close()

	// Destination directory OUTSIDE workspaces/ so Webuddy's workspace retention cleaner never sweeps it
	// and never falls back to os.Getwd() (which would write into /app inside a container).
	safeSlug := sanitizeProjectSlug(pid)
	baseDir := resolveUploadedReposDir()
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	destDir := filepath.Join(baseDir, safeSlug)
	stageDir := filepath.Join(baseDir, ".stage-"+safeSlug+"-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer func() {
		if stageDir != "" {
			_ = os.RemoveAll(stageDir)
		}
	}()

	var fileCount int
	if isZip {
		fileCount, err = extractZipSafe(tmpPath, stageDir)
	} else {
		fileCount, err = extractTarGzSafe(tmpPath, stageDir)
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "解压失败: " + err.Error()})
		return
	}
	if fileCount == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "压缩包内没有有效代码文件"})
		return
	}

	// Unwrap single top-level directory (common when zipping a folder)
	contentRoot := unwrapSingleRootDir(stageDir)
	// Preserve existing .tmh knowledge base (confirmed pages & Q&A) across archive re-uploads!
	tmhBackup := filepath.Join(baseDir, ".tmh-bak-"+safeSlug+"-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	hadTMH := false
	if st, statErr := os.Stat(filepath.Join(destDir, ".tmh")); statErr == nil && st.IsDir() {
		if os.Rename(filepath.Join(destDir, ".tmh"), tmhBackup) == nil {
			hadTMH = true
		}
	}
	defer func() {
		if hadTMH {
			_ = os.RemoveAll(tmhBackup)
		}
	}()
	if err := os.RemoveAll(destDir); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := os.Rename(contentRoot, destDir); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "落盘代码目录失败: " + err.Error()})
		return
	}
	if hadTMH {
		_ = os.RemoveAll(filepath.Join(destDir, ".tmh"))
		_ = os.Rename(tmhBackup, filepath.Join(destDir, ".tmh"))
	}

	// Initialize local git repo if not already one so tmh/codereview/HEAD provenance works seamlessly
	gitHead := ensureLocalGitRepo(destDir)

	// Register or update repository in DB
	repoID := "repo-archive-" + safeSlug
	for _, r := range h.repo.Repositories() {
		if r.ProjectID == pid && (r.ID == repoID || r.Provider == "archive") {
			repoID = r.ID
			break
		}
	}
	now := time.Now().UTC()
	repoObj := domain.Repository{
		ID:            repoID,
		ProjectID:     pid,
		Provider:      "archive",
		Name:          strings.TrimSuffix(filepath.Base(fh.Filename), filepath.Ext(fh.Filename)),
		DefaultBranch: "main",
		LocalPath:     destDir,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := h.repo.PutRepository(repoObj); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "注册项目代码仓失败: " + err.Error()})
		return
	}
	if strings.TrimSpace(proj.DefaultRepositoryID) == "" {
		proj.DefaultRepositoryID = repoID
		proj.UpdatedAt = now
		_ = h.repo.PutProject(proj)
	}

	// Auto-configure lightweight native sandbox so chat workspace works immediately
	readiness, actions, _ := h.autoConfigureProjectEnv(userID, pid)

	// Trigger initial code graph & KB scan
	kbBuilt := false
	flowCount := 0
	tableCount := 0
	svc := webuddykb.NewService()
	if _, scanErr := svc.Scan(c.Request.Context(), destDir); scanErr == nil {
		kbBuilt = true
		if ovRaw, ovErr := svc.Overview(c.Request.Context(), destDir); ovErr == nil {
			var ov struct {
				Flows  []any `json:"flows"`
				Tables []any `json:"tables"`
			}
			if json.Unmarshal(ovRaw, &ov) == nil {
				flowCount = len(ov.Flows)
				tableCount = len(ov.Tables)
			}
		}
		// Automatically kick off background AI synthesis of unanalyzed/stale KB pages!
		actor := "archive-auto-kb"
		if u, ok := h.requestUser(c.Request, true); ok && strings.TrimSpace(u.LoginName) != "" {
			actor = strings.TrimSpace(u.LoginName)
		}
		_ = h.deps.startAsyncKBBatchUpdate(actor, pid, 3, "", true)
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"projectId":   pid,
		"projectName": proj.Name,
		"repoId":      repoID,
		"archiveName": fh.Filename,
		"localPath":   destDir,
		"gitHead":     gitHead,
		"fileCount":   fileCount,
		"kbBuilt":     kbBuilt,
		"flowCount":   flowCount,
		"tableCount":  tableCount,
		"sandboxId":   readiness.Sandbox.ID,
		"actions":     actions,
		"verifiedAt":  time.Now().Format(time.RFC3339),
	})
}

func sanitizeProjectSlug(s string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			sb.WriteRune(r)
		} else {
			sb.WriteByte('-')
		}
	}
	out := strings.Trim(sb.String(), "-_")
	if out == "" {
		return "project"
	}
	return out
}

func safeArchiveJoin(root, name string) (string, bool) {
	clean := filepath.Clean(strings.ReplaceAll(name, "\\", "/"))
	if clean == "." || clean == "" || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "..") {
		return "", false
	}
	parts := strings.Split(clean, string(filepath.Separator))
	for _, p := range parts {
		if p == ".." || p == "__MACOSX" {
			return "", false
		}
	}
	target := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return target, true
}

func extractZipSafe(zipPath, destDir string) (int, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	var totalBytes int64
	count := 0
	for _, f := range r.File {
		if f.FileInfo().IsDir() || f.Mode()&os.ModeSymlink != 0 {
			continue
		}
		target, ok := safeArchiveJoin(destDir, f.Name)
		if !ok {
			continue
		}
		count++
		if count > maxArchiveExtractFiles {
			return count, fmt.Errorf("压缩包文件数量超过上限 (%d)", maxArchiveExtractFiles)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return count, err
		}
		rc, err := f.Open()
		if err != nil {
			return count, err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			rc.Close()
			return count, err
		}
		n, copyErr := io.Copy(out, io.LimitReader(rc, maxArchiveExtractBytes-totalBytes+1))
		rc.Close()
		out.Close()
		if copyErr != nil {
			return count, copyErr
		}
		totalBytes += n
		if totalBytes > maxArchiveExtractBytes {
			return count, fmt.Errorf("解压体积超过上限 (%d MB)", maxArchiveExtractBytes>>20)
		}
	}
	return count, nil
}

func extractTarGzSafe(tgzPath, destDir string) (int, error) {
	f, err := os.Open(tgzPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return 0, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var totalBytes int64
	count := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		target, ok := safeArchiveJoin(destDir, hdr.Name)
		if !ok {
			continue
		}
		count++
		if count > maxArchiveExtractFiles {
			return count, fmt.Errorf("压缩包文件数量超过上限 (%d)", maxArchiveExtractFiles)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return count, err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return count, err
		}
		n, copyErr := io.Copy(out, io.LimitReader(tr, maxArchiveExtractBytes-totalBytes+1))
		out.Close()
		if copyErr != nil {
			return count, copyErr
		}
		totalBytes += n
		if totalBytes > maxArchiveExtractBytes {
			return count, fmt.Errorf("解压体积超过上限 (%d MB)", maxArchiveExtractBytes>>20)
		}
	}
	return count, nil
}

func unwrapSingleRootDir(stageDir string) string {
	entries, err := os.ReadDir(stageDir)
	if err != nil {
		return stageDir
	}
	var dirs []os.DirEntry
	files := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || e.Name() == "__MACOSX" {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, e)
		} else {
			files++
		}
	}
	if len(dirs) == 1 && files == 0 {
		return filepath.Join(stageDir, dirs[0].Name())
	}
	return stageDir
}

func ensureLocalGitRepo(dir string) string {
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Webuddy",
			"GIT_AUTHOR_EMAIL=webuddy@local",
			"GIT_COMMITTER_NAME=Webuddy",
			"GIT_COMMITTER_EMAIL=webuddy@local",
		)
		out, _ := cmd.CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		run("init", "-b", "main")
		run("add", "-A")
		run("commit", "-m", "init: imported from code archive")
	}
	head := run("rev-parse", "--short=10", "HEAD")
	if head == "" {
		run("add", "-A")
		run("commit", "-m", "snapshot: imported from code archive")
		head = run("rev-parse", "--short=10", "HEAD")
	}
	return head
}
