package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"agentflow/internal/builtinagent"
	"agentflow/internal/webuddykb"

	"github.com/gin-gonic/gin"
)

type kbCoverageSnapshot struct {
	Total     int            `json:"total"`
	Analyzed  int            `json:"analyzed"`
	Confirmed int            `json:"confirmed"`
	Draft     int            `json:"draft"`
	Pending   int            `json:"pending"`
	Stale     int            `json:"stale"`
	FactsOnly int            `json:"factsOnly,omitempty"`
	Raw       map[string]int `json:"raw,omitempty"`
}

type kbUpdatedPageReceipt struct {
	PageID       string   `json:"pageId"`
	Title        string   `json:"title"`
	Summary      string   `json:"summary"`
	Status       string   `json:"status"`
	Commit       string   `json:"commit"`
	Updated      string   `json:"updated"`
	By           string   `json:"by"`
	RelPath      string   `json:"relPath"`
	EvidenceRefs []string `json:"evidenceRefs,omitempty"`
}

type kbFailedPageReceipt struct {
	PageID string   `json:"pageId"`
	Errors []string `json:"errors"`
}

type kbStatusPayload struct {
	OK            bool                   `json:"ok"`
	ProjectID     string                 `json:"projectId"`
	ProjectName   string                 `json:"projectName"`
	RepoPath      string                 `json:"repoPath"`
	GitHead       string                 `json:"gitHead"`
	BuildHead     string                 `json:"buildHead,omitempty"`
	HeadDrifted   bool                   `json:"headDrifted,omitempty"`
	AutoSyncTask  string                 `json:"autoSyncTask,omitempty"`
	Coverage      kbCoverageSnapshot     `json:"coverage"`
	RecentUpdated []kbUpdatedPageReceipt `json:"recentUpdated"`
	VerifiedAt    string                 `json:"verifiedAt"`
}

type kbBatchUpdateReceipt struct {
	OK             bool                   `json:"ok"`
	ProjectID      string                 `json:"projectId"`
	ProjectName    string                 `json:"projectName"`
	RepoPath       string                 `json:"repoPath"`
	GitHead        string                 `json:"gitHead"`
	Model          string                 `json:"model,omitempty"`
	Query          string                 `json:"query,omitempty"`
	Remaining      int                    `json:"remaining"`
	CoverageBefore kbCoverageSnapshot     `json:"coverageBefore"`
	CoverageAfter  kbCoverageSnapshot     `json:"coverageAfter"`
	UpdatedPages   []kbUpdatedPageReceipt `json:"updatedPages"`
	FailedPages    []kbFailedPageReceipt  `json:"failedPages,omitempty"`
	RecentUpdated  []kbUpdatedPageReceipt `json:"recentUpdated,omitempty"`
	VerifiedAt     string                 `json:"verifiedAt"`
}

func (d *deps) resolveProjectAndRepoPath(projectID string) (string, string, string) {
	pid := strings.TrimSpace(projectID)
	projName := pid
	if pid == "" {
		if projs := d.repo.Projects(); len(projs) > 0 {
			pid = projs[0].ID
			projName = projs[0].Name
		}
	} else if p, ok := d.repo.Project(pid); ok {
		projName = p.Name
	}
	repoPath := ""
	for _, r := range d.repo.Repositories() {
		if (pid == "" || r.ProjectID == pid) && strings.TrimSpace(r.LocalPath) != "" {
			repoPath = strings.TrimSpace(r.LocalPath)
			if pid == "" {
				pid = r.ProjectID
				if p, ok := d.repo.Project(pid); ok {
					projName = p.Name
				}
			}
			break
		}
	}
	return pid, projName, repoPath
}

func (h *webuddyEnvHandlers) kbStatus(c *gin.Context) {
	rawPid := strings.TrimSpace(c.Query("projectId"))
	if authRequired() && rawPid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请指定 projectId"})
		return
	}
	pid, projName, repoPath := h.resolveProjectAndRepoPath(rawPid)
	if authRequired() && pid != "" && !h.requireProjectReader(c, pid) {
		return
	}
	if repoPath == "" {
		c.JSON(http.StatusOK, kbStatusPayload{
			OK:          false,
			ProjectID:   pid,
			ProjectName: projName,
			VerifiedAt:  time.Now().Format(time.RFC3339),
		})
		return
	}
	svc := webuddykb.NewService()
	raw, err := svc.KBStatus(c.Request.Context(), repoPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var res kbStatusPayload
	if err := json.Unmarshal(raw, &res); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	res.ProjectID = pid
	res.ProjectName = projName
	res.VerifiedAt = time.Now().Format(time.RFC3339)
	// Automatic timely KB sync when git HEAD drifts ahead of last KB build!
	if res.HeadDrifted && pid != "" {
		tk := h.deps.startAsyncKBBatchUpdate("auto-git-sync", pid, 3, "", false)
		res.AutoSyncTask = tk.ID
	}
	c.JSON(http.StatusOK, res)
}

func (h *webuddyEnvHandlers) kbBatchUpdate(c *gin.Context) {
	var req struct {
		ProjectID string `json:"projectId"`
		Limit     int    `json:"limit"`
		Query     string `json:"query"`
		Async     bool   `json:"async"`
		All       bool   `json:"all"`
	}
	_ = c.ShouldBindJSON(&req)
	actor := "webuddy-ai"
	if u, ok := h.requestUser(c.Request, true); ok {
		if strings.TrimSpace(u.LoginName) != "" {
			actor = strings.TrimSpace(u.LoginName)
		} else if u.ID > 0 {
			actor = fmt.Sprintf("user-%d", u.ID)
		}
	}
	rawPid := strings.TrimSpace(req.ProjectID)
	if authRequired() && rawPid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请指定 projectId"})
		return
	}
	pid, _, _ := h.resolveProjectAndRepoPath(rawPid)
	if authRequired() {
		if pid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "未找到目标项目"})
			return
		}
		if !h.requireProjectWriter(c, pid) {
			return
		}
	}
	if req.Async || req.All {
		task := h.deps.startAsyncKBBatchUpdate(actor, pid, req.Limit, req.Query, req.All)
		c.JSON(http.StatusAccepted, gin.H{
			"ok":     true,
			"async":  true,
			"all":    req.All,
			"task":   task,
			"taskId": task.ID,
		})
		return
	}
	receipt, err := h.deps.runWebuddyKBBatchUpdate(c.Request.Context(), actor, pid, req.Limit, req.Query)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, receipt)
}

type kbNextJob struct {
	PageID           string   `json:"pageId"`
	Kind             string   `json:"kind"`
	RelPath          string   `json:"relPath"`
	CurrentTitle     string   `json:"currentTitle"`
	RequiredSections []string `json:"requiredSections"`
	Instructions     string   `json:"instructions"`
	PackText         string   `json:"packText"`
}

type kbNextResponse struct {
	OK        bool               `json:"ok"`
	RepoPath  string             `json:"repoPath"`
	GitHead   string             `json:"gitHead"`
	Coverage  kbCoverageSnapshot `json:"coverage"`
	Remaining int                `json:"remaining"`
	Jobs      []kbNextJob        `json:"jobs"`
}

type kbGeneratedPage struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Body    string `json:"body"`
}

func parseKBGeneratedPage(raw string) (kbGeneratedPage, error) {
	txt := strings.TrimSpace(raw)
	// Strip outer markdown fence if the model wrapped the whole output
	if strings.HasPrefix(txt, "```") {
		if nl := strings.Index(txt, "\n"); nl >= 0 {
			txt = txt[nl+1:]
		}
		if end := strings.LastIndex(txt, "```"); end >= 0 {
			txt = txt[:end]
		}
		txt = strings.TrimSpace(txt)
	}

	// Primary format: TITLE: ... / SUMMARY: ... / --- / ## ...
	var out kbGeneratedPage
	lines := strings.Split(txt, "\n")
	bodyStart := -1
	for i, line := range lines {
		t := strings.TrimSpace(line)
		upper := strings.ToUpper(t)
		switch {
		case strings.HasPrefix(upper, "TITLE:") || strings.HasPrefix(t, "TITLE："):
			val := strings.TrimSpace(t[strings.IndexAny(t, ":：")+1:])
			out.Title = strings.Trim(val, "`\"' ")
		case strings.HasPrefix(upper, "SUMMARY:") || strings.HasPrefix(t, "SUMMARY："):
			val := strings.TrimSpace(t[strings.IndexAny(t, ":：")+1:])
			out.Summary = strings.Trim(val, "`\"' ")
		case t == "---" && i > 0:
			bodyStart = i + 1
		case strings.HasPrefix(t, "## "):
			bodyStart = i
		}
		if bodyStart >= 0 {
			break
		}
	}
	if bodyStart >= 0 && bodyStart < len(lines) {
		out.Body = strings.TrimSpace(strings.Join(lines[bodyStart:], "\n"))
	}
	if out.Title != "" && out.Summary != "" && out.Body != "" {
		return out, nil
	}

	// Fallback: JSON format
	if start := strings.Index(txt, "{"); start >= 0 {
		if end := strings.LastIndex(txt, "}"); end > start {
			if err := json.Unmarshal([]byte(txt[start:end+1]), &out); err == nil {
				out.Title = strings.TrimSpace(out.Title)
				out.Summary = strings.TrimSpace(out.Summary)
				out.Body = strings.TrimSpace(out.Body)
				if out.Title != "" && out.Summary != "" && out.Body != "" {
					return out, nil
				}
			}
		}
	}
	return out, fmt.Errorf("模型输出缺少 TITLE / SUMMARY / 正文结构")
}

func (d *deps) runWebuddyKBBatchUpdate(ctx context.Context, actor, projectID string, limit int, query string) (kbBatchUpdateReceipt, error) {
	pid, projName, repoPath := d.resolveProjectAndRepoPath(projectID)
	if repoPath == "" {
		return kbBatchUpdateReceipt{}, fmt.Errorf("当前项目尚未挂载本地代码仓库，无法执行服务端知识库增量更新")
	}
	if limit <= 0 {
		limit = 2
	}
	if limit > 5 {
		limit = 5
	}
	if strings.TrimSpace(actor) == "" {
		actor = "webuddy-ai"
	}

	svc := webuddykb.NewService()
	nextRaw, err := svc.KBNext(ctx, repoPath, limit, query, actor)
	if err != nil {
		return kbBatchUpdateReceipt{}, err
	}
	var next kbNextResponse
	if err := json.Unmarshal(nextRaw, &next); err != nil {
		return kbBatchUpdateReceipt{}, err
	}

	receipt := kbBatchUpdateReceipt{
		OK:             true,
		ProjectID:      pid,
		ProjectName:    projName,
		RepoPath:       next.RepoPath,
		GitHead:        next.GitHead,
		Query:          strings.TrimSpace(query),
		Remaining:      next.Remaining,
		CoverageBefore: next.Coverage,
		CoverageAfter:  next.Coverage,
		UpdatedPages:   []kbUpdatedPageReceipt{},
		FailedPages:    []kbFailedPageReceipt{},
		VerifiedAt:     time.Now().Format(time.RFC3339),
	}
	if len(next.Jobs) == 0 {
		return receipt, nil
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 2)
	for _, job := range next.Jobs {
		job := job
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			pageReceipt, usedModel, errs := synthesizeAndCommitKBPage(ctx, svc, repoPath, actor, job)
			mu.Lock()
			defer mu.Unlock()
			if usedModel != "" && receipt.Model == "" {
				receipt.Model = usedModel
			}
			if len(errs) == 0 && pageReceipt.PageID != "" {
				receipt.UpdatedPages = append(receipt.UpdatedPages, pageReceipt)
			} else {
				receipt.FailedPages = append(receipt.FailedPages, kbFailedPageReceipt{
					PageID: job.PageID,
					Errors: errs,
				})
			}
		}()
	}
	wg.Wait()

	if statusRaw, err := svc.KBStatus(ctx, repoPath); err == nil {
		var st kbStatusPayload
		if json.Unmarshal(statusRaw, &st) == nil {
			receipt.CoverageAfter = st.Coverage
			receipt.RecentUpdated = st.RecentUpdated
			receipt.Remaining = st.Coverage.Pending + st.Coverage.Stale
		}
	}
	if len(receipt.UpdatedPages) > 0 && pid != "" {
		_ = d.syncProjectKBFilesToShared(pid, actor)
	}
	receipt.VerifiedAt = time.Now().Format(time.RFC3339)
	receipt.OK = len(receipt.UpdatedPages) > 0 || len(receipt.FailedPages) == 0
	return receipt, nil
}

func synthesizeAndCommitKBPage(ctx context.Context, svc *webuddykb.Service, repoPath, actor string, job kbNextJob) (kbUpdatedPageReceipt, string, []string) {
	secList := strings.Join(job.RequiredSections, "、")
	sysPrompt := "你是严谨的代码知识库架构师，负责把源码上下文提炼成简洁、可信溯源的中文知识库页面。\n" +
		"严格遵守以下输出格式（不要输出 JSON，不要用代码块包裹全文）：\n" +
		"TITLE: <简洁中文业务名称>\n" +
		"SUMMARY: <一句话业务说明，不超过55字>\n" +
		"---\n" +
		"接下来直接写 Markdown 正文：\n" +
		"1. 必须严格包含以下所有二级标题（一个都不能少，也不能重复）：" + secList + "。每节精炼在 1-3 句，无内容写“无”。\n" +
		"2. 每个核心结论必须附带真实源码证据，格式严格为 `相对路径:行号` 或 `相对路径:起行-止行`（只能原样使用下方源码上下文里出现过的真实文件路径和行号，严禁编造！）。\n" +
		"3. 不要写 YAML frontmatter 或 tmh:facts 块；不确定处标【推断】。"

	pack := job.PackText
	if runes := []rune(pack); len(runes) > 9500 {
		pack = string(runes[:9500]) + "\n...(后续上下文已截断)"
	}
	instNote := ""
	if strings.TrimSpace(job.Instructions) != "" {
		instNote = "\n修订要求：\n" + strings.TrimSpace(job.Instructions) + "\n"
	}
	baseUserPrompt := fmt.Sprintf("页面 ID：%s\n文件：%s\n必须包含的二级标题：%s%s\n===== 源码与图谱上下文 =====\n%s",
		job.PageID, job.RelPath, secList, instNote, pack)

	var lastErrs []string
	var usedModel string
	userPrompt := baseUserPrompt

	for attempt := 0; attempt < 2; attempt++ {
		res := builtinagent.Default().Complete(ctx, builtinagent.CompletionRequest{
			SystemPrompt: sysPrompt,
			UserPrompt:   userPrompt,
			MaxTokens:    2400,
			Temperature:  0.15,
			Timeout:      75 * time.Second,
		})
		if res.Model != "" {
			usedModel = res.Model
		}
		if res.Error != "" || strings.TrimSpace(res.Text) == "" {
			lastErrs = []string{fmt.Sprintf("内置模型调用失败: %s", res.Error)}
			continue
		}
		gen, err := parseKBGeneratedPage(res.Text)
		if err != nil {
			lastErrs = []string{err.Error()}
			userPrompt = baseUserPrompt + "\n\n上次输出格式校验失败：" + err.Error() + "\n请严格按 TITLE: / SUMMARY: / --- / ## 标题 纯文本格式重新输出（严禁使用代码块包裹全文，严禁输出 JSON）。"
			continue
		}
		commitCtx, commitCancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
		commitRaw, err := svc.KBCommit(commitCtx, repoPath, job.PageID, gen.Title, gen.Summary, gen.Body, actor)
		commitCancel()
		if err != nil {
			lastErrs = []string{err.Error()}
			continue
		}
		var commitRes struct {
			OK           bool     `json:"ok"`
			PageID       string   `json:"pageId"`
			Title        string   `json:"title"`
			Summary      string   `json:"summary"`
			Status       string   `json:"status"`
			Commit       string   `json:"commit"`
			Updated      string   `json:"updated"`
			By           string   `json:"by"`
			RelPath      string   `json:"relPath"`
			EvidenceRefs []string `json:"evidenceRefs"`
			Errors       []string `json:"errors"`
		}
		if err := json.Unmarshal(commitRaw, &commitRes); err != nil {
			lastErrs = []string{err.Error()}
			continue
		}
		if commitRes.OK {
			return kbUpdatedPageReceipt{
				PageID:       commitRes.PageID,
				Title:        commitRes.Title,
				Summary:      commitRes.Summary,
				Status:       commitRes.Status,
				Commit:       commitRes.Commit,
				Updated:      commitRes.Updated,
				By:           commitRes.By,
				RelPath:      commitRes.RelPath,
				EvidenceRefs: commitRes.EvidenceRefs,
			}, usedModel, nil
		}
		lastErrs = commitRes.Errors
		userPrompt = baseUserPrompt + "\n\n上次提交未通过源码溯源或章节校验，错误如下：\n- " + strings.Join(commitRes.Errors, "\n- ") +
			"\n请逐条修正（只引用上方源码上下文里真实存在的文件路径和行号，包含全部必需二级标题），并严格按 TITLE: / SUMMARY: / --- / ## 标题 格式输出（不要输出 JSON）。"
	}
	return kbUpdatedPageReceipt{}, usedModel, lastErrs
}
