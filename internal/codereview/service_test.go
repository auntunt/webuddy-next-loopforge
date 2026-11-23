package codereview_test

import (
	"context"
	"strings"
	"testing"

	"agentflow/internal/apperr"
	"agentflow/internal/codereview"
	"agentflow/internal/domain"
	"agentflow/internal/repo"
)

func TestSplitDiffPartsPreservesEveryCharacter(t *testing.T) {
	fileA := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n+" + strings.Repeat("x", 30) + "\n"
	fileB := "diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n+" + strings.Repeat("中", 25) + "\n"
	full := fileA + fileB

	parts := codereview.SplitDiffParts(full, 20)
	if len(parts) < 4 {
		t.Fatalf("expected >=4 parts, got %d", len(parts))
	}
	var rebuilt strings.Builder
	for _, p := range parts {
		rebuilt.WriteString(p.Text)
	}
	if rebuilt.String() != full {
		t.Fatalf("rebuilt diff mismatch: got len=%d want len=%d", rebuilt.Len(), len(full))
	}
	if !strings.HasPrefix(parts[0].Header, "diff --git a/a.go") {
		t.Fatalf("unexpected first header: %q", parts[0].Header)
	}
}

func errKind(err error) apperr.Kind {
	if ae, ok := apperr.As(err); ok {
		return ae.Kind
	}
	return 0
}

func TestCodeReviewAndFeedbackInvariants(t *testing.T) {
	store, err := repo.OpenTest()
	if err != nil {
		t.Fatalf("OpenTest: %v", err)
	}
	defer store.Close()
	seq := 0
	svc := codereview.NewService(store, func() string {
		seq++
		return "id-" + string(rune('0'+seq))
	})

	diff := "diff --git a/auth.go b/auth.go\n--- a/auth.go\n+++ b/auth.go\n@@ -1,2 +1,3 @@\n+func checkAuth() {}\n"
	rec, err := svc.CreateReview(context.Background(), codereview.CreateReviewInput{
		ProjectID: "proj-1",
		Title:     "权限边界审查",
		Diff:      diff,
		Writer:    "codex",
		Reviewer:  "claude-code",
		Blocking:  []string{"auth.go:1 缺少租户校验"},
		Advisory:  []string{"建议补充单元测试"},
	}, "alice")
	if err != nil {
		t.Fatalf("CreateReview: %v", err)
	}
	if rec.Verdict != "fail" || !rec.DifferentExecutor || !rec.Complete {
		t.Fatalf("unexpected review state: %+v", rec)
	}
	if rec.Binding == "" {
		t.Fatalf("expected non-empty binding")
	}

	// Non-human executor must be rejected with Forbidden.
	mins := 15
	_, err = svc.SaveFeedback(rec.ID, "codex", false, codereview.FeedbackInput{
		RequestID: "req-1",
		Binding:   rec.Binding,
		Minutes:   &mins,
		Findings: []domain.CodeReviewFindingJudgment{
			{ID: "blocking:0", Verdict: "valid", Reason: "确实漏了租户隔离"},
			{ID: "advisory:0", Verdict: "false_positive", Reason: "已有集成测试覆盖"},
		},
		Missed: []string{"auth.go:2 未记录审计日志"},
		Note:   "人工核对了 auth.go 全部路径",
	})
	if errKind(err) != apperr.Forbidden {
		t.Fatalf("expected Forbidden for non-human, got %v", err)
	}

	// Valid human feedback succeeds.
	fbInput := codereview.FeedbackInput{
		RequestID: "req-1",
		Binding:   rec.Binding,
		Minutes:   &mins,
		Findings: []domain.CodeReviewFindingJudgment{
			{ID: "blocking:0", Verdict: "valid", Reason: "确实漏了租户隔离"},
			{ID: "advisory:0", Verdict: "false_positive", Reason: "已有集成测试覆盖"},
		},
		Missed: []string{"auth.go:2 未记录审计日志"},
		Note:   "人工核对了 auth.go 全部路径",
	}
	fb1, err := svc.SaveFeedback(rec.ID, "alice", true, fbInput)
	if err != nil {
		t.Fatalf("SaveFeedback: %v", err)
	}
	// Idempotent replay with same requestId and payload returns same record.
	fb2, err := svc.SaveFeedback(rec.ID, "alice", true, fbInput)
	if err != nil || fb2.ID != fb1.ID {
		t.Fatalf("expected idempotent replay, got fb2=%+v err=%v", fb2, err)
	}
	// Same requestId with different payload must return Conflict.
	fbModified := fbInput
	fbModified.Note = "篡改内容"
	if _, err := svc.SaveFeedback(rec.ID, "alice", true, fbModified); errKind(err) != apperr.Conflict {
		t.Fatalf("expected Conflict on changed payload, got %v", err)
	}
	// Wrong binding must return Conflict.
	fbStale := fbInput
	fbStale.RequestID = "req-2"
	fbStale.Binding = "stale-binding"
	if _, err := svc.SaveFeedback(rec.ID, "alice", true, fbStale); errKind(err) != apperr.Conflict {
		t.Fatalf("expected Conflict on stale binding, got %v", err)
	}

	// Summary valid_rate should be 50.0% (1 valid, 1 false_positive).
	sum := svc.FeedbackSummary("proj-1")
	if sum.Valid != 1 || sum.FalsePositive != 1 || sum.Missed != 1 || sum.ValidRate == nil || *sum.ValidRate != 50.0 {
		t.Fatalf("unexpected summary: %+v", sum)
	}
}

func TestBinaryDiffMarksIncompleteUnknown(t *testing.T) {
	store, err := repo.OpenTest()
	if err != nil {
		t.Fatalf("OpenTest: %v", err)
	}
	defer store.Close()
	svc := codereview.NewService(store, func() string { return "bin-1" })
	rec, err := svc.CreateReview(context.Background(), codereview.CreateReviewInput{
		ProjectID: "proj-1",
		Diff:      "diff --git a/logo.png b/logo.png\nBinary files a/logo.png and b/logo.png differ\n",
	}, "alice")
	if err != nil {
		t.Fatalf("CreateReview: %v", err)
	}
	if !rec.BinaryDetected || rec.Complete || rec.Verdict != "unknown" {
		t.Fatalf("expected binary incomplete unknown, got %+v", rec)
	}
}

type fakeDiagnoser struct {
	calls int
}

func (f *fakeDiagnoser) DiagnoseBatch(_ context.Context, req codereview.BatchRequest) (codereview.BatchDiagnosis, error) {
	f.calls++
	if f.calls == 1 {
		return codereview.ParseBatchDiagnosis("```json\n{\"verdict\":\"fail\",\"blocking\":[\"a.go:10 缺少权限校验\"],\"advisory\":[\"可补充注释\"],\"ac\":[{\"id\":\"AC-1\",\"result\":\"fail\",\"evidence\":\"未鉴权\"}],\"scope\":{\"new\":[\"AC-1\"]},\"evidence\":\"已核查 a.go\",\"cause\":\"缺少鉴权\"}\n```")
	}
	return codereview.ParseBatchDiagnosis(`{"verdict":"pass","advisory":["b.go 风格良好"],"ac":[{"id":"AC-2","result":"pass","evidence":"测试覆盖"}]}`)
}

func TestRunAutomatedReviewBatchedAggregation(t *testing.T) {
	store, err := repo.OpenTest()
	if err != nil {
		t.Fatalf("OpenTest: %v", err)
	}
	defer store.Close()
	svc := codereview.NewService(store, func() string { return "auto-1" })
	diff := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n+func a() {}\n" +
		"diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n+func b() {}\n"

	diag := &fakeDiagnoser{}
	rec, err := svc.RunAutomatedReview(context.Background(), codereview.RunAutomatedReviewInput{
		ProjectID: "proj-1",
		Diff:      diff,
		Writer:    "codex",
		Reviewer:  "claude",
	}, "alice", diag)
	if err != nil {
		t.Fatalf("RunAutomatedReview: %v", err)
	}
	if diag.calls != 2 || rec.TotalParts != 2 || !rec.Complete || !rec.DifferentExecutor {
		t.Fatalf("unexpected automated review: calls=%d rec=%+v", diag.calls, rec)
	}
	if rec.Verdict != "fail" || len(rec.Blocking) != 1 || len(rec.Advisory) != 2 || len(rec.AC) != 2 {
		t.Fatalf("unexpected aggregated findings: %+v", rec)
	}
}
