package engine

import (
	"agentflow/internal/domain"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"
)

// assignWorkBranches gives every repository without a branch the first
// repository's work branch, else a generated one. A branch the caller
// explicitly continues or creates belongs to its own repository, and a
// read-only checkout has no work branch at all.
func assignWorkBranches(repositories []domain.RepositoryContext, generate func() string) {
	if len(repositories) == 0 {
		return
	}
	workBranch := ""
	if first := repositories[0]; !first.RequireExistingWorkBranch && !first.CreateNewWorkBranch && !first.ReadOnly {
		workBranch = strings.TrimSpace(first.WorkBranch)
	}
	for i := range repositories {
		if repositories[i].ReadOnly {
			repositories[i].WorkBranch = ""
			continue
		}
		if strings.TrimSpace(repositories[i].WorkBranch) == "" {
			if workBranch == "" {
				workBranch = generate()
			}
			repositories[i].WorkBranch = workBranch
		}
	}
}

func (e *Engine) Start(workflowID, input string) (domain.Run, error) {
	return e.StartWithContext(workflowID, input, domain.RepositoryContext{})
}

func (e *Engine) StartWithContext(workflowID, input string, repository domain.RepositoryContext) (domain.Run, error) {
	return e.StartWithRepositoriesContext(workflowID, input, []domain.RepositoryContext{repository}, domain.TelemetryContext{})
}

func (e *Engine) StartWithExecutionContext(workflowID, input string, repository domain.RepositoryContext, executionContext domain.TelemetryContext) (domain.Run, error) {
	return e.StartWithRepositoriesContext(workflowID, input, []domain.RepositoryContext{repository}, executionContext)
}

// StartWithRepositoriesContext starts a run with one or more repositories.
// All repositories are equals: each is checked out into a first-level
// sub-directory, gets the same work branch and, on scheduled runs, its own
// integration merge and MR. SOP deliverables stay at the run workspace root.
func (e *Engine) StartWithRepositoriesContext(workflowID, input string, repositories []domain.RepositoryContext, executionContext domain.TelemetryContext) (domain.Run, error) {
	return e.startWorkflow(workflowID, input, repositories, executionContext, false)
}

func (e *Engine) startWorkflow(workflowID, input string, repositories []domain.RepositoryContext, executionContext domain.TelemetryContext, workspace bool) (domain.Run, error) {
	wf, ok := e.repo.Workflow(workflowID)
	if !ok {
		return domain.Run{}, fmt.Errorf("workflow not found")
	}
	return e.startWorkflowDefinition(wf, input, repositories, executionContext, workspace)
}

// startWorkflowDefinition also accepts a run-only conversation workspace definition.
// It never writes a template to the catalog.
func (e *Engine) startWorkflowDefinition(wf domain.WorkflowDefinition, input string, repositories []domain.RepositoryContext, executionContext domain.TelemetryContext, workspace bool) (domain.Run, error) {
	workflowID := wf.ID
	repositories = normalizeRepositories(repositories)
	domain.AssignRepositoryDirs(repositories)
	e.mu.Lock()
	draining, executeRuns := e.draining, e.executeRuns
	instanceID, ttl := e.instanceID, e.leaseTTL
	e.mu.Unlock()
	if draining {
		return domain.Run{}, ErrExecutionDisabled
	}
	wf.CollaborationMode = ""
	wf.TaskTitle = ""
	wf.GroupMembers = nil
	wf.SOPCheckpoint = nil
	wf.GroupSOPHandoff = nil
	wf.GroupHistory = nil
	if wf.Generic {
		if strings.TrimSpace(executionContext.ProjectID) == "" {
			return domain.Run{}, fmt.Errorf("直接运行通用模板需要先选择项目")
		}
		if _, found := e.repo.Project(executionContext.ProjectID); !found {
			return domain.Run{}, fmt.Errorf("项目 %q 不存在，请刷新后重试", executionContext.ProjectID)
		}
	}
	wf = defaultWorkflowSandboxProfile(wf)
	wf, err := configureBaselineAlignCLI(wf)
	if err != nil {
		return domain.Run{}, err
	}
	wf, err = domain.AttachPreviewBuild(wf)
	if err != nil {
		return domain.Run{}, err
	}
	if err := validate(wf); err != nil {
		return domain.Run{}, err
	}
	profile, ok := e.repo.Sandbox(wf.SandboxProfileID)
	if !ok {
		return domain.Run{}, fmt.Errorf("sandbox profile %q not found", wf.SandboxProfileID)
	}
	if workspace {
		if _, err := e.repo.ConversationSandbox(executionContext.ProjectID, profile.ID); err != nil {
			return domain.Run{}, err
		}
	}
	resources, err := resolveSandboxResources(e.repo, profile)
	if err != nil {
		return domain.Run{}, err
	}
	e.selectSOPExecutionTarget(&executionContext, repositories...)
	wf.ExecutorDefaults = nil
	if executionContext.ResponsibleUserID > 0 {
		wf.ExecutorDefaults, _, err = e.planSOPExecutors(wf, executionContext, false)
		if err != nil {
			return domain.Run{}, err
		}
	}
	if _, err := workflowSandboxRequirements(e.repo, wf); err != nil {
		return domain.Run{}, err
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("run-%d", now.UnixNano())
	assignWorkBranches(repositories, func() string {
		issueID := strings.TrimSpace(executionContext.IssueID)
		if issueID != "" {
			if issue, ok := e.repo.Issue(issueID); ok {
				return domain.DefaultWorkBranch(issue, now)
			}
		}
		return defaultWorkBranch(executionContext.IssueNumber, issueID, now)
	})
	var firstRepository domain.RepositoryContext
	if len(repositories) > 0 {
		firstRepository = repositories[0]
	}
	executionContext.RunID, executionContext.TraceID, executionContext.WorkflowID, executionContext.WorkflowVersion = id, id, workflowID, wf.Version
	if err := e.requireWorkBranchCapabilities(executionContext.ExecutionRunnerID, repositories); err != nil {
		return domain.Run{}, err
	}
	wf.SandboxSnapshot = resolvedSandboxSnapshot(profile, resources)
	r := domain.Run{
		ID: id, WorkflowID: workflowID, WorkflowSnapshot: wf, Status: "running", Input: input,
		Repository: firstRepository, ExecutionContext: executionContext, Nodes: map[string]*domain.NodeRun{},
		CreatedAt: now, UpdatedAt: now,
	}
	if len(repositories) > 0 {
		r.Repositories = append([]domain.RepositoryContext(nil), repositories...)
	}
	for _, n := range wf.Nodes {
		r.Nodes[n.ID] = &domain.NodeRun{NodeID: n.ID, Status: "pending"}
	}
	if executionContext.ParentRunID != "" {
		parent, exists := e.repo.RunState(executionContext.ParentRunID)
		if !exists {
			return domain.Run{}, fmt.Errorf("父 SOP 运行不存在")
		}
		if err := inheritSOPHandoff(parent, &r); err != nil {
			return domain.Run{}, err
		}
	}
	if workspace {
		r.Kind = domain.RunKindWorkspace
		r.Status = "waiting"
	}
	if workspace {
		e.event(&r, "", "workspace.created", "对话工作区已创建")
	} else {
		e.event(&r, "", "run.started", "流水线开始执行")
	}
	if executeRuns && !workspace {
		if strings.TrimSpace(instanceID) == "" || ttl <= 0 {
			return domain.Run{}, ErrExecutionDisabled
		}
		r.OwnerInstanceID, r.Generation, r.LeaseUntil = instanceID, 1, now.Add(ttl)
	}
	// Control plane persists an unowned running row. The worker claims it
	// through ResumeInterruptedRuns. Combined instances still insert already
	// leased so another process cannot start a second execute() on the same run.
	if err := e.repo.PutRun(r); err != nil {
		return domain.Run{}, err
	}
	if current, ok := e.repo.RunState(r.ID); ok {
		r = current
	}
	e.publish(r.ID)
	if !executeRuns || r.Status == "queued" || workspace {
		return r, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	if !e.claimRunning(id, cancel) {
		cancel()
		return r, nil
	}
	go e.execute(ctx, id)
	return r, nil
}

func (e *Engine) selectSOPExecutionTarget(executionContext *domain.TelemetryContext, repositories ...domain.RepositoryContext) {
	if executionContext.ParentRunID == "" && !executionContext.ExecutionPinned {
		executionContext.ExecutionRunnerID = e.repo.DefaultRunner(executionContext.ResponsibleUserID)
		executionContext.WorkspaceRunID = ""
	}
	// Archive / local-path-only repositories live on the control-plane host disk
	// (e.g. /opt/webuddy-loopforge/uploaded-repos/<slug>) and cannot be cloned from
	// a remote laptop/WSL runner. Route them to the co-located host runner (if online)
	// or directly to control-plane cloud mode.
	if !executionContext.ExecutionPinned && hasControlPlaneHostedLocalRepo(repositories) {
		if hostRunnerID := e.coLocatedHostRunnerID(); hostRunnerID != "" {
			executionContext.ExecutionRunnerID = hostRunnerID
		} else if executionContext.ExecutionRunnerID != "" {
			if runner, ok := e.repo.Runner(executionContext.ExecutionRunnerID); !ok || runner.Status != domain.RunnerStatusOnline || isRemoteNonHostRunner(runner) {
				executionContext.ExecutionRunnerID = ""
			}
		}
	}
	if executionContext.ExecutionRunnerID == "" && !executionContext.ExecutionPinned {
		if runner, ok := e.cloudPoolRunner(executionContext.ProjectID); ok {
			executionContext.ExecutionRunnerID = runner.ID
			executionContext.ExecutionPool = domain.ExecutionModeCloud
		} else if e.cloudPoolRequired(executionContext.ProjectID) {
			// The first node binds a pool runner once one is online.
			executionContext.ExecutionPool = domain.ExecutionModeCloud
		} else if sharedID := domain.SharedHostRunnerID(); sharedID != "" {
			if shared, ok := e.repo.Runner(sharedID); ok && shared.Status == domain.RunnerStatusOnline && shared.DefaultSelectionBlocker() == "" {
				executionContext.ExecutionRunnerID = shared.ID
			}
		}
	}
	if executionContext.ExecutionRunnerID != "" || executionContext.ExecutionPool == domain.ExecutionModeCloud {
		executionContext.ExecutionMode = domain.ExecutionModeRunner
		executionContext.RunnerWorkspaceLayout = domain.RunnerWorkspaceLayoutShared
	} else {
		executionContext.ExecutionMode = domain.ExecutionModeCloud
	}
}

func hasControlPlaneHostedLocalRepo(repositories []domain.RepositoryContext) bool {
	for _, rc := range repositories {
		if strings.TrimSpace(rc.CloneURL) != "" {
			continue
		}
		lp := strings.TrimSpace(rc.LocalPath)
		if lp == "" {
			continue
		}
		isHostedProvider := rc.Provider == "archive" || rc.Provider == "local" || strings.Contains(filepath.ToSlash(lp), "/uploaded-repos/")
		if !isHostedProvider {
			continue
		}
		if info, err := os.Stat(lp); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

func isRemoteNonHostRunner(r domain.Runner) bool {
	if r.OS != "" && r.OS != goruntime.GOOS {
		return true
	}
	upper := strings.ToUpper(r.Name)
	return strings.Contains(upper, "WSL") || strings.Contains(upper, "WINDOWS") || strings.Contains(upper, "MAC")
}

func (e *Engine) coLocatedHostRunnerID() string {
	if envID := strings.TrimSpace(os.Getenv("AGENTFLOW_LOCAL_RUNNER_ID")); envID != "" {
		if r, ok := e.repo.Runner(envID); ok && r.Status == domain.RunnerStatusOnline {
			return r.ID
		}
	}
	if raw, err := os.ReadFile("runner-state.json"); err == nil {
		var st struct {
			RunnerID string `json:"runnerId"`
		}
		if json.Unmarshal(raw, &st) == nil && strings.TrimSpace(st.RunnerID) != "" {
			if r, ok := e.repo.Runner(strings.TrimSpace(st.RunnerID)); ok && r.Status == domain.RunnerStatusOnline {
				return r.ID
			}
		}
	}
	for _, r := range e.repo.Runners() {
		if r.Status != domain.RunnerStatusOnline || isRemoteNonHostRunner(r) {
			continue
		}
		if r.OS == goruntime.GOOS && strings.Contains(r.Name, "本机") {
			return r.ID
		}
	}
	return ""
}

func (e *Engine) executes() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.executeRuns && !e.draining
}
