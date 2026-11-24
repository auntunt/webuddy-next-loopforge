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

// POST /api/webuddy/kb/impact
func (h *webuddyKBHandlers) impact(c *gin.Context) {
	var input struct {
		RepoPath string `json:"repoPath"`
		Target   string `json:"target"`
		Depth    int    `json:"depth"`
	}
	if !decode(c, &input) {
		return
	}
	raw, err := h.svc.Impact(c.Request.Context(), input.RepoPath, input.Target, input.Depth)
	renderRawJSON(c, raw, err)
}

// POST /api/webuddy/kb/chain
func (h *webuddyKBHandlers) chain(c *gin.Context) {
	var input struct {
		RepoPath string `json:"repoPath"`
		Query    string `json:"query"`
		Flows    int    `json:"flows"`
	}
	if !decode(c, &input) {
		return
	}
	raw, err := h.svc.Chain(c.Request.Context(), input.RepoPath, input.Query, input.Flows)
	renderRawJSON(c, raw, err)
}

// GET /api/webuddy/kb/questions?repoPath=...
func (h *webuddyKBHandlers) questions(c *gin.Context) {
	raw, err := h.svc.Questions(c.Request.Context(), c.Query("repoPath"))
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
	who := "user"
	if u, ok := h.requestUser(c.Request, true); ok && u.LoginName != "" {
		who = u.LoginName
	}
	raw, err := h.svc.AskQuestion(c.Request.Context(), input.RepoPath, input.Question, input.Pages, who)
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
	by := "user"
	if u, ok := h.requestUser(c.Request, true); ok && u.LoginName != "" {
		by = u.LoginName
	}
	raw, err := h.svc.AnswerQuestion(c.Request.Context(), input.RepoPath, input.Num, input.Text, by)
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
	by := "user"
	if u, ok := h.requestUser(c.Request, true); ok && u.LoginName != "" {
		by = u.LoginName
	}
	raw, err := h.svc.ConfirmPage(c.Request.Context(), input.RepoPath, input.PageID, by)
	renderRawJSON(c, raw, err)
}
