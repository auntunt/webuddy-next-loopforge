package httpapi

import (
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"

	"agentflow/internal/codereview"
	"agentflow/internal/domain"

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
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	pid := strings.TrimSpace(c.Query("projectId"))
	if authRequired() {
		visible := h.visibleProjectIDSetForRequest(c.Request)
		if pid != "" {
			if !visible[pid] {
				c.JSON(http.StatusOK, gin.H{"reviews": []domain.CodeReviewRecord{}})
				return
			}
			items := h.svc.ListReviews(pid, limit)
			c.JSON(http.StatusOK, gin.H{"reviews": items})
			return
		}
		// Fetch broader window before tenant filtering so other tenants don't starve results.
		fetchLimit := limit * 4
		if fetchLimit < 200 {
			fetchLimit = 200
		}
		items := h.svc.ListReviews("", fetchLimit)
		scoped := make([]domain.CodeReviewRecord, 0, limit)
		for _, it := range items {
			if it.ProjectID != "" && visible[it.ProjectID] {
				scoped = append(scoped, it)
				if len(scoped) >= limit {
					break
				}
			}
		}
		c.JSON(http.StatusOK, gin.H{"reviews": scoped})
		return
	}
	items := h.svc.ListReviews(pid, limit)
	c.JSON(http.StatusOK, gin.H{"reviews": items})
}

func (h *codeReviewHandlers) resolveAndAuthorizeProject(c *gin.Context, projectID, repoPath, runID, issueID string) (string, bool) {
	pid := strings.TrimSpace(projectID)
	rp := strings.TrimRight(strings.TrimSpace(repoPath), "/")
	if rp != "" && h.repo != nil {
		matched := false
		for _, r := range h.repo.Repositories() {
			if strings.TrimRight(strings.TrimSpace(r.LocalPath), "/") == rp {
				matched = true
				if r.ProjectID != "" {
					if pid != "" && pid != r.ProjectID {
						c.JSON(http.StatusBadRequest, gin.H{"error": "所选仓库与当前项目不匹配"})
						return "", false
					}
					pid = r.ProjectID
				}
				break
			}
		}
		if authRequired() && !matched {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权访问未登记的仓库路径"})
			return "", false
		}
	}
	if iid := strings.TrimSpace(issueID); iid != "" && h.repo != nil {
		if iss, ok := h.repo.Issue(iid); ok && strings.TrimSpace(iss.ProjectID) != "" {
			if pid != "" && pid != iss.ProjectID {
				c.JSON(http.StatusBadRequest, gin.H{"error": "关联需求与当前项目不一致"})
				return "", false
			}
			pid = iss.ProjectID
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
	if authRequired() {
		if pid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请先选择所属项目再发起代码审查"})
			return "", false
		}
		if !h.requireProjectWriter(c, pid) {
			return "", false
		}
	}
	return pid, true
}

// POST /api/codereview/reviews
func (h *codeReviewHandlers) create(c *gin.Context) {
	var input codereview.CreateReviewInput
	if !decode(c, &input) {
		return
	}
	pid, ok := h.resolveAndAuthorizeProject(c, input.ProjectID, input.RepoPath, input.RunID, input.IssueID)
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
	if err == nil && authRequired() {
		if detail.Review.ProjectID == "" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权访问未归属项目的审查记录"})
			return
		}
		if !h.requireProjectReader(c, detail.Review.ProjectID) {
			return
		}
	}
	render(c, detail, err)
}

// POST /api/codereview/reviews/:id/feedback
func (h *codeReviewHandlers) saveFeedback(c *gin.Context) {
	var input codereview.FeedbackInput
	if !decode(c, &input) {
		return
	}
	if detail, err := h.svc.GetReview(c.Param("id")); err == nil && authRequired() {
		if detail.Review.ProjectID == "" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权为未归属项目的审查提交反馈"})
			return
		}
		if !h.requireProjectWriter(c, detail.Review.ProjectID) {
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
	pid := strings.TrimSpace(c.Query("projectId"))
	if authRequired() {
		if pid == "" {
			// Never expose cross-tenant feedback records when projectId is omitted.
			c.JSON(http.StatusOK, domain.CodeReviewFeedbackSummary{})
			return
		}
		if !h.requireProjectReader(c, pid) {
			return
		}
	}
	c.JSON(http.StatusOK, h.svc.FeedbackSummary(pid))
}

// GET /api/codereview/executors
func (h *codeReviewHandlers) executors(c *gin.Context) {
	host, _ := os.Hostname()
	execs := codereview.DetectExecutors()
	c.JSON(http.StatusOK, gin.H{
		"host":            host,
		"canPickDir":      runtime.GOOS == "darwin",
		"defaultExecutor": codereview.PickDefaultExecutor(execs),
		"executors":       execs,
	})
}

// POST /api/codereview/reviews/run
func (h *codeReviewHandlers) runAutomated(c *gin.Context) {
	var input codereview.RunAutomatedReviewInput
	if !decode(c, &input) {
		return
	}
	pid, ok := h.resolveAndAuthorizeProject(c, input.ProjectID, input.RepoPath, input.RunID, input.IssueID)
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
