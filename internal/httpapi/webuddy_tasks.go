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
	tasks := defaultTaskRegistry.list()
	now := time.Now()

	// Also surface active SOP runs so users always see running workflows in the same floating pill
	if h.repo != nil {
		for _, run := range h.repo.Runs() {
			st := strings.ToLower(strings.TrimSpace(string(run.Status)))
			if st == "running" || st == "queued" || st == "waiting_input" {
				pid := strings.TrimSpace(run.WorkflowSnapshot.ProjectID)
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

// startAsyncKBBatchUpdate launches a non-blocking background KB update and registers it
// in defaultTaskRegistry so the floating pill tracks live progress across all views.
func (d *deps) startAsyncKBBatchUpdate(actor, projectID string, limit int, query string) WebuddyLiveTask {
	pid, projName, _ := d.resolveProjectAndRepoPath(projectID)
	if limit <= 0 {
		limit = 3
	}
	if limit > 5 {
		limit = 5
	}
	title := fmt.Sprintf("知识库增量更新（%s）", projName)
	if projName == "" {
		title = "知识库增量更新"
	}
	task := defaultTaskRegistry.start(
		"kb_update",
		pid,
		projName,
		title,
		fmt.Sprintf("正在分析源码并做溯源校验（计划 %d 页）...", limit),
		"#/knowledgebase",
	)
	taskID := task.ID

	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		receipt, err := d.runWebuddyKBBatchUpdate(bgCtx, actor, pid, limit, query)
		if err != nil {
			defaultTaskRegistry.finish(taskID, "error", "更新失败", err.Error())
			return
		}
		if len(receipt.UpdatedPages) > 0 {
			defaultTaskRegistry.finish(
				taskID,
				"done",
				fmt.Sprintf("已完成 +%d 页（总已分析 %d/%d 页）", len(receipt.UpdatedPages), receipt.CoverageAfter.Analyzed, receipt.CoverageAfter.Total),
				receipt.UpdatedPages[0].Title,
			)
		} else if len(receipt.FailedPages) > 0 {
			errMsg := ""
			if len(receipt.FailedPages[0].Errors) > 0 {
				errMsg = receipt.FailedPages[0].Errors[0]
			}
			defaultTaskRegistry.finish(taskID, "error", "本次未写入新页面", errMsg)
		} else {
			defaultTaskRegistry.finish(
				taskID,
				"done",
				fmt.Sprintf("知识库已是最新（%d/%d 页）", receipt.CoverageAfter.Analyzed, receipt.CoverageAfter.Total),
				"",
			)
		}
	}()

	return *task
}
