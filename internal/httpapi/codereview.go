package httpapi

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"agentflow/internal/codereview"

	"github.com/gin-gonic/gin"
)

// codeReviewHandlers exposes Webuddy's Complete Diff Code Review + Human Feedback
// endpoints over HTTP.
type codeReviewHandlers struct {
	*deps
	svc       *codereview.Service
	diagnoser codereview.BatchDiagnoser
}

func (h *codeReviewHandlers) actor(r *http.Request) (string, bool) {
	if user, ok := h.requestUser(r, true); ok && user != nil && user.LoginName != "" {
		return user.LoginName, true
	}
	if !authRequired() {
		return "local", true
	}
	return "", false
}

// GET /api/codereview/reviews?projectId=...&limit=...
func (h *codeReviewHandlers) list(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	items := h.svc.ListReviews(c.Query("projectId"), limit)
	c.JSON(http.StatusOK, gin.H{"reviews": items})
}

func (h *codeReviewHandlers) resolveAndAuthorizeProject(c *gin.Context, projectID, repoPath, runID string) (string, bool) {
	pid := strings.TrimSpace(projectID)
	rp := strings.TrimRight(strings.TrimSpace(repoPath), "/")
	if rp != "" && h.repo != nil {
		for _, r := range h.repo.Repositories() {
			if strings.TrimRight(strings.TrimSpace(r.LocalPath), "/") == rp {
				if r.ProjectID != "" {
					pid = r.ProjectID
				}
				break
			}
		}
	}
	if rid := strings.TrimSpace(runID); rid != "" && h.repo != nil {
		if run, ok := h.repo.Run(rid); ok {
			rpid := strings.TrimSpace(run.WorkflowSnapshot.ProjectID)
			if rpid != "" {
				if pid != "" && pid != rpid {
					c.JSON(http.StatusBadRequest, gin.H{"error": "所选仓库与关联运行不属于同一项目"})
					return "", false
				}
				pid = rpid
			}
		}
	}
	if authRequired() && pid != "" && !h.requireProjectWriter(c, pid) {
		return "", false
	}
	return pid, true
}

// POST /api/codereview/reviews
func (h *codeReviewHandlers) create(c *gin.Context) {
	var input codereview.CreateReviewInput
	if !decode(c, &input) {
		return
	}
	pid, ok := h.resolveAndAuthorizeProject(c, input.ProjectID, input.RepoPath, input.RunID)
	if !ok {
		return
	}
	input.ProjectID = pid
	actor, _ := h.actor(c.Request)
	if actor == "" {
		actor = "anonymous"
	}
	rec, err := h.svc.CreateReview(c.Request.Context(), input, actor)
	render(c, rec, err)
}

// GET /api/codereview/reviews/:id
func (h *codeReviewHandlers) get(c *gin.Context) {
	detail, err := h.svc.GetReview(c.Param("id"))
	render(c, detail, err)
}

// POST /api/codereview/reviews/:id/feedback
func (h *codeReviewHandlers) saveFeedback(c *gin.Context) {
	var input codereview.FeedbackInput
	if !decode(c, &input) {
		return
	}
	if detail, err := h.svc.GetReview(c.Param("id")); err == nil && detail.Review.ProjectID != "" {
		if authRequired() && !h.requireProjectWriter(c, detail.Review.ProjectID) {
			return
		}
	}
	actor, isHuman := h.actor(c.Request)
	// Reject automated Bearer API tokens or explicit executor headers from spoofing human feedback
	if c.GetHeader("X-Webuddy-Executor") != "" || strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.GetHeader("Authorization"))), "bearer ") {
		isHuman = false
	}
	fb, err := h.svc.SaveFeedback(c.Param("id"), actor, isHuman, input)
	render(c, fb, err)
}

// GET /api/codereview/summary?projectId=...
func (h *codeReviewHandlers) summary(c *gin.Context) {
	c.JSON(http.StatusOK, h.svc.FeedbackSummary(c.Query("projectId")))
}

// GET /api/codereview/executors
func (h *codeReviewHandlers) executors(c *gin.Context) {
	host, _ := os.Hostname()
	c.JSON(http.StatusOK, gin.H{"host": host, "executors": codereview.DetectExecutors()})
}

// POST /api/codereview/reviews/run
func (h *codeReviewHandlers) runAutomated(c *gin.Context) {
	var input codereview.RunAutomatedReviewInput
	if !decode(c, &input) {
		return
	}
	pid, ok := h.resolveAndAuthorizeProject(c, input.ProjectID, input.RepoPath, input.RunID)
	if !ok {
		return
	}
	input.ProjectID = pid
	actor, _ := h.actor(c.Request)
	if actor == "" {
		actor = "anonymous"
	}
	rec, err := h.svc.RunAutomatedReview(c.Request.Context(), input, actor, h.diagnoser)
	render(c, rec, err)
}
