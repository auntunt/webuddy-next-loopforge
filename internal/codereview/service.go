package codereview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"agentflow/internal/apperr"
	"agentflow/internal/domain"
)

// PartSize mirrors Webuddy's code_review.py PART_SIZE = 14000.
const PartSize = 14000

var binaryFileLineRE = regexp.MustCompile(`(?m)^Binary files .* differ$`)

// Store is the persistence contract for the CodeReview domain.
type Store interface {
	CodeReviews(projectID string, limit int) []domain.CodeReviewRecord
	CodeReview(id string) (domain.CodeReviewRecord, bool)
	PutCodeReview(domain.CodeReviewRecord) error
	CodeReviewFeedbacks(reviewID string) []domain.CodeReviewFeedback
	CodeReviewFeedbacksByProject(projectID string) []domain.CodeReviewFeedback
	CodeReviewFeedbackByRequest(reviewID, actor, requestID string) (domain.CodeReviewFeedback, bool)
	PutCodeReviewFeedback(domain.CodeReviewFeedback) error
}

// DiffPart is one sliced chunk of a unified diff.
type DiffPart struct {
	Header string `json:"header"`
	Text   string `json:"text"`
	SHA256 string `json:"sha256"`
}

// SplitDiffParts splits a unified diff by "diff --git " file boundaries and
// subdivides any file larger than partSize without dropping a single character.
func SplitDiffParts(diff string, partSize int) []DiffPart {
	if partSize <= 0 {
		partSize = PartSize
	}
	if diff == "" {
		return nil
	}
	blocks := splitByDiffGit(diff)
	var out []DiffPart
	for _, block := range blocks {
		if block == "" {
			continue
		}
		header := block
		if idx := strings.IndexByte(block, '\n'); idx >= 0 {
			header = block[:idx]
		}
		runes := []rune(block)
		for start := 0; start < len(runes); start += partSize {
			end := start + partSize
			if end > len(runes) {
				end = len(runes)
			}
			chunk := string(runes[start:end])
			out = append(out, DiffPart{
				Header: header,
				Text:   chunk,
				SHA256: sha256Hex(chunk),
			})
		}
	}
	return out
}

// PackDiffParts splits a unified diff by file boundaries and packs consecutive
// small files into batches up to partSize runes, preserving every character
// while dramatically reducing per-file CLI round-trips.
func PackDiffParts(diff string, partSize int) []DiffPart {
	if partSize <= 0 {
		partSize = PartSize
	}
	if diff == "" {
		return nil
	}
	blocks := splitByDiffGit(diff)
	var out []DiffPart

	var buf strings.Builder
	bufRunes := 0
	var headers []string

	flush := func() {
		if buf.Len() == 0 {
			return
		}
		chunk := buf.String()
		hdr := ""
		if len(headers) == 1 {
			hdr = headers[0]
		} else if len(headers) > 1 {
			hdr = fmt.Sprintf("%s 等 %d 个文件", shortDiffHeader(headers[0]), len(headers))
		}
		out = append(out, DiffPart{
			Header: hdr,
			Text:   chunk,
			SHA256: sha256Hex(chunk),
		})
		buf.Reset()
		bufRunes = 0
		headers = nil
	}

	for _, block := range blocks {
		if block == "" {
			continue
		}
		header := block
		if idx := strings.IndexByte(block, '\n'); idx >= 0 {
			header = block[:idx]
		}
		runes := []rune(block)
		if len(runes) <= partSize {
			if bufRunes > 0 && bufRunes+len(runes) > partSize {
				flush()
			}
			buf.WriteString(block)
			bufRunes += len(runes)
			headers = append(headers, header)
			continue
		}
		// Large single file: flush pending small files first, then split by partSize.
		flush()
		for start := 0; start < len(runes); start += partSize {
			end := start + partSize
			if end > len(runes) {
				end = len(runes)
			}
			chunk := string(runes[start:end])
			out = append(out, DiffPart{
				Header: header,
				Text:   chunk,
				SHA256: sha256Hex(chunk),
			})
		}
	}
	flush()
	return out
}

func shortDiffHeader(header string) string {
	h := strings.TrimPrefix(strings.TrimSpace(header), "diff --git ")
	if idx := strings.Index(h, " b/"); idx >= 0 {
		return h[idx+3:]
	}
	return h
}

func splitByDiffGit(diff string) []string {
	const marker = "diff --git "
	var blocks []string
	start := 0
	for i := 0; i < len(diff); i++ {
		if (i == 0 || diff[i-1] == '\n') && strings.HasPrefix(diff[i:], marker) {
			if i > start {
				blocks = append(blocks, diff[start:i])
			}
			start = i
		}
	}
	if start < len(diff) {
		blocks = append(blocks, diff[start:])
	}
	return blocks
}

// DetectBinary mirrors Webuddy's binary diff detection.
func DetectBinary(diff string) bool {
	return strings.Contains(diff, "GIT binary patch") || binaryFileLineRE.MatchString(diff)
}

// BuildBatchPrompt builds the exact Webuddy per-batch independent review prompt.
func BuildBatchPrompt(index, total int, header, text string) string {
	return fmt.Sprintf(
		"审查分批 %d/%d：%s\n仅对本批及其调用关系形成结论；可只读查看工作树核对上下文。"+
			"不要修改代码或把代码阅读当作实测。不相关验收项不填入 ac，不能以未阅读部分推断通过。\n"+
			"阻断问题须写明文件、行号、触发条件和实际影响，区分本次引入与既有问题；安全边界、权限和数据写入优先检查。"+
			"复审仅追踪尚未解决的问题及新差异，不重复已解决项，不以风格建议阻断交付。\n"+
			"重新核对本批影响到的新需求、旧功能和不变约束，在 scope 中分别用 new、regression、invariants 数组记录验收项编号和依据；缺少检查写入 gaps，不得推断已覆盖。"+
			"若未提供验收项清单（全量或独立代码审查），请基于代码安全、权限、数据一致性与逻辑边界直接判定：发现阻断缺陷输出 fail，未发现阻断缺陷输出 pass（不要仅因缺少验收项编号而输出 unknown）。\n%s",
		index, total, header, text,
	)
}

// Snapshot builds the immutable human-feedback binding snapshot from a review.
func Snapshot(rec domain.CodeReviewRecord) *domain.CodeReviewSnapshot {
	if rec.ID == "" || rec.Binding == "" {
		return nil
	}
	findings := make([]domain.CodeReviewFinding, 0, len(rec.Blocking)+len(rec.Advisory))
	for i, item := range rec.Blocking {
		findings = append(findings, domain.CodeReviewFinding{
			ID:     fmt.Sprintf("blocking:%d", i),
			Kind:   "blocking",
			Detail: item,
		})
	}
	for i, item := range rec.Advisory {
		findings = append(findings, domain.CodeReviewFinding{
			ID:     fmt.Sprintf("advisory:%d", i),
			Kind:   "advisory",
			Detail: item,
		})
	}
	return &domain.CodeReviewSnapshot{
		Binding:    rec.Binding,
		Base:       rec.BaseCommit,
		DiffSHA256: rec.DiffSHA256,
		CreatedAt:  rec.CreatedAt,
		Findings:   findings,
	}
}

// ComputeBinding computes a deterministic SHA-256 digest of the diagnosis fields.
func ComputeBinding(rec domain.CodeReviewRecord) string {
	payload := map[string]any{
		"baseCommit":        rec.BaseCommit,
		"diffSha256":        rec.DiffSHA256,
		"writer":            rec.Writer,
		"reviewer":          rec.Reviewer,
		"model":             rec.Model,
		"differentExecutor": rec.DifferentExecutor,
		"verdict":           rec.Verdict,
		"complete":          rec.Complete,
		"binaryDetected":    rec.BinaryDetected,
		"totalParts":        rec.TotalParts,
		"parts":             rec.Parts,
		"uncovered":         rec.Uncovered,
		"blocking":          rec.Blocking,
		"advisory":          rec.Advisory,
		"ac":                rec.AC,
		"scope":             rec.Scope,
		"evidence":          rec.Evidence,
		"cause":             rec.Cause,
		"guide":             rec.Guide,
	}
	raw, _ := json.Marshal(payload)
	return sha256Hex(string(raw))
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func truncateDiffPreview(diff string) string {
	const maxBytes = 256 * 1024
	if len(diff) <= maxBytes {
		return diff
	}
	return diff[:maxBytes] + "\n... (diff preview truncated at 256KB; full diff was hashed and batched) ..."
}

// Service implements Webuddy's Complete Diff Code Review + Human Feedback loop.
type Service struct {
	store Store
	now   func() time.Time
	newID func() string
}

func NewService(store Store, newID func() string) *Service {
	return &Service{
		store: store,
		now:   func() time.Time { return time.Now().UTC() },
		newID: newID,
	}
}

// CreateReviewInput accepts either a raw unified diff or a local git repo + ref range.
type CreateReviewInput struct {
	ProjectID   string                `json:"projectId"`
	IssueID     string                `json:"issueId"`
	RunID       string                `json:"runId"`
	Title       string                `json:"title"`
	RepoPath    string                `json:"repoPath"`
	BaseRef     string                `json:"baseRef"`
	HeadRef     string                `json:"headRef"`
	Diff        string                `json:"diff"`
	WriterKind  string                `json:"writerKind"` // human | agent
	Writer      string                `json:"writer"`
	Reviewer    string                `json:"reviewer"`
	Model       string                `json:"model"`
	Verdict     string                `json:"verdict"` // pass | fail | unknown (optional)
	Blocking    []string              `json:"blocking"`
	Advisory    []string              `json:"advisory"`
	AC          []domain.CodeReviewAC `json:"ac"`
	Scope       domain.CodeReviewScope `json:"scope"`
	Evidence    string                `json:"evidence"`
	Cause       string                `json:"cause"`
	Guide       string                `json:"guide"`
	PartVerdict []string              `json:"partVerdicts"`
}

// CreateReview splits the complete diff into tamper-evident batches, verifies
// base commit integrity and binary patches, and persists the review record.
func (s *Service) CreateReview(ctx context.Context, input CreateReviewInput, actor string) (domain.CodeReviewRecord, error) {
	diff := input.Diff
	baseCommit := strings.TrimSpace(input.BaseRef)
	headCommit := strings.TrimSpace(input.HeadRef)
	repoPath := strings.TrimSpace(input.RepoPath)

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
		return domain.CodeReviewRecord{}, apperr.New(apperr.Invalid, "请提供 Unified Diff 内容，或指定有效的本地 Git 仓库与提交范围")
	}

	batches := SplitDiffParts(diff, PartSize)
	binary := DetectBinary(diff)

	parts := make([]domain.CodeReviewPart, 0, len(batches))
	var uncovered []string
	hasFail := false
	allResolved := len(batches) > 0

	defaultPartVerdict := normalizeVerdict(input.Verdict)
	if defaultPartVerdict == "" {
		if len(input.Blocking) > 0 {
			defaultPartVerdict = "fail"
		} else {
			defaultPartVerdict = "pass"
		}
	}

	for i, b := range batches {
		pv := defaultPartVerdict
		if i < len(input.PartVerdict) {
			if norm := normalizeVerdict(input.PartVerdict[i]); norm != "" {
				pv = norm
			}
		}
		if pv == "fail" {
			hasFail = true
		}
		if pv == "unknown" {
			allResolved = false
			uncovered = append(uncovered, fmt.Sprintf("第 %d 批 %s：未形成有效结论", i+1, b.Header))
		}
		parts = append(parts, domain.CodeReviewPart{
			Part:       i + 1,
			File:       b.Header,
			Characters: len([]rune(b.Text)),
			SHA256:     b.SHA256,
			Verdict:    pv,
		})
	}

	if binary {
		uncovered = append(uncovered, "二进制内容需专门验证")
	}

	complete := allResolved && !binary
	verdict := "unknown"
	if hasFail || len(input.Blocking) > 0 {
		verdict = "fail"
	} else if complete {
		verdict = "pass"
	}

	vagueFail := verdict == "fail" && len(input.Blocking) == 0 && strings.TrimSpace(input.Cause) == ""

	writer := strings.TrimSpace(input.Writer)
	if writer == "" {
		writer = actor
	}
	reviewer := strings.TrimSpace(input.Reviewer)
	if reviewer == "" {
		reviewer = "webuddy-reviewer"
	}
	writerKind := strings.TrimSpace(input.WriterKind)
	if writerKind != "human" && writerKind != "agent" {
		writerKind = "agent"
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = fmt.Sprintf("代码审查 (%d 批)", len(batches))
	}
	if baseCommit == "" {
		baseCommit = "manual-diff"
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
		Blocking:          cleanStrings(input.Blocking),
		Advisory:          cleanStrings(input.Advisory),
		AC:                nonNilAC(input.AC),
		Scope:             input.Scope,
		Evidence:          strings.TrimSpace(input.Evidence),
		Cause:             strings.TrimSpace(input.Cause),
		Guide:             strings.TrimSpace(input.Guide),
		CreatedBy:         actor,
		CreatedAt:         s.now(),
	}
	rec.Binding = ComputeBinding(rec)

	if err := s.store.PutCodeReview(rec); err != nil {
		return domain.CodeReviewRecord{}, err
	}
	return rec, nil
}

func (s *Service) ListReviews(projectID string, limit int) []domain.CodeReviewRecord {
	if limit <= 0 {
		limit = 50
	}
	return s.store.CodeReviews(strings.TrimSpace(projectID), limit)
}

// ReviewDetail returns a review, its immutable feedback snapshot, and all feedback rows.
type ReviewDetail struct {
	Review   domain.CodeReviewRecord    `json:"review"`
	Snapshot *domain.CodeReviewSnapshot `json:"snapshot"`
	Feedback []domain.CodeReviewFeedback `json:"feedback"`
}

func (s *Service) GetReview(id string) (ReviewDetail, error) {
	rec, ok := s.store.CodeReview(strings.TrimSpace(id))
	if !ok {
		return ReviewDetail{}, apperr.New(apperr.NotFound, "代码审查记录不存在")
	}
	snap := Snapshot(rec)
	rows := s.store.CodeReviewFeedbacks(rec.ID)
	for i := range rows {
		rows[i].Current = snap != nil && rows[i].Binding == snap.Binding
	}
	return ReviewDetail{
		Review:   rec,
		Snapshot: snap,
		Feedback: rows,
	}, nil
}

// FeedbackInput mirrors Webuddy's review_feedback.py strict payload contract.
type FeedbackInput struct {
	RequestID string                             `json:"requestId"`
	Binding   string                             `json:"binding"`
	Minutes   *int                               `json:"minutes"`
	Findings  []domain.CodeReviewFindingJudgment `json:"findings"`
	Missed    []string                           `json:"missed"`
	Note      string                             `json:"note"`
}

// SaveFeedback enforces all Webuddy human review effectiveness feedback invariants:
//   - human-only (executors/bots cannot forge feedback)
//   - idempotent requestId with payload digest conflict detection (409)
//   - exact binding match against current AI review snapshot (409)
//   - every finding accounted for, valid/false_positive require non-empty reason
//   - minutes in [0, 1440], non-empty note
func (s *Service) SaveFeedback(reviewID, actor string, isHuman bool, input FeedbackInput) (domain.CodeReviewFeedback, error) {
	if !isHuman {
		return domain.CodeReviewFeedback{}, apperr.New(apperr.Forbidden, "Review 效果由人工记录，不能由执行器代填")
	}
	rec, ok := s.store.CodeReview(strings.TrimSpace(reviewID))
	if !ok {
		return domain.CodeReviewFeedback{}, apperr.New(apperr.NotFound, "代码审查记录不存在")
	}
	reqID := strings.TrimSpace(input.RequestID)
	if len(reqID) < 1 || len(reqID) > 100 {
		return domain.CodeReviewFeedback{}, apperr.New(apperr.Invalid, "记录请求标识无效")
	}
	if input.Minutes == nil || *input.Minutes < 0 || *input.Minutes > 1440 {
		return domain.CodeReviewFeedback{}, apperr.New(apperr.Invalid, "实际复核时间须为 0 至 1440 的整数分钟")
	}
	note := strings.TrimSpace(input.Note)
	if note == "" || len([]rune(note)) > 4000 {
		return domain.CodeReviewFeedback{}, apperr.New(apperr.Invalid, "请填写实际复核范围与说明；0 分钟也须说明原因")
	}
	for _, m := range input.Missed {
		if strings.TrimSpace(m) == "" || len([]rune(m)) > 2000 {
			return domain.CodeReviewFeedback{}, apperr.New(apperr.Invalid, "人工另行发现的问题须逐条填写且不能为空")
		}
	}

	rawBody, _ := json.Marshal(input)
	identity := sha256Hex(string(rawBody))

	if old, exists := s.store.CodeReviewFeedbackByRequest(rec.ID, actor, reqID); exists {
		if old.PayloadDigest != identity {
			return domain.CodeReviewFeedback{}, apperr.New(apperr.Conflict, "同一请求不能更换记录内容")
		}
		old.Current = old.Binding == rec.Binding
		return old, nil
	}

	snap := Snapshot(rec)
	if snap == nil || input.Binding != snap.Binding {
		return domain.CodeReviewFeedback{}, apperr.New(apperr.Conflict, "AI Review 已变化，请重新打开后记录")
	}

	expected := make(map[string]struct{}, len(snap.Findings))
	for _, f := range snap.Findings {
		expected[f.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(input.Findings))
	for _, j := range input.Findings {
		if _, ok := expected[j.ID]; !ok {
			return domain.CodeReviewFeedback{}, apperr.New(apperr.Invalid, "逐项判断须对应原问题: "+j.ID)
		}
		if _, dup := seen[j.ID]; dup {
			return domain.CodeReviewFeedback{}, apperr.New(apperr.Invalid, "重复的问题判断: "+j.ID)
		}
		if j.Verdict != "valid" && j.Verdict != "false_positive" && j.Verdict != "unassessed" {
			return domain.CodeReviewFeedback{}, apperr.New(apperr.Invalid, "无效的问题判断状态: "+j.Verdict)
		}
		if len([]rune(j.Reason)) > 2000 || (j.Verdict != "unassessed" && strings.TrimSpace(j.Reason) == "") {
			return domain.CodeReviewFeedback{}, apperr.New(apperr.Invalid, "已判断项需要填写依据: "+j.ID)
		}
		seen[j.ID] = struct{}{}
	}
	if len(seen) != len(expected) {
		return domain.CodeReviewFeedback{}, apperr.New(apperr.Invalid, "每条 AI 问题都须保留判断，未判断请选待核实")
	}

	fb := domain.CodeReviewFeedback{
		ID:            s.newID(),
		ReviewID:      rec.ID,
		ProjectID:     rec.ProjectID,
		Actor:         actor,
		RequestID:     reqID,
		Binding:       snap.Binding,
		PayloadDigest: identity,
		Minutes:       *input.Minutes,
		Findings:      input.Findings,
		Missed:        nonNilSlice(input.Missed),
		Note:          note,
		Current:       true,
		CreatedAt:     s.now(),
	}
	if err := s.store.PutCodeReviewFeedback(fb); err != nil {
		return domain.CodeReviewFeedback{}, err
	}
	return fb, nil
}

// FeedbackSummary computes project-level review effectiveness metrics matching
// Webuddy's review_feedback.summary().
func (s *Service) FeedbackSummary(projectID string) domain.CodeReviewFeedbackSummary {
	rows := s.store.CodeReviewFeedbacksByProject(strings.TrimSpace(projectID))
	var selected []domain.CodeReviewFeedback
	seen := make(map[string]struct{})
	bindings := make(map[string]string)

	for _, row := range rows {
		key := row.ReviewID + "\x00" + row.Actor
		if _, dup := seen[key]; dup {
			continue
		}
		b, loaded := bindings[row.ReviewID]
		if !loaded {
			if rec, ok := s.store.CodeReview(row.ReviewID); ok {
				b = rec.Binding
			}
			bindings[row.ReviewID] = b
		}
		if b != "" && row.Binding == b {
			row.Current = true
			selected = append(selected, row)
			seen[key] = struct{}{}
		}
	}

	valid, fp, unassessed, totalMinutes, missed := 0, 0, 0, 0, 0
	reviewIDs := make(map[string]struct{})
	for _, r := range selected {
		reviewIDs[r.ReviewID] = struct{}{}
		totalMinutes += r.Minutes
		missed += len(r.Missed)
		for _, item := range r.Findings {
			switch item.Verdict {
			case "valid":
				valid++
			case "false_positive":
				fp++
			default:
				unassessed++
			}
		}
	}

	var minutesPtr *int
	if len(selected) > 0 {
		m := totalMinutes
		minutesPtr = &m
	}
	var validRatePtr *float64
	if assessed := valid + fp; assessed > 0 {
		rate := math.Round(float64(valid)/float64(assessed)*1000) / 10
		validRatePtr = &rate
	}
	if selected == nil {
		selected = []domain.CodeReviewFeedback{}
	}
	return domain.CodeReviewFeedbackSummary{
		Valid:         valid,
		FalsePositive: fp,
		Unassessed:    unassessed,
		Observations:  len(selected),
		Reviews:       len(reviewIDs),
		Minutes:       minutesPtr,
		Missed:        missed,
		ValidRate:     validRatePtr,
		Records:       selected,
	}
}

func normalizeVerdict(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "pass":
		return "pass"
	case "fail":
		return "fail"
	case "unknown":
		return "unknown"
	default:
		return ""
	}
}

func cleanStrings(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if t := strings.TrimSpace(item); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func nonNilSlice(items []string) []string {
	if items == nil {
		return []string{}
	}
	return items
}

func nonNilAC(items []domain.CodeReviewAC) []domain.CodeReviewAC {
	if items == nil {
		return []domain.CodeReviewAC{}
	}
	return items
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := gitOutputRaw(ctx, dir, args...)
	return strings.TrimSpace(out), err
}

func gitOutputRaw(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return stdout.String(), nil
}
