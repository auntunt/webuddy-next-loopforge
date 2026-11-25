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
	pid, projName, repoPath := h.resolveProjectAndRepoPath(c.Query("projectId"))
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
	c.JSON(http.StatusOK, res)
}

func (h *webuddyEnvHandlers) kbBatchUpdate(c *gin.Context) {
	var req struct {
		ProjectID string `json:"projectId"`
		Limit     int    `json:"limit"`
		Query     string `json:"query"`
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
	pid := strings.TrimSpace(req.ProjectID)
	if authRequired() && pid != "" && !h.requireProjectWriter(c, pid) {
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
	if idx := strings.Index(txt, "```json"); idx >= 0 {
		txt = txt[idx+7:]
		if end := strings.Index(txt, "```"); end >= 0 {
			txt = txt[:end]
		}
	} else if idx := strings.Index(txt, "```"); idx >= 0 {
		txt = txt[idx+3:]
		if end := strings.Index(txt, "```"); end >= 0 {
			txt = txt[:end]
		}
	}
	txt = strings.TrimSpace(txt)
	if start := strings.Index(txt, "{"); start >= 0 {
		if end := strings.LastIndex(txt, "}"); end > start {
			txt = txt[start : end+1]
		}
	}
	var out kbGeneratedPage
	if err := json.Unmarshal([]byte(txt), &out); err != nil {
		return out, fmt.Errorf("模型未返回合法 JSON: %w", err)
	}
	out.Title = strings.TrimSpace(out.Title)
	out.Summary = strings.TrimSpace(out.Summary)
	out.Body = strings.TrimSpace(out.Body)
	if out.Title == "" || out.Summary == "" || out.Body == "" {
		return out, fmt.Errorf("生成结果缺少 title / summary / body")
	}
	return out, nil
}

func (d *deps) runWebuddyKBBatchUpdate(ctx context.Context, actor, projectID string, limit int, query string) (kbBatchUpdateReceipt, error) {
	pid, projName, repoPath := d.resolveProjectAndRepoPath(projectID)
	if repoPath == "" {
		return kbBatchUpdateReceipt{}, fmt.Errorf("当前项目尚未挂载本地代码仓库，无法执行服务端知识库增量更新")
	}
	if limit <= 0 {
		limit = 3
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
	for _, job := range next.Jobs {
		job := job
		wg.Add(1)
		go func() {
			defer wg.Done()
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
	receipt.VerifiedAt = time.Now().Format(time.RFC3339)
	receipt.OK = len(receipt.UpdatedPages) > 0 || len(receipt.FailedPages) == 0
	return receipt, nil
}

func synthesizeAndCommitKBPage(ctx context.Context, svc *webuddykb.Service, repoPath, actor string, job kbNextJob) (kbUpdatedPageReceipt, string, []string) {
	secList := strings.Join(job.RequiredSections, "、")
	sysPrompt := "你是严谨的代码知识库架构师，负责把源码上下文提炼成可信溯源的中文知识库页面。\n" +
		"严格遵守以下规则：\n" +
		"1. 只能输出一个合法 JSON 对象，格式：{\"title\":\"简洁中文业务名称\",\"summary\":\"一句话业务说明，不超过55字\",\"body\":\"Markdown正文\"}\n" +
		"2. body 必须严格包含以下所有二级标题（一个都不能少，也不能重复）：" + secList + "。某节无内容写“无”。\n" +
		"3. 每个核心结论必须附带真实的源码位置证据，格式严格为 `相对路径:行号` 或 `相对路径:起行-止行`（只能原样使用下方源码上下文里出现过的真实文件路径和行号范围，严禁编造任何路径或超出范围的行号！）。\n" +
		"4. body 不要包含 YAML frontmatter（---）也不要包含 tmh:facts 块，只写二级标题及正文。\n" +
		"5. 信息不确定处标【推断】，严禁胡编外部系统名称。"

	baseUserPrompt := fmt.Sprintf("页面 ID：%s\n文件：%s\n必须包含的二级标题：%s\n\n===== 源码与图谱上下文 =====\n%s",
		job.PageID, job.RelPath, secList, job.PackText)

	var lastErrs []string
	var usedModel string
	userPrompt := baseUserPrompt

	for attempt := 0; attempt < 2; attempt++ {
		res := builtinagent.Default().Complete(ctx, builtinagent.CompletionRequest{
			SystemPrompt: sysPrompt,
			UserPrompt:   userPrompt,
			MaxTokens:    1600,
			Temperature:  0.15,
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
			userPrompt = baseUserPrompt + "\n\n上次输出格式校验失败：" + err.Error() + "\n请严格只输出一个合法 JSON 对象。"
			continue
		}
		commitRaw, err := svc.KBCommit(ctx, repoPath, job.PageID, gen.Title, gen.Summary, gen.Body, actor)
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
		userPrompt = baseUserPrompt + "\n\n上次提交未通过源码溯源校验，错误如下：\n- " + strings.Join(commitRes.Errors, "\n- ") +
			"\n请逐条修正（尤其是只引用上下文里真实存在的文件路径和行号），并严格只输出一个合法 JSON 对象。"
	}
	return kbUpdatedPageReceipt{}, usedModel, lastErrs
}
