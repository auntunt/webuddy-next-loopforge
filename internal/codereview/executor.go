package codereview

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"agentflow/internal/apperr"
	"agentflow/internal/domain"
	"agentflow/internal/runtime"
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
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Binary    string   `json:"binary"`
	Available bool     `json:"available"`
	LoggedIn  bool     `json:"loggedIn"`
	AuthHint  string   `json:"authHint,omitempty"`
	Path      string   `json:"path,omitempty"`
	Models    []string `json:"models,omitempty"`
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
		if raw, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml")); err == nil {
			text := string(raw)
			if strings.Contains(text, "experimental_bearer_token") || strings.Contains(text, "requires_openai_auth = false") {
				return true, "复用本机 ~/.codex/config.toml 网关配置"
			}
		}
		if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "" {
			return true, "复用环境变量 OPENAI_API_KEY"
		}
		return false, "未检测到 ~/.codex/auth.json 或网关配置"
	case "claude":
		if raw, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json")); err == nil && len(bytes.TrimSpace(raw)) > 2 {
			if strings.Contains(string(raw), "accessToken") || strings.Contains(string(raw), "token") || strings.Contains(string(raw), "apiKey") {
				return true, "复用本机 Claude 登录态"
			}
		}
		if raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json")); err == nil {
			var parsed struct {
				Env map[string]string `json:"env"`
			}
			if json.Unmarshal(raw, &parsed) == nil && (strings.TrimSpace(parsed.Env["ANTHROPIC_AUTH_TOKEN"]) != "" || strings.TrimSpace(parsed.Env["ANTHROPIC_API_KEY"]) != "") {
				return true, "复用本机 ~/.claude/settings.json 网关配置"
			}
		}
		if strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != "" || strings.TrimSpace(os.Getenv("ANTHROPIC_AUTH_TOKEN")) != "" {
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

func extraCLISearchDirs() []string {
	home, _ := os.UserHomeDir()
	dirs := make([]string, 0, 24)
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			dirs = append(dirs, p)
		}
	}
	if home != "" {
		add(filepath.Join(home, ".npm-global", "bin"))
		add(filepath.Join(home, ".local", "bin"))
		add(filepath.Join(home, "bin"))
		add(filepath.Join(home, ".volta", "bin"))
		add(filepath.Join(home, ".bun", "bin"))
		add(filepath.Join(home, ".pnpm-global", "bin"))
		add(filepath.Join(home, ".local", "share", "pnpm"))
		add(filepath.Join(home, "Library", "pnpm"))
		add(filepath.Join(home, ".yarn", "bin"))
		add(filepath.Join(home, ".config", "yarn", "global", "node_modules", ".bin"))
		add(filepath.Join(home, ".cargo", "bin"))
		add(filepath.Join(home, ".asdf", "shims"))
		add(filepath.Join(home, ".mise", "shims"))
		for _, pattern := range []string{
			filepath.Join(home, ".nvm", "versions", "node", "*", "bin"),
			filepath.Join(home, ".fnm", "node-versions", "*", "installation", "bin"),
		} {
			matches, _ := filepath.Glob(pattern)
			sort.Sort(sort.Reverse(sort.StringSlice(matches)))
			for _, m := range matches {
				add(m)
			}
		}
	}
	add("/opt/homebrew/bin")
	add("/usr/local/bin")
	add("/snap/bin")
	return dirs
}

func isExecutableFile(p string) bool {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return false
	}
	return st.Mode()&0o111 != 0
}

// lookupCLIPath resolves a CLI binary from PATH first, then falls back to
// common user and package-manager install directories (e.g. ~/.npm-global/bin,
// ~/.nvm, ~/.volta, ~/.local/bin, /opt/homebrew/bin) so non-interactive
// services such as systemd/launchd still discover installed CLIs reliably.
func lookupCLIPath(binary string) (string, bool) {
	if p, err := exec.LookPath(binary); err == nil && p != "" {
		return p, true
	}
	for _, dir := range extraCLISearchDirs() {
		cand := filepath.Join(dir, binary)
		if isExecutableFile(cand) {
			return cand, true
		}
	}
	return "", false
}

func isSensitivePlatformEnv(kv string) bool {
	upper := strings.ToUpper(kv)
	for _, prefix := range []string{
		"AGENTFLOW_MYSQL_DSN=",
		"AGENTFLOW_DATABASE_URL=",
		"DATABASE_URL=",
		"AGENTFLOW_SESSION_SECRET=",
		"AGENTFLOW_AUTH_SECRET=",
		"AGENTFLOW_ENCRYPT_KEY=",
		"MYSQL_PWD=",
		"MYSQL_PASSWORD=",
	} {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

func expandedCLIEnv(resolvedBin string) []string {
	rawEnv := os.Environ()
	env := make([]string, 0, len(rawEnv)+1)
	for _, kv := range rawEnv {
		if isSensitivePlatformEnv(kv) {
			continue
		}
		env = append(env, kv)
	}
	extra := make([]string, 0, 8)
	if dir := filepath.Dir(resolvedBin); dir != "" && dir != "." {
		extra = append(extra, dir)
	}
	extra = append(extra, extraCLISearchDirs()...)
	seen := make(map[string]struct{})
	var merged []string
	addPath := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		merged = append(merged, p)
	}
	pathIdx := -1
	for i, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			pathIdx = i
			for _, seg := range filepath.SplitList(strings.TrimPrefix(kv, "PATH=")) {
				addPath(seg)
			}
			break
		}
	}
	for _, seg := range extra {
		addPath(seg)
	}
	newPath := "PATH=" + strings.Join(merged, string(os.PathListSeparator))
	if pathIdx >= 0 {
		out := make([]string, len(env))
		copy(out, env)
		out[pathIdx] = newPath
		return out
	}
	return append(env, newPath)
}

// DetectExecutors checks which review CLIs are available in PATH or common
// user/package-manager install directories, and whether local credentials exist.
func DetectExecutors() []ExecutorInfo {
	out := make([]ExecutorInfo, 0, len(supportedExecutors))
	for _, item := range supportedExecutors {
		path, avail := lookupCLIPath(item.binary)
		loggedIn, hint := false, ""
		if avail {
			loggedIn, hint = detectLocalAuth(item.id)
		}
		var models []string
		if avail && item.id == "codex" {
			models = DiscoverCodexGatewayModels(context.Background())
		}
		out = append(out, ExecutorInfo{
			ID:        item.id,
			Name:      item.name,
			Binary:    item.binary,
			Available: avail,
			LoggedIn:  loggedIn,
			AuthHint:  hint,
			Path:      path,
			Models:    models,
		})
	}
	return out
}

// PickDefaultExecutor dynamically selects the first available and logged-in
// executor, falling back to the first available installed CLI.
func PickDefaultExecutor(execs []ExecutorInfo) string {
	for _, ex := range execs {
		if ex.Available && ex.LoggedIn {
			return ex.ID
		}
	}
	for _, ex := range execs {
		if ex.Available {
			return ex.ID
		}
	}
	return ""
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
	resolvedBin, ok := lookupCLIPath(bin)
	if !ok {
		return BatchDiagnosis{}, apperr.Newf(apperr.Unavailable, "审查执行器 %s 未安装或不在 PATH / 常见用户目录中", bin)
	}

	cmd := exec.CommandContext(cctx, resolvedBin, args...)
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = expandedCLIEnv(resolvedBin)
	if strings.TrimSpace(req.RepoPath) != "" {
		cmd.Dir = strings.TrimSpace(req.RepoPath)
	} else if tmpDir, err := os.MkdirTemp("", "webuddy-cr-*"); err == nil {
		defer os.RemoveAll(tmpDir)
		cmd.Dir = tmpDir
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
	Async      bool   `json:"async,omitempty"`
}

// ProgressFunc receives live batch progress updates during automated review.
type ProgressFunc func(completed, total int, currentHeader string)

// RunAutomatedReview executes Webuddy's exact batched independent diff review loop:
//   - packs complete diff into 14k parts (never dropping a character)
//   - calls independent reviewer CLI per batch
//   - verifies git HEAD did not mutate mid-review (same_base)
//   - detects binary patches and uncovered batches
//   - aggregates blocking/advisory/AC/scope/verdict and persists tamper-evident Binding
func (s *Service) RunAutomatedReview(ctx context.Context, input RunAutomatedReviewInput, actor string, diagnoser BatchDiagnoser) (domain.CodeReviewRecord, error) {
	return s.RunAutomatedReviewWithProgress(ctx, input, actor, diagnoser, nil)
}

// RunAutomatedReviewWithProgress executes an automated review and emits live
// batch progress updates via onProgress.
func (s *Service) RunAutomatedReviewWithProgress(ctx context.Context, input RunAutomatedReviewInput, actor string, diagnoser BatchDiagnoser, onProgress ProgressFunc) (domain.CodeReviewRecord, error) {
	if diagnoser == nil {
		diagnoser = NewCLIExecutor()
	}
	reviewer := strings.TrimSpace(input.Reviewer)
	if reviewer == "" || strings.EqualFold(reviewer, "auto") {
		reviewer = PickDefaultExecutor(DetectExecutors())
	}
	if reviewer == "" {
		return domain.CodeReviewRecord{}, apperr.New(apperr.Invalid, "服务端未检测到可用的代码审查执行器，请先安装并登录任一 CLI（如 codex / claude / codebuddy / opencode）")
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
		const gitEmptyTreeHash = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
		if baseCommit == "" {
			baseCommit = "HEAD~1"
		}
		if headCommit == "" {
			headCommit = "HEAD"
		}
		if strings.HasPrefix(baseCommit, "-") || strings.HasPrefix(headCommit, "-") {
			return domain.CodeReviewRecord{}, apperr.New(apperr.Invalid, "非法 Git 引用：不能以 '-' 开头")
		}
		var err error
		headCommit, err = gitOutput(ctx, repoPath, "-c", "core.fsmonitor=", "rev-parse", "--verify", "--end-of-options", headCommit+"^{commit}")
		if err != nil {
			return domain.CodeReviewRecord{}, apperr.New(apperr.Invalid, "无法解析目标提交: "+err.Error())
		}
		origBase := baseCommit
		baseCommit, err = gitOutput(ctx, repoPath, "-c", "core.fsmonitor=", "rev-parse", "--verify", "--end-of-options", origBase+"^{commit}")
		if err != nil {
			// Single-commit repos (e.g. imported code archives) have no HEAD~1;
			// fall back to Git's empty tree hash so the initial commit can be reviewed cleanly.
			if origBase == "HEAD~1" {
				baseCommit = gitEmptyTreeHash
			} else {
				return domain.CodeReviewRecord{}, apperr.New(apperr.Invalid, "无法解析基准提交: "+err.Error())
			}
		}
		excludeNoisyFiles := []string{
			"--", ".",
			":!package-lock.json", ":!**/package-lock.json",
			":!pnpm-lock.yaml", ":!**/pnpm-lock.yaml",
			":!yarn.lock", ":!**/yarn.lock",
			":!go.sum", ":!**/go.sum",
			":!Cargo.lock", ":!**/Cargo.lock",
			":!poetry.lock", ":!**/poetry.lock",
			":!.DS_Store", ":!**/.DS_Store",
			":!*.png", ":!**/*.png",
			":!*.jpg", ":!**/*.jpg",
			":!*.jpeg", ":!**/*.jpeg",
			":!*.gif", ":!**/*.gif",
			":!*.ico", ":!**/*.ico",
			":!*.webp", ":!**/*.webp",
			":!*.woff", ":!**/*.woff",
			":!*.woff2", ":!**/*.woff2",
			":!*.ttf", ":!**/*.ttf",
		}
		diffBaseArgs := []string{"-c", "core.fsmonitor=", "-c", "diff.external=", "diff", "--no-ext-diff", "--no-textconv"}
		if baseCommit == gitEmptyTreeHash {
			args := append(append(diffBaseArgs, gitEmptyTreeHash, headCommit), excludeNoisyFiles...)
			diff, err = gitOutputRaw(ctx, repoPath, args...)
		} else {
			args := append(append(diffBaseArgs, baseCommit+".."+headCommit), excludeNoisyFiles...)
			diff, err = gitOutputRaw(ctx, repoPath, args...)
		}
		if err != nil {
			return domain.CodeReviewRecord{}, apperr.New(apperr.Invalid, "获取 git diff 失败: "+err.Error())
		}
	}

	if strings.TrimSpace(diff) == "" {
		return domain.CodeReviewRecord{}, apperr.New(apperr.Invalid, "请提供 Unified Diff 或指定有改动的本地 Git 仓库")
	}

	batches := PackDiffParts(diff, PartSize)
	binary := DetectBinary(diff)

	parts := make([]domain.CodeReviewPart, 0, len(batches))
	answers := make([]BatchDiagnosis, 0, len(batches))
	failedCall := false
	failedReason := ""

	for i, b := range batches {
		if onProgress != nil {
			onProgress(i, len(batches), shortDiffHeader(b.Header))
		}
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
			failedReason = strings.TrimSpace(err.Error())
			if len(failedReason) > 200 {
				failedReason = failedReason[:200] + "…"
			}
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
		trackedHeadAfter, err := gitOutput(ctx, repoPath, "rev-parse", "HEAD")
		if err != nil || trackedHeadAfter != trackedHeadBefore {
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
			if failedReason != "" && i == len(parts)-1 {
				uncovered = append(uncovered, fmt.Sprintf("第 %d 批 %s：执行器失败（%s）", i+1, b.Header, failedReason))
			} else {
				uncovered = append(uncovered, fmt.Sprintf("第 %d 批 %s：未形成有效结论", i+1, b.Header))
			}
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
			hasFail = true
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

var (
	codexGwCacheMu sync.Mutex
	codexGwModels  []string
	codexGwUntil   time.Time
)

func parseCodexGatewayConfig(raw string) (baseURL, token, defaultModel string, ok bool) {
	var activeProvider string
	section := ""
	providers := map[string][2]string{}
	for _, ln := range strings.Split(raw, "\n") {
		line := strings.TrimSpace(ln)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		if idx := strings.Index(v, " #"); idx >= 0 {
			v = strings.TrimSpace(v[:idx])
		}
		v = strings.Trim(v, `"'`)
		if section == "" && k == "model_provider" {
			activeProvider = v
		} else if section == "" && k == "model" {
			defaultModel = v
		} else if strings.HasPrefix(section, "model_providers.") {
			pname := strings.TrimPrefix(section, "model_providers.")
			pair := providers[pname]
			if k == "base_url" {
				pair[0] = strings.TrimRight(v, "/")
			} else if k == "experimental_bearer_token" {
				pair[1] = v
			}
			providers[pname] = pair
		}
	}
	if activeProvider != "" {
		if pair, exists := providers[activeProvider]; exists && pair[0] != "" && pair[1] != "" {
			return pair[0], pair[1], defaultModel, true
		}
	}
	for _, pair := range providers {
		if pair[0] != "" && pair[1] != "" {
			return pair[0], pair[1], defaultModel, true
		}
	}
	return "", "", defaultModel, false
}

// DiscoverCodexGatewayModels queries the custom Codex gateway configured in
// ~/.codex/config.toml (e.g. experimental_bearer_token + base_url/models) and
// returns selectable chat/coding model IDs (excluding pure image models).
// Results are cached in memory for 5 minutes.
func DiscoverCodexGatewayModels(ctx context.Context) []string {
	codexGwCacheMu.Lock()
	if time.Now().Before(codexGwUntil) && len(codexGwModels) > 0 {
		out := append([]string(nil), codexGwModels...)
		codexGwCacheMu.Unlock()
		return out
	}
	codexGwCacheMu.Unlock()

	configDir := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if configDir == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			configDir = filepath.Join(home, ".codex")
		}
	}
	if configDir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(configDir, "config.toml"))
	if err != nil {
		return nil
	}
	baseURL, token, defaultModel, ok := parseCodexGatewayConfig(string(raw))
	if !ok {
		if defaultModel != "" {
			return []string{defaultModel}
		}
		return nil
	}
	fallbackDefault := func() []string {
		if defaultModel != "" {
			return []string{defaultModel}
		}
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return fallbackDefault()
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fallbackDefault()
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fallbackDefault()
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return fallbackDefault()
	}
	seen := make(map[string]struct{}, len(payload.Data)+1)
	models := make([]string, 0, len(payload.Data)+1)
	if defaultModel != "" {
		seen[defaultModel] = struct{}{}
		models = append(models, defaultModel)
	}
	for _, item := range payload.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" || strings.HasPrefix(strings.ToLower(id), "gpt-image") {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, id)
	}
	if len(models) > 1 {
		_, _, _, _, _, _, hdrs, _ := runtime.ParseCodexConfigFull(string(raw))
		models = runtime.FilterAccessibleModelIDs(ctx, configDir, baseURL, token, hdrs, models)
	}
	if len(models) > 0 {
		codexGwCacheMu.Lock()
		codexGwModels = append([]string(nil), models...)
		codexGwUntil = time.Now().Add(5 * time.Minute)
		codexGwCacheMu.Unlock()
	}
	return models
}
