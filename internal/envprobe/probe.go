package envprobe

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const DefaultProbeTTL = 5 * time.Minute

type cachedCLIProbe struct {
	Fingerprint string
	Status      string
	Error       string
	LatencyMs   int64
	Models      []string
	ProbedAt    time.Time
}

func cliConfigFingerprint(c CLIStatus) string {
	return c.AuthMode + "|" + c.GatewayURL + "|" + c.DefaultModel
}

type cachedGitProbe struct {
	Reachable bool
	Error     string
	ProbedAt  time.Time
}

// ProbeManager caches real live CLI & Git connectivity probes with a TTL so
// UI loads stay instant (<200ms) while surfacing real auth/gateway errors
// (e.g. 403 account banned, model_not_found) cleanly.
type ProbeManager struct {
	ttl          time.Duration
	mu           sync.RWMutex
	probing      bool
	probeDone    chan struct{}
	lastRun      time.Time
	lastSnap     *HostProfile
	lastSnapTime time.Time
	clis         map[string]cachedCLIProbe
	gits         map[string]cachedGitProbe
}

func NewProbeManager(ttl time.Duration) *ProbeManager {
	if ttl <= 0 {
		ttl = DefaultProbeTTL
	}
	return &ProbeManager{
		ttl:  ttl,
		clis: make(map[string]cachedCLIProbe),
		gits: make(map[string]cachedGitProbe),
	}
}

// Snapshot returns a fast static ScanHost (cached 30s) merged with cached live probe results.
func (m *ProbeManager) Snapshot(ctx context.Context) HostProfile {
	m.mu.RLock()
	if m.lastSnap != nil && time.Since(m.lastSnapTime) < 30*time.Second {
		prof := cloneHostProfile(*m.lastSnap)
		m.mu.RUnlock()
		m.applyCache(&prof)
		return prof
	}
	m.mu.RUnlock()

	prof := ScanHost(ctx)
	m.mu.Lock()
	cp := cloneHostProfile(prof)
	m.lastSnap = &cp
	m.lastSnapTime = time.Now()
	m.mu.Unlock()
	m.applyCache(&prof)
	return prof
}

func cloneHostProfile(p HostProfile) HostProfile {
	out := p
	if p.CLIs != nil {
		out.CLIs = make([]CLIStatus, len(p.CLIs))
		copy(out.CLIs, p.CLIs)
	}
	if p.GitRemotes != nil {
		out.GitRemotes = make([]GitRemoteStatus, len(p.GitRemotes))
		copy(out.GitRemotes, p.GitRemotes)
	}
	if p.KnowledgeBases != nil {
		out.KnowledgeBases = make([]DiscoveredKB, len(p.KnowledgeBases))
		copy(out.KnowledgeBases, p.KnowledgeBases)
	}
	if p.ExternalMCPs != nil {
		out.ExternalMCPs = make([]DiscoveredMCP, len(p.ExternalMCPs))
		copy(out.ExternalMCPs, p.ExternalMCPs)
	}
	return out
}

// TriggerProbeAsync kicks off a background probe if not already running and either
// force is true or the cache TTL has expired.
func (m *ProbeManager) TriggerProbeAsync(force bool) {
	m.mu.Lock()
	if m.probing || (!force && !m.lastRun.IsZero() && time.Since(m.lastRun) < m.ttl) {
		m.mu.Unlock()
		return
	}
	m.probing = true
	m.probeDone = make(chan struct{})
	m.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		m.runProbeInternal(ctx)
	}()
}

// TriggerProbe runs live probes synchronously (or waits for an in-flight probe).
func (m *ProbeManager) TriggerProbe(ctx context.Context, force bool) HostProfile {
	m.mu.Lock()
	if m.probing && m.probeDone != nil {
		done := m.probeDone
		m.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
		}
		return m.Snapshot(ctx)
	}
	need := force || m.lastRun.IsZero() || time.Since(m.lastRun) >= m.ttl
	if !need {
		m.mu.Unlock()
		return m.Snapshot(ctx)
	}
	m.probing = true
	m.probeDone = make(chan struct{})
	m.mu.Unlock()

	return m.runProbeInternal(ctx)
}

func (m *ProbeManager) runProbeInternal(ctx context.Context) HostProfile {
	defer func() {
		m.mu.Lock()
		m.probing = false
		m.lastRun = time.Now().UTC()
		if m.probeDone != nil {
			close(m.probeDone)
			m.probeDone = nil
		}
		m.mu.Unlock()
	}()

	prof := ScanHost(ctx)
	var wg sync.WaitGroup

	// Probe installed & configured CLIs in parallel; clear stale cache if unconfigured
	for i := range prof.CLIs {
		cli := prof.CLIs[i]
		if !cli.Available || cli.AuthMode == "none" {
			m.mu.Lock()
			delete(m.clis, cli.ID)
			m.mu.Unlock()
			continue
		}
		if cli.ID != "codex" && cli.ID != "claude" {
			continue
		}
		wg.Add(1)
		go func(c CLIStatus) {
			defer wg.Done()
			res := probeSingleCLI(ctx, c)
			res.Fingerprint = cliConfigFingerprint(c)
			m.mu.Lock()
			m.clis[c.ID] = res
			m.mu.Unlock()
		}(cli)
	}

	// Probe repo Git remotes in parallel (skip bare host-only entries from .git-credentials)
	for _, r := range prof.GitRemotes {
		if r.Source != "repo" || r.URL == "" {
			continue
		}
		wg.Add(1)
		go func(remote GitRemoteStatus) {
			defer wg.Done()
			res := probeGitRemote(ctx, remote.URL)
			m.mu.Lock()
			m.gits[remote.URL] = res
			m.mu.Unlock()
		}(r)
	}

	wg.Wait()
	m.applyCache(&prof)
	return prof
}

func (m *ProbeManager) applyCache(prof *HostProfile) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for i := range prof.CLIs {
		c := &prof.CLIs[i]
		if !c.Available || c.AuthMode == "none" {
			c.ProbeStatus = "untested"
			c.ProbeError = ""
			continue
		}
		if cached, ok := m.clis[c.ID]; ok && (cached.Fingerprint == "" || cached.Fingerprint == cliConfigFingerprint(*c)) {
			c.ProbeStatus = cached.Status
			c.ProbeError = cached.Error
			c.ProbeLatency = cached.LatencyMs
			c.ProbedAt = cached.ProbedAt
			if len(cached.Models) > 0 {
				c.Models = cached.Models
			}
		} else if m.probing && (c.ID == "codex" || c.ID == "claude") {
			c.ProbeStatus = "probing"
		}
	}
	for i := range prof.GitRemotes {
		g := &prof.GitRemotes[i]
		if cached, ok := m.gits[g.URL]; ok {
			r := cached.Reachable
			g.Reachable = &r
			g.Error = cached.Error
		}
	}
}

func probeSingleCLI(ctx context.Context, c CLIStatus) cachedCLIProbe {
	start := time.Now()
	tctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	var models []string
	if c.ID == "codex" {
		models = discoverCodexModels(tctx, c.DefaultModel)
	}

	var cmd *exec.Cmd
	switch c.ID {
	case "codex":
		args := []string{"exec", "--sandbox", "read-only", "--skip-git-repo-check"}
		if c.DefaultModel != "" {
			args = append(args, "-m", c.DefaultModel)
		}
		args = append(args, "Reply with the single word OK")
		cmd = exec.CommandContext(tctx, c.Path, args...)
	case "claude":
		args := []string{"-p", "Reply with the single word OK"}
		cmd = exec.CommandContext(tctx, c.Path, args...)
	default:
		return cachedCLIProbe{Status: "untested", ProbedAt: time.Now().UTC()}
	}
	cmd.Env = ExpandedEnv()
	out, err := cmd.CombinedOutput()
	latency := time.Since(start).Milliseconds()
	text := strings.TrimSpace(string(out))

	if err == nil && !looksLikeAuthOrModelError(text) {
		return cachedCLIProbe{
			Status:    "ok",
			LatencyMs: latency,
			Models:    models,
			ProbedAt:  time.Now().UTC(),
		}
	}
	errMsg := sanitizeProbeError(text, err)
	status := classifyProbeError(errMsg)
	return cachedCLIProbe{
		Status:    status,
		Error:     errMsg,
		LatencyMs: latency,
		Models:    models,
		ProbedAt:  time.Now().UTC(),
	}
}

func discoverCodexModels(ctx context.Context, defaultModel string) []string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		return nil
	}
	cfg := parseCodexConfig(string(raw))
	if cfg.GatewayURL == "" {
		return nil
	}
	endpoint := strings.TrimRight(cfg.GatewayURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil
	}
	if cfg.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.BearerToken)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	if defaultModel != "" {
		seen[defaultModel] = true
		out = append(out, defaultModel)
	}
	for _, item := range payload.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func probeGitRemote(ctx context.Context, repoURL string) cachedGitProbe {
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	gitBin := LookupBinary("git")
	if gitBin == "" {
		return cachedGitProbe{Reachable: false, Error: "未安装 git", ProbedAt: time.Now().UTC()}
	}
	cmd := exec.CommandContext(tctx, gitBin, "ls-remote", "--heads", repoURL)
	cmd.Env = append(ExpandedEnv(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return cachedGitProbe{Reachable: true, ProbedAt: time.Now().UTC()}
	}
	return cachedGitProbe{
		Reachable: false,
		Error:     sanitizeProbeError(string(out), err),
		ProbedAt:  time.Now().UTC(),
	}
}

var secretTokenRe = regexp.MustCompile(`(?i)(Bearer\s+[A-Za-z0-9._\-]+|sk-[A-Za-z0-9._\-]+|cr_[A-Za-z0-9._\-]+|ghp_[A-Za-z0-9._\-]+|glpat-[A-Za-z0-9._\-]+)`)

func sanitizeProbeError(output string, err error) string {
	// Pick the most informative error line
	var best string
	for _, line := range strings.Split(output, "\n") {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		lower := strings.ToLower(l)
		if strings.Contains(lower, "error") || strings.Contains(lower, "403") || strings.Contains(lower, "401") ||
			strings.Contains(l, "封禁") || strings.Contains(lower, "not_found") || strings.Contains(lower, "failed") ||
			strings.Contains(lower, "denied") || strings.Contains(lower, "refused") {
			best = l
			break
		}
		if best == "" {
			best = l
		}
	}
	if best == "" && err != nil {
		best = err.Error()
	}
	best = secretTokenRe.ReplaceAllString(best, "[REDACTED]")
	if len(best) > 200 {
		best = best[:200] + "..."
	}
	return best
}

func looksLikeAuthOrModelError(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "api error: 403") ||
		strings.Contains(lower, "api error: 401") ||
		strings.Contains(text, "用户已被封禁") ||
		strings.Contains(lower, "model_not_found")
}

func classifyProbeError(msg string) string {
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "403") || strings.Contains(lower, "401") ||
		strings.Contains(msg, "封禁") || strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "invalid_api_key") || strings.Contains(lower, "authentication") {
		return "auth_error"
	}
	if strings.Contains(lower, "model_not_found") || strings.Contains(lower, "not supported") ||
		strings.Contains(lower, "does not exist") || strings.Contains(msg, "模型不存在") {
		return "model_error"
	}
	return "error"
}
