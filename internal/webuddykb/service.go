package webuddykb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agentflow/internal/apperr"
)

const DefaultTimeout = 60 * time.Second

// Service bridges LoopForge to the embedded Webuddy Python KB & Code Graph engine
// (webuddy_core/bridge.py).
type Service struct {
	PythonBin  string
	BridgePath string
	Timeout    time.Duration
}

func NewService() *Service {
	py := strings.TrimSpace(os.Getenv("WEBUDDY_PYTHON"))
	if py == "" {
		py = "python3"
	}
	return &Service{
		PythonBin:  py,
		BridgePath: resolveBridgePath(),
		Timeout:    DefaultTimeout,
	}
}

func resolveBridgePath() string {
	if custom := strings.TrimSpace(os.Getenv("WEBUDDY_CORE_DIR")); custom != "" {
		return filepath.Join(custom, "bridge.py")
	}
	candidates := []string{
		"webuddy_core/bridge.py",
		"../webuddy_core/bridge.py",
		"../../webuddy_core/bridge.py",
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "webuddy_core", "bridge.py"),
			filepath.Join(dir, "..", "..", "webuddy_core", "bridge.py"),
		)
	}
	for _, c := range candidates {
		if abs, err := filepath.Abs(c); err == nil {
			if st, err := os.Stat(abs); err == nil && !st.IsDir() {
				return abs
			}
		}
	}
	return "webuddy_core/bridge.py"
}

func (s *Service) runJSON(ctx context.Context, args ...string) (json.RawMessage, error) {
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmdArgs := append([]string{s.BridgePath}, args...)
	cmd := exec.CommandContext(cctx, s.PythonBin, cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		var errBody struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(stderr.Bytes(), &errBody) == nil && errBody.Error != "" {
			return nil, apperr.New(apperr.Invalid, errBody.Error)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("webuddy kb bridge: %s", msg)
	}
	raw := bytes.TrimSpace(stdout.Bytes())
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	return json.RawMessage(raw), nil
}

func (s *Service) Overview(ctx context.Context, repoPath string) (json.RawMessage, error) {
	args := []string{"overview"}
	if p := strings.TrimSpace(repoPath); p != "" {
		args = append(args, "--repo", p)
	}
	return s.runJSON(ctx, args...)
}

func (s *Service) Scan(ctx context.Context, repoPath string) (json.RawMessage, error) {
	p := strings.TrimSpace(repoPath)
	if p == "" {
		return nil, apperr.New(apperr.Invalid, "请提供要扫描的本地代码仓库路径")
	}
	return s.runJSON(ctx, "scan", "--repo", p)
}

func (s *Service) Search(ctx context.Context, repoPath, query string, limit int) (json.RawMessage, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, apperr.New(apperr.Invalid, "搜索关键词不能为空")
	}
	if limit <= 0 {
		limit = 30
	}
	args := []string{"search", "--query", q, "--limit", strconv.Itoa(limit)}
	if p := strings.TrimSpace(repoPath); p != "" {
		args = append(args, "--repo", p)
	}
	return s.runJSON(ctx, args...)
}

func (s *Service) ReadFile(ctx context.Context, repoPath, relPath string, start, end int) (json.RawMessage, error) {
	rp := strings.TrimSpace(relPath)
	if rp == "" {
		return nil, apperr.New(apperr.Invalid, "文档路径不能为空")
	}
	args := []string{"read", "--path", rp}
	if p := strings.TrimSpace(repoPath); p != "" {
		args = append(args, "--repo", p)
	}
	if start > 0 {
		args = append(args, "--start", strconv.Itoa(start))
	}
	if end > 0 {
		args = append(args, "--end", strconv.Itoa(end))
	}
	return s.runJSON(ctx, args...)
}

func (s *Service) VerifyEvidence(ctx context.Context, repoPath, ref string) (json.RawMessage, error) {
	p := strings.TrimSpace(repoPath)
	r := strings.TrimSpace(ref)
	if p == "" || r == "" {
		return nil, apperr.New(apperr.Invalid, "需要提供仓库路径和源码证据引用（格式：文件:行 或 文件:起行-止行）")
	}
	return s.runJSON(ctx, "verify-evidence", "--repo", p, "--ref", r)
}

func (s *Service) Statecharts(ctx context.Context, repoPath string) (json.RawMessage, error) {
	args := []string{"statecharts"}
	if p := strings.TrimSpace(repoPath); p != "" {
		args = append(args, "--repo", p)
	}
	return s.runJSON(ctx, args...)
}

func (s *Service) Impact(ctx context.Context, repoPath, target string, depth int) (json.RawMessage, error) {
	p := strings.TrimSpace(repoPath)
	t := strings.TrimSpace(target)
	if p == "" || t == "" {
		return nil, apperr.New(apperr.Invalid, "需要提供仓库路径和变更影响分析目标（文件/符号/表名/接口）")
	}
	if depth <= 0 {
		depth = 3
	}
	return s.runJSON(ctx, "impact", "--repo", p, "--target", t, "--depth", strconv.Itoa(depth))
}

func (s *Service) Chain(ctx context.Context, repoPath, query string, flows int) (json.RawMessage, error) {
	p := strings.TrimSpace(repoPath)
	q := strings.TrimSpace(query)
	if p == "" || q == "" {
		return nil, apperr.New(apperr.Invalid, "需要提供仓库路径和业务问题")
	}
	if flows <= 0 {
		flows = 6
	}
	return s.runJSON(ctx, "chain", "--repo", p, "--query", q, "--flows", strconv.Itoa(flows))
}

func (s *Service) Questions(ctx context.Context, repoPath string) (json.RawMessage, error) {
	p := strings.TrimSpace(repoPath)
	if p == "" {
		return nil, apperr.New(apperr.Invalid, "需要提供仓库路径")
	}
	return s.runJSON(ctx, "questions", "--repo", p)
}

func (s *Service) AskQuestion(ctx context.Context, repoPath, question, pages, who string) (json.RawMessage, error) {
	p := strings.TrimSpace(repoPath)
	q := strings.TrimSpace(question)
	if p == "" || q == "" {
		return nil, apperr.New(apperr.Invalid, "需要提供仓库路径与问题内容")
	}
	args := []string{"ask", "--repo", p, "--question", q}
	if strings.TrimSpace(pages) != "" {
		args = append(args, "--pages", strings.TrimSpace(pages))
	}
	if strings.TrimSpace(who) != "" {
		args = append(args, "--who", strings.TrimSpace(who))
	}
	return s.runJSON(ctx, args...)
}

func (s *Service) AnswerQuestion(ctx context.Context, repoPath string, num int, text, by string) (json.RawMessage, error) {
	p := strings.TrimSpace(repoPath)
	t := strings.TrimSpace(text)
	if p == "" || num <= 0 || t == "" {
		return nil, apperr.New(apperr.Invalid, "需要提供仓库路径、问题编号与回答内容")
	}
	args := []string{"answer", "--repo", p, "--num", strconv.Itoa(num), "--text", t}
	if strings.TrimSpace(by) != "" {
		args = append(args, "--by", strings.TrimSpace(by))
	}
	return s.runJSON(ctx, args...)
}

func (s *Service) ConfirmPage(ctx context.Context, repoPath, pageID, by string) (json.RawMessage, error) {
	p := strings.TrimSpace(repoPath)
	pid := strings.TrimSpace(pageID)
	if p == "" || pid == "" {
		return nil, apperr.New(apperr.Invalid, "需要提供仓库路径与知识页 ID")
	}
	args := []string{"confirm-page", "--repo", p, "--page-id", pid}
	if strings.TrimSpace(by) != "" {
		args = append(args, "--by", strings.TrimSpace(by))
	}
	return s.runJSON(ctx, args...)
}
