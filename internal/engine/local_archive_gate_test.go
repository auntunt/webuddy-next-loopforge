package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentflow/internal/domain"
)

func TestDetectArtifactBlockerReason(t *testing.T) {
	ws := t.TempDir()
	reportPath := filepath.Join(ws, "summary-report.md")
	if err := os.WriteFile(reportPath, []byte("# 报告\n\n本次交付未完成，修复验收未通过。指定仓库未出现在本工作区。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	node := domain.WorkflowNode{
		ID: "report",
		ArtifactOutputs: []domain.ArtifactContract{
			{Name: "summary-report", Path: "summary-report.md", Required: true},
		},
	}
	got := detectArtifactBlockerReason(workspaceOutputs(ws), domain.Run{}, node, "")
	if got == "" {
		t.Fatal("expected blocker reason for blocked summary-report.md, got empty")
	}

	if err := os.WriteFile(reportPath, []byte("# 交付完成报告\n\n已完成路由修复并通过全部回归验证。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if clean := detectArtifactBlockerReason(workspaceOutputs(ws), domain.Run{}, node, ""); clean != "" {
		t.Fatalf("expected clean report to have empty blocker, got %q", clean)
	}
}

func TestArchiveRepoRoutesToCoLocatedHostRunnerAndBindsLocalPath(t *testing.T) {
	e, _ := cvEEngine(t)
	now := time.Now().UTC()
	remoteWSL := domain.Runner{
		ID:        "runner-wsl-remote",
		Name:      "Windows WSL2 测试机",
		OS:        "linux",
		Arch:      "amd64",
		Status:    domain.RunnerStatusOnline,
		CreatedAt: now,
		UpdatedAt: now,
	}
	hostLocal := domain.Runner{
		ID:        "runner-43-local",
		Name:      "43 服务器本机 (Claude + Codex)",
		OS:        "linux",
		Arch:      "amd64",
		Status:    domain.RunnerStatusOnline,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := e.repo.PutRunner(remoteWSL); err != nil {
		t.Fatal(err)
	}
	if err := e.repo.PutRunner(hostLocal); err != nil {
		t.Fatal(err)
	}
	if err := e.repo.SetDefaultRunner(1, remoteWSL.ID, now); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTFLOW_LOCAL_RUNNER_ID", hostLocal.ID)

	archiveDir := t.TempDir()
	repos := []domain.RepositoryContext{{
		RepositoryID: "repo-archive-we-earn",
		Provider:     "archive",
		Name:         "we-earn",
		LocalPath:    archiveDir,
		BaseBranch:   "main",
		WorkspaceDir: "we-earn",
	}}
	ec := domain.TelemetryContext{ResponsibleUserID: 1, ProjectID: "proj-we-earn"}
	e.selectSOPExecutionTarget(&ec, repos...)
	if ec.ExecutionRunnerID != hostLocal.ID {
		t.Fatalf("expected archive repo to route to co-located host runner %q, got %q", hostLocal.ID, ec.ExecutionRunnerID)
	}

	inv := &domain.RunnerInvocation{}
	bindRunnerRepositories(inv, domain.Run{Repositories: repos}, "tester", "tester@example.com")
	if len(inv.Repositories) != 1 || inv.Repositories[0].CloneURL != archiveDir || inv.Repositories[0].Dir != "we-earn" {
		t.Fatalf("expected archive repo LocalPath bound into RunnerInvocation.Repositories, got %+v", inv.Repositories)
	}
}
