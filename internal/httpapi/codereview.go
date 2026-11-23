package httpapi

import (
	"net/http"
	"os"
	"strconv"

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

// POST /api/codereview/reviews
func (h *codeReviewHandlers) create(c *gin.Context) {
	var input codereview.CreateReviewInput
	if !decode(c, &input) {
		return
	}
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
	actor, isHuman := h.actor(c.Request)
	if c.GetHeader("X-Webuddy-Executor") != "" {
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
	actor, _ := h.actor(c.Request)
	if actor == "" {
		actor = "anonymous"
	}
	rec, err := h.svc.RunAutomatedReview(c.Request.Context(), input, actor, h.diagnoser)
	render(c, rec, err)
}
