package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"agentflow/internal/domain"

	"github.com/gin-gonic/gin"
)

type localRepoItem struct {
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	RepoID      string `json:"repoId"`
	RepoName    string `json:"repoName"`
	LocalPath   string `json:"localPath"`
	Exists      bool   `json:"exists"`
	HasGraph    bool   `json:"hasGraph"`
	HasKB       bool   `json:"hasKb"`
	IsGit       bool   `json:"isGit"`
}

func inspectLocalRepo(p string) (exists, hasGraph, hasKB, isGit bool) {
	p = strings.TrimSpace(p)
	if p == "" {
		return false, false, false, false
	}
	st, err := os.Stat(p)
	if err != nil || !st.IsDir() {
		return false, false, false, false
	}
	exists = true
	if _, err := os.Stat(filepath.Join(p, ".tmh", "cache", "graph.db")); err == nil {
		hasGraph = true
	} else if _, err := os.Stat(filepath.Join(p, ".tmh", "graph.db")); err == nil {
		hasGraph = true
	}
	if stKB, err := os.Stat(filepath.Join(p, ".tmh", "knowledge")); err == nil && stKB.IsDir() {
		hasKB = true
	}
	if _, err := os.Stat(filepath.Join(p, ".git")); err == nil {
		isGit = true
	}
	return exists, hasGraph, hasKB, isGit
}

// GET /api/webuddy/kb/repos
// Returns all imported projects/repositories with local paths and live disk status.
func (h *webuddyKBHandlers) repos(c *gin.Context) {
	projects := h.repo.Projects()
	projName := make(map[string]string, len(projects))
	for _, p := range projects {
		projName[p.ID] = p.Name
	}
	allRepos := h.repo.Repositories()
	items := make([]localRepoItem, 0, len(allRepos))
	for _, r := range allRepos {
		lp := strings.TrimSpace(r.LocalPath)
		if lp == "" {
			continue
		}
		ex, hg, hkb, ig := inspectLocalRepo(lp)
		pn := projName[r.ProjectID]
		if pn == "" {
			pn = r.ProjectID
		}
		items = append(items, localRepoItem{
			ProjectID:   r.ProjectID,
			ProjectName: pn,
			RepoID:      r.ID,
			RepoName:    r.Name,
			LocalPath:   lp,
			Exists:      ex,
			HasGraph:    hg,
			HasKB:       hkb,
			IsGit:       ig,
		})
	}
	host, _ := os.Hostname()
	c.JSON(http.StatusOK, gin.H{"host": host, "repos": items})
}

// POST /api/webuddy/kb/pick-folder
// Opens the native OS folder chooser (macOS Finder) so users select a local repo
// instead of typing filesystem paths.
func (h *webuddyKBHandlers) pickFolder(c *gin.Context) {
	if runtime.GOOS != "darwin" {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "原生文件夹选择器当前仅支持 macOS"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "osascript", "-e", `POSIX path of (choose folder with prompt "选择要导入 Webuddy 的本地代码仓库目录")`)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, "-128") || strings.Contains(strings.ToLower(msg), "cancel") {
			c.JSON(http.StatusOK, gin.H{"cancelled": true})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "打开文件夹选择器失败: " + msg})
		return
	}
	picked := strings.TrimRight(strings.TrimSpace(stdout.String()), "/")
	if picked == "" {
		c.JSON(http.StatusOK, gin.H{"cancelled": true})
		return
	}
	ex, hg, hkb, ig := inspectLocalRepo(picked)
	c.JSON(http.StatusOK, gin.H{
		"cancelled": false,
		"path":      picked,
		"name":      filepath.Base(picked),
		"exists":    ex,
		"hasGraph":  hg,
		"hasKb":     hkb,
		"isGit":     ig,
	})
}

// POST /api/webuddy/kb/import-local
// Imports a selected local directory as a Project + Local Repository in one click.
func (h *webuddyKBHandlers) importLocal(c *gin.Context) {
	var input struct {
		LocalPath string `json:"localPath"`
		Name      string `json:"name"`
	}
	if !decode(c, &input) {
		return
	}
	lp := strings.TrimRight(strings.TrimSpace(input.LocalPath), "/")
	ex, hg, hkb, ig := inspectLocalRepo(lp)
	if !ex {
		c.JSON(http.StatusBadRequest, gin.H{"error": "所选本地目录不存在: " + lp})
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = filepath.Base(lp)
	}
	slug := sanitizeRepoSlug(name)
	if slug == "" {
		slug = "local-" + randomID()[:6]
	}
	rid := "repo-" + slug

	// Reuse existing project/repo if already imported for this path.
	for _, r := range h.repo.Repositories() {
		if strings.TrimRight(strings.TrimSpace(r.LocalPath), "/") == lp {
			p, _ := h.repo.Project(r.ProjectID)
			c.JSON(http.StatusOK, gin.H{
				"reused": true,
				"repo": localRepoItem{
					ProjectID: r.ProjectID, ProjectName: p.Name, RepoID: r.ID, RepoName: r.Name,
					LocalPath: lp, Exists: ex, HasGraph: hg, HasKB: hkb, IsGit: ig,
				},
			})
			return
		}
	}

	key := strings.ToUpper(slug)
	if len(key) > 4 {
		key = key[:4]
	}
	proj := domain.Project{
		ID:                  slug,
		Key:                 key,
		Name:                name,
		Description:         "本地导入项目：" + lp,
		Color:               "#2563eb",
		DefaultRepositoryID: rid,
	}
	if err := h.repo.PutProject(proj); err != nil {
		respond(c, err)
		return
	}
	defaultBranch := "main"
	if ig {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		out, err := exec.CommandContext(ctx, "git", "-C", lp, "rev-parse", "--abbrev-ref", "HEAD").Output()
		cancel()
		if b := strings.TrimSpace(string(out)); err == nil && b != "" && b != "HEAD" {
			defaultBranch = b
		}
	}
	repoObj := domain.Repository{
		ID:            rid,
		ProjectID:     slug,
		Name:          name,
		DefaultBranch: defaultBranch,
		LocalPath:     lp,
	}
	if err := h.repo.PutRepository(repoObj); err != nil {
		respond(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"reused": false,
		"repo": localRepoItem{
			ProjectID: slug, ProjectName: name, RepoID: rid, RepoName: name,
			LocalPath: lp, Exists: ex, HasGraph: hg, HasKB: hkb, IsGit: ig,
		},
	})
}

func sanitizeRepoSlug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' || r == '.' {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-_")
}

type gitRefItem struct {
	Ref     string `json:"ref"`
	Label   string `json:"label"`
	Kind    string `json:"kind"` // branch | commit
	Subject string `json:"subject,omitempty"`
}

// GET /api/webuddy/kb/git-refs?repoPath=...
// Returns local branches and recent commits for dropdown selection in Code Review.
func (h *webuddyKBHandlers) gitRefs(c *gin.Context) {
	rp := strings.TrimSpace(c.Query("repoPath"))
	ex, _, _, isGit := inspectLocalRepo(rp)
	if !ex || !isGit {
		c.JSON(http.StatusOK, gin.H{"isGit": false, "branches": []gitRefItem{}, "commits": []gitRefItem{}})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()

	var branches []gitRefItem
	if out, err := runGitQuick(ctx, rp, "for-each-ref", "--format=%(refname:short)", "refs/heads"); err == nil {
		for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
			if ln = strings.TrimSpace(ln); ln != "" {
				branches = append(branches, gitRefItem{Ref: ln, Label: "分支 · " + ln, Kind: "branch"})
			}
		}
	}

	var commits []gitRefItem
	if out, err := runGitQuick(ctx, rp, "log", "-n", "15", "--pretty=format:%H%x1f%h%x1f%s"); err == nil {
		for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
			parts := strings.Split(ln, "\x1f")
			if len(parts) >= 3 {
				commits = append(commits, gitRefItem{
					Ref:     parts[0],
					Label:   parts[1] + " · " + parts[2],
					Kind:    "commit",
					Subject: parts[2],
				})
			}
		}
	}
	if branches == nil {
		branches = []gitRefItem{}
	}
	if commits == nil {
		commits = []gitRefItem{}
	}
	c.JSON(http.StatusOK, gin.H{"isGit": true, "branches": branches, "commits": commits})
}

func runGitQuick(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}
