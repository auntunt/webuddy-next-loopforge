package httpapi

import (
	"net/http"
	"strconv"

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

// GET /api/webuddy/kb/overview?repoPath=...
func (h *webuddyKBHandlers) overview(c *gin.Context) {
	raw, err := h.svc.Overview(c.Request.Context(), c.Query("repoPath"))
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
	raw, err := h.svc.Scan(c.Request.Context(), input.RepoPath)
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
	raw, err := h.svc.Search(c.Request.Context(), input.RepoPath, input.Query, input.Limit)
	renderRawJSON(c, raw, err)
}

// GET /api/webuddy/kb/file?repoPath=...&path=...&start=...&end=...
func (h *webuddyKBHandlers) file(c *gin.Context) {
	start, _ := strconv.Atoi(c.Query("start"))
	end, _ := strconv.Atoi(c.Query("end"))
	raw, err := h.svc.ReadFile(c.Request.Context(), c.Query("repoPath"), c.Query("path"), start, end)
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
	raw, err := h.svc.VerifyEvidence(c.Request.Context(), input.RepoPath, input.Ref)
	renderRawJSON(c, raw, err)
}

// GET /api/webuddy/kb/statecharts?repoPath=...
func (h *webuddyKBHandlers) statecharts(c *gin.Context) {
	raw, err := h.svc.Statecharts(c.Request.Context(), c.Query("repoPath"))
	renderRawJSON(c, raw, err)
}
