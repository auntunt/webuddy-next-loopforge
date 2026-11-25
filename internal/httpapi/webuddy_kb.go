package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"agentflow/internal/webuddykb"

	"github.com/gin-gonic/gin"
)

// webuddyKBHandlers exposes Webuddy's built-in Code Graph & Knowledge Base engine
// (graph.db, .tmh/knowledge, kb/*.md, evidence verification) over HTTP.
type webuddyKBHandlers struct {
	*deps
	svc *webuddykb.Service
}

func renderRawJSON(c *gin.Context, raw []byte, err error) {
	if err != nil {
		respond(c, err)
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
}

// resolveAuthorizedKBRepo validates that repoPath belongs to a registered platform
// repository (preventing arbitrary filesystem access when auth is enabled) and
// enforces project write permission for mutating KB operations.
func (h *webuddyKBHandlers) resolveAuthorizedKBRepo(c *gin.Context, rawRepoPath string, requireWrite bool) (string, bool) {
	rp := strings.TrimRight(strings.TrimSpace(rawRepoPath), "/")
	if h.repo == nil {
		return rp, true
	}
	repos := h.repo.Repositories()
	if len(repos) == 0 {
		if authRequired() && rp != "" {
			c.JSON(http.StatusForbidden, gin.H{"error": "仓库路径未在平台注册，禁止访问未授权目录"})
			return "", false
		}
		return rp, true
	}
	if rp == "" {
		for _, r := range repos {
			if lp := strings.TrimRight(strings.TrimSpace(r.LocalPath), "/"); lp != "" {
				if requireWrite && authRequired() && r.ProjectID != "" && !h.requireProjectWriter(c, r.ProjectID) {
					return "", false
				}
				return lp, true
			}
		}
		return "", true
	}
	for _, r := range repos {
		lp := strings.TrimRight(strings.TrimSpace(r.LocalPath), "/")
		if (lp != "" && lp == rp) || r.ID == rp {
			if requireWrite && authRequired() && r.ProjectID != "" && !h.requireProjectWriter(c, r.ProjectID) {
				return "", false
			}
			return lp, true
		}
	}
	if authRequired() {
		c.JSON(http.StatusForbidden, gin.H{"error": "仓库路径未在平台注册，禁止访问未授权目录"})
		return "", false
	}
	return rp, true
}

// GET /api/webuddy/kb/overview?repoPath=...
func (h *webuddyKBHandlers) overview(c *gin.Context) {
	rp, ok := h.resolveAuthorizedKBRepo(c, c.Query("repoPath"), false)
	if !ok {
		return
	}
	raw, err := h.svc.Overview(c.Request.Context(), rp)
	renderRawJSON(c, raw, err)
}

// POST /api/webuddy/kb/scan
func (h *webuddyKBHandlers) scan(c *gin.Context) {
	var input struct {
		RepoPath string `json:"repoPath"`
	}
	if !decode(c, &input) {
		return
	}
	rp, ok := h.resolveAuthorizedKBRepo(c, input.RepoPath, true)
	if !ok {
		return
	}
	raw, err := h.svc.Scan(c.Request.Context(), rp)
	renderRawJSON(c, raw, err)
}

// POST /api/webuddy/kb/search
func (h *webuddyKBHandlers) search(c *gin.Context) {
	var input struct {
		RepoPath string `json:"repoPath"`
		Query    string `json:"query"`
		Limit    int    `json:"limit"`
	}
	if !decode(c, &input) {
		return
	}
	rp, ok := h.resolveAuthorizedKBRepo(c, input.RepoPath, false)
	if !ok {
		return
	}
	raw, err := h.svc.Search(c.Request.Context(), rp, input.Query, input.Limit)
	renderRawJSON(c, raw, err)
}

// GET /api/webuddy/kb/file?repoPath=...&path=...&start=...&end=...
func (h *webuddyKBHandlers) file(c *gin.Context) {
	rp, ok := h.resolveAuthorizedKBRepo(c, c.Query("repoPath"), false)
	if !ok {
		return
	}
	start, _ := strconv.Atoi(c.Query("start"))
	end, _ := strconv.Atoi(c.Query("end"))
	raw, err := h.svc.ReadFile(c.Request.Context(), rp, c.Query("path"), start, end)
	renderRawJSON(c, raw, err)
}

// POST /api/webuddy/kb/verify-evidence
func (h *webuddyKBHandlers) verifyEvidence(c *gin.Context) {
	var input struct {
		RepoPath string `json:"repoPath"`
		Ref      string `json:"ref"`
	}
	if !decode(c, &input) {
		return
	}
	rp, ok := h.resolveAuthorizedKBRepo(c, input.RepoPath, false)
	if !ok {
		return
	}
	raw, err := h.svc.VerifyEvidence(c.Request.Context(), rp, input.Ref)
	renderRawJSON(c, raw, err)
}

// GET /api/webuddy/kb/statecharts?repoPath=...
func (h *webuddyKBHandlers) statecharts(c *gin.Context) {
	rp, ok := h.resolveAuthorizedKBRepo(c, c.Query("repoPath"), false)
	if !ok {
		return
	}
	raw, err := h.svc.Statecharts(c.Request.Context(), rp)
	renderRawJSON(c, raw, err)
}

// POST /api/webuddy/kb/impact
func (h *webuddyKBHandlers) impact(c *gin.Context) {
	var input struct {
		RepoPath string `json:"repoPath"`
		Target   string `json:"target"`
		Depth    int    `json:"depth"`
		NoAI     bool   `json:"noAi"`
	}
	if !decode(c, &input) {
		return
	}
	rp, ok := h.resolveAuthorizedKBRepo(c, input.RepoPath, false)
	if !ok {
		return
	}
	raw, err := h.svc.Impact(c.Request.Context(), rp, input.Target, input.Depth)
	if err != nil {
		respond(c, err)
		return
	}
	if input.NoAI {
		renderRawJSON(c, raw, nil)
		return
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		renderRawJSON(c, raw, nil)
		return
	}
	compact, _ := json.Marshal(map[string]any{
		"affectedDomains": payload["affectedDomains"],
		"affectedFlows":   payload["affectedFlows"],
		"affectedTables":  payload["affectedTables"],
		"codeImpact":      payload["codeImpact"],
	})
	qa := synthesizeKBAnswer(c.Request.Context(), "impact", input.Target, string(compact))
	if qa.Answer != "" {
		payload["aiSummary"] = qa.Answer
		payload["aiModel"] = qa.Model
		payload["aiLatencyMs"] = qa.LatencyMs
	} else if qa.Error != "" {
		payload["aiError"] = qa.Error
	}
	c.JSON(http.StatusOK, payload)
}

// POST /api/webuddy/kb/chain
func (h *webuddyKBHandlers) chain(c *gin.Context) {
	var input struct {
		RepoPath string `json:"repoPath"`
		Query    string `json:"query"`
		Flows    int    `json:"flows"`
		NoAI     bool   `json:"noAi"`
	}
	if !decode(c, &input) {
		return
	}
	rp, ok := h.resolveAuthorizedKBRepo(c, input.RepoPath, false)
	if !ok {
		return
	}
	raw, err := h.svc.Chain(c.Request.Context(), rp, input.Query, input.Flows)
	if err != nil {
		respond(c, err)
		return
	}
	if input.NoAI {
		renderRawJSON(c, raw, nil)
		return
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		renderRawJSON(c, raw, nil)
		return
	}
	md, _ := payload["markdown"].(string)
	if strings.TrimSpace(md) != "" {
		qa := synthesizeKBAnswer(c.Request.Context(), "chain", input.Query, fmt.Sprintf("%s", md))
		if qa.Answer != "" {
			payload["aiAnswer"] = qa.Answer
			payload["aiCitations"] = qa.Citations
			payload["aiModel"] = qa.Model
			payload["aiLatencyMs"] = qa.LatencyMs
		} else if qa.Error != "" {
			payload["aiError"] = qa.Error
		}
	}
	c.JSON(http.StatusOK, payload)
}

// GET /api/webuddy/kb/questions?repoPath=...
func (h *webuddyKBHandlers) questions(c *gin.Context) {
	rp, ok := h.resolveAuthorizedKBRepo(c, c.Query("repoPath"), false)
	if !ok {
		return
	}
	raw, err := h.svc.Questions(c.Request.Context(), rp)
	renderRawJSON(c, raw, err)
}

// POST /api/webuddy/kb/questions/ask
func (h *webuddyKBHandlers) askQuestion(c *gin.Context) {
	var input struct {
		RepoPath string `json:"repoPath"`
		Question string `json:"question"`
		Pages    string `json:"pages"`
	}
	if !decode(c, &input) {
		return
	}
	rp, ok := h.resolveAuthorizedKBRepo(c, input.RepoPath, true)
	if !ok {
		return
	}
	who := "user"
	if u, ok := h.requestUser(c.Request, true); ok && u.LoginName != "" {
		who = u.LoginName
	}
	raw, err := h.svc.AskQuestion(c.Request.Context(), rp, input.Question, input.Pages, who)
	renderRawJSON(c, raw, err)
}

// POST /api/webuddy/kb/questions/answer
func (h *webuddyKBHandlers) answerQuestion(c *gin.Context) {
	var input struct {
		RepoPath string `json:"repoPath"`
		Num      int    `json:"num"`
		Text     string `json:"text"`
	}
	if !decode(c, &input) {
		return
	}
	rp, ok := h.resolveAuthorizedKBRepo(c, input.RepoPath, true)
	if !ok {
		return
	}
	by := "user"
	if u, ok := h.requestUser(c.Request, true); ok && u.LoginName != "" {
		by = u.LoginName
	}
	raw, err := h.svc.AnswerQuestion(c.Request.Context(), rp, input.Num, input.Text, by)
	renderRawJSON(c, raw, err)
}

// POST /api/webuddy/kb/confirm-page
func (h *webuddyKBHandlers) confirmPage(c *gin.Context) {
	var input struct {
		RepoPath string `json:"repoPath"`
		PageID   string `json:"pageId"`
	}
	if !decode(c, &input) {
		return
	}
	rp, ok := h.resolveAuthorizedKBRepo(c, input.RepoPath, true)
	if !ok {
		return
	}
	by := "user"
	if u, ok := h.requestUser(c.Request, true); ok && u.LoginName != "" {
		by = u.LoginName
	}
	raw, err := h.svc.ConfirmPage(c.Request.Context(), rp, input.PageID, by)
	renderRawJSON(c, raw, err)
}
