package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// WebuddyLiveTask represents a background or long-running operation surfaced in the
// global floating live-task pill so users can continue working freely without blocking.
type WebuddyLiveTask struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"` // kb_update | chat | sop | archive_upload
	ProjectID   string `json:"projectId,omitempty"`
	ProjectName string `json:"projectName,omitempty"`
	Title       string `json:"title"`
	Status      string `json:"status"` // running | done | error
	Progress    string `json:"progress,omitempty"`
	Detail      string `json:"detail,omitempty"`
	URLHash     string `json:"urlHash,omitempty"`
	StartedAt   string `json:"startedAt"`
	UpdatedAt   string `json:"updatedAt"`
	FinishedAt  string `json:"finishedAt,omitempty"`
	ElapsedSec  int64  `json:"elapsedSec"`
	startedTime time.Time
	doneTime    time.Time
}

type webuddyTaskRegistry struct {
	mu    sync.RWMutex
	tasks map[string]*WebuddyLiveTask
}

var defaultTaskRegistry = &webuddyTaskRegistry{
	tasks: make(map[string]*WebuddyLiveTask),
}

func (r *webuddyTaskRegistry) start(kind, projectID, projectName, title, progress, urlHash string) *WebuddyLiveTask {
	now := time.Now()
	id := fmt.Sprintf("wbt-%s-%d", kind, now.UnixNano())
	t := &WebuddyLiveTask{
		ID:          id,
		Kind:        kind,
		ProjectID:   strings.TrimSpace(projectID),
		ProjectName: strings.TrimSpace(projectName),
		Title:       strings.TrimSpace(title),
		Status:      "running",
		Progress:    strings.TrimSpace(progress),
		URLHash:     urlHash,
		StartedAt:   now.Format(time.RFC3339),
		UpdatedAt:   now.Format(time.RFC3339),
		startedTime: now,
	}
	r.mu.Lock()
	r.pruneLocked(now)
	r.tasks[id] = t
	r.mu.Unlock()
	return t
}

func (r *webuddyTaskRegistry) update(id, progress, detail string) {
	now := time.Now()
	r.mu.Lock()
	if t, ok := r.tasks[id]; ok {
		if progress != "" {
			t.Progress = progress
		}
		if detail != "" {
			t.Detail = detail
		}
		t.UpdatedAt = now.Format(time.RFC3339)
	}
	r.mu.Unlock()
}

func (r *webuddyTaskRegistry) finish(id, status, progress, detail string) {
	now := time.Now()
	r.mu.Lock()
	if t, ok := r.tasks[id]; ok {
		t.Status = status
		if progress != "" {
			t.Progress = progress
		}
		if detail != "" {
			t.Detail = detail
		}
		t.UpdatedAt = now.Format(time.RFC3339)
		t.FinishedAt = now.Format(time.RFC3339)
		t.doneTime = now
	}
	r.mu.Unlock()
}

func (r *webuddyTaskRegistry) pruneLocked(now time.Time) {
	for id, t := range r.tasks {
		if t.Status != "running" && !t.doneTime.IsZero() && now.Sub(t.doneTime) > 10*time.Minute {
			delete(r.tasks, id)
		}
	}
}

func (r *webuddyTaskRegistry) list() []WebuddyLiveTask {
	now := time.Now()
	r.mu.Lock()
	r.pruneLocked(now)
	out := make([]WebuddyLiveTask, 0, len(r.tasks))
	for _, t := range r.tasks {
		cp := *t
		if cp.Status == "running" {
			cp.ElapsedSec = int64(now.Sub(t.startedTime).Seconds())
		} else if !t.doneTime.IsZero() {
			cp.ElapsedSec = int64(t.doneTime.Sub(t.startedTime).Seconds())
		}
		if cp.ElapsedSec < 0 {
			cp.ElapsedSec = 0
		}
		out = append(out, cp)
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Status == "running" && out[j].Status != "running" {
			return true
		}
		if out[i].Status != "running" && out[j].Status == "running" {
			return false
		}
		return out[i].StartedAt > out[j].StartedAt
	})
	return out
}

// GET /api/webuddy/tasks/active
func (h *webuddyEnvHandlers) activeTasks(c *gin.Context) {
	allTasks := defaultTaskRegistry.list()
	visibleProjects := h.visibleProjectIDSetForRequest(c.Request)
	tasks := make([]WebuddyLiveTask, 0, len(allTasks))
	for _, tk := range allTasks {
		if authRequired() && tk.ProjectID != "" && !visibleProjects[tk.ProjectID] {
			continue
		}
		tasks = append(tasks, tk)
	}
	now := time.Now()

	// Also surface active SOP runs so users always see running workflows in the same floating pill
	if h.repo != nil {
		for _, run := range h.repo.Runs() {
			st := strings.ToLower(strings.TrimSpace(string(run.Status)))
			if st == "running" || st == "queued" || st == "waiting_input" {
				pid := strings.TrimSpace(run.WorkflowSnapshot.ProjectID)
				if authRequired() && pid != "" && !visibleProjects[pid] {
					continue
				}
				projName := pid
				if p, ok := h.repo.Project(pid); ok && p.Name != "" {
					projName = p.Name
				}
				title := strings.TrimSpace(run.WorkflowSnapshot.Name)
				if title == "" {
					title = run.ID
				}
				elapsed := int64(now.Sub(run.CreatedAt).Seconds())
				if elapsed < 0 {
					elapsed = 0
				}
				tasks = append(tasks, WebuddyLiveTask{
					ID:          "run-" + run.ID,
					Kind:        "sop",
					ProjectID:   pid,
					ProjectName: projName,
					Title:       "SOP 执行：" + title,
					Status:      "running",
					Progress:    "状态：" + st,
					URLHash:     "#/sops",
					StartedAt:   run.CreatedAt.Format(time.RFC3339),
					UpdatedAt:   run.UpdatedAt.Format(time.RFC3339),
					ElapsedSec:  elapsed,
				})
			}
		}
	}

	running := 0
	for _, t := range tasks {
		if t.Status == "running" {
			running++
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"runningCount": running,
		"tasks":        tasks,
		"serverTime":   now.Format(time.RFC3339),
	})
}

func (r *webuddyTaskRegistry) findRunning(kind, projectID string) (WebuddyLiveTask, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, t := range r.tasks {
		if t.Status == "running" && t.Kind == kind && t.ProjectID == projectID {
			cp := *t
			cp.ElapsedSec = int64(time.Since(t.startedTime).Seconds())
			return cp, true
		}
	}
	return WebuddyLiveTask{}, false
}

// startAsyncKBBatchUpdate launches a non-blocking background KB update and registers it
// in defaultTaskRegistry so the floating pill tracks live progress across all views.
// Deduplicates concurrent runs per project so multiple triggers reuse the active task.
func (d *deps) startAsyncKBBatchUpdate(actor, projectID string, limit int, query string, runAll bool) WebuddyLiveTask {
	pid, projName, _ := d.resolveProjectAndRepoPath(projectID)
	if pid != "" && strings.TrimSpace(query) == "" {
		if existing, ok := defaultTaskRegistry.findRunning("kb_update", pid); ok {
			return existing
		}
	}
	if limit <= 0 {
		limit = 3
	}
	if limit > 5 {
		limit = 5
	}
	title := fmt.Sprintf("知识库%s（%s）", func() string {
		if runAll {
			return "全量持续分析"
		}
		return "增量更新"
	}(), projName)
	if projName == "" {
		title = "知识库后台分析"
	}
	initProgress := fmt.Sprintf("正在分析源码并做溯源校验（每批 %d 页）...", limit)
	if runAll {
		initProgress = "已启动全量持续分析，逐批写入并实时更新进度..."
	}
	task := defaultTaskRegistry.start(
		"kb_update",
		pid,
		projName,
		title,
		initProgress,
		"#/knowledgebase",
	)
	taskID := task.ID

	go func() {
		maxBatches := 1
		if runAll {
			maxBatches = 20
		}
		totalAdded := 0
		lastTitle := ""
		var lastCov kbCoverageSnapshot
		zeroStreak := 0

		for batch := 1; batch <= maxBatches; batch++ {
			bgCtx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
			receipt, err := d.runWebuddyKBBatchUpdate(bgCtx, actor, pid, limit, query)
			cancel()
			if err != nil {
				if totalAdded > 0 {
					defaultTaskRegistry.finish(taskID, "done", fmt.Sprintf("已完成 +%d 页（%d/%d 页）", totalAdded, lastCov.Analyzed, lastCov.Total), "部分批次中断："+err.Error())
				} else {
					defaultTaskRegistry.finish(taskID, "error", "更新失败", err.Error())
				}
				return
			}
			lastCov = receipt.CoverageAfter
			added := len(receipt.UpdatedPages)
			totalAdded += added
			if added > 0 {
				zeroStreak = 0
				lastTitle = receipt.UpdatedPages[len(receipt.UpdatedPages)-1].Title
			} else {
				zeroStreak++
			}
			if !runAll || receipt.Remaining <= 0 || zeroStreak >= 2 {
				break
			}
			defaultTaskRegistry.update(
				taskID,
				fmt.Sprintf("持续分析中：本轮已新增 +%d 页（总进度 %d/%d 页，剩余 %d 页）", totalAdded, lastCov.Analyzed, lastCov.Total, receipt.Remaining),
				lastTitle,
			)
			time.Sleep(2 * time.Second)
		}

		if totalAdded > 0 {
			defaultTaskRegistry.finish(
				taskID,
				"done",
				fmt.Sprintf("已完成 +%d 页（总已分析 %d/%d 页）", totalAdded, lastCov.Analyzed, lastCov.Total),
				lastTitle,
			)
		} else if lastCov.Pending+lastCov.Stale == 0 {
			defaultTaskRegistry.finish(
				taskID,
				"done",
				fmt.Sprintf("全部页面已完成分析（%d/%d 页）", lastCov.Analyzed, lastCov.Total),
				"",
			)
		} else {
			defaultTaskRegistry.finish(taskID, "error", "本次未写入新页面", "模型校验未通过或上游限流，可稍后重试")
		}
	}()

	return *task
}
