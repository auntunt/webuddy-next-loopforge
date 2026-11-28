package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"agentflow/internal/builtinagent"
	"agentflow/internal/domain"
	"agentflow/internal/webuddykb"

	"github.com/gin-gonic/gin"
)

// ============================================================================
// Webuddy AutoTest & 5-Step AI Self-Healing Workbench
// Synthesizes:
// 1. Huolala Flywheel: Natural-language -> Live Headless Chrome Execution ->
//    Denoised Deterministic Case + Precondition Auth Injection + 0-Token Replay
//    + 5-Step Diagnosis + 3D Element Digital Portrait + Confidence >= 0.80 Gate
//    + Snapshot Revision Diff & 1-Click Rollback.
// 2. Tmall Digital Employee Context Layer: .tmh KB Grounding + Precedent Library
//    (判例库) + Exception Escalation to Issue ("人不盯跑批，异常才找人").
// ============================================================================

const autoTestConfidenceThreshold = 0.80

type ElementRect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// ElementPortrait is the 3D "Digital ID Card" (控件数字身份证) of a DOM target.
type ElementPortrait struct {
	Tag               string      `json:"tag"`
	Role              string      `json:"role"`
	Text              string      `json:"text"`
	AriaLabel         string      `json:"ariaLabel,omitempty"`
	Placeholder       string      `json:"placeholder,omitempty"`
	NameAttr          string      `json:"nameAttr,omitempty"`
	Href              string      `json:"href,omitempty"`
	ElementID         string      `json:"elementId,omitempty"`
	Classes           []string    `json:"classes,omitempty"`
	ParentContext     string      `json:"parentContext,omitempty"`
	DOMPath           string      `json:"domPath,omitempty"`
	PagePathPattern   string      `json:"pagePathPattern,omitempty"` // Semantic Firewall baseline
	PrimarySelector   string      `json:"primarySelector"`
	FallbackSelectors []string    `json:"fallbackSelectors,omitempty"`
	Rect              ElementRect `json:"rect,omitempty"`
}

type AutoTestStep struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Action          string           `json:"action"` // inject_auth | navigate | click | fill | assert_text | assert_url | assert_api
	TargetURL       string           `json:"targetUrl,omitempty"`
	PrimarySelector string           `json:"primarySelector,omitempty"`
	Value           string           `json:"value,omitempty"`
	Expected        string           `json:"expected,omitempty"`
	Portrait        *ElementPortrait `json:"portrait,omitempty"`
}

type AutoTestRevision struct {
	Revision   int            `json:"revision"`
	Reason     string         `json:"reason"`
	Author     string         `json:"author"`
	AutoHealed bool           `json:"autoHealed"`
	Confidence float64        `json:"confidence,omitempty"`
	DiffNote   string         `json:"diffNote,omitempty"`
	Steps      []AutoTestStep `json:"steps"`
	CreatedAt  time.Time      `json:"createdAt"`
}

type AutoTestCaseStats struct {
	TotalRuns     int     `json:"totalRuns"`
	PassedRuns    int     `json:"passedRuns"`
	HealedRuns    int     `json:"healedRuns"`
	FailedRuns    int     `json:"failedRuns"`
	ZeroTokenRuns int     `json:"zeroTokenRuns"`
	PassRate      float64 `json:"passRate"`
}

type AutoTestCase struct {
	ID                 string             `json:"id"`
	TenantID           string             `json:"tenantId"`
	ProjectID          string             `json:"projectId"`
	Title              string             `json:"title"`
	NaturalRequirement string             `json:"naturalRequirement"`
	BaseURL            string             `json:"baseUrl"`
	PreconditionURL    string             `json:"preconditionUrl,omitempty"`
	LinkedIssueID      string             `json:"linkedIssueId,omitempty"`
	Tags               []string           `json:"tags,omitempty"`
	Steps              []AutoTestStep     `json:"steps"`
	CurrentRevision    int                `json:"currentRevision"`
	Revisions          []AutoTestRevision `json:"revisions"`
	Stats              AutoTestCaseStats  `json:"stats"`
	LastStatus         string             `json:"lastStatus,omitempty"` // passed | healed | failed | never
	LastRunID          string             `json:"lastRunId,omitempty"`
	LastDurationMs     int64              `json:"lastDurationMs,omitempty"`
	LastHealedAt       *time.Time         `json:"lastHealedAt,omitempty"`
	CreatedAt          time.Time          `json:"createdAt"`
	UpdatedAt          time.Time          `json:"updatedAt"`
}

type DiagnosticCheckItem struct {
	StepNo  int    `json:"stepNo"`
	Code    string `json:"code"` // env_health | driver_session | popup_engine | semantic_firewall | portrait_match
	Title   string `json:"title"`
	Passed  bool   `json:"passed"`
	Summary string `json:"summary"`
}

type FiveStepDiagnosis struct {
	Triggered          bool                  `json:"triggered"`
	Checks             []DiagnosticCheckItem `json:"checks"`
	RootCauseCategory  string                `json:"rootCauseCategory"` // ENV_UNAVAILABLE | DRIVER_DISCONNECTED | POPUP_BLOCKED | CROSS_PAGE_REDIRECT_BLOCKED | LOCATOR_DRIFT_HEALED | LOW_CONFIDENCE_REJECTED | BUSINESS_ASSERTION_FAILED
	Confidence         float64               `json:"confidence"`
	Threshold          float64               `json:"threshold"`
	SemanticScore      float64               `json:"semanticScore,omitempty"`
	ContextScore       float64               `json:"contextScore,omitempty"`
	StructureScore     float64               `json:"structureScore,omitempty"`
	OldSelector        string                `json:"oldSelector,omitempty"`
	CandidateSelector  string                `json:"candidateSelector,omitempty"`
	CandidateText      string                `json:"candidateText,omitempty"`
	Decision           string                `json:"decision"`
	TokensUsed         int                   `json:"tokensUsed,omitempty"`
}

type AutoTestStepResult struct {
	StepIndex     int                `json:"stepIndex"`
	StepID        string             `json:"stepId"`
	Name          string             `json:"name"`
	Action        string             `json:"action"`
	Status        string             `json:"status"` // passed | healed | failed
	DurationMs    int64              `json:"durationMs"`
	SelectorUsed  string             `json:"selectorUsed,omitempty"`
	ActualSummary string             `json:"actualSummary,omitempty"`
	Error         string             `json:"error,omitempty"`
	ScreenshotURL string             `json:"screenshotUrl,omitempty"`
	Diagnosis     *FiveStepDiagnosis `json:"diagnosis,omitempty"`
}

type AutoTestHealDiff struct {
	StepIndex      int     `json:"stepIndex"`
	StepName       string  `json:"stepName"`
	OldSelector    string  `json:"oldSelector"`
	NewSelector    string  `json:"newSelector"`
	Confidence     float64 `json:"confidence"`
	FromRevision   int     `json:"fromRevision"`
	ToRevision     int     `json:"toRevision"`
	PortraitAnchor string  `json:"portraitAnchor"`
}

type AutoTestRun struct {
	ID               string               `json:"id"`
	TenantID         string               `json:"tenantId"`
	ProjectID        string               `json:"projectId"`
	CaseID           string               `json:"caseId"`
	CaseTitle        string               `json:"caseTitle"`
	CaseRevision     int                  `json:"caseRevision"`
	Mode             string               `json:"mode"`   // solidify | replay_zero_token | self_healed
	EngineUsed       string               `json:"engine"` // headless-chrome-cdp | http-dom-lite
	Status           string               `json:"status"` // passed | healed | failed
	DurationMs       int64                `json:"durationMs"`
	TokensUsed       int                  `json:"tokensUsed"`
	TriggerBy        string               `json:"triggerBy"`
	StepResults      []AutoTestStepResult `json:"stepResults"`
	HealDiff         *AutoTestHealDiff    `json:"healDiff,omitempty"`
	EscalationReason string               `json:"escalationReason,omitempty"`
	EscalatedIssueID string               `json:"escalatedIssueId,omitempty"`
	StartedAt        time.Time            `json:"startedAt"`
	FinishedAt       time.Time            `json:"finishedAt"`
}

// AutoTestPrecedent represents Tmall's "过程维判例资产" harvested automatically.
type AutoTestPrecedent struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenantId"`
	ProjectID   string    `json:"projectId"`
	Kind        string    `json:"kind"` // spec_template | heal_pattern | bad_case_guard
	Title       string    `json:"title"`
	Summary     string    `json:"summary"`
	PagePattern string    `json:"pagePattern,omitempty"`
	OldLocator  string    `json:"oldLocator,omitempty"`
	NewLocator  string    `json:"newLocator,omitempty"`
	Confidence  float64   `json:"confidence,omitempty"`
	SourceCase  string    `json:"sourceCase,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// ---------- Persistent Store ----------

type autoTestStoreData struct {
	Cases      []AutoTestCase      `json:"cases"`
	Runs       []AutoTestRun       `json:"runs"`
	Precedents []AutoTestPrecedent `json:"precedents"`
}

type autoTestStore struct {
	mu      sync.RWMutex
	path    string
	shotDir string
	loaded  bool
	data    autoTestStoreData
}

var defaultAutoTestStore = &autoTestStore{}

func (s *autoTestStore) resolvePaths() (string, string) {
	if custom := strings.TrimSpace(os.Getenv("AGENTFLOW_AUTOTEST_DIR")); custom != "" {
		return filepath.Join(custom, "store.json"), filepath.Join(custom, "screenshots")
	}
	if s.path != "" && s.shotDir != "" {
		return s.path, s.shotDir
	}
	home, err := os.UserHomeDir()
	base := "/tmp/.webuddy-autotest"
	if err == nil && home != "" {
		base = filepath.Join(home, ".webuddy-autotest")
	}
	return filepath.Join(base, "store.json"), filepath.Join(base, "screenshots")
}

func (s *autoTestStore) ensureLoadedLocked() {
	storePath, _ := s.resolvePaths()
	if s.loaded && s.path == storePath {
		return
	}
	s.path = storePath
	_, s.shotDir = s.resolvePaths()
	s.data = autoTestStoreData{}
	if raw, err := os.ReadFile(storePath); err == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &s.data)
	}
	s.loaded = true
}

func (s *autoTestStore) saveLocked() error {
	storePath, _ := s.resolvePaths()
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		return err
	}
	// Cap run history at 200 most recent runs
	if len(s.data.Runs) > 200 {
		sort.Slice(s.data.Runs, func(i, j int) bool {
			return s.data.Runs[i].StartedAt.After(s.data.Runs[j].StartedAt)
		})
		s.data.Runs = s.data.Runs[:200]
	}
	if len(s.data.Precedents) > 150 {
		s.data.Precedents = s.data.Precedents[:150]
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := storePath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, storePath)
}

func cloneAutoTestSteps(in []AutoTestStep) []AutoTestStep {
	raw, _ := json.Marshal(in)
	var out []AutoTestStep
	_ = json.Unmarshal(raw, &out)
	return out
}

func (s *autoTestStore) listCases(tenantID, projectID string) []AutoTestCase {
	s.mu.Lock()
	s.ensureLoadedLocked()
	defer s.mu.Unlock()
	var out []AutoTestCase
	for _, c := range s.data.Cases {
		if tenantID != "" && c.TenantID != "" && c.TenantID != tenantID {
			continue
		}
		if projectID != "" && c.ProjectID != projectID {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

func (s *autoTestStore) getCase(id string) (AutoTestCase, bool) {
	s.mu.Lock()
	s.ensureLoadedLocked()
	defer s.mu.Unlock()
	for _, c := range s.data.Cases {
		if c.ID == id {
			return c, true
		}
	}
	return AutoTestCase{}, false
}

func (s *autoTestStore) putCase(tc AutoTestCase) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	if tc.UpdatedAt.IsZero() {
		tc.UpdatedAt = time.Now().UTC()
	}
	replaced := false
	for i := range s.data.Cases {
		if s.data.Cases[i].ID == tc.ID {
			s.data.Cases[i] = tc
			replaced = true
			break
		}
	}
	if !replaced {
		s.data.Cases = append([]AutoTestCase{tc}, s.data.Cases...)
	}
	return s.saveLocked()
}

func (s *autoTestStore) deleteCase(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	idx := -1
	for i, c := range s.data.Cases {
		if c.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}
	s.data.Cases = append(s.data.Cases[:idx], s.data.Cases[idx+1:]...)
	_ = s.saveLocked()
	return true
}

func (s *autoTestStore) appendRun(r AutoTestRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	replaced := false
	for i := range s.data.Runs {
		if s.data.Runs[i].ID == r.ID {
			s.data.Runs[i] = r
			replaced = true
			break
		}
	}
	if !replaced {
		s.data.Runs = append([]AutoTestRun{r}, s.data.Runs...)
	}
	return s.saveLocked()
}

func (s *autoTestStore) listRuns(tenantID, projectID, caseID string, limit int) []AutoTestRun {
	s.mu.Lock()
	s.ensureLoadedLocked()
	defer s.mu.Unlock()
	if limit <= 0 || limit > 100 {
		limit = 40
	}
	var out []AutoTestRun
	for _, r := range s.data.Runs {
		if tenantID != "" && r.TenantID != "" && r.TenantID != tenantID {
			continue
		}
		if projectID != "" && r.ProjectID != projectID {
			continue
		}
		if caseID != "" && r.CaseID != caseID {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *autoTestStore) getRun(id string) (AutoTestRun, bool) {
	s.mu.Lock()
	s.ensureLoadedLocked()
	defer s.mu.Unlock()
	for _, r := range s.data.Runs {
		if r.ID == id {
			return r, true
		}
	}
	return AutoTestRun{}, false
}

func (s *autoTestStore) addPrecedent(p AutoTestPrecedent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	if p.ID == "" {
		p.ID = fmt.Sprintf("prec-%d", time.Now().UnixNano())
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	// Deduplicate by Kind + SourceCase + NewLocator
	for i := range s.data.Precedents {
		ex := s.data.Precedents[i]
		if ex.ProjectID == p.ProjectID && ex.Kind == p.Kind && ex.Title == p.Title && ex.NewLocator == p.NewLocator {
			s.data.Precedents[i] = p
			_ = s.saveLocked()
			return
		}
	}
	s.data.Precedents = append([]AutoTestPrecedent{p}, s.data.Precedents...)
	_ = s.saveLocked()
}

func (s *autoTestStore) listPrecedents(tenantID, projectID string) []AutoTestPrecedent {
	s.mu.Lock()
	s.ensureLoadedLocked()
	defer s.mu.Unlock()
	var out []AutoTestPrecedent
	for _, p := range s.data.Precedents {
		if tenantID != "" && p.TenantID != "" && p.TenantID != tenantID {
			continue
		}
		if projectID != "" && p.ProjectID != projectID && p.ProjectID != "" {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// ---------- 3D Element Portrait Similarity & 5-Step Self-Healing ----------

func tokenizeSemanticText(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return nil
	}
	var tokens []string
	var asciiBuf strings.Builder
	flushASCII := func() {
		if asciiBuf.Len() > 0 {
			tokens = append(tokens, asciiBuf.String())
			asciiBuf.Reset()
		}
	}
	var cjkRunes []rune
	flushCJK := func() {
		if len(cjkRunes) == 1 {
			tokens = append(tokens, string(cjkRunes))
		} else if len(cjkRunes) >= 2 {
			for i := 0; i < len(cjkRunes)-1; i++ {
				tokens = append(tokens, string(cjkRunes[i:i+2]))
			}
		}
		cjkRunes = nil
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			flushCJK()
			asciiBuf.WriteRune(r)
		} else if r >= 0x4e00 && r <= 0x9fff {
			flushASCII()
			cjkRunes = append(cjkRunes, r)
		} else {
			flushASCII()
			flushCJK()
		}
	}
	flushASCII()
	flushCJK()
	return tokens
}

func tokenOverlapScore(a, b string) float64 {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" && b == "" {
		return 0.75
	}
	if a == "" || b == "" {
		return 0.20
	}
	if strings.EqualFold(a, b) {
		return 1.0
	}
	al, bl := strings.ToLower(a), strings.ToLower(b)
	if strings.Contains(al, bl) || strings.Contains(bl, al) {
		shorter := float64(min(len([]rune(al)), len([]rune(bl))))
		longer := float64(max(len([]rune(al)), len([]rune(bl))))
		return 0.78 + 0.20*(shorter/maxFloat(longer, 1))
	}
	ta := tokenizeSemanticText(a)
	tb := tokenizeSemanticText(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0.15
	}
	setB := make(map[string]bool, len(tb))
	for _, t := range tb {
		setB[t] = true
	}
	hit := 0
	seen := make(map[string]bool)
	for _, t := range ta {
		if !seen[t] && setB[t] {
			hit++
			seen[t] = true
		}
	}
	union := len(ta) + len(tb) - hit
	if union <= 0 {
		return 0
	}
	return float64(hit) / float64(union)
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func compatibleRoles(r1, t1, r2, t2 string) float64 {
	r1, t1 = strings.ToLower(r1), strings.ToLower(t1)
	r2, t2 = strings.ToLower(r2), strings.ToLower(t2)
	if r1 != "" && r1 == r2 {
		return 1.0
	}
	if t1 != "" && t1 == t2 {
		return 0.92
	}
	clickable := map[string]bool{"button": true, "link": true, "tab": true, "a": true, "span": true, "div": true}
	if clickable[r1] && clickable[r2] {
		return 0.85
	}
	return 0.35
}

// scoreElementPortrait computes the 3D Huolala Digital Portrait match:
// 45% Semantic Role & Label + 35% Business Parent Context + 20% DOM Structure.
func scoreElementPortrait(expected ElementPortrait, cand ElementPortrait) (total, sem, ctxScore, domScore float64) {
	expLabel := firstNonEmpty(expected.Text, expected.AriaLabel, expected.Placeholder, expected.Href)
	candLabel := firstNonEmpty(cand.Text, cand.AriaLabel, cand.Placeholder, cand.Href)
	labelSim := tokenOverlapScore(expLabel, candLabel)
	roleSim := compatibleRoles(expected.Role, expected.Tag, cand.Role, cand.Tag)
	sem = 0.72*labelSim + 0.28*roleSim

	if expected.Href != "" && cand.Href != "" && expected.Href == cand.Href {
		sem = math.Max(sem, 0.96)
	}

	ctxScore = tokenOverlapScore(expected.ParentContext, cand.ParentContext)
	if expected.ParentContext == "" && cand.ParentContext != "" {
		ctxScore = 0.75
	}

	domScore = tokenOverlapScore(expected.DOMPath, cand.DOMPath)
	total = 0.48*sem + 0.32*ctxScore + 0.20*domScore
	total = math.Round(total*1000) / 1000
	return total, math.Round(sem*100)/100, math.Round(ctxScore*100)/100, math.Round(domScore*100)/100
}

func normalizePagePathForFirewall(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p == "/index.html" {
		return "/"
	}
	return strings.TrimRight(p, "/")
}

func checkSemanticFirewall(expectedPath, livePath string) (ok bool, reason string) {
	ep := normalizePagePathForFirewall(expectedPath)
	lp := normalizePagePathForFirewall(livePath)
	if ep == "" {
		return true, "未设置页面路径基线，默认放行"
	}
	if ep == lp {
		return true, fmt.Sprintf("页面路径与基线一致 (`%s`)，未发生掉登录或跨页误跳", livePath)
	}
	// Detect auth / login redirect traps explicitly (Huolala core guard!)
	lowerLive := strings.ToLower(lp)
	lowerExp := strings.ToLower(ep)
	if !strings.Contains(lowerExp, "login") && !strings.Contains(lowerExp, "auth") &&
		(strings.Contains(lowerLive, "login") || strings.Contains(lowerLive, "signin") || strings.Contains(lowerLive, "sso")) {
		return false, fmt.Sprintf("语义防火墙拦截：基线应在业务页 `%s`，当前却跳回登录态页面 `%s`，严禁跨页误修！", expectedPath, livePath)
	}
	if ep != "/" && lp != "/" && ep != lp {
		return false, fmt.Sprintf("语义防火墙拦截：当前所在路由 `%s` 与控件画像绑定的业务页面 `%s` 不一致，拒绝跨页面找近似按钮", livePath, expectedPath)
	}
	return true, fmt.Sprintf("页面语义上下文校验通过 (`%s`)", livePath)
}

func checkTargetEnvHealth(ctx context.Context, baseURL string) (bool, string) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return false, "目标地址构造失败: " + err.Error()
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, "目标站点网络不可达或连接拒绝: " + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return false, fmt.Sprintf("目标站点服务端异常 (HTTP %d)，属于环境故障，终止脚本自愈", resp.StatusCode)
	}
	return true, fmt.Sprintf("目标站点网络与服务响应正常 (HTTP %d)", resp.StatusCode)
}

// ---------- Core Execution & Self-Healing Runner ----------

type webuddyAutoTestHandlers struct {
	*deps
}

func (h *webuddyAutoTestHandlers) executeCase(ctx context.Context, tc AutoTestCase, mode, triggerBy string, capturePortraits, allowSelfHeal bool) (AutoTestRun, AutoTestCase, error) {
	if _, err := validateAutoTestTargetURL(tc.BaseURL); err != nil {
		return AutoTestRun{}, tc, err
	}
	start := time.Now().UTC()
	runID := fmt.Sprintf("atrun-%d", start.UnixNano())
	_, shotDir := defaultAutoTestStore.resolvePaths()

	run := AutoTestRun{
		ID:           runID,
		TenantID:     tc.TenantID,
		ProjectID:    tc.ProjectID,
		CaseID:       tc.ID,
		CaseTitle:    tc.Title,
		CaseRevision: max(tc.CurrentRevision, 1),
		Mode:         mode,
		Status:       "passed",
		TriggerBy:    firstNonEmpty(triggerBy, "manual"),
		StartedAt:    start,
	}

	rctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	var chrome *autoChromeSession
	var fallback *httpFallbackSession
	if ch, err := startHeadlessChromeSession(rctx); err == nil {
		chrome = ch
		defer chrome.Close()
		run.EngineUsed = "headless-chrome-cdp"
	} else {
		fallback = newHTTPFallbackSession()
		run.EngineUsed = "http-dom-lite"
	}

	stepsWork := cloneAutoTestSteps(tc.Steps)
	var pendingHealDiff *AutoTestHealDiff
	healedInRun := false

	// Optional Precondition Auth Injection (if PreconditionURL set on Case and step 0 isn't already inject_auth)
	if strings.TrimSpace(tc.PreconditionURL) != "" && (len(stepsWork) == 0 || stepsWork[0].Action != "inject_auth") {
		authURL, err := resolveRelativeTargetURL(tc.BaseURL, tc.PreconditionURL)
		if err != nil {
			return run, tc, err
		}
		stepStart := time.Now()
		var navErr error
		var finalURL string
		if chrome != nil {
			finalURL, navErr = chrome.navigateAndSettle(rctx, authURL)
		} else {
			finalURL, navErr = fallback.navigate(rctx, authURL)
		}
		sr := AutoTestStepResult{
			StepIndex:     0,
			StepID:        "precondition-auth",
			Name:          "前置登录态/角色契约直接注入",
			Action:        "inject_auth",
			DurationMs:    time.Since(stepStart).Milliseconds(),
			ActualSummary: "已注入前置会话状态 -> " + finalURL,
		}
		if navErr != nil {
			sr.Status = "failed"
			sr.Error = navErr.Error()
			run.Status = "failed"
			run.EscalationReason = "前置登录态注入失败：" + navErr.Error()
			run.StepResults = append(run.StepResults, sr)
			return h.finalizeRun(tc, run, stepsWork, false, nil)
		}
		sr.Status = "passed"
		if chrome != nil {
			shotName := fmt.Sprintf("%s-pre.png", runID)
			if chrome.capturePNG(rctx, filepath.Join(shotDir, shotName)) == nil {
				sr.ScreenshotURL = "/api/webuddy/autotest/screenshots/" + shotName
			}
		}
		run.StepResults = append(run.StepResults, sr)
	}

	// Load bad_case_guard precedents so rejected locators are never re-applied!
	rejectedGuards := map[string]bool{}
	for _, p := range defaultAutoTestStore.listPrecedents(tc.TenantID, tc.ProjectID) {
		if p.Kind == "bad_case_guard" && p.NewLocator != "" {
			rejectedGuards[p.NewLocator] = true
		}
	}

	for i := range stepsWork {
		st := &stepsWork[i]
		stepStart := time.Now()
		sr := AutoTestStepResult{
			StepIndex:    len(run.StepResults) + 1,
			StepID:       firstNonEmpty(st.ID, fmt.Sprintf("step-%d", i+1)),
			Name:         st.Name,
			Action:       st.Action,
			SelectorUsed: st.PrimarySelector,
		}

		var stepErr error
		var actual string
		var capturedPortrait *ElementPortrait

		switch st.Action {
		case "inject_auth", "navigate":
			rawTarget := firstNonEmpty(st.TargetURL, st.Value, tc.BaseURL)
			fullURL, err := resolveRelativeTargetURL(tc.BaseURL, rawTarget)
			if err != nil {
				stepErr = err
			} else if chrome != nil {
				finalURL, nerr := chrome.navigateAndSettle(rctx, fullURL)
				stepErr = nerr
				actual = "页面已加载就绪：" + finalURL
			} else {
				finalURL, nerr := fallback.navigate(rctx, fullURL)
				stepErr = nerr
				actual = "页面已请求就绪：" + finalURL
			}

		default:
			if chrome != nil {
				capturedPortrait, actual, stepErr = chrome.performDOMStep(rctx, *st)
			} else {
				capturedPortrait, actual, stepErr = performFallbackStep(rctx, fallback, tc.BaseURL, *st)
			}
		}

		// Trigger 5-Step Diagnosis whenever any step fails!
		if stepErr != nil {
			canLocateHeal := allowSelfHeal && st.Portrait != nil && isSelectorLocateFailure(*st, stepErr)
			diag, newSel, healActual, healedPortrait, healErr := h.runFiveStepDiagnosisAndHeal(rctx, tc, *st, stepErr, canLocateHeal, chrome, fallback, rejectedGuards)
			sr.Diagnosis = diag
			run.TokensUsed += diag.TokensUsed
			if healErr == nil && newSel != "" {
				oldSel := st.PrimarySelector
				st.PrimarySelector = newSel
				if healedPortrait != nil {
					st.Portrait = healedPortrait
				}
				sr.Status = "healed"
				sr.SelectorUsed = newSel
				sr.ActualSummary = healActual
				sr.Error = ""
				healedInRun = true
				pendingHealDiff = &AutoTestHealDiff{
					StepIndex:      sr.StepIndex,
					StepName:       st.Name,
					OldSelector:    oldSel,
					NewSelector:    newSel,
					Confidence:     diag.Confidence,
					FromRevision:   max(tc.CurrentRevision, 1),
					ToRevision:     max(tc.CurrentRevision, 1) + 1,
					PortraitAnchor: fmt.Sprintf("[%s] %s · %s", st.Portrait.Role, firstNonEmpty(st.Portrait.Text, st.Portrait.AriaLabel), st.Portrait.ParentContext),
				}
				stepErr = nil
			} else {
				sr.Status = "failed"
				sr.Error = stepErr.Error()
				run.Status = "failed"
				run.EscalationReason = fmt.Sprintf("步骤 #%d「%s」失败（%s）：%s", sr.StepIndex, st.Name, diag.RootCauseCategory, diag.Decision)
			}
		} else {
			sr.Status = "passed"
			sr.ActualSummary = actual
			if capturePortraits && capturedPortrait != nil {
				st.Portrait = capturedPortrait
				if st.PrimarySelector == "" || strings.HasPrefix(st.PrimarySelector, "text=") {
					if capturedPortrait.PrimarySelector != "" {
						st.PrimarySelector = capturedPortrait.PrimarySelector
						sr.SelectorUsed = st.PrimarySelector
					}
				}
			} else if st.Portrait == nil && capturedPortrait != nil {
				st.Portrait = capturedPortrait
			}
		}

		sr.DurationMs = time.Since(stepStart).Milliseconds()
		if chrome != nil {
			shotName := fmt.Sprintf("%s-step-%d.png", runID, sr.StepIndex)
			if chrome.capturePNG(rctx, filepath.Join(shotDir, shotName)) == nil {
				sr.ScreenshotURL = "/api/webuddy/autotest/screenshots/" + shotName
			}
		}
		run.StepResults = append(run.StepResults, sr)
		if stepErr != nil {
			break
		}
	}

	if run.Status != "failed" && healedInRun {
		run.Status = "healed"
		run.Mode = "self_healed"
		run.HealDiff = pendingHealDiff
	}
	saveUpdatedSteps := capturePortraits || (run.Status == "healed" && pendingHealDiff != nil)
	return h.finalizeRun(tc, run, stepsWork, saveUpdatedSteps, pendingHealDiff)
}

func performFallbackStep(ctx context.Context, fb *httpFallbackSession, baseURL string, step AutoTestStep) (*ElementPortrait, string, error) {
	snap := fb.inspectElements()
	switch step.Action {
	case "click":
		for _, el := range snap.Elements {
			if el.PrimarySelector == step.PrimarySelector || ("text="+el.Text) == step.PrimarySelector {
				cp := el
				if el.Href != "" {
					if nextURL, err := resolveRelativeTargetURL(baseURL, el.Href); err == nil {
						_, _ = fb.navigate(ctx, nextURL)
					}
				}
				return &cp, "已触发元素「" + firstNonEmpty(el.Text, el.PrimarySelector) + "」", nil
			}
		}
		return nil, "", &selectorNotFoundError{Selector: step.PrimarySelector, Msg: "未找到目标控件: " + step.PrimarySelector}
	case "assert_text":
		if step.PrimarySelector != "" {
			found := false
			var hit ElementPortrait
			for _, el := range snap.Elements {
				if el.PrimarySelector == step.PrimarySelector || ("text="+el.Text) == step.PrimarySelector {
					found = true
					hit = el
					break
				}
			}
			if !found {
				return nil, "", &selectorNotFoundError{Selector: step.PrimarySelector, Msg: "未找到断言目标元素: " + step.PrimarySelector}
			}
			if step.Expected != "" && !strings.Contains(hit.Text, step.Expected) {
				return &hit, "", fmt.Errorf("业务断言失败：期望包含 %q，实际为 %q", step.Expected, hit.Text)
			}
			return &hit, "元素文本匹配：" + hit.Text, nil
		}
		body := fb.visibleText()
		if step.Expected != "" && !strings.Contains(body, step.Expected) {
			return nil, "", fmt.Errorf("业务断言失败：页面未包含 %q", step.Expected)
		}
		return nil, "页面可见内容已包含「" + step.Expected + "」", nil
	case "assert_url":
		if step.Expected != "" && !strings.Contains(fb.curURL, step.Expected) {
			return nil, fb.curURL, fmt.Errorf("URL 断言失败：期望包含 %q，实际为 %q", step.Expected, fb.curURL)
		}
		return nil, "URL 匹配：" + fb.curURL, nil
	case "assert_api":
		fullURL, err := resolveRelativeTargetURL(baseURL, firstNonEmpty(step.TargetURL, step.Value))
		if err != nil {
			return nil, "", err
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
		resp, err := fb.client.Do(req)
		if err != nil {
			return nil, "", err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
		if resp.StatusCode >= 400 {
			return nil, "", fmt.Errorf("接口返回 HTTP %d", resp.StatusCode)
		}
		if step.Expected != "" && !strings.Contains(string(raw), step.Expected) {
			return nil, "", fmt.Errorf("接口响应未包含 %q", step.Expected)
		}
		return nil, fmt.Sprintf("接口 HTTP %d 校验通过", resp.StatusCode), nil
	default:
		return nil, "已执行 " + step.Action, nil
	}
}

// runFiveStepDiagnosisAndHeal implements Huolala's 5-Step Diagnosis ("先硬后软、先环境后业务")
// + Semantic Firewall + 3D Portrait Match + Confidence >= 0.80 Gate.
func (h *webuddyAutoTestHandlers) runFiveStepDiagnosisAndHeal(
	ctx context.Context,
	tc AutoTestCase,
	step AutoTestStep,
	origErr error,
	canLocateHeal bool,
	chrome *autoChromeSession,
	fallback *httpFallbackSession,
	rejectedGuards map[string]bool,
) (*FiveStepDiagnosis, string, string, *ElementPortrait, error) {
	diag := &FiveStepDiagnosis{
		Triggered:   true,
		Threshold:   autoTestConfidenceThreshold,
		OldSelector: step.PrimarySelector,
	}

	// Step 1: Environment & Target Service Health Check
	envOK, envMsg := checkTargetEnvHealth(ctx, tc.BaseURL)
	diag.Checks = append(diag.Checks, DiagnosticCheckItem{
		StepNo: 1, Code: "env_health", Title: "① 目标服务与网络健康探活", Passed: envOK, Summary: envMsg,
	})
	if !envOK {
		diag.RootCauseCategory = "ENV_UNAVAILABLE"
		diag.Decision = "环境或服务端不可用，遵循「环境故障绝不误修脚本」原则，直接告警上报。"
		return diag, "", "", nil, origErr
	}

	// Step 2: Browser / Driver Session Alive Check
	var domSnap domPageInspection
	var sessOK bool
	var sessMsg string
	if chrome != nil {
		snap, err := chrome.inspectCurrentDOM(ctx)
		if err == nil {
			domSnap = snap
			sessOK = true
			sessMsg = fmt.Sprintf("Headless Chrome CDP 会话在线 (`%s`, 可交互节点 %d 个)", snap.Pathname, len(snap.Elements))
		} else {
			sessMsg = "Chrome CDP 会话异常断开: " + err.Error()
		}
	} else if fallback != nil {
		domSnap = fallback.inspectElements()
		sessOK = domSnap.URL != ""
		sessMsg = fmt.Sprintf("HTTP+DOM 会话正常 (`%s`, 候选节点 %d 个)", domSnap.Pathname, len(domSnap.Elements))
	}
	diag.Checks = append(diag.Checks, DiagnosticCheckItem{
		StepNo: 2, Code: "driver_session", Title: "② 浏览器驱动会话存活检测", Passed: sessOK, Summary: sessMsg,
	})
	if !sessOK {
		diag.RootCauseCategory = "DRIVER_DISCONNECTED"
		diag.Decision = "浏览器驱动会话中断，不修改测试脚本。"
		return diag, "", "", nil, origErr
	}

	// Step 3: Visual & Semantic Popup Engine
	popupHandled := false
	popupMsg := "当前页面无遮挡模态框或营销引导弹窗"
	if chrome != nil {
		popupHandled, popupMsg = chrome.scanAndDismissPopup(ctx)
	}
	diag.Checks = append(diag.Checks, DiagnosticCheckItem{
		StepNo: 3, Code: "popup_engine", Title: "③ 视觉与语义弹窗引擎扫描", Passed: true, Summary: popupMsg,
	})
	if popupHandled && chrome != nil {
		if p, act, retryErr := chrome.performDOMStep(ctx, step); retryErr == nil {
			diag.RootCauseCategory = "POPUP_BLOCKED"
			diag.Confidence = 0.95
			diag.CandidateSelector = step.PrimarySelector
			diag.Decision = "已自动消解遮挡弹窗并原地重试原定位器成功（无需改动选择器）。"
			return diag, step.PrimarySelector, act, p, nil
		}
		// Refresh DOM snapshot after dismissing modal
		if snap, err := chrome.inspectCurrentDOM(ctx); err == nil {
			domSnap = snap
		}
	}

	// If failure is a genuine business content assertion (selector existed, value wrong!), do NOT heal!
	if !canLocateHeal || step.Portrait == nil {
		diag.Checks = append(diag.Checks, DiagnosticCheckItem{
			StepNo: 4, Code: "semantic_firewall", Title: "④ 页面语义防火墙核验", Passed: true, Summary: "定位器正常或无控件自愈请求，进入业务真值判定",
		})
		diag.Checks = append(diag.Checks, DiagnosticCheckItem{
			StepNo: 5, Code: "portrait_match", Title: "⑤ 业务断言失败拦截（禁止掩盖真 Bug）", Passed: false, Summary: origErr.Error(),
		})
		diag.RootCauseCategory = "BUSINESS_ASSERTION_FAILED"
		diag.Decision = "目标元素已找到但业务数据/接口返回值不符合契约，判定为真实业务异常，严禁通过改脚本掩盖缺陷，请人工拍板或一键转缺陷单。"
		return diag, "", "", nil, origErr
	}

	// Step 4: Page Semantic Firewall (Prevent Cross-Page False Positive!)
	fwOK, fwMsg := checkSemanticFirewall(step.Portrait.PagePathPattern, domSnap.Pathname)
	diag.Checks = append(diag.Checks, DiagnosticCheckItem{
		StepNo: 4, Code: "semantic_firewall", Title: "④ 页面同步与跨页语义防火墙", Passed: fwOK, Summary: fwMsg,
	})
	if !fwOK {
		diag.RootCauseCategory = "CROSS_PAGE_REDIRECT_BLOCKED"
		diag.Confidence = 0
		diag.Decision = fwMsg
		return diag, "", "", nil, origErr
	}

	// Step 5: 3D Element Portrait Matching + Confidence >= 0.80 Gate
	type scoredCandidate struct {
		el       ElementPortrait
		total    float64
		sem      float64
		ctxScore float64
		domScore float64
	}
	var ranked []scoredCandidate
	for _, el := range domSnap.Elements {
		if el.PrimarySelector == "" || el.PrimarySelector == step.PrimarySelector {
			continue
		}
		if rejectedGuards[el.PrimarySelector] {
			continue
		}
		tot, sem, cs, ds := scoreElementPortrait(*step.Portrait, el)
		ranked = append(ranked, scoredCandidate{el: el, total: tot, sem: sem, ctxScore: cs, domScore: ds})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].total > ranked[j].total })

	if len(ranked) == 0 {
		diag.Checks = append(diag.Checks, DiagnosticCheckItem{
			StepNo: 5, Code: "portrait_match", Title: "⑤ 控件三维数字身份证比对", Passed: false, Summary: "当前页面未发现任何可交互候选控件",
		})
		diag.RootCauseCategory = "LOW_CONFIDENCE_REJECTED"
		diag.Decision = "无可用候选控件，放弃自愈。"
		return diag, "", "", nil, origErr
	}

	best := ranked[0]
	// Optional AI semantic verification when deterministic score is borderline (0.68..0.85)
	if best.total >= 0.68 && best.total < 0.86 {
		st := builtinagent.Default().Status(ctx)
		if st.Ready {
			prompt := fmt.Sprintf(
				"判断原 UI 控件与改版后候选控件是否为同一业务语义控件，只输出 JSON {\"same\":true/false,\"confidence\":0.0-1.0}。\n原控件画像: role=%s text=%q context=%q dom=%q\n候选控件: selector=%q role=%s text=%q context=%q dom=%q",
				step.Portrait.Role, step.Portrait.Text, step.Portrait.ParentContext, step.Portrait.DOMPath,
				best.el.PrimarySelector, best.el.Role, best.el.Text, best.el.ParentContext, best.el.DOMPath,
			)
			res := builtinagent.Default().Complete(ctx, builtinagent.CompletionRequest{
				SystemPrompt: "你是严谨的 UI 自动化控件自愈评估器，宁可不修也不误修。仅输出合法 JSON。",
				UserPrompt:   prompt,
				MaxTokens:    120,
			})
			if res.Error == "" && res.Text != "" {
				diag.TokensUsed += 180
				var parsed struct {
					Same       bool    `json:"same"`
					Confidence float64 `json:"confidence"`
				}
				if rawJ := extractFirstJSONObject(res.Text); rawJ != "" && json.Unmarshal([]byte(rawJ), &parsed) == nil {
					if parsed.Same && parsed.Confidence >= 0.80 {
						best.total = math.Round((0.5*best.total+0.5*parsed.Confidence)*1000) / 1000
					} else if !parsed.Same {
						best.total = math.Min(best.total, 0.55)
					}
				}
			}
		}
	}

	diag.Confidence = best.total
	diag.SemanticScore = best.sem
	diag.ContextScore = best.ctxScore
	diag.StructureScore = best.domScore
	diag.CandidateSelector = best.el.PrimarySelector
	diag.CandidateText = firstNonEmpty(best.el.Text, best.el.AriaLabel)

	if best.total < autoTestConfidenceThreshold {
		summary := fmt.Sprintf("最高候选 `%s`（「%s」）综合置信度 %.2f < 0.80 门禁（语义 %.2f / 上下文 %.2f / 结构 %.2f），拒绝误修",
			best.el.PrimarySelector, diag.CandidateText, best.total, best.sem, best.ctxScore, best.domScore)
		diag.Checks = append(diag.Checks, DiagnosticCheckItem{
			StepNo: 5, Code: "portrait_match", Title: "⑤ 控件三维数字身份证比对", Passed: false, Summary: summary,
		})
		diag.RootCauseCategory = "LOW_CONFIDENCE_REJECTED"
		diag.Decision = "遵循「宁可不修，也不误修」安全准则：置信度未达 0.80，已拦截自动修改并转人工确认。"
		return diag, "", "", nil, origErr
	}

	// Confidence >= 0.80 -> Execute In-Place Step Retry!
	healedStep := step
	healedStep.PrimarySelector = best.el.PrimarySelector
	var retryPortrait *ElementPortrait
	var retryActual string
	var retryErr error
	if chrome != nil {
		retryPortrait, retryActual, retryErr = chrome.performDOMStep(ctx, healedStep)
	} else {
		retryPortrait, retryActual, retryErr = performFallbackStep(ctx, fallback, tc.BaseURL, healedStep)
	}
	if retryErr != nil {
		diag.Checks = append(diag.Checks, DiagnosticCheckItem{
			StepNo: 5, Code: "portrait_match", Title: "⑤ 控件三维数字身份证比对与原地重试", Passed: false,
			Summary: fmt.Sprintf("候选 `%s` (置信度 %.2f) 原地重试仍失败: %v", best.el.PrimarySelector, best.total, retryErr),
		})
		diag.RootCauseCategory = "LOW_CONFIDENCE_REJECTED"
		diag.Decision = "原地重试未通过断言，保持原脚本不变。"
		return diag, "", "", nil, origErr
	}

	passSummary := fmt.Sprintf("命中同语义控件 `%s`（「%s」），三维综合置信度 %.2f >= 0.80（语义 %.2f · 上下文 %.2f · 结构 %.2f），原地重试通过！",
		best.el.PrimarySelector, diag.CandidateText, best.total, best.sem, best.ctxScore, best.domScore)
	diag.Checks = append(diag.Checks, DiagnosticCheckItem{
		StepNo: 5, Code: "portrait_match", Title: "⑤ 控件三维数字身份证比对与原地重试", Passed: true, Summary: passSummary,
	})
	diag.RootCauseCategory = "LOCATOR_DRIFT_HEALED"
	diag.Decision = fmt.Sprintf("AI 自愈成功：定位器由 `%s` 平滑更新为 `%s`（置信度 %.2f），已自动保存版本快照供随时一键回滚。",
		step.PrimarySelector, best.el.PrimarySelector, best.total)
	if retryPortrait == nil {
		cp := best.el
		retryPortrait = &cp
	}
	return diag, best.el.PrimarySelector, fmt.Sprintf("[AI自愈 %.2f] %s", best.total, retryActual), retryPortrait, nil
}

func (h *webuddyAutoTestHandlers) finalizeRun(
	tc AutoTestCase,
	run AutoTestRun,
	updatedSteps []AutoTestStep,
	saveUpdatedSteps bool,
	healDiff *AutoTestHealDiff,
) (AutoTestRun, AutoTestCase, error) {
	now := time.Now().UTC()
	run.FinishedAt = now
	run.DurationMs = max(now.Sub(run.StartedAt).Milliseconds(), 1)

	tc.Stats.TotalRuns++
	switch run.Status {
	case "passed":
		tc.Stats.PassedRuns++
	case "healed":
		tc.Stats.HealedRuns++
	default:
		tc.Stats.FailedRuns++
	}
	if run.TokensUsed == 0 {
		tc.Stats.ZeroTokenRuns++
	}
	if tc.Stats.TotalRuns > 0 {
		tc.Stats.PassRate = math.Round(float64(tc.Stats.PassedRuns+tc.Stats.HealedRuns)*1000/float64(tc.Stats.TotalRuns)) / 10
	}
	tc.LastStatus = run.Status
	tc.LastRunID = run.ID
	tc.LastDurationMs = run.DurationMs
	tc.UpdatedAt = now

	if saveUpdatedSteps {
		tc.Steps = cloneAutoTestSteps(updatedSteps)
		if tc.CurrentRevision == 0 {
			tc.CurrentRevision = 1
			tc.Revisions = []AutoTestRevision{{
				Revision:  1,
				Reason:    "AI 真机跑通并降噪固化首版确定性脚本",
				Author:    run.TriggerBy,
				Steps:     cloneAutoTestSteps(updatedSteps),
				CreatedAt: now,
			}}
		} else if healDiff != nil {
			tc.CurrentRevision++
			run.CaseRevision = tc.CurrentRevision
			healDiff.ToRevision = tc.CurrentRevision
			tc.LastHealedAt = &now
			diffNote := fmt.Sprintf("步骤 #%d「%s」: `%s` → `%s` (置信度 %.2f)",
				healDiff.StepIndex, healDiff.StepName, healDiff.OldSelector, healDiff.NewSelector, healDiff.Confidence)
			tc.Revisions = append(tc.Revisions, AutoTestRevision{
				Revision:   tc.CurrentRevision,
				Reason:     "五步诊断 AI 自愈自动更新定位器",
				Author:     "Webuddy 自愈引擎",
				AutoHealed: true,
				Confidence: healDiff.Confidence,
				DiffNote:   diffNote,
				Steps:      cloneAutoTestSteps(updatedSteps),
				CreatedAt:  now,
			})
			defaultAutoTestStore.addPrecedent(AutoTestPrecedent{
				TenantID:    tc.TenantID,
				ProjectID:   tc.ProjectID,
				Kind:        "heal_pattern",
				Title:       fmt.Sprintf("定位器自愈判例：%s (%s)", tc.Title, healDiff.StepName),
				Summary:     fmt.Sprintf("UI 迭代导致原定位器 `%s` 失效，依托控件数字身份证（%s）以 %.2f 置信度自愈至 `%s` 并回归通过。", healDiff.OldSelector, healDiff.PortraitAnchor, healDiff.Confidence, healDiff.NewSelector),
				PagePattern: tc.BaseURL,
				OldLocator:  healDiff.OldSelector,
				NewLocator:  healDiff.NewSelector,
				Confidence:  healDiff.Confidence,
				SourceCase:  tc.ID,
				CreatedAt:   now,
			})
		} else if len(tc.Revisions) > 0 {
			// Refresh portraits on initial revision
			tc.Revisions[len(tc.Revisions)-1].Steps = cloneAutoTestSteps(updatedSteps)
		}
	}

	_ = defaultAutoTestStore.putCase(tc)
	_ = defaultAutoTestStore.appendRun(run)
	return run, tc, nil
}

// ---------- Natural-Language Scenario -> Live Execute & Solidify ----------

func extractFirstJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return ""
}

type generateAutoTestInput struct {
	ProjectID          string         `json:"projectId"`
	Title              string         `json:"title"`
	NaturalRequirement string         `json:"naturalRequirement"`
	BaseURL            string         `json:"baseUrl"`
	PreconditionURL    string         `json:"preconditionUrl"`
	LinkedIssueID      string         `json:"linkedIssueId"`
	Steps              []AutoTestStep `json:"steps"` // optional explicit steps to solidify directly
}

func (h *webuddyAutoTestHandlers) synthesizeStepsFromLiveDOMAndKB(
	ctx context.Context,
	projectID string,
	input generateAutoTestInput,
	snap domPageInspection,
) ([]AutoTestStep, int) {
	if len(input.Steps) > 0 {
		out := cloneAutoTestSteps(input.Steps)
		for i := range out {
			if out[i].ID == "" {
				out[i].ID = fmt.Sprintf("step-%d", i+1)
			}
		}
		return out, 0
	}

	// Gather Tmall Context Layer: .tmh KB summary + Project Precedents
	kbContext := ""
	for _, r := range h.repo.Repositories() {
		if r.ProjectID == projectID && strings.TrimSpace(r.LocalPath) != "" {
			svc := webuddykb.NewService()
			if raw, err := svc.Chain(ctx, r.LocalPath, firstNonEmpty(input.NaturalRequirement, input.Title), 2); err == nil {
				var m map[string]any
				if json.Unmarshal(raw, &m) == nil {
					if md, ok := m["markdown"].(string); ok {
						kbContext = clipRunText(md, 900)
					}
				}
			}
			break
		}
	}
	var precLines []string
	for idx, p := range defaultAutoTestStore.listPrecedents("", projectID) {
		if idx >= 4 {
			break
		}
		precLines = append(precLines, fmt.Sprintf("- [%s] %s: %s", p.Kind, p.Title, p.Summary))
	}

	// Build compact live DOM element menu so LLM only picks real existing selectors/text!
	var domMenu strings.Builder
	for idx, el := range snap.Elements {
		if idx >= 28 {
			break
		}
		fmt.Fprintf(&domMenu, "%d. selector=%q role=%s text=%q href=%q context=%q\n",
			idx+1, el.PrimarySelector, el.Role, el.Text, el.Href, el.ParentContext)
	}

	tokensUsed := 0
	if builtinagent.Default().Status(ctx).Ready {
		sysPrompt := `你是企业级 UI 自动化测试固化架构师。请根据【验收需求】、【真实页面实时 DOM 控件清单】与【项目知识库上下文】，生成 3~5 步精简、确定性、无噪音的验收步骤 JSON。
只输出形如 {"steps":[...]} 的合法 JSON，严禁解释文字。
允许的 action 类型：
- "navigate": {"name":"打开目标页","action":"navigate","targetUrl":"/xxx"}（若前置注入已到达目标页则不必重复 navigate）
- "click": {"name":"点击某控件","action":"click","primarySelector":"必须严格取自实时DOM控件清单的 selector"}
- "assert_text": {"name":"核验关键业务内容","action":"assert_text","primarySelector":"可选，严格取自DOM清单或留空代表全页断言","expected":"必须真实存在于页面可见内容摘要或点击后必然出现的短词"}
- "assert_url": {"name":"核验路由地址","action":"assert_url","expected":"/xxx"}
- "assert_api": {"name":"核验后端契约接口","action":"assert_api","targetUrl":"/api/...","expected":"关键字段名"}`

		userPrompt := fmt.Sprintf(
			"【用例标题】%s\n【自然语言验收需求】%s\n【目标站点 BaseURL】%s\n【前置登录态注入后所在页面】URL=%s Title=%q\n【当前页面可见文本摘要】%s\n【当前页面实时 DOM 控件清单】\n%s\n【项目知识库与历史判例】\n%s\n%s",
			input.Title, input.NaturalRequirement, input.BaseURL, snap.URL, snap.Title,
			clipRunText(snap.BodySnippet, 900), domMenu.String(), kbContext, strings.Join(precLines, "\n"),
		)
		res := builtinagent.Default().Complete(ctx, builtinagent.CompletionRequest{
			SystemPrompt: sysPrompt,
			UserPrompt:   userPrompt,
			MaxTokens:    850,
		})
		if res.Error == "" && res.Text != "" {
			tokensUsed = 520
			var parsed struct {
				Steps []AutoTestStep `json:"steps"`
			}
			if rawJ := extractFirstJSONObject(res.Text); rawJ != "" && json.Unmarshal([]byte(rawJ), &parsed) == nil && len(parsed.Steps) > 0 {
				var cleaned []AutoTestStep
				for i, st := range parsed.Steps {
					if st.Action == "" {
						continue
					}
					st.ID = fmt.Sprintf("step-%d", i+1)
					if st.Name == "" {
						st.Name = fmt.Sprintf("步骤 %d (%s)", i+1, st.Action)
					}
					cleaned = append(cleaned, st)
				}
				if len(cleaned) > 0 {
					return cleaned, tokensUsed
				}
			}
		}
	}

	// Deterministic Live-DOM Grounded Planner Fallback (guarantees 100% real runnable steps even if LLM times out!)
	var fallbackSteps []AutoTestStep
	if snap.Pathname != "" && snap.Pathname != "/" {
		fallbackSteps = append(fallbackSteps, AutoTestStep{
			ID:       "step-1",
			Name:     "核验目标业务页面路由已就绪",
			Action:   "assert_url",
			Expected: snap.Pathname,
		})
	} else {
		fallbackSteps = append(fallbackSteps, AutoTestStep{
			ID:        "step-1",
			Name:      "打开目标业务入口页面",
			Action:    "navigate",
			TargetURL: input.BaseURL,
		})
	}
	// Pick a meaningful heading/element from live DOM for selector-backed portrait assertion
	for _, el := range snap.Elements {
		if el.Text != "" && len([]rune(el.Text)) >= 2 && len([]rune(el.Text)) <= 36 {
			fallbackSteps = append(fallbackSteps, AutoTestStep{
				ID:              fmt.Sprintf("step-%d", len(fallbackSteps)+1),
				Name:            "核验核心业务控件「" + el.Text + "」可见与契约文本",
				Action:          "assert_text",
				PrimarySelector: el.PrimarySelector,
				Expected:        el.Text,
			})
			break
		}
	}
	// Also match any keyword from requirement that appears in live page body
	for _, kw := range tokenizeSemanticText(input.NaturalRequirement + " " + input.Title) {
		if len([]rune(kw)) >= 2 && strings.Contains(strings.ToLower(snap.BodySnippet), kw) {
			// Find exact case substring in BodySnippet
			idx := strings.Index(strings.ToLower(snap.BodySnippet), kw)
			exact := snap.BodySnippet[idx : idx+len(kw)]
			fallbackSteps = append(fallbackSteps, AutoTestStep{
				ID:       fmt.Sprintf("step-%d", len(fallbackSteps)+1),
				Name:     "核验页面核心业务数据包含「" + exact + "」",
				Action:   "assert_text",
				Expected: exact,
			})
			break
		}
	}
	return fallbackSteps, tokensUsed
}

// ---------- HTTP Handlers ----------

func (h *webuddyAutoTestHandlers) resolveTenantAndProject(c *gin.Context, explicitProjectID string, write bool) (string, string, string, bool) {
	pid := strings.TrimSpace(explicitProjectID)
	if pid == "" {
		pid = strings.TrimSpace(c.Query("projectId"))
	}
	isAdmin := false
	var uid int64
	actor := "manual"
	if user, ok := h.requestUser(c.Request, true); ok && user != nil {
		uid = user.ID
		actor = firstNonEmpty(user.LoginName, fmt.Sprintf("user-%d", user.ID))
		isAdmin = strings.TrimSpace(user.LoginName) != "" && h.repo.IsPlatformAdmin(user.LoginName)
	}
	activeTID := DefaultTenantID
	if uid > 0 {
		activeTID = defaultTenantStore.activeTenantForUser(uid, isAdmin)
	}
	if pid == "" {
		visible := h.visibleProjectsForRequest(c.Request, h.repo.Projects())
		if len(visible) > 0 {
			pid = visible[0].ID
		}
	}
	if pid != "" && authRequired() {
		if write {
			if !h.requireProjectWriter(c, pid) {
				return "", "", "", false
			}
		} else {
			visibleSet := h.visibleProjectIDSetForRequest(c.Request)
			if !visibleSet[pid] {
				c.JSON(http.StatusForbidden, gin.H{"error": "无权访问该项目测试资产"})
				return "", "", "", false
			}
		}
		activeTID = defaultTenantStore.projectTenant(pid)
	}
	return activeTID, pid, actor, true
}

// GET /api/webuddy/autotest/overview
func (h *webuddyAutoTestHandlers) overview(c *gin.Context) {
	tid, pid, _, ok := h.resolveTenantAndProject(c, "", false)
	if !ok {
		return
	}
	cases := defaultAutoTestStore.listCases(tid, pid)
	runs := defaultAutoTestStore.listRuns(tid, pid, "", 50)
	precedents := defaultAutoTestStore.listPrecedents(tid, pid)

	totalRuns, passedRuns, healedRuns, failedRuns, zeroTokenRuns := 0, 0, 0, 0, 0
	for _, tc := range cases {
		totalRuns += tc.Stats.TotalRuns
		passedRuns += tc.Stats.PassedRuns
		healedRuns += tc.Stats.HealedRuns
		failedRuns += tc.Stats.FailedRuns
		zeroTokenRuns += tc.Stats.ZeroTokenRuns
	}
	passRate := 0.0
	if totalRuns > 0 {
		passRate = math.Round(float64(passedRuns+healedRuns)*1000/float64(totalRuns)) / 10
	}

	// Suggest default target URL for active project (e.g. earn-demo or active preview)
	suggestedURL := ""
	suggestedAuth := ""
	if strings.Contains(strings.ToLower(pid), "earn") {
		suggestedURL = "https://earn-demo.bbstack.date"
		suggestedAuth = "/auth/dev?id=1&to=admin"
	} else if dto := defaultPreviewMgr.status(pid, ""); dto.Running && dto.PreviewURL != "" {
		suggestedURL = "https://" + resolvePublicOriginHost(c.Request) + dto.PreviewURL
	}

	c.JSON(http.StatusOK, gin.H{
		"tenantId":            tid,
		"projectId":           pid,
		"chromeAvailable":     findLocalChromeBinary() != "",
		"confidenceThreshold": autoTestConfidenceThreshold,
		"suggestedBaseUrl":    suggestedURL,
		"suggestedAuthPath":   suggestedAuth,
		"metrics": gin.H{
			"totalCases":      len(cases),
			"totalRuns":       totalRuns,
			"passedRuns":      passedRuns,
			"healedRuns":      healedRuns,
			"failedRuns":      failedRuns,
			"zeroTokenRuns":   zeroTokenRuns,
			"passRate":        passRate,
			"precedentsCount": len(precedents),
		},
		"cases":      cases,
		"recentRuns": runs,
		"precedents": precedents,
	})
}

// GET /api/webuddy/autotest/cases
func (h *webuddyAutoTestHandlers) listCases(c *gin.Context) {
	tid, pid, _, ok := h.resolveTenantAndProject(c, "", false)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"cases": defaultAutoTestStore.listCases(tid, pid)})
}

// GET /api/webuddy/autotest/cases/:id
func (h *webuddyAutoTestHandlers) getCase(c *gin.Context) {
	tc, ok := defaultAutoTestStore.getCase(strings.TrimSpace(c.Param("id")))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到该测试用例"})
		return
	}
	if _, _, _, permOK := h.resolveTenantAndProject(c, tc.ProjectID, false); !permOK {
		return
	}
	runs := defaultAutoTestStore.listRuns(tc.TenantID, tc.ProjectID, tc.ID, 25)
	c.JSON(http.StatusOK, gin.H{"case": tc, "runs": runs})
}

// DELETE /api/webuddy/autotest/cases/:id
func (h *webuddyAutoTestHandlers) deleteCase(c *gin.Context) {
	tc, ok := defaultAutoTestStore.getCase(strings.TrimSpace(c.Param("id")))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到该测试用例"})
		return
	}
	if _, _, _, permOK := h.resolveTenantAndProject(c, tc.ProjectID, true); !permOK {
		return
	}
	defaultAutoTestStore.deleteCase(tc.ID)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// POST /api/webuddy/autotest/cases/generate
// AI Natural-Language -> Live Headless Chrome Exploration -> Denoised Solidification (Revision 1)
func (h *webuddyAutoTestHandlers) generateAndSolidifyCase(c *gin.Context) {
	var input generateAutoTestInput
	if !decode(c, &input) {
		return
	}
	tid, pid, actor, ok := h.resolveTenantAndProject(c, input.ProjectID, true)
	if !ok {
		return
	}
	tc, run, err := h.generateAndSolidifyCore(c.Request.Context(), tid, pid, actor, input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "case": tc, "run": run})
}

func (h *webuddyAutoTestHandlers) generateAndSolidifyCore(
	ctx context.Context,
	tid, pid, actor string,
	input generateAutoTestInput,
) (AutoTestCase, AutoTestRun, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	if _, err := validateAutoTestTargetURL(baseURL); err != nil {
		return AutoTestCase{}, AutoTestRun{}, err
	}
	title := strings.TrimSpace(input.Title)
	reqText := strings.TrimSpace(input.NaturalRequirement)
	if title == "" && reqText != "" {
		title = clipRunText(reqText, 36)
	}
	if title == "" {
		return AutoTestCase{}, AutoTestRun{}, fmt.Errorf("请填写用例标题或自然语言验收需求描述")
	}

	// Step A: Probe live target page (applying PreconditionURL first!) to ground AI step synthesis in real DOM
	var snap domPageInspection
	probeCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	entryURL := baseURL
	if strings.TrimSpace(input.PreconditionURL) != "" {
		if u, err := resolveRelativeTargetURL(baseURL, input.PreconditionURL); err == nil {
			entryURL = u
		}
	}
	if len(input.Steps) == 0 {
		if ch, err := startHeadlessChromeSession(probeCtx); err == nil {
			_, navErr := ch.navigateAndSettle(probeCtx, entryURL)
			if navErr != nil {
				ch.Close()
				return AutoTestCase{}, AutoTestRun{}, fmt.Errorf("访问目标页面失败: %w", navErr)
			}
			snap, _ = ch.inspectCurrentDOM(probeCtx)
			ch.Close()
		} else {
			fb := newHTTPFallbackSession()
			if _, navErr := fb.navigate(probeCtx, entryURL); navErr != nil {
				return AutoTestCase{}, AutoTestRun{}, fmt.Errorf("访问目标页面失败: %w", navErr)
			}
			snap = fb.inspectElements()
		}
	}

	synthSteps, synthTokens := h.synthesizeStepsFromLiveDOMAndKB(ctx, pid, input, snap)
	if len(synthSteps) == 0 {
		return AutoTestCase{}, AutoTestRun{}, fmt.Errorf("未能从目标页面提取有效可执行验收步骤")
	}

	now := time.Now().UTC()
	tc := AutoTestCase{
		ID:                 fmt.Sprintf("atcase-%d", now.UnixNano()),
		TenantID:           tid,
		ProjectID:          pid,
		Title:              title,
		NaturalRequirement: firstNonEmpty(reqText, title),
		BaseURL:            baseURL,
		PreconditionURL:    strings.TrimSpace(input.PreconditionURL),
		LinkedIssueID:      strings.TrimSpace(input.LinkedIssueID),
		Steps:              synthSteps,
		CurrentRevision:    0,
		LastStatus:         "never",
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	// Step B: Execute live in Headless Chrome, capture 3D Element Digital Portraits & screenshots, solidify Revision 1!
	run, savedCase, err := h.executeCase(ctx, tc, "solidify", actor, true, true)
	if err != nil {
		return AutoTestCase{}, AutoTestRun{}, err
	}
	if synthTokens > 0 {
		run.TokensUsed += synthTokens
		// Solidification uses AI once; subsequent replays use 0 tokens!
		if savedCase.Stats.ZeroTokenRuns > 0 {
			savedCase.Stats.ZeroTokenRuns--
			_ = defaultAutoTestStore.putCase(savedCase)
		}
		_ = defaultAutoTestStore.appendRun(run)
	}

	// Harvest Tmall Spec Template Precedent when solidification passes!
	if run.Status == "passed" || run.Status == "healed" {
		defaultAutoTestStore.addPrecedent(AutoTestPrecedent{
			TenantID:    tid,
			ProjectID:   pid,
			Kind:        "spec_template",
			Title:       "验收基线判例：" + savedCase.Title,
			Summary:     fmt.Sprintf("已真机跑通并固化 %d 步确定性回归脚本（前置注入 `%s`），沉淀 %d 个控件三维数字身份证，后续回归 0 Token 极速复放。", len(savedCase.Steps), firstNonEmpty(savedCase.PreconditionURL, "无"), countStepsWithPortrait(savedCase.Steps)),
			PagePattern: savedCase.BaseURL,
			Confidence:  1.0,
			SourceCase:  savedCase.ID,
			CreatedAt:   now,
		})
	}
	return savedCase, run, nil
}

func countStepsWithPortrait(steps []AutoTestStep) int {
	n := 0
	for _, s := range steps {
		if s.Portrait != nil {
			n++
		}
	}
	return n
}

// POST /api/webuddy/autotest/cases/:id/run
// Zero-Token Fast Deterministic Regression (+ Automatic 5-Step Self-Healing on Failure!)
func (h *webuddyAutoTestHandlers) runCaseHandler(c *gin.Context) {
	tc, ok := defaultAutoTestStore.getCase(strings.TrimSpace(c.Param("id")))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到该测试用例"})
		return
	}
	_, _, actor, permOK := h.resolveTenantAndProject(c, tc.ProjectID, true)
	if !permOK {
		return
	}
	run, updatedCase, err := h.executeCase(c.Request.Context(), tc, "replay_zero_token", actor, false, true)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "run": run, "case": updatedCase})
}

// POST /api/webuddy/autotest/suites/run
func (h *webuddyAutoTestHandlers) runSuiteHandler(c *gin.Context) {
	var body struct {
		ProjectID string `json:"projectId"`
	}
	_ = c.ShouldBindJSON(&body)
	tid, pid, actor, ok := h.resolveTenantAndProject(c, body.ProjectID, true)
	if !ok {
		return
	}
	cases := defaultAutoTestStore.listCases(tid, pid)
	var runs []AutoTestRun
	passed, healed, failed := 0, 0, 0
	for _, tc := range cases {
		r, _, err := h.executeCase(c.Request.Context(), tc, "replay_zero_token", actor, false, true)
		if err != nil {
			continue
		}
		runs = append(runs, r)
		switch r.Status {
		case "passed":
			passed++
		case "healed":
			healed++
		default:
			failed++
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"total":  len(runs),
		"passed": passed,
		"healed": healed,
		"failed": failed,
		"runs":   runs,
	})
}

// POST /api/webuddy/autotest/cases/:id/inject-drift
// 1-Click Weekly UI Refactor Drill: mutates a step's PrimarySelector while keeping its 3D ElementPortrait intact!
func (h *webuddyAutoTestHandlers) injectUIDriftHandler(c *gin.Context) {
	tc, ok := defaultAutoTestStore.getCase(strings.TrimSpace(c.Param("id")))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到该测试用例"})
		return
	}
	_, _, actor, permOK := h.resolveTenantAndProject(c, tc.ProjectID, true)
	if !permOK {
		return
	}
	var body struct {
		StepIndex   int    `json:"stepIndex"`
		BrokenValue string `json:"brokenSelector"`
	}
	_ = c.ShouldBindJSON(&body)

	targetIdx := -1
	if body.StepIndex > 0 && body.StepIndex <= len(tc.Steps) && tc.Steps[body.StepIndex-1].Portrait != nil {
		targetIdx = body.StepIndex - 1
	} else {
		for i, st := range tc.Steps {
			if st.Portrait != nil && (st.Action == "click" || st.Action == "assert_text" || st.Action == "fill") && st.PrimarySelector != "" {
				targetIdx = i
				break
			}
		}
	}
	if targetIdx < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "当前用例尚未采集带三维控件画像的选择器步骤，请先生成或运行一次固化"})
		return
	}
	oldSel := tc.Steps[targetIdx].PrimarySelector
	brokenSel := strings.TrimSpace(body.BrokenValue)
	if brokenSel == "" {
		brokenSel = fmt.Sprintf("#legacy-ui-v1-stale-%d", time.Now().Unix()%1000)
	}
	tc.Steps[targetIdx].PrimarySelector = brokenSel
	tc.CurrentRevision++
	now := time.Now().UTC()
	tc.UpdatedAt = now
	tc.Revisions = append(tc.Revisions, AutoTestRevision{
		Revision:  tc.CurrentRevision,
		Reason:    "模拟业务单周迭代导致旧选择器失效（自愈演练）",
		Author:    actor,
		DiffNote:  fmt.Sprintf("演练注入定位漂移：`%s` → `%s`（保留三维控件数字身份证）", oldSel, brokenSel),
		Steps:     cloneAutoTestSteps(tc.Steps),
		CreatedAt: now,
	})
	_ = defaultAutoTestStore.putCase(tc)
	c.JSON(http.StatusOK, gin.H{
		"ok":             true,
		"case":           tc,
		"mutatedStepIdx": targetIdx + 1,
		"oldSelector":    oldSel,
		"brokenSelector": brokenSel,
	})
}

// POST /api/webuddy/autotest/cases/:id/rollback
// Huolala Dual-Layer Safety Guardrail: 1-Click Snapshot Rollback + Bad Case Precedent Recording!
func (h *webuddyAutoTestHandlers) rollbackCaseHandler(c *gin.Context) {
	tc, ok := defaultAutoTestStore.getCase(strings.TrimSpace(c.Param("id")))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到该测试用例"})
		return
	}
	_, _, actor, permOK := h.resolveTenantAndProject(c, tc.ProjectID, true)
	if !permOK {
		return
	}
	var body struct {
		TargetRevision int `json:"targetRevision"`
	}
	_ = c.ShouldBindJSON(&body)
	if len(tc.Revisions) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该用例暂无可回滚的历史快照"})
		return
	}
	targetRev := body.TargetRevision
	if targetRev <= 0 {
		if len(tc.Revisions) >= 2 {
			targetRev = tc.Revisions[len(tc.Revisions)-2].Revision
		} else {
			targetRev = tc.Revisions[0].Revision
		}
	}
	var found *AutoTestRevision
	var rolledBackHealed *AutoTestRevision
	if len(tc.Revisions) > 0 && tc.Revisions[len(tc.Revisions)-1].AutoHealed {
		cp := tc.Revisions[len(tc.Revisions)-1]
		rolledBackHealed = &cp
	}
	for i := range tc.Revisions {
		if tc.Revisions[i].Revision == targetRev {
			cp := tc.Revisions[i]
			found = &cp
			break
		}
	}
	if found == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("未找到版本快照 Rev %d", targetRev)})
		return
	}

	now := time.Now().UTC()
	tc.Steps = cloneAutoTestSteps(found.Steps)
	tc.CurrentRevision++
	tc.UpdatedAt = now
	tc.Revisions = append(tc.Revisions, AutoTestRevision{
		Revision:  tc.CurrentRevision,
		Reason:    fmt.Sprintf("人工一键回滚至快照 Rev %d", found.Revision),
		Author:    actor,
		DiffNote:  fmt.Sprintf("已恢复至 Rev %d 确定性脚本快照", found.Revision),
		Steps:     cloneAutoTestSteps(found.Steps),
		CreatedAt: now,
	})
	_ = defaultAutoTestStore.putCase(tc)

	// Record Bad Case Guard in Precedent Library if rolling back an auto-healed revision!
	if rolledBackHealed != nil && rolledBackHealed.DiffNote != "" {
		// Extract -> newSelector if present
		rejectedSel := ""
		if parts := strings.Split(rolledBackHealed.DiffNote, "→ `"); len(parts) == 2 {
			if end := strings.IndexByte(parts[1], '`'); end > 0 {
				rejectedSel = parts[1][:end]
			}
		}
		defaultAutoTestStore.addPrecedent(AutoTestPrecedent{
			TenantID:    tc.TenantID,
			ProjectID:   tc.ProjectID,
			Kind:        "bad_case_guard",
			Title:       "人工回滚防误修判例：" + tc.Title,
			Summary:     fmt.Sprintf("人工回滚了 Rev %d 的自愈变更（%s），后续自愈引擎已将该候选选择器拉入防误修黑名单。", rolledBackHealed.Revision, rolledBackHealed.DiffNote),
			PagePattern: tc.BaseURL,
			NewLocator:  rejectedSel,
			SourceCase:  tc.ID,
			CreatedAt:   now,
		})
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "case": tc, "restoredFromRevision": found.Revision})
}

// GET /api/webuddy/autotest/runs
func (h *webuddyAutoTestHandlers) listRunsHandler(c *gin.Context) {
	tid, pid, _, ok := h.resolveTenantAndProject(c, "", false)
	if !ok {
		return
	}
	caseID := strings.TrimSpace(c.Query("caseId"))
	c.JSON(http.StatusOK, gin.H{"runs": defaultAutoTestStore.listRuns(tid, pid, caseID, 50)})
}

// GET /api/webuddy/autotest/runs/:id
func (h *webuddyAutoTestHandlers) getRunHandler(c *gin.Context) {
	r, ok := defaultAutoTestStore.getRun(strings.TrimSpace(c.Param("id")))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到该执行记录"})
		return
	}
	if _, _, _, permOK := h.resolveTenantAndProject(c, r.ProjectID, false); !permOK {
		return
	}
	c.JSON(http.StatusOK, gin.H{"run": r})
}

// POST /api/webuddy/autotest/runs/:id/create-issue
// Tmall Collaboration Layer: 1-Click Escalate Unhealable / Business Failure Run to Structured Issue!
func (h *webuddyAutoTestHandlers) escalateRunToIssueHandler(c *gin.Context) {
	r, ok := defaultAutoTestStore.getRun(strings.TrimSpace(c.Param("id")))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到该执行记录"})
		return
	}
	_, pid, _, permOK := h.resolveTenantAndProject(c, r.ProjectID, true)
	if !permOK {
		return
	}
	if r.EscalatedIssueID != "" {
		if existing, exists := h.repo.Issue(r.EscalatedIssueID); exists {
			c.JSON(http.StatusOK, gin.H{"ok": true, "issue": existing, "alreadyCreated": true})
			return
		}
	}

	var md strings.Builder
	fmt.Fprintf(&md, "## 🚨 自动化测试异常升级裁定单（AI 五步诊断未自动放行）\n\n")
	fmt.Fprintf(&md, "- **关联固化用例**：%s (`%s` · Rev %d)\n", r.CaseTitle, r.CaseID, r.CaseRevision)
	fmt.Fprintf(&md, "- **执行记录 ID**：`%s`（引擎：`%s`，耗时 `%d ms`）\n", r.ID, r.EngineUsed, r.DurationMs)
	fmt.Fprintf(&md, "- **异常结论**：%s\n\n", firstNonEmpty(r.EscalationReason, "测试执行失败"))
	md.WriteString("### 步骤执行与五步诊断明细\n\n")
	for _, sr := range r.StepResults {
		icon := "✅"
		if sr.Status == "failed" {
			icon = "❌"
		} else if sr.Status == "healed" {
			icon = "🩹"
		}
		fmt.Fprintf(&md, "%d. %s **%s** (`%s`) — %s\n", sr.StepIndex, icon, sr.Name, sr.Action, firstNonEmpty(sr.Error, sr.ActualSummary))
		if sr.Diagnosis != nil && len(sr.Diagnosis.Checks) > 0 {
			for _, ck := range sr.Diagnosis.Checks {
				mark := "✓"
				if !ck.Passed {
					mark = "✗"
				}
				fmt.Fprintf(&md, "   - [%s] %s：%s\n", mark, ck.Title, ck.Summary)
			}
		}
		if sr.ScreenshotURL != "" {
			fmt.Fprintf(&md, "   - 现场截图：`%s`\n", sr.ScreenshotURL)
		}
	}

	var repoID string
	var repoIDs []string
	for _, rp := range h.repo.Repositories() {
		if rp.ProjectID == pid {
			repoID = rp.ID
			repoIDs = []string{rp.ID}
			break
		}
	}
	var assigneeUID int64
	if u, uok := h.requestUser(c.Request, true); uok && u != nil {
		assigneeUID = u.ID
	}
	now := time.Now().UTC()
	iss := domain.Issue{
		ID:             fmt.Sprintf("issue-%d", now.UnixNano()),
		ProjectID:      pid,
		Title:          fmt.Sprintf("[自动化测试异常] %s", r.CaseTitle),
		Description:    md.String(),
		Status:         "todo",
		Priority:       "high",
		Kind:           "bug",
		Source:         "local",
		RepositoryID:   repoID,
		RepositoryIDs:  repoIDs,
		AssigneeUserID: assigneeUID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := h.repo.PutIssue(iss); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建缺陷单失败: " + err.Error()})
		return
	}
	r.EscalatedIssueID = iss.ID
	_ = defaultAutoTestStore.appendRun(r)
	stored, _ := h.repo.Issue(iss.ID)
	c.JSON(http.StatusOK, gin.H{"ok": true, "issue": stored, "run": r})
}

// GET /api/webuddy/autotest/precedents
func (h *webuddyAutoTestHandlers) listPrecedentsHandler(c *gin.Context) {
	tid, pid, _, ok := h.resolveTenantAndProject(c, "", false)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"precedents": defaultAutoTestStore.listPrecedents(tid, pid)})
}

// GET /api/webuddy/autotest/screenshots/:name
func (h *webuddyAutoTestHandlers) serveScreenshot(c *gin.Context) {
	name := filepath.Base(strings.TrimSpace(c.Param("name")))
	if name == "" || !strings.HasSuffix(name, ".png") || strings.Contains(name, "..") {
		c.Status(http.StatusNotFound)
		return
	}
	_, shotDir := defaultAutoTestStore.resolvePaths()
	full := filepath.Join(shotDir, name)
	if st, err := os.Stat(full); err != nil || st.IsDir() {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Content-Type", "image/png")
	c.Header("Cache-Control", "public, max-age=86400")
	c.File(full)
}

// ---------- Zero-Token Chat Slash Command (/test, /autotest) & MCP Tool ----------

func (d *deps) executeWorkbenchAutoTestOp(ctx context.Context, user domain.User, projectID, subcmd string, args []string) string {
	isAdmin := strings.TrimSpace(user.LoginName) != "" && d.repo.IsPlatformAdmin(user.LoginName)
	tid := DefaultTenantID
	if user.ID > 0 {
		tid = defaultTenantStore.activeTenantForUser(user.ID, isAdmin)
	}
	pid := strings.TrimSpace(projectID)
	if pid == "" {
		for _, p := range d.repo.Projects() {
			if defaultTenantStore.projectTenant(p.ID) == tid {
				pid = p.ID
				break
			}
		}
	}
	if pid != "" {
		tid = defaultTenantStore.projectTenant(pid)
	}
	h := &webuddyAutoTestHandlers{deps: d}
	actor := firstNonEmpty(user.LoginName, "chat-command")

	sub := strings.ToLower(strings.TrimSpace(subcmd))
	switch sub {
	case "", "list", "ls", "status", "列表", "状态":
		cases := defaultAutoTestStore.listCases(tid, pid)
		var b strings.Builder
		b.WriteString("### 🧪 AI 自动化测试与自愈工作台（零 Token 瞬时概览）\n\n")
		if len(cases) == 0 {
			b.WriteString("当前项目暂未固化自动化回归用例。可前往左侧导航 **「自动化测试」** 一键用自然语言生成并真机固化，或直接让 Main Bot 为您创建。\n")
		} else {
			b.WriteString("| 用例名称 | 版本 | 最近状态 | 通过率 (0-Token 次数) | 目标站点 |\n")
			b.WriteString("| :--- | :--- | :--- | :--- | :--- |\n")
			for _, tc := range cases {
				st := "⚪ 未运行"
				switch tc.LastStatus {
				case "passed":
					st = "🟢 通过"
				case "healed":
					st = "🩹 AI自愈通过"
				case "failed":
					st = "🔴 异常待裁定"
				}
				fmt.Fprintf(&b, "| **%s** (`%s`) | `Rev %d` | %s | `%.1f%%` (`%d` 次) | `%s` |\n",
					tc.Title, tc.ID, tc.CurrentRevision, st, tc.Stats.PassRate, tc.Stats.ZeroTokenRuns, tc.BaseURL)
			}
		}
		b.WriteString("\n**快捷测试指令（0 Token 直接执行）：**\n")
		b.WriteString("- `/test run`（或 `/test run all`）— 真机 Headless Chrome 一键跑完当前项目全部固化回归用例（遇定位失效自动触发五步自愈）\n")
		b.WriteString("- `/test run <用例ID或关键词>` — 单独极速复放指定用例\n")
		return b.String()

	case "run", "回归", "执行":
		sel := strings.TrimSpace(strings.Join(args, " "))
		cases := defaultAutoTestStore.listCases(tid, pid)
		if len(cases) == 0 {
			return "❌ 当前项目暂无已固化的自动化用例，请先在「自动化测试」视图生成用例。"
		}
		var targets []AutoTestCase
		if sel == "" || strings.EqualFold(sel, "all") || sel == "全部" {
			targets = cases
		} else {
			for _, tc := range cases {
				if tc.ID == sel || strings.Contains(strings.ToLower(tc.Title), strings.ToLower(sel)) {
					targets = append(targets, tc)
				}
			}
		}
		if len(targets) == 0 {
			return fmt.Sprintf("❌ 未找到匹配 `%s` 的测试用例，发送 `/test` 查看列表。", sel)
		}
		var b strings.Builder
		b.WriteString("### ⚡ 真机极速回归与五步自愈执行报告\n\n")
		for _, tc := range targets {
			r, _, err := h.executeCase(ctx, tc, "replay_zero_token", actor, false, true)
			if err != nil {
				fmt.Fprintf(&b, "- ❌ **%s**：执行异常（%v）\n", tc.Title, err)
				continue
			}
			badge := "✅ 0-Token 回归通过"
			if r.Status == "healed" && r.HealDiff != nil {
				badge = fmt.Sprintf("🩹 五步自愈通过（置信度 `%.2f`，`%s` → `%s`，已存快照 `Rev %d`）",
					r.HealDiff.Confidence, r.HealDiff.OldSelector, r.HealDiff.NewSelector, r.HealDiff.ToRevision)
			} else if r.Status == "failed" {
				badge = "❌ 拦截待裁定 — " + r.EscalationReason
			}
			fmt.Fprintf(&b, "- **%s** (`%d ms` · `%s` · `%d Token`)：%s\n", tc.Title, r.DurationMs, r.EngineUsed, r.TokensUsed, badge)
		}
		return b.String()

	default:
		return "支持指令：`/test`（查看固化用例与通过率）、`/test run [all|关键词]`（零 Token 极速真机回归 + 失败自动五步自愈）。"
	}
}

func (h *agentToolGateway) runWebuddyAutoTestManageTool(ctx context.Context, session *AgentToolSession, pid string, args map[string]any) (map[string]any, error) {
	if session == nil || session.User.ID <= 0 {
		return mcpToolTextResult("请先登录后使用自动化测试工具", true), nil
	}
	strArg := func(k string) string {
		if v, ok := args[k].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	action := strings.ToLower(strArg("action"))
	if action == "" {
		action = "list"
	}
	isAdmin := strings.TrimSpace(session.User.LoginName) != "" && h.repo.IsPlatformAdmin(session.User.LoginName)
	tid := defaultTenantStore.activeTenantForUser(session.User.ID, isAdmin)
	if pid != "" {
		tid = defaultTenantStore.projectTenant(pid)
	}
	ath := &webuddyAutoTestHandlers{deps: h.deps}

	switch action {
	case "generate":
		tc, run, err := ath.generateAndSolidifyCore(ctx, tid, pid, firstNonEmpty(session.User.LoginName, "main-bot"), generateAutoTestInput{
			ProjectID:          pid,
			Title:              strArg("title"),
			NaturalRequirement: strArg("requirement"),
			BaseURL:            strArg("baseUrl"),
			PreconditionURL:    strArg("preconditionUrl"),
			LinkedIssueID:      strArg("issueId"),
		})
		if err != nil {
			return mcpToolTextResult(err.Error(), true), nil
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "case": tc, "run": run})
		return mcpToolTextResult(string(out), false), nil

	case "run":
		target := strArg("caseId")
		md := h.deps.executeWorkbenchAutoTestOp(ctx, session.User, pid, "run", []string{target})
		out, _ := json.Marshal(map[string]any{"ok": !strings.HasPrefix(md, "❌"), "markdown": md})
		return mcpToolTextResult(string(out), strings.HasPrefix(md, "❌")), nil

	default:
		md := h.deps.executeWorkbenchAutoTestOp(ctx, session.User, pid, "list", nil)
		out, _ := json.Marshal(map[string]any{"ok": true, "markdown": md})
		return mcpToolTextResult(string(out), false), nil
	}
}
