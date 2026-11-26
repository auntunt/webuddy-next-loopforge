package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// SharedKnowledgeItem is a cross-project knowledge entry shared across all projects
// inside one tenant (sourced from verified project KB pages, code reviews, or team experience).
type SharedKnowledgeItem struct {
	ID                string   `json:"id"`
	TenantID          string   `json:"tenantId"`
	Title             string   `json:"title"`
	Summary           string   `json:"summary"`
	Content           string   `json:"content"`
	SourceType        string   `json:"sourceType"` // kb_page | code_review | experience | sop_lesson
	SourceProjectID   string   `json:"sourceProjectId,omitempty"`
	SourceProjectName string   `json:"sourceProjectName,omitempty"`
	SourceRelPath     string   `json:"sourceRelPath,omitempty"`
	Commit            string   `json:"commit,omitempty"`
	EvidenceRefs      []string `json:"evidenceRefs,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	Author            string   `json:"author,omitempty"`
	CreatedAt         string   `json:"createdAt"`
	UpdatedAt         string   `json:"updatedAt"`
}

type sharedKBStore struct {
	mu     sync.RWMutex
	loaded bool
	path   string
	items  []SharedKnowledgeItem
}

var defaultSharedKBStore = &sharedKBStore{}

func (s *sharedKBStore) ensureLoaded() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return
	}
	if s.path == "" {
		home, _ := os.UserHomeDir()
		if home == "" {
			home = os.TempDir()
		}
		s.path = filepath.Join(home, ".webuddy-shared-kb.json")
	}
	if raw, err := os.ReadFile(s.path); err == nil {
		var list []SharedKnowledgeItem
		if json.Unmarshal(raw, &list) == nil {
			s.items = list
		}
	}
	s.loaded = true
}

func (s *sharedKBStore) saveLocked() error {
	return writeAtomicJSON(s.path, s.items)
}

func (s *sharedKBStore) list(tenantID, q, sourceType string) []SharedKnowledgeItem {
	s.ensureLoaded()
	tid := strings.TrimSpace(tenantID)
	if tid == "" {
		tid = DefaultTenantID
	}
	needle := strings.ToLower(strings.TrimSpace(q))
	st := strings.ToLower(strings.TrimSpace(sourceType))

	s.mu.RLock()
	out := make([]SharedKnowledgeItem, 0, len(s.items))
	for _, it := range s.items {
		if it.TenantID != tid {
			continue
		}
		if st != "" && strings.ToLower(it.SourceType) != st {
			continue
		}
		if needle != "" {
			hay := strings.ToLower(it.Title + " " + it.Summary + " " + it.Content + " " + it.SourceProjectName + " " + strings.Join(it.Tags, " "))
			if !strings.Contains(hay, needle) {
				continue
			}
		}
		out = append(out, it)
	}
	s.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out
}

func (s *sharedKBStore) upsert(item SharedKnowledgeItem) (SharedKnowledgeItem, error) {
	s.ensureLoaded()
	now := time.Now().UTC().Format(time.RFC3339)
	if item.TenantID == "" {
		item.TenantID = DefaultTenantID
	}
	if item.SourceType == "" {
		item.SourceType = "experience"
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Idempotent update when syncing the same project KB page
	if item.SourceProjectID != "" && item.SourceRelPath != "" {
		for i := range s.items {
			if s.items[i].TenantID == item.TenantID &&
				s.items[i].SourceProjectID == item.SourceProjectID &&
				s.items[i].SourceRelPath == item.SourceRelPath {
				item.ID = s.items[i].ID
				item.CreatedAt = s.items[i].CreatedAt
				item.UpdatedAt = now
				s.items[i] = item
				return item, s.saveLocked()
			}
		}
	}

	if item.ID == "" {
		suffix := randomID()
		if len(suffix) > 8 {
			suffix = suffix[:8]
		}
		item.ID = "skb-" + suffix
	}
	if item.CreatedAt == "" {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	s.items = append(s.items, item)
	return item, s.saveLocked()
}

func (s *sharedKBStore) remove(tenantID, id string) bool {
	s.ensureLoaded()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, it := range s.items {
		if it.TenantID == tenantID && it.ID == id {
			s.items = append(s.items[:i], s.items[i+1:]...)
			_ = s.saveLocked()
			return true
		}
	}
	return false
}

// ---------- HTTP Handlers ----------

// GET /api/webuddy/kb/shared?q=...&sourceType=...
func (h *webuddyEnvHandlers) listSharedKB(c *gin.Context) {
	tid := h.currentActiveTenant(c.Request)
	items := defaultSharedKBStore.list(tid, c.Query("q"), c.Query("sourceType"))
	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"tenantId": tid,
		"items":    items,
	})
}

// POST /api/webuddy/kb/shared
func (h *webuddyEnvHandlers) publishSharedKB(c *gin.Context) {
	tid := h.currentActiveTenant(c.Request)
	actor := "平台成员"
	if user, ok := h.requestUser(c.Request, true); ok && user != nil {
		if authRequired() && !h.isPlatformAdmin(c.Request) {
			if role, in := defaultTenantStore.userTenantRole(tid, user.ID); !in || role == "viewer" {
				c.JSON(http.StatusForbidden, gin.H{"error": "只读访客无法发布团队共享知识"})
				return
			}
		}
		if strings.TrimSpace(user.LoginName) != "" {
			actor = strings.TrimSpace(user.LoginName)
		}
	}
	var req struct {
		Title             string   `json:"title"`
		Summary           string   `json:"summary"`
		Content           string   `json:"content"`
		SourceType        string   `json:"sourceType"`
		SourceProjectID   string   `json:"sourceProjectId"`
		SourceProjectName string   `json:"sourceProjectName"`
		SourceRelPath     string   `json:"sourceRelPath"`
		Commit            string   `json:"commit"`
		EvidenceRefs      []string `json:"evidenceRefs"`
		Tags              []string `json:"tags"`
	}
	if !decode(c, &req) {
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "共享知识标题不能为空"})
		return
	}
	srcPid := strings.TrimSpace(req.SourceProjectID)
	if srcPid != "" && authRequired() {
		if defaultTenantStore.projectTenant(srcPid) != tid || !h.requireProjectWriter(c, srcPid) {
			return
		}
	}
	projName := strings.TrimSpace(req.SourceProjectName)
	if projName == "" && srcPid != "" {
		if p, ok := h.repo.Project(srcPid); ok {
			projName = p.Name
		}
	}
	item, err := defaultSharedKBStore.upsert(SharedKnowledgeItem{
		TenantID:          tid,
		Title:             strings.TrimSpace(req.Title),
		Summary:           strings.TrimSpace(req.Summary),
		Content:           strings.TrimSpace(req.Content),
		SourceType:        strings.TrimSpace(req.SourceType),
		SourceProjectID:   strings.TrimSpace(req.SourceProjectID),
		SourceProjectName: projName,
		SourceRelPath:     strings.TrimSpace(req.SourceRelPath),
		Commit:            strings.TrimSpace(req.Commit),
		EvidenceRefs:      req.EvidenceRefs,
		Tags:              req.Tags,
		Author:            actor,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "item": item})
}

func (d *deps) syncProjectKBFilesToShared(projectID, actor string) int {
	pid, projName, repoPath := d.resolveProjectAndRepoPath(projectID)
	if pid == "" || repoPath == "" {
		return 0
	}
	tid := defaultTenantStore.projectTenant(pid)
	if strings.TrimSpace(actor) == "" {
		actor = "自动同步"
	}
	synced := 0
	for _, folder := range []string{"modules", "flows", "entities", "domains"} {
		dir := filepath.Join(repoPath, ".tmh", "knowledge", folder)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".md") {
				continue
			}
			rawBytes, err := os.ReadFile(filepath.Join(dir, ent.Name()))
			if err != nil {
				continue
			}
			text := string(rawBytes)
			// Skip unanalyzed stubs cleanly (proper parenthesized boolean logic!)
			if strings.Contains(text, "status: pending") || strings.Contains(text, "status: 未分析") || (strings.Contains(text, "未分析") && len(text) < 400) {
				continue
			}
			title, summary, commit, body := parseKBMdFrontmatter(text, strings.TrimSuffix(ent.Name(), ".md"))
			if strings.TrimSpace(body) == "" {
				continue
			}
			if title == "" {
				title = fmt.Sprintf("%s · %s", projName, strings.TrimSuffix(ent.Name(), ".md"))
			}
			relPath := filepath.ToSlash(filepath.Join(".tmh/knowledge", folder, ent.Name()))
			_, err = defaultSharedKBStore.upsert(SharedKnowledgeItem{
				TenantID:          tid,
				Title:             title,
				Summary:           summary,
				Content:           body,
				SourceType:        "kb_page",
				SourceProjectID:   pid,
				SourceProjectName: projName,
				SourceRelPath:     relPath,
				Commit:            commit,
				Tags:              []string{projName, "项目知识库", folder},
				Author:            actor,
			})
			if err == nil {
				synced++
			}
		}
	}
	return synced
}

// POST /api/webuddy/kb/shared/sync-project
// Publishes all analyzed .tmh/knowledge/{modules,flows,entities,domains}/*.md pages from a project into the active tenant's Shared Knowledge Hub.
func (h *webuddyEnvHandlers) syncProjectKBToShared(c *gin.Context) {
	var req struct {
		ProjectID string `json:"projectId"`
	}
	_ = c.ShouldBindJSON(&req)
	rawPid := strings.TrimSpace(req.ProjectID)
	if authRequired() && rawPid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请指定 projectId"})
		return
	}
	pid, _, repoPath := h.resolveProjectAndRepoPath(rawPid)
	if pid == "" || repoPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "当前项目尚未挂载本地知识库目录"})
		return
	}
	if authRequired() && !h.requireProjectWriter(c, pid) {
		return
	}
	tid := defaultTenantStore.projectTenant(pid)
	actor := "自动同步"
	if user, ok := h.requestUser(c.Request, true); ok && user != nil && strings.TrimSpace(user.LoginName) != "" {
		actor = strings.TrimSpace(user.LoginName)
	}
	synced := h.deps.syncProjectKBFilesToShared(pid, actor)
	c.JSON(http.StatusOK, gin.H{
		"ok":        true,
		"projectId": pid,
		"tenantId":  tid,
		"synced":    synced,
	})
}

// DELETE /api/webuddy/kb/shared/:id
func (h *webuddyEnvHandlers) deleteSharedKB(c *gin.Context) {
	tid := h.currentActiveTenant(c.Request)
	if user, ok := h.requestUser(c.Request, true); ok && user != nil && authRequired() && !h.isPlatformAdmin(c.Request) {
		if role, in := defaultTenantStore.userTenantRole(tid, user.ID); !in || role == "viewer" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权删除团队共享知识"})
			return
		}
	}
	if !defaultSharedKBStore.remove(tid, strings.TrimSpace(c.Param("id"))) {
		c.JSON(http.StatusNotFound, gin.H{"error": "共享知识条目不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func parseKBMdFrontmatter(raw, fallbackTitle string) (title, summary, commit, body string) {
	body = raw
	if strings.HasPrefix(raw, "---\n") {
		if end := strings.Index(raw[4:], "\n---"); end >= 0 {
			fm := raw[4 : 4+end]
			body = strings.TrimSpace(raw[4+end+4:])
			for _, line := range strings.Split(fm, "\n") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) != 2 {
					continue
				}
				k := strings.ToLower(strings.TrimSpace(parts[0]))
				v := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
				switch k {
				case "title":
					title = v
				case "summary":
					summary = v
				case "commit":
					commit = v
				}
			}
		}
	}
	if title == "" {
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, "# ") {
				title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
				break
			}
		}
	}
	if title == "" {
		title = fallbackTitle
	}
	if summary == "" && len(body) > 0 {
		s := strings.ReplaceAll(body, "\n", " ")
		if len([]rune(s)) > 120 {
			summary = string([]rune(s)[:120]) + "…"
		} else {
			summary = s
		}
	}
	return title, summary, commit, body
}
