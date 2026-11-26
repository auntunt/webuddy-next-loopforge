package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentflow/internal/codereview"
	"agentflow/internal/domain"

	"github.com/gin-gonic/gin"
)

type codeReviewJob struct {
	ID               string                   `json:"jobId"`
	ProjectID        string                   `json:"projectId"`
	Status           string                   `json:"status"` // running | succeeded | failed
	CompletedBatches int                      `json:"completedBatches"`
	TotalBatches     int                      `json:"totalBatches"`
	CurrentHeader    string                   `json:"currentHeader"`
	StartedAt        time.Time                `json:"startedAt"`
	ElapsedSec       int                      `json:"elapsedSec"`
	Review           *domain.CodeReviewRecord `json:"review,omitempty"`
	Error            string                   `json:"error,omitempty"`
}

// codeReviewHandlers exposes Webuddy's Complete Diff Code Review + Human Feedback
// endpoints over HTTP.
type codeReviewHandlers struct {
	*deps
	svc       *codereview.Service
	diagnoser codereview.BatchDiagnoser
	jobsMu    sync.Mutex
	jobs      map[string]*codeReviewJob
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
	if input.Async || c.Query("async") == "1" {
		jobID := fmt.Sprintf("crjob-%d", time.Now().UnixNano())
		job := &codeReviewJob{
			ID:            jobID,
			ProjectID:     pid,
			Status:        "running",
			CurrentHeader: "正在读取仓库 Diff 与智能分批打包…",
			StartedAt:     time.Now().UTC(),
		}
		h.jobsMu.Lock()
		if h.jobs == nil {
			h.jobs = make(map[string]*codeReviewJob)
		}
		// Keep memory bounded to recent jobs.
		if len(h.jobs) > 100 {
			cutoff := time.Now().Add(-2 * time.Hour)
			for k, v := range h.jobs {
				if v.Status != "running" && v.StartedAt.Before(cutoff) {
					delete(h.jobs, k)
				}
			}
		}
		h.jobs[jobID] = job
		initialSnap := *job
		h.jobsMu.Unlock()

		go func(in codereview.RunAutomatedReviewInput, act string, j *codeReviewJob) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
			defer cancel()
			rec, err := h.svc.RunAutomatedReviewWithProgress(ctx, in, act, h.diagnoser, func(completed, total int, header string) {
				h.jobsMu.Lock()
				j.CompletedBatches = completed
				j.TotalBatches = total
				j.CurrentHeader = header
				h.jobsMu.Unlock()
			})
			h.jobsMu.Lock()
			defer h.jobsMu.Unlock()
			if err != nil {
				j.Status = "failed"
				j.Error = err.Error()
				return
			}
			j.Status = "succeeded"
			j.CompletedBatches = rec.TotalParts
			j.TotalBatches = rec.TotalParts
			j.CurrentHeader = "审查完成"
			cp := rec
			j.Review = &cp
		}(input, actor, job)

		c.JSON(http.StatusAccepted, initialSnap)
		return
	}
	rec, err := h.svc.RunAutomatedReview(c.Request.Context(), input, actor, h.diagnoser)
	render(c, rec, err)
}

// GET /api/codereview/jobs/:jobId
func (h *codeReviewHandlers) getJob(c *gin.Context) {
	id := strings.TrimSpace(c.Param("jobId"))
	h.jobsMu.Lock()
	j, ok := h.jobs[id]
	if !ok {
		h.jobsMu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "审查任务不存在或已过期"})
		return
	}
	snap := *j
	snap.ElapsedSec = int(time.Since(j.StartedAt).Seconds())
	h.jobsMu.Unlock()

	if authRequired() && snap.ProjectID != "" && !h.requireProjectReader(c, snap.ProjectID) {
		return
	}
	c.JSON(http.StatusOK, snap)
}

func buildReviewPREvidenceBody(detail codereview.ReviewDetail) string {
	r := detail.Review
	var b strings.Builder
	b.WriteString("## Webuddy 代码审查与人工签收凭证\n\n")
	verdictLabel := "待核实 (unknown)"
	switch r.Verdict {
	case "pass":
		verdictLabel = "通过 (pass)"
	case "fail":
		verdictLabel = "阻断 (fail · 已人工复核放行)"
	}
	fmt.Fprintf(&b, "- **审查结论**：%s\n", verdictLabel)
	fmt.Fprintf(&b, "- **独立审查**：Writer `%s` ≠ Reviewer `%s`（共 %d 批）\n", r.Writer, r.Reviewer, r.TotalParts)
	if r.IssueID != "" {
		fmt.Fprintf(&b, "- **关联需求**：`%s`\n", r.IssueID)
	}
	if r.RunID != "" {
		fmt.Fprintf(&b, "- **关联运行**：`%s`\n", r.RunID)
	}
	fmt.Fprintf(&b, "- **Diff SHA256**：`%s`\n", r.DiffSHA256)
	fmt.Fprintf(&b, "- **凭证指纹 (Binding)**：`%s`\n", r.Binding)
	for _, fb := range detail.Feedback {
		if fb.Current {
			fmt.Fprintf(&b, "- **人工复核签收**：`%s`（耗时 %d 分钟，%s）\n", fb.Actor, fb.Minutes, fb.CreatedAt.UTC().Format(time.RFC3339))
			if strings.TrimSpace(fb.Note) != "" {
				fmt.Fprintf(&b, "- **复核说明**：%s\n", strings.TrimSpace(fb.Note))
			}
			break
		}
	}
	if len(r.Blocking) > 0 {
		b.WriteString("\n### 审查阻断项记录\n")
		for i, item := range r.Blocking {
			fmt.Fprintf(&b, "%d. %s\n", i+1, item)
		}
	}
	return b.String()
}

// POST /api/codereview/reviews/:id/deliver-pr
// Enforces the code-review blocking gate, attaches review + signoff evidence,
// and creates a remote PR/MR (without auto-merging) or returns patch_only mode
// for archive/local-only repositories without a configured remote.
func (h *codeReviewHandlers) deliverPR(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	detail, err := h.svc.GetReview(id)
	if err != nil {
		render(c, detail, err)
		return
	}
	pid := strings.TrimSpace(detail.Review.ProjectID)
	if authRequired() {
		if pid == "" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权操作未归属项目的审查记录"})
			return
		}
		if !h.requireProjectWriter(c, pid) {
			return
		}
	}

	var input struct {
		SourceBranch string `json:"sourceBranch"`
		TargetBranch string `json:"targetBranch"`
		Title        string `json:"title"`
	}
	_ = c.ShouldBindJSON(&input)

	hasCurrentSignoff := false
	for _, fb := range detail.Feedback {
		if fb.Current {
			hasCurrentSignoff = true
			break
		}
	}
	if detail.Review.Verdict == "fail" && !hasCurrentSignoff {
		c.JSON(http.StatusConflict, gin.H{
			"error":   "当前代码审查存在阻断项且尚未完成人工复核签收，已拦截 PR 提交。请先修复阻断项，或在本页提交人工复核反馈后再发起 PR。",
			"blocked": true,
		})
		return
	}

	desc := buildReviewPREvidenceBody(detail)
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = strings.TrimSpace(detail.Review.Title)
	}
	if title == "" {
		title = "Webuddy 交付变更"
	}

	// Resolve remote project & default target branch from project repositories.
	remoteProject := ""
	defaultTarget := strings.TrimSpace(input.TargetBranch)
	if h.repo != nil && pid != "" {
		for _, r := range h.repo.Repositories() {
			if r.ProjectID != pid {
				continue
			}
			if rp := strings.TrimSpace(r.GongfengProjectID); rp != "" {
				remoteProject = rp
				if defaultTarget == "" && strings.TrimSpace(r.DefaultBranch) != "" {
					defaultTarget = strings.TrimSpace(r.DefaultBranch)
				}
				break
			}
		}
	}

	// Graceful fallback for local/archive-imported repositories with no remote.
	if remoteProject == "" || h.sync == nil {
		c.JSON(http.StatusOK, gin.H{
			"mode":        "patch_only",
			"patchUrl":    "/api/codereview/reviews/" + id + "/patch",
			"description": desc,
			"message":     "当前项目为本地/压缩包导入仓库（未绑定 GitHub/工蜂远端），已生成带审查与签收凭证的交付 Patch，可直接一键下载交付。",
		})
		return
	}

	srcBranch := strings.TrimSpace(input.SourceBranch)
	if srcBranch == "" && detail.Review.RunID != "" && h.repo != nil {
		if run, ok := h.repo.Run(detail.Review.RunID); ok && strings.TrimSpace(run.Repository.WorkBranch) != "" {
			srcBranch = strings.TrimSpace(run.Repository.WorkBranch)
		}
	}
	if srcBranch == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填写要提交 PR 的源分支名（例如 feature/...）"})
		return
	}
	if defaultTarget == "" {
		defaultTarget = "main"
	}

	mr, err := h.sync.CreateBranchPR(remoteProject, srcBranch, defaultTarget, title, desc)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "创建远端 PR/MR 失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"mode":        "pr_created",
		"mr":          mr,
		"description": desc,
		"message":     "已创建 PR/MR（未自动合并），并附带 Webuddy 代码审查与人工签收凭证。",
	})
}

// GET /api/codereview/reviews/:id/patch
// Exports a tamper-evident .patch artifact with review + human signoff metadata
// so local/archive-imported repos without a remote still have a complete delivery handoff.
func (h *codeReviewHandlers) exportPatch(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	detail, err := h.svc.GetReview(id)
	if err != nil {
		render(c, detail, err)
		return
	}
	if authRequired() {
		if detail.Review.ProjectID == "" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权导出未归属项目的审查 Patch"})
			return
		}
		if !h.requireProjectReader(c, detail.Review.ProjectID) {
			return
		}
	}
	var b strings.Builder
	b.WriteString("# Webuddy Delivery Patch\n")
	fmt.Fprintf(&b, "# Review-ID: %s\n", detail.Review.ID)
	fmt.Fprintf(&b, "# Verdict: %s\n", detail.Review.Verdict)
	fmt.Fprintf(&b, "# Writer: %s | Reviewer: %s\n", detail.Review.Writer, detail.Review.Reviewer)
	fmt.Fprintf(&b, "# Diff-SHA256: %s\n", detail.Review.DiffSHA256)
	fmt.Fprintf(&b, "# Binding-Fingerprint: %s\n", detail.Review.Binding)
	for _, fb := range detail.Feedback {
		if fb.Current {
			fmt.Fprintf(&b, "# Human-Signoff: %s (%s) - %s\n", fb.Actor, fb.CreatedAt.UTC().Format(time.RFC3339), strings.ReplaceAll(strings.TrimSpace(fb.Note), "\n", " "))
			break
		}
	}
	b.WriteString("\n")
	b.WriteString(detail.Review.DiffPreview)
	if !strings.HasSuffix(detail.Review.DiffPreview, "\n") {
		b.WriteString("\n")
	}

	safeID := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, detail.Review.ID)
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="webuddy-review-%s.patch"`, safeID))
	c.Data(http.StatusOK, "text/x-patch; charset=utf-8", []byte(b.String()))
}
