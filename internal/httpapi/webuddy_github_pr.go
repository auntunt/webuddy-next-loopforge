package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agentflow/internal/domain"

	"github.com/gin-gonic/gin"
)

func (h *runsHandlers) createGitHubPR(c *gin.Context) {
	runID := strings.TrimSpace(c.Param("id"))
	if runID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少运行 ID"})
		return
	}
	run, ok := h.repo.Run(runID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "运行不存在"})
		return
	}
	pid := strings.TrimSpace(run.WorkflowSnapshot.ProjectID)
	if pid == "" {
		pid = strings.TrimSpace(run.ExecutionContext.ProjectID)
	}
	if authRequired() && pid != "" && !h.requireProjectWriter(c, pid) {
		return
	}
	var actorUID int64
	if u, ok := h.requestUser(c.Request, true); ok && u != nil {
		actorUID = u.ID
	}
	mr, err := h.deps.createGitHubPRFromRunPatch(c.Request.Context(), runID, actorUID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":  true,
		"mr":  mr,
		"url": mr.URL,
	})
}

func parseGitHubOwnerRepo(rc domain.RepositoryContext) (owner, repo string, ok bool) {
	if strings.HasPrefix(rc.GongfengProjectID, "github:") {
		parts := strings.Split(strings.TrimPrefix(rc.GongfengProjectID, "github:"), "/")
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], strings.TrimSuffix(parts[1], ".git"), true
		}
	}
	for _, raw := range []string{rc.CloneURL, rc.WebURL} {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || !strings.EqualFold(u.Hostname(), "github.com") {
			continue
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], strings.TrimSuffix(parts[1], ".git"), true
		}
	}
	return "", "", false
}

func extractUnifiedDiffFromArtifactContent(raw string) string {
	if idx := strings.Index(raw, "--- unified diff ---\n"); idx >= 0 {
		diff := raw[idx+len("--- unified diff ---\n"):]
		if trunc := strings.Index(diff, "\n... (diff truncated at "); trunc >= 0 {
			diff = diff[:trunc]
		}
		diff = strings.TrimLeft(diff, "\r\n")
		if diff != "" && !strings.HasSuffix(diff, "\n") {
			diff += "\n"
		}
		return diff
	}
	trimmed := strings.TrimLeft(raw, "\ufeff \t\r\n")
	if strings.HasPrefix(trimmed, "diff --git ") || strings.HasPrefix(trimmed, "--- ") {
		if !strings.HasSuffix(trimmed, "\n") {
			trimmed += "\n"
		}
		return trimmed
	}
	return ""
}

func latestRunUnifiedDiff(run domain.Run, repoDir string) string {
	for i := len(run.Artifacts) - 1; i >= 0; i-- {
		a := run.Artifacts[i]
		if repoDir != "" && a.RepositoryDir != "" && a.RepositoryDir != repoDir {
			continue
		}
		isPatch := a.Name == "repository-patch" || strings.HasSuffix(strings.ToLower(a.Path), ".patch") || strings.HasSuffix(strings.ToLower(a.Name), ".patch")
		if !isPatch {
			continue
		}
		if diff := extractUnifiedDiffFromArtifactContent(a.Content); diff != "" {
			return diff
		}
	}
	for i := len(run.Artifacts) - 1; i >= 0; i-- {
		if diff := extractUnifiedDiffFromArtifactContent(run.Artifacts[i].Content); diff != "" {
			return diff
		}
	}
	return ""
}

// createGitHubPRFromRunPatch materializes a Runner's repository-patch onto a clean
// GitHub branch from the control plane and opens (or reuses) a real GitHub Pull Request.
func (d *deps) createGitHubPRFromRunPatch(ctx context.Context, runID string, actorUID int64) (domain.DeliveryMR, error) {
	if d == nil || d.repo == nil {
		return domain.DeliveryMR{}, errors.New("服务未就绪")
	}
	run, ok := d.repo.Run(runID)
	if !ok {
		return domain.DeliveryMR{}, errors.New("运行不存在")
	}
	pid := strings.TrimSpace(run.WorkflowSnapshot.ProjectID)
	if pid == "" {
		pid = strings.TrimSpace(run.ExecutionContext.ProjectID)
	}
	issueID := strings.TrimSpace(run.ExecutionContext.IssueID)
	var issue domain.Issue
	var hasIssue bool
	if issueID != "" {
		issue, hasIssue = d.repo.Issue(issueID)
		if hasIssue && pid == "" {
			pid = issue.ProjectID
		}
	}

	// If issue already has an open/merged GitHub PR from an earlier step, return it idempotently
	// when triggered automatically (actorUID == 0).
	if actorUID == 0 && hasIssue && strings.Contains(issue.DeliveryMRURL, "github.com/") && strings.Contains(issue.DeliveryMRURL, "/pull/") {
		existing := issue.DeliveryMRList()
		if len(existing) > 0 {
			return existing[0], nil
		}
	}

	var targetRC domain.RepositoryContext
	var ghOwner, ghRepo string
	for _, rc := range run.RepositoryContexts() {
		if o, r, ok := parseGitHubOwnerRepo(rc); ok {
			targetRC, ghOwner, ghRepo = rc, o, r
			break
		}
	}
	if ghOwner == "" && pid != "" {
		for _, r := range d.repo.Repositories() {
			if r.ProjectID != pid {
				continue
			}
			rc := domain.RepositoryContext{
				RepositoryID:      r.ID,
				Provider:          r.Provider,
				Name:              r.Name,
				GongfengProjectID: r.GongfengProjectID,
				WebURL:            r.WebURL,
				CloneURL:          r.CloneURL,
				DefaultBranch:     r.DefaultBranch,
			}
			if o, repoName, ok := parseGitHubOwnerRepo(rc); ok {
				targetRC, ghOwner, ghRepo = rc, o, repoName
				break
			}
		}
	}
	if ghOwner == "" || ghRepo == "" {
		return domain.DeliveryMR{}, errors.New("当前运行未关联 GitHub 代码仓库")
	}

	diff := latestRunUnifiedDiff(run, targetRC.WorkspaceDir)
	if strings.TrimSpace(diff) == "" {
		return domain.DeliveryMR{}, errors.New("当前运行没有可提交的代码变更补丁（repository-patch）")
	}

	candidates := []int64{actorUID, run.ExecutionContext.ResponsibleUserID}
	if hasIssue && issue.AssigneeUserID > 0 {
		candidates = append(candidates, issue.AssigneeUserID)
	}
	if pid != "" {
		if p, ok := d.repo.Project(pid); ok && p.OwnerID > 0 {
			candidates = append(candidates, p.OwnerID)
		}
	}
	gitUser, gitToken := d.resolveGitCredForHost(ctx, "github.com", candidates...)
	if strings.TrimSpace(gitToken) == "" {
		return domain.DeliveryMR{}, errors.New("未找到可用的 GitHub 访问令牌，请先在「设置 -> Git 配置」中绑定 GitHub Token")
	}

	cloneURL := fmt.Sprintf("https://github.com/%s/%s.git", ghOwner, ghRepo)
	env := gitAuthEnvForURL(cloneURL, gitUser, gitToken)

	tmpDir, err := os.MkdirTemp("", "webuddy-gh-pr-*")
	if err != nil {
		return domain.DeliveryMR{}, fmt.Errorf("创建临时工作目录失败: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	opCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	preferredBase := strings.TrimSpace(targetRC.BaseBranch)
	if preferredBase == "" {
		preferredBase = strings.TrimSpace(targetRC.DefaultBranch)
	}

	runGit := func(dir string, args ...string) (string, error) {
		cmd := exec.CommandContext(opCtx, "git", args...)
		if dir != "" {
			cmd.Dir = dir
		}
		cmd.Env = env
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			redacted := strings.ReplaceAll(strings.TrimSpace(out.String()), gitToken, "***")
			return redacted, fmt.Errorf("%w: %s", err, redacted)
		}
		return strings.TrimSpace(out.String()), nil
	}

	repoDir := filepath.Join(tmpDir, "repo")
	cloned := false
	if preferredBase != "" {
		if _, err := runGit("", "clone", "--depth", "20", "--branch", preferredBase, cloneURL, repoDir); err == nil {
			cloned = true
		} else {
			_ = os.RemoveAll(repoDir)
		}
	}
	if !cloned {
		if _, err := runGit("", "clone", "--depth", "20", cloneURL, repoDir); err != nil {
			return domain.DeliveryMR{}, fmt.Errorf("克隆 GitHub 仓库失败: %v", err)
		}
	}

	actualBase, err := runGit(repoDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || actualBase == "" || actualBase == "HEAD" {
		if preferredBase != "" {
			actualBase = preferredBase
		} else {
			actualBase = "main"
		}
	}

	workBranch := strings.TrimSpace(targetRC.WorkBranch)
	if workBranch == "" || workBranch == actualBase {
		suffix := sanitizeRepoSlug(run.ID)
		if len(suffix) > 20 {
			suffix = suffix[len(suffix)-20:]
		}
		workBranch = "webuddy/pr-" + suffix
	}

	if _, err := runGit(repoDir, "checkout", "-B", workBranch); err != nil {
		return domain.DeliveryMR{}, fmt.Errorf("创建工作分支 %s 失败: %v", workBranch, err)
	}

	patchFile := filepath.Join(tmpDir, "changes.patch")
	if err := os.WriteFile(patchFile, []byte(diff), 0o600); err != nil {
		return domain.DeliveryMR{}, fmt.Errorf("写入补丁文件失败: %w", err)
	}

	if _, err := runGit(repoDir, "apply", "--3way", "--whitespace=nowarn", patchFile); err != nil {
		if _, err2 := runGit(repoDir, "apply", "--whitespace=nowarn", patchFile); err2 != nil {
			return domain.DeliveryMR{}, fmt.Errorf("应用代码补丁失败（基线可能已变更）: %v", err2)
		}
	}

	_, _ = runGit(repoDir, "config", "user.name", "Webuddy Bot")
	_, _ = runGit(repoDir, "config", "user.email", "webuddy-bot@users.noreply.github.com")
	if _, err := runGit(repoDir, "add", "-A"); err != nil {
		return domain.DeliveryMR{}, fmt.Errorf("暂存变更失败: %v", err)
	}
	statusOut, _ := runGit(repoDir, "status", "--porcelain")
	if strings.TrimSpace(statusOut) == "" {
		return domain.DeliveryMR{}, errors.New("补丁中的变更已存在于目标基线分支，无需重复提交")
	}

	prTitle := strings.TrimSpace(run.WorkflowSnapshot.Name)
	if hasIssue && strings.TrimSpace(issue.Title) != "" {
		prTitle = strings.TrimSpace(issue.Title)
	}
	if prTitle == "" {
		prTitle = "chore: automated delivery from Webuddy SOP (" + run.ID + ")"
	}
	commitMsg := prTitle + "\n\nDelivered by Webuddy Run " + run.ID
	if _, err := runGit(repoDir, "commit", "-m", commitMsg); err != nil {
		return domain.DeliveryMR{}, fmt.Errorf("提交 Git 变更失败: %v", err)
	}

	if _, err := runGit(repoDir, "push", "--force", "origin", "HEAD:refs/heads/"+workBranch); err != nil {
		return domain.DeliveryMR{}, fmt.Errorf("推送分支 %s 到 GitHub 失败: %v", workBranch, err)
	}

	prBody := fmt.Sprintf("## Webuddy 自动化交付 PR\n\n- **关联运行**: `%s`\n- **SOP 流程**: %s\n- **源分支**: `%s` → `%s`\n",
		run.ID,
		strings.TrimSpace(run.WorkflowSnapshot.Name),
		workBranch,
		actualBase,
	)
	if hasIssue {
		prBody += fmt.Sprintf("- **关联需求**: #%d %s\n", issue.Number, strings.TrimSpace(issue.Title))
	}

	prNum, prURL, prState, err := openOrFindGitHubPullRequest(opCtx, gitToken, ghOwner, ghRepo, prTitle, prBody, workBranch, actualBase)
	if err != nil {
		return domain.DeliveryMR{}, err
	}

	mr := domain.DeliveryMR{
		RepositoryID: targetRC.RepositoryID,
		Repository:   ghRepo,
		Project:      "github:" + ghOwner + "/" + ghRepo,
		IID:          prNum,
		URL:          prURL,
		State:        prState,
		TargetBranch: actualBase,
	}

	if hasIssue {
		list := issue.DeliveryMRList()
		replaced := false
		for idx := range list {
			if list[idx].URL == mr.URL || list[idx].Project == mr.Project {
				list[idx] = mr
				replaced = true
				break
			}
		}
		if !replaced {
			list = append([]domain.DeliveryMR{mr}, list...)
		}
		issue.SetDeliveryMRs(list)
		issue.DeliveryUpdatedAt = time.Now().UTC()
		if issue.Status == "todo" || issue.Status == "backlog" || issue.Status == "in_progress" {
			issue.Status = "in_review"
		}
		issue.AppendOperation(domain.IssueOperation{
			ActorUserID: actorUID,
			Action:      "create_github_pr",
			Detail:      mr.URL,
		})
		_ = d.repo.PutIssue(issue)
	}

	return mr, nil
}

func openOrFindGitHubPullRequest(ctx context.Context, token, owner, repo, title, body, headBranch, baseBranch string) (int, string, string, error) {
	apiBase := "https://api.github.com"
	payload, _ := json.Marshal(map[string]any{
		"title": title,
		"body":  body,
		"head":  headBranch,
		"base":  baseBranch,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/repos/%s/%s/pulls", apiBase, owner, repo), bytes.NewReader(payload))
	if err != nil {
		return 0, "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", "", fmt.Errorf("请求 GitHub Pull Request API 失败: %w", err)
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	var prResp struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
		Merged  bool   `json:"merged"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(respBytes, &prResp)

	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
		st := "opened"
		if prResp.Merged {
			st = "merged"
		} else if prResp.State == "closed" {
			st = "closed"
		}
		return prResp.Number, prResp.HTMLURL, st, nil
	}

	// If PR already exists for head -> base (HTTP 422), look it up via GET /pulls.
	if resp.StatusCode == http.StatusUnprocessableEntity {
		q := url.Values{}
		q.Set("state", "open")
		q.Set("head", owner+":"+headBranch)
		q.Set("base", baseBranch)
		listReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/%s/pulls?%s", apiBase, owner, repo, q.Encode()), nil)
		if err == nil {
			listReq.Header.Set("Authorization", "Bearer "+token)
			listReq.Header.Set("Accept", "application/vnd.github+json")
			if listResp, err := http.DefaultClient.Do(listReq); err == nil {
				defer listResp.Body.Close()
				var items []struct {
					Number  int    `json:"number"`
					HTMLURL string `json:"html_url"`
					State   string `json:"state"`
				}
				if lb, _ := io.ReadAll(io.LimitReader(listResp.Body, 64*1024)); json.Unmarshal(lb, &items) == nil && len(items) > 0 {
					return items[0].Number, items[0].HTMLURL, "opened", nil
				}
			}
		}
	}

	msg := strings.TrimSpace(prResp.Message)
	if msg == "" {
		msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return 0, "", "", fmt.Errorf("创建 GitHub PR 失败: %s", msg)
}
