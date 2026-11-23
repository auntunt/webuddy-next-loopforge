package codereview

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"agentflow/internal/apperr"
	"agentflow/internal/domain"
)

const DefaultBatchTimeout = 5 * time.Minute

// BatchRequest is passed to a BatchDiagnoser for one 14k diff slice.
type BatchRequest struct {
	Index    int
	Total    int
	Header   string
	DiffText string
	Prompt   string
	RepoPath string
	Executor string
	Model    string
}

// BatchDiagnosis is the structured verdict returned for one diff batch.
type BatchDiagnosis struct {
	Verdict  string                 `json:"verdict"` // pass | fail | unknown
	Blocking []string               `json:"blocking"`
	Advisory []string               `json:"advisory"`
	AC       []domain.CodeReviewAC  `json:"ac"`
	Scope    domain.CodeReviewScope `json:"scope"`
	Evidence string                 `json:"evidence"`
	Cause    string                 `json:"cause"`
	Guide    string                 `json:"guide"`
	Text     string                 `json:"text"`
}

// BatchDiagnoser runs one review batch against an AI reviewer (real CLI or test double).
type BatchDiagnoser interface {
	DiagnoseBatch(ctx context.Context, req BatchRequest) (BatchDiagnosis, error)
}

// ExecutorInfo reports whether a supported coding-agent CLI is installed locally
// and whether local login credentials are detected (without exposing secret values).
type ExecutorInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Binary    string `json:"binary"`
	Available bool   `json:"available"`
	LoggedIn  bool   `json:"loggedIn"`
	AuthHint  string `json:"authHint,omitempty"`
	Path      string `json:"path,omitempty"`
}

var supportedExecutors = []struct {
	id     string
	name   string
	binary string
}{
	{id: "codex", name: "Codex CLI (read-only sandbox)", binary: "codex"},
	{id: "claude", name: "Claude Code CLI", binary: "claude"},
	{id: "codebuddy", name: "CodeBuddy CLI", binary: "codebuddy"},
	{id: "opencode", name: "OpenCode CLI", binary: "opencode"},
	{id: "cursor", name: "Cursor CLI", binary: "cursor"},
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func detectLocalAuth(id string) (bool, string) {
	home, _ := os.UserHomeDir()
	switch id {
	case "codex":
		if fileExists(filepath.Join(home, ".codex", "auth.json")) {
			return true, "复用本机 ~/.codex 登录态"
		}
		if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "" {
			return true, "复用环境变量 OPENAI_API_KEY"
		}
		return false, "未检测到 ~/.codex/auth.json"
	case "claude":
		if fileExists(filepath.Join(home, ".claude", ".credentials.json")) || fileExists(filepath.Join(home, ".claude.json")) {
			return true, "复用本机 Claude 登录态"
		}
		if strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != "" {
			return true, "复用环境变量 ANTHROPIC_API_KEY"
		}
		return false, "未检测到本机 Claude 登录态"
	case "codebuddy":
		if fileExists(filepath.Join(home, ".codebuddy", "config.json")) || strings.TrimSpace(os.Getenv("CODEBUDDY_API_KEY")) != "" {
			return true, "复用本机 CodeBuddy 登录态"
		}
		return false, ""
	case "opencode":
		if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "" || strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != "" {
			return true, "复用环境变量凭据"
		}
		return false, ""
	case "cursor":
		if fileExists(filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")) {
			return true, "复用本机 Cursor 登录态"
		}
		return false, ""
	default:
		return false, ""
	}
}

// DetectExecutors checks which review CLIs are available in PATH and whether
// local credentials exist.
func DetectExecutors() []ExecutorInfo {
	out := make([]ExecutorInfo, 0, len(supportedExecutors))
	for _, item := range supportedExecutors {
		path, err := exec.LookPath(item.binary)
		avail := err == nil && path != ""
		loggedIn, hint := false, ""
		if avail {
			loggedIn, hint = detectLocalAuth(item.id)
		}
		out = append(out, ExecutorInfo{
			ID:        item.id,
			Name:      item.name,
			Binary:    item.binary,
			Available: avail,
			LoggedIn:  loggedIn,
			AuthHint:  hint,
			Path:      path,
		})
	}
	return out
}

// CLIExecutor invokes real coding-agent CLIs in non-interactive review mode.
type CLIExecutor struct {
	Timeout time.Duration
}

func NewCLIExecutor() *CLIExecutor {
	return &CLIExecutor{Timeout: DefaultBatchTimeout}
}

func (e *CLIExecutor) DiagnoseBatch(ctx context.Context, req BatchRequest) (BatchDiagnosis, error) {
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = DefaultBatchTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	fullPrompt := req.Prompt + "\n\n" + jsonSchemaInstruction
	bin, args, err := buildCLIInvocation(req.Executor, req.Model, fullPrompt)
	if err != nil {
		return BatchDiagnosis{}, err
	}
	if _, lookErr := exec.LookPath(bin); lookErr != nil {
		return BatchDiagnosis{}, apperr.Newf(apperr.Unavailable, "审查执行器 %s 未安装或不在 PATH 中", bin)
	}

	cmd := exec.CommandContext(cctx, bin, args...)
	if strings.TrimSpace(req.RepoPath) != "" {
		cmd.Dir = strings.TrimSpace(req.RepoPath)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if runErr := cmd.Run(); runErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = runErr.Error()
		}
		return BatchDiagnosis{Verdict: "unknown", Text: msg}, fmt.Errorf("executor %s failed: %s", req.Executor, msg)
	}

	raw := strings.TrimSpace(stdout.String())
	diag, parseErr := ParseBatchDiagnosis(raw)
	if parseErr != nil {
		return BatchDiagnosis{Verdict: "unknown", Text: raw}, nil
	}
	return diag, nil
}

func buildCLIInvocation(executor, model, prompt string) (string, []string, error) {
	execID := strings.ToLower(strings.TrimSpace(executor))
	m := strings.TrimSpace(model)
	switch execID {
	case "codex":
		args := []string{"exec", "--sandbox", "read-only", "--skip-git-repo-check"}
		if m != "" {
			args = append(args, "-m", m)
		}
		args = append(args, prompt)
		return "codex", args, nil
	case "claude", "claude-code":
		args := []string{"-p", "--output-format", "text"}
		if m != "" {
			args = append(args, "--model", m)
		}
		args = append(args, prompt)
		return "claude", args, nil
	case "codebuddy":
		args := []string{"-p"}
		if m != "" {
			args = append(args, "--model", m)
		}
		args = append(args, prompt)
		return "codebuddy", args, nil
	case "opencode":
		args := []string{"run"}
		if m != "" {
			args = append(args, "--model", m)
		}
		args = append(args, prompt)
		return "opencode", args, nil
	default:
		return "", nil, apperr.Newf(apperr.Invalid, "不支持的审查执行器: %s（支持 codex / claude / codebuddy / opencode）", executor)
	}
}

const jsonSchemaInstruction = `请严格只输出一个 JSON 对象（可用 ` + "```json" + ` 包裹），字段如下：
{
  "verdict": "pass" | "fail" | "unknown",
  "blocking": ["文件:行号 触发条件与实际影响（仅阻断项）"],
  "advisory": ["建议项（不阻断交付）"],
  "ac": [{"id": "AC-1", "result": "pass" | "fail" | "unverified", "evidence": "依据"}],
  "scope": {"new": [], "regression": [], "invariants": [], "gaps": []},
  "evidence": "本批核查证据摘要",
  "cause": "失败根因（verdict=fail 时必填）",
  "guide": "修复建议"
}`

var jsonFenceRE = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")

// ParseBatchDiagnosis extracts structured JSON diagnosis from raw CLI stdout.
func ParseBatchDiagnosis(raw string) (BatchDiagnosis, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return BatchDiagnosis{Verdict: "unknown"}, fmt.Errorf("empty output")
	}

	candidates := []string{trimmed}
	if m := jsonFenceRE.FindStringSubmatch(trimmed); len(m) > 1 {
		candidates = append([]string{m[1]}, candidates...)
	}
	if first, last := strings.IndexByte(trimmed, '{'), strings.LastIndexByte(trimmed, '}'); first >= 0 && last > first {
		candidates = append(candidates, trimmed[first:last+1])
	}

	for _, c := range candidates {
		var d BatchDiagnosis
		if err := json.Unmarshal([]byte(c), &d); err == nil {
			v := normalizeVerdict(d.Verdict)
			if v == "" {
				v = "unknown"
			}
			d.Verdict = v
			d.Text = trimmed
			return d, nil
		}
	}
	return BatchDiagnosis{Verdict: "unknown", Text: trimmed}, fmt.Errorf("no valid diagnosis JSON found")
}

// RunAutomatedReviewInput triggers a real batched AI code review via a CLI executor.
type RunAutomatedReviewInput struct {
	ProjectID  string `json:"projectId"`
	IssueID    string `json:"issueId"`
	RunID      string `json:"runId"`
	Title      string `json:"title"`
	RepoPath   string `json:"repoPath"`
	BaseRef    string `json:"baseRef"`
	HeadRef    string `json:"headRef"`
	Diff       string `json:"diff"`
	WriterKind string `json:"writerKind"`
	Writer     string `json:"writer"`
	Reviewer   string `json:"reviewer"` // codex | claude | codebuddy | opencode
	Model      string `json:"model"`
}

// RunAutomatedReview executes Webuddy's exact batched independent diff review loop:
//   - splits complete diff into 14k parts (never dropping a character)
//   - calls independent reviewer CLI per batch
//   - verifies git HEAD did not mutate mid-review (same_base)
//   - detects binary patches and uncovered batches
//   - aggregates blocking/advisory/AC/scope/verdict and persists tamper-evident Binding
func (s *Service) RunAutomatedReview(ctx context.Context, input RunAutomatedReviewInput, actor string, diagnoser BatchDiagnoser) (domain.CodeReviewRecord, error) {
	if diagnoser == nil {
		diagnoser = NewCLIExecutor()
	}
	reviewer := strings.TrimSpace(input.Reviewer)
	if reviewer == "" {
		return domain.CodeReviewRecord{}, apperr.New(apperr.Invalid, "请选择独立审查执行器（如 codex / claude / codebuddy / opencode）")
	}

	diff := input.Diff
	repoPath := strings.TrimSpace(input.RepoPath)
	baseCommit := strings.TrimSpace(input.BaseRef)
	headCommit := strings.TrimSpace(input.HeadRef)
	trackedHeadBefore := ""

	if repoPath != "" {
		if h, err := gitOutput(ctx, repoPath, "rev-parse", "HEAD"); err == nil {
			trackedHeadBefore = h
		}
	}

	if strings.TrimSpace(diff) == "" && repoPath != "" {
		if baseCommit == "" {
			baseCommit = "HEAD~1"
		}
		if headCommit == "" {
			headCommit = "HEAD"
		}
		var err error
		baseCommit, err = gitOutput(ctx, repoPath, "rev-parse", baseCommit)
		if err != nil {
			return domain.CodeReviewRecord{}, apperr.New(apperr.Invalid, "无法解析基准提交: "+err.Error())
		}
		headCommit, err = gitOutput(ctx, repoPath, "rev-parse", headCommit)
		if err != nil {
			return domain.CodeReviewRecord{}, apperr.New(apperr.Invalid, "无法解析目标提交: "+err.Error())
		}
		diff, err = gitOutputRaw(ctx, repoPath, "diff", baseCommit+".."+headCommit)
		if err != nil {
			return domain.CodeReviewRecord{}, apperr.New(apperr.Invalid, "获取 git diff 失败: "+err.Error())
		}
	}

	if strings.TrimSpace(diff) == "" {
		return domain.CodeReviewRecord{}, apperr.New(apperr.Invalid, "请提供 Unified Diff 或指定有改动的本地 Git 仓库")
	}

	batches := SplitDiffParts(diff, PartSize)
	binary := DetectBinary(diff)

	parts := make([]domain.CodeReviewPart, 0, len(batches))
	answers := make([]BatchDiagnosis, 0, len(batches))
	failedCall := false

	for i, b := range batches {
		prompt := BuildBatchPrompt(i+1, len(batches), b.Header, b.Text)
		diag, err := diagnoser.DiagnoseBatch(ctx, BatchRequest{
			Index:    i + 1,
			Total:    len(batches),
			Header:   b.Header,
			DiffText: b.Text,
			Prompt:   prompt,
			RepoPath: repoPath,
			Executor: reviewer,
			Model:    strings.TrimSpace(input.Model),
		})
		if err != nil {
			failedCall = true
			diag.Verdict = "unknown"
		}
		if diag.Verdict != "pass" && diag.Verdict != "fail" {
			diag.Verdict = "unknown"
		}
		answers = append(answers, diag)
		parts = append(parts, domain.CodeReviewPart{
			Part:       i + 1,
			File:       b.Header,
			Characters: len([]rune(b.Text)),
			SHA256:     b.SHA256,
			Verdict:    diag.Verdict,
		})
		if failedCall {
			break
		}
	}

	sameBase := true
	if trackedHeadBefore != "" {
		if trackedHeadAfter, err := gitOutput(ctx, repoPath, "rev-parse", "HEAD"); err == nil && trackedHeadAfter != trackedHeadBefore {
			sameBase = false
		}
	}

	allPartsResolved := len(batches) > 0 && len(parts) == len(batches)
	for _, p := range parts {
		if p.Verdict == "unknown" {
			allPartsResolved = false
		}
	}
	complete := sameBase && allPartsResolved && !binary

	var uncovered []string
	for i, b := range batches {
		if i >= len(parts) {
			uncovered = append(uncovered, fmt.Sprintf("第 %d 批 %s：未送审", i+1, b.Header))
		} else if parts[i].Verdict == "unknown" {
			uncovered = append(uncovered, fmt.Sprintf("第 %d 批 %s：未形成有效结论", i+1, b.Header))
		}
	}
	if binary {
		uncovered = append(uncovered, "二进制内容需专门验证")
	}
	if !sameBase {
		uncovered = append(uncovered, "审查期间基准版本变化")
	}

	var blocking, advisory []string
	var evParts, causeParts, guideParts []string
	var scope domain.CodeReviewScope
	acGroups := make(map[string][]domain.CodeReviewAC)
	var acOrder []string
	hasFail := false

	for i, a := range answers {
		if a.Verdict == "fail" {
			hasFail = true
		}
		blocking = append(blocking, cleanStrings(a.Blocking)...)
		advisory = append(advisory, cleanStrings(a.Advisory)...)
		scope.New = append(scope.New, cleanStrings(a.Scope.New)...)
		scope.Regression = append(scope.Regression, cleanStrings(a.Scope.Regression)...)
		scope.Invariants = append(scope.Invariants, cleanStrings(a.Scope.Invariants)...)
		scope.Gaps = append(scope.Gaps, cleanStrings(a.Scope.Gaps)...)
		prefix := ""
		if len(answers) > 1 {
			prefix = fmt.Sprintf("第 %d 批：", i+1)
		}
		if t := strings.TrimSpace(a.Evidence); t != "" {
			evParts = append(evParts, prefix+t)
		}
		if t := strings.TrimSpace(a.Cause); t != "" {
			causeParts = append(causeParts, prefix+t)
		}
		if t := strings.TrimSpace(a.Guide); t != "" {
			guideParts = append(guideParts, prefix+t)
		}
		for _, item := range a.AC {
			if strings.TrimSpace(item.ID) == "" {
				continue
			}
			if _, exists := acGroups[item.ID]; !exists {
				acOrder = append(acOrder, item.ID)
			}
			acGroups[item.ID] = append(acGroups[item.ID], item)
		}
	}

	acAgg := make([]domain.CodeReviewAC, 0, len(acOrder))
	for _, id := range acOrder {
		items := acGroups[id]
		acFail, allPass := false, true
		var evs []string
		for _, it := range items {
			if it.Result == "fail" {
				acFail = true
			}
			if it.Result != "pass" {
				allPass = false
			}
			if strings.TrimSpace(it.Evidence) != "" {
				evs = append(evs, it.Evidence)
			}
		}
		status := "unverified"
		if acFail {
			status = "fail"
		} else if complete && allPass {
			status = "pass"
		}
		acAgg = append(acAgg, domain.CodeReviewAC{
			ID:       id,
			Result:   status,
			Evidence: strings.Join(evs, "\n"),
		})
	}

	verdict := "unknown"
	if hasFail || len(blocking) > 0 {
		verdict = "fail"
	} else if complete {
		verdict = "pass"
	}
	vagueFail := verdict == "fail" && len(blocking) == 0 && len(causeParts) == 0

	writer := strings.TrimSpace(input.Writer)
	if writer == "" {
		writer = actor
	}
	writerKind := strings.TrimSpace(input.WriterKind)
	if writerKind != "human" && writerKind != "agent" {
		writerKind = "agent"
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = fmt.Sprintf("AI 分批审查 · %s (%d 批)", reviewer, len(batches))
	}
	if baseCommit == "" {
		if trackedHeadBefore != "" {
			baseCommit = trackedHeadBefore
		} else {
			baseCommit = "manual-diff"
		}
	}

	rec := domain.CodeReviewRecord{
		ID:                s.newID(),
		ProjectID:         strings.TrimSpace(input.ProjectID),
		IssueID:           strings.TrimSpace(input.IssueID),
		RunID:             strings.TrimSpace(input.RunID),
		Title:             title,
		RepoPath:          repoPath,
		BaseCommit:        baseCommit,
		HeadCommit:        headCommit,
		DiffSHA256:        sha256Hex(diff),
		DiffPreview:       truncateDiffPreview(diff),
		WriterKind:        writerKind,
		Writer:            writer,
		Reviewer:          reviewer,
		Model:             strings.TrimSpace(input.Model),
		DifferentExecutor: reviewer != writer,
		Verdict:           verdict,
		VagueFail:         vagueFail,
		Complete:          complete,
		BinaryDetected:    binary,
		TotalParts:        len(batches),
		Parts:             parts,
		Uncovered:         nonNilSlice(uncovered),
		Blocking:          nonNilSlice(blocking),
		Advisory:          nonNilSlice(advisory),
		AC:                acAgg,
		Scope:             scope,
		Evidence:          strings.Join(evParts, "\n\n"),
		Cause:             strings.Join(causeParts, "\n\n"),
		Guide:             strings.Join(guideParts, "\n\n"),
		CreatedBy:         actor,
		CreatedAt:         s.now(),
	}
	rec.Binding = ComputeBinding(rec)

	if err := s.store.PutCodeReview(rec); err != nil {
		return domain.CodeReviewRecord{}, err
	}
	return rec, nil
}
