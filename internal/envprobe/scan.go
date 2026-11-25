package envprobe

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// LookupBinary searches PATH plus common user-installed CLI directories
// (~/.npm-global/bin, ~/.local/bin, ~/.nvm/versions/node/*/bin, homebrew, etc.)
// so systemd services with minimal PATH still discover all installed CLIs.
func LookupBinary(binary string) string {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		return ""
	}
	if p, err := exec.LookPath(binary); err == nil && p != "" {
		return p
	}
	for _, dir := range extraSearchDirs() {
		cand := filepath.Join(dir, binary)
		if st, err := os.Stat(cand); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return cand
		}
	}
	return ""
}

// ExpandedEnv returns os.Environ() with PATH augmented by extraSearchDirs.
func ExpandedEnv() []string {
	env := os.Environ()
	extras := extraSearchDirs()
	if len(extras) == 0 {
		return env
	}
	extraPath := strings.Join(extras, string(os.PathListSeparator))
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			out = append(out, kv+string(os.PathListSeparator)+extraPath)
			found = true
		} else {
			out = append(out, kv)
		}
	}
	if !found {
		out = append(out, "PATH="+extraPath)
	}
	return out
}

func extraSearchDirs() []string {
	home, _ := os.UserHomeDir()
	var dirs []string
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".npm-global", "bin"),
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".bun", "bin"),
			filepath.Join(home, ".volta", "bin"),
			filepath.Join(home, ".asdf", "shims"),
			filepath.Join(home, ".cargo", "bin"),
			filepath.Join(home, "go", "bin"),
		)
		if matches, err := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "bin")); err == nil && len(matches) > 0 {
			sort.Sort(sort.Reverse(sort.StringSlice(matches)))
			dirs = append(dirs, matches...)
		}
	}
	dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin", "/usr/local/go/bin", "/usr/bin", "/bin")
	return dirs
}

// ScanHost performs a fast (< 300ms typical) static snapshot of this machine's
// hardware, toolchains, coding CLIs, sanitized Git remotes, local .tmh KBs,
// and external MCP servers configured in Codex / Claude.
func ScanHost(ctx context.Context) HostProfile {
	host, _ := os.Hostname()
	prof := HostProfile{
		Host:      host,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		ScannedAt: time.Now().UTC(),
	}

	var wg sync.WaitGroup
	wg.Add(5)

	go func() {
		defer wg.Done()
		prof.Toolchains = scanToolchains(ctx)
	}()
	go func() {
		defer wg.Done()
		prof.CLIs = scanCLIs(ctx)
	}()
	go func() {
		defer wg.Done()
		prof.KnowledgeBases = scanKnowledgeBases(ctx)
	}()
	go func() {
		defer wg.Done()
		prof.ExternalMCPs = scanExternalMCPs()
	}()
	go func() {
		defer wg.Done()
		prof.Hardware = scanHardwareBase()
	}()

	wg.Wait()

	// Finalize sandbox recommendation now that both Hardware and Toolchains.Docker are populated.
	finalizeSandboxHint(&prof.Hardware, prof.Toolchains.Docker.Available)

	// Collect sanitized Git remotes from ~/.git-credentials + discovered repos.
	prof.GitRemotes = scanGitRemotes(prof.KnowledgeBases)

	return prof
}

func scanHardwareBase() HardwareInfo {
	hw := HardwareInfo{
		CPUCores: runtime.NumCPU(),
	}
	hw.MemTotalMB, hw.MemAvailMB = readMemoryMB()
	hw.DiskTotalGB, hw.DiskAvailGB = readDiskGB("/")
	return hw
}

func finalizeSandboxHint(hw *HardwareInfo, dockerRunning bool) {
	if dockerRunning && hw.MemAvailMB >= 4096 && hw.DiskAvailGB >= 20 {
		hw.SandboxHint = "docker_ready"
		hw.SandboxLabel = "推荐 Docker 沙箱（Docker 正常，内存与磁盘充裕）"
	} else if dockerRunning {
		hw.SandboxHint = "local_recommended"
		hw.SandboxLabel = "建议本地直跑（Docker 可用，但可用内存或磁盘偏紧）"
	} else {
		hw.SandboxHint = "local_recommended"
		hw.SandboxLabel = "建议本地直跑（未检测到运行中的 Docker 引擎）"
	}
}

func readMemoryMB() (totalMB, availMB int64) {
	if runtime.GOOS == "linux" {
		f, err := os.Open("/proc/meminfo")
		if err != nil {
			return 0, 0
		}
		defer f.Close()
		var totalKB, availKB, freeKB, buffersKB, cachedKB int64
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 2 {
				continue
			}
			val, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				continue
			}
			switch fields[0] {
			case "MemTotal:":
				totalKB = val
			case "MemAvailable:":
				availKB = val
			case "MemFree:":
				freeKB = val
			case "Buffers:":
				buffersKB = val
			case "Cached:":
				cachedKB = val
			}
		}
		if availKB == 0 {
			availKB = freeKB + buffersKB + cachedKB
		}
		return totalKB / 1024, availKB / 1024
	}
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
			if bytesTotal, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64); err == nil {
				totalMB = bytesTotal / (1024 * 1024)
			}
		}
		if out, err := exec.Command("vm_stat").Output(); err == nil {
			pageSize := int64(16384)
			var freePages, inactivePages, specPages int64
			for _, line := range strings.Split(string(out), "\n") {
				if strings.Contains(line, "page size of") {
					re := regexp.MustCompile(`page size of (\d+) bytes`)
					if m := re.FindStringSubmatch(line); len(m) == 2 {
						if ps, err := strconv.ParseInt(m[1], 10, 64); err == nil && ps > 0 {
							pageSize = ps
						}
					}
				}
				parts := strings.SplitN(line, ":", 2)
				if len(parts) != 2 {
					continue
				}
				numStr := strings.TrimRight(strings.TrimSpace(parts[1]), ".")
				n, err := strconv.ParseInt(numStr, 10, 64)
				if err != nil {
					continue
				}
				switch strings.TrimSpace(parts[0]) {
				case "Pages free":
					freePages = n
				case "Pages inactive":
					inactivePages = n
				case "Pages speculative":
					specPages = n
				}
			}
			availMB = (freePages + inactivePages + specPages) * pageSize / (1024 * 1024)
		}
		return totalMB, availMB
	}
	return 0, 0
}

func readDiskGB(path string) (totalGB, availGB float64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	bsize := uint64(st.Bsize)
	total := float64(st.Blocks*bsize) / (1024 * 1024 * 1024)
	avail := float64(st.Bavail*bsize) / (1024 * 1024 * 1024)
	return math.Round(total*10) / 10, math.Round(avail*10) / 10
}

func quickCmdVersion(ctx context.Context, bin string, args ...string) (string, error) {
	tctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(tctx, bin, args...)
	cmd.Env = ExpandedEnv()
	out, err := cmd.CombinedOutput()
	line := firstNonEmptyLine(string(out))
	if err != nil {
		return line, err
	}
	return line, nil
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func scanToolchains(ctx context.Context) ToolchainInfo {
	var tc ToolchainInfo
	var wg sync.WaitGroup
	wg.Add(6)

	go func() {
		defer wg.Done()
		p := LookupBinary("docker")
		if p == "" {
			return
		}
		tc.Docker.Path = p
		ver, err := quickCmdVersion(ctx, p, "version", "--format", "{{.Server.Version}}")
		if err == nil && ver != "" {
			tc.Docker.Available = true
			tc.Docker.Version = ver
			tc.Docker.Detail = "引擎运行中"
		} else {
			tc.Docker.Available = false
			tc.Docker.Detail = "已安装但引擎未运行或无权限"
		}
	}()

	go func() {
		defer wg.Done()
		if p := LookupBinary("git"); p != "" {
			tc.Git.Path = p
			tc.Git.Available = true
			if ver, err := quickCmdVersion(ctx, p, "--version"); err == nil {
				tc.Git.Version = strings.TrimPrefix(ver, "git version ")
			}
		}
	}()

	go func() {
		defer wg.Done()
		if p := LookupBinary("node"); p != "" {
			tc.Node.Path = p
			tc.Node.Available = true
			if ver, err := quickCmdVersion(ctx, p, "--version"); err == nil {
				tc.Node.Version = ver
			}
		}
	}()

	go func() {
		defer wg.Done()
		if p := LookupBinary("python3"); p != "" {
			tc.Python.Path = p
			tc.Python.Available = true
			if ver, err := quickCmdVersion(ctx, p, "--version"); err == nil {
				tc.Python.Version = strings.TrimPrefix(ver, "Python ")
			}
		}
	}()

	go func() {
		defer wg.Done()
		if p := LookupBinary("go"); p != "" {
			tc.Go.Path = p
			tc.Go.Available = true
			if ver, err := quickCmdVersion(ctx, p, "version"); err == nil {
				tc.Go.Version = strings.TrimPrefix(ver, "go version ")
			}
		}
	}()

	go func() {
		defer wg.Done()
		home, _ := os.UserHomeDir()
		if home == "" {
			return
		}
		patterns := []string{
			filepath.Join(home, ".cache", "ms-playwright", "chromium-*", "chrome-linux", "chrome"),
			filepath.Join(home, ".cache", "ms-playwright", "chromium-*", "chrome-mac", "Chromium.app"),
			filepath.Join(home, "Library", "Caches", "ms-playwright", "chromium-*", "chrome-mac*", "Chromium.app"),
		}
		for _, pat := range patterns {
			if matches, err := filepath.Glob(pat); err == nil && len(matches) > 0 {
				sort.Sort(sort.Reverse(sort.StringSlice(matches)))
				tc.Playwright.Available = true
				tc.Playwright.Path = matches[0]
				tc.Playwright.Detail = filepath.Base(filepath.Dir(filepath.Dir(matches[0])))
				return
			}
		}
	}()

	wg.Wait()
	return tc
}

type cliSpec struct {
	id     string
	name   string
	binary string
}

var knownCLIs = []cliSpec{
	{id: "codex", name: "Codex CLI", binary: "codex"},
	{id: "claude", name: "Claude Code CLI", binary: "claude"},
	{id: "codebuddy", name: "CodeBuddy CLI", binary: "codebuddy"},
	{id: "opencode", name: "OpenCode CLI", binary: "opencode"},
	{id: "cursor", name: "Cursor CLI", binary: "cursor-agent"},
	{id: "agy", name: "Antigravity CLI", binary: "agy"},
	{id: "kimi", name: "Kimi Code CLI", binary: "kimi"},
	{id: "pi", name: "Pi Coding Agent", binary: "pi"},
	{id: "dsh", name: "DeepSeek Harness", binary: "dsh"},
	{id: "zcode", name: "ZCode CLI", binary: "zcode"},
	{id: "gemini-cli", name: "Gemini CLI", binary: "gemini"},
	{id: "aider", name: "Aider CLI", binary: "aider"},
	{id: "cline", name: "Cline CLI", binary: "cline"},
	{id: "qwen-code", name: "Qwen Code CLI", binary: "qwen"},
	{id: "trae", name: "Trae CLI", binary: "trae"},
	{id: "copilot", name: "GitHub Copilot CLI", binary: "copilot"},
	{id: "amp", name: "Sourcegraph Amp", binary: "amp"},
	{id: "goose", name: "Block Goose", binary: "goose"},
	{id: "windsurf", name: "Windsurf CLI", binary: "windsurf"},
	{id: "auggie", name: "Augment Code (Auggie)", binary: "auggie"},
}

func scanCLIs(ctx context.Context) []CLIStatus {
	home, _ := os.UserHomeDir()
	results := make([]CLIStatus, len(knownCLIs))
	var wg sync.WaitGroup
	for i, spec := range knownCLIs {
		wg.Add(1)
		go func(idx int, s cliSpec) {
			defer wg.Done()
			st := CLIStatus{
				ID:          s.id,
				Name:        s.name,
				Binary:      s.binary,
				AuthMode:    "none",
				ProbeStatus: "untested",
			}
			binPath := LookupBinary(s.binary)
			if binPath != "" {
				st.Available = true
				st.Path = binPath
				if ver, err := quickCmdVersion(ctx, binPath, "--version"); err == nil {
					st.Version = ver
				}
			}
			enrichCLIAuth(home, &st)
			results[idx] = st
		}(i, spec)
	}
	wg.Wait()
	return results
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

type parsedCodexConfig struct {
	DefaultModel string
	GatewayURL   string
	HasAuth      bool
	BearerToken  string // kept in memory only, never serialized
	MCPs         []DiscoveredMCP
}

func parseCodexConfig(content string) parsedCodexConfig {
	var cfg parsedCodexConfig
	var section string
	var activeProvider string
	type provEntry struct {
		baseURL     string
		bearerToken string
		hasAuth     bool
	}
	providers := make(map[string]provEntry)
	var currentMCP *DiscoveredMCP
	flushMCP := func() {
		if currentMCP != nil && currentMCP.Name != "" && currentMCP.Target != "" {
			currentMCP.Args = sanitizeMCPArgs(currentMCP.Args)
			cfg.MCPs = append(cfg.MCPs, *currentMCP)
		}
		currentMCP = nil
	}

	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flushMCP()
			section = strings.TrimSpace(line[1 : len(line)-1])
			if strings.HasPrefix(section, "mcp_servers.") {
				name := strings.Trim(strings.TrimPrefix(section, "mcp_servers."), `"' `)
				// Ignore nested sub-tables like mcp_servers.foo.env
				if !strings.Contains(name, ".") && name != "" {
					currentMCP = &DiscoveredMCP{
						Name:   name,
						Source: "~/.codex/config.toml",
					}
				}
			}
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		// Strip trailing inline comment outside quotes
		val = stripTOMLComment(val)
		unquoted := unquoteTOMLString(val)

		if currentMCP != nil {
			switch key {
			case "url":
				currentMCP.Transport = "http"
				currentMCP.Target = sanitizeURL(unquoted)
			case "command":
				currentMCP.Transport = "stdio"
				currentMCP.Target = unquoted
			case "args":
				currentMCP.Args = parseTOMLStringArray(val)
			}
			continue
		}

		if section == "" {
			if key == "model" && unquoted != "" {
				cfg.DefaultModel = unquoted
			} else if key == "model_provider" && unquoted != "" {
				activeProvider = unquoted
			}
		} else if strings.HasPrefix(section, "model_providers.") {
			pname := strings.Trim(strings.TrimPrefix(section, "model_providers."), `"' `)
			if strings.Contains(pname, ".") || pname == "" {
				continue
			}
			p := providers[pname]
			switch key {
			case "base_url":
				if unquoted != "" {
					p.baseURL = sanitizeURL(unquoted)
				}
			case "experimental_bearer_token":
				if unquoted != "" {
					p.hasAuth = true
					p.bearerToken = unquoted
				}
			case "requires_openai_auth":
				if strings.EqualFold(val, "false") {
					p.hasAuth = true
				}
			}
			providers[pname] = p
		}
	}
	flushMCP()

	if activeProvider != "" {
		if p, ok := providers[activeProvider]; ok {
			cfg.GatewayURL = p.baseURL
			cfg.BearerToken = p.bearerToken
			cfg.HasAuth = p.hasAuth
		}
	} else {
		for _, p := range providers {
			if p.baseURL != "" && (p.bearerToken != "" || p.hasAuth) {
				cfg.GatewayURL = p.baseURL
				cfg.BearerToken = p.bearerToken
				cfg.HasAuth = p.hasAuth
				break
			}
		}
	}
	return cfg
}

func stripTOMLComment(val string) string {
	inSingle, inDouble := false, false
	for i := 0; i < len(val); i++ {
		c := val[i]
		if c == '\'' && !inDouble {
			inSingle = !inSingle
		} else if c == '"' && !inSingle && (i == 0 || val[i-1] != '\\') {
			inDouble = !inDouble
		} else if c == '#' && !inSingle && !inDouble {
			return strings.TrimSpace(val[:i])
		}
	}
	return strings.TrimSpace(val)
}

func unquoteTOMLString(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return s[1 : len(s)-1]
	}
	return s
}

func parseTOMLStringArray(val string) []string {
	val = strings.TrimSpace(val)
	if !strings.HasPrefix(val, "[") || !strings.HasSuffix(val, "]") {
		return nil
	}
	inner := strings.TrimSpace(val[1 : len(val)-1])
	if inner == "" {
		return nil
	}
	parts := strings.Split(inner, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		u := unquoteTOMLString(p)
		if u != "" {
			out = append(out, u)
		}
	}
	return out
}

type parsedClaudeSettings struct {
	GatewayURL   string
	DefaultModel string
	HasAuth      bool
	MCPs         []DiscoveredMCP
}

func parseClaudeSettings(raw []byte, source string) parsedClaudeSettings {
	var out parsedClaudeSettings
	var doc struct {
		Env        map[string]string `json:"env"`
		Model      string            `json:"model"`
		MCPServers map[string]struct {
			URL     string   `json:"url"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return out
	}
	if u := strings.TrimSpace(doc.Env["ANTHROPIC_BASE_URL"]); u != "" {
		out.GatewayURL = sanitizeURL(u)
	}
	if strings.TrimSpace(doc.Env["ANTHROPIC_AUTH_TOKEN"]) != "" || strings.TrimSpace(doc.Env["ANTHROPIC_API_KEY"]) != "" {
		out.HasAuth = true
	}
	if m := strings.TrimSpace(doc.Env["ANTHROPIC_MODEL"]); m != "" {
		out.DefaultModel = m
	} else if m := strings.TrimSpace(doc.Model); m != "" {
		out.DefaultModel = m
	}
	names := make([]string, 0, len(doc.MCPServers))
	for name := range doc.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		srv := doc.MCPServers[name]
		if u := strings.TrimSpace(srv.URL); u != "" {
			out.MCPs = append(out.MCPs, DiscoveredMCP{
				Name:      name,
				Source:    source,
				Transport: "http",
				Target:    sanitizeURL(u),
			})
		} else if c := strings.TrimSpace(srv.Command); c != "" {
			out.MCPs = append(out.MCPs, DiscoveredMCP{
				Name:      name,
				Source:    source,
				Transport: "stdio",
				Target:    c,
				Args:      sanitizeMCPArgs(srv.Args),
			})
		}
	}
	return out
}

func enrichCLIAuth(home string, st *CLIStatus) {
	switch st.ID {
	case "codex":
		if raw, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml")); err == nil {
			cfg := parseCodexConfig(string(raw))
			st.GatewayURL = cfg.GatewayURL
			st.DefaultModel = cfg.DefaultModel
			if cfg.DefaultModel != "" {
				st.Models = []string{cfg.DefaultModel}
			}
			if cfg.HasAuth {
				st.AuthMode = "custom_gateway"
				st.AuthHint = "复用 ~/.codex/config.toml 自定义网关"
				return
			}
		}
		if fileExists(filepath.Join(home, ".codex", "auth.json")) {
			st.AuthMode = "oauth"
			st.AuthHint = "复用 ~/.codex/auth.json 登录态"
			return
		}
		if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "" {
			st.AuthMode = "env_key"
			st.AuthHint = "复用环境变量 OPENAI_API_KEY"
			return
		}
		st.AuthHint = "未检测到 ~/.codex 凭据"

	case "claude":
		if raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json")); err == nil {
			cfg := parseClaudeSettings(raw, "~/.claude/settings.json")
			st.GatewayURL = cfg.GatewayURL
			st.DefaultModel = cfg.DefaultModel
			if cfg.DefaultModel != "" {
				st.Models = []string{cfg.DefaultModel}
			}
			if cfg.HasAuth {
				st.AuthMode = "custom_gateway"
				st.AuthHint = "复用 ~/.claude/settings.json 自定义网关"
				return
			}
		}
		if fileExists(filepath.Join(home, ".claude", ".credentials.json")) || fileExists(filepath.Join(home, ".claude.json")) {
			st.AuthMode = "oauth"
			st.AuthHint = "复用本机 Claude 登录态"
			return
		}
		if strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != "" {
			st.AuthMode = "env_key"
			st.AuthHint = "复用环境变量 ANTHROPIC_API_KEY"
			return
		}
		st.AuthHint = "未检测到 ~/.claude 凭据"

	case "codebuddy":
		if fileExists(filepath.Join(home, ".codebuddy", "config.json")) {
			st.AuthMode = "oauth"
			st.AuthHint = "复用 ~/.codebuddy/config.json"
		} else if strings.TrimSpace(os.Getenv("CODEBUDDY_API_KEY")) != "" {
			st.AuthMode = "env_key"
			st.AuthHint = "复用环境变量 CODEBUDDY_API_KEY"
		}
	case "opencode":
		if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "" || strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != "" {
			st.AuthMode = "env_key"
			st.AuthHint = "复用环境变量凭据"
		}
	case "cursor":
		if fileExists(filepath.Join(home, ".cursor", "auth.json")) {
			st.AuthMode = "oauth"
			st.AuthHint = "复用 ~/.cursor 登录态"
		}
	case "kimi":
		if fileExists(filepath.Join(home, ".kimi", "config.toml")) || strings.TrimSpace(os.Getenv("MOONSHOT_API_KEY")) != "" {
			st.AuthMode = "oauth"
			st.AuthHint = "复用本机 Kimi 配置"
		}
	case "agy":
		if fileExists(filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")) || strings.TrimSpace(os.Getenv("GEMINI_API_KEY")) != "" {
			st.AuthMode = "oauth"
			st.AuthHint = "复用本机 Antigravity / Gemini 配置"
		}
	case "gemini-cli":
		if fileExists(filepath.Join(home, ".gemini", "oauth_creds.json")) || fileExists(filepath.Join(home, ".gemini", "settings.json")) {
			st.AuthMode = "oauth"
			st.AuthHint = "复用 ~/.gemini 登录态"
		} else if strings.TrimSpace(os.Getenv("GEMINI_API_KEY")) != "" || strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")) != "" {
			st.AuthMode = "env_key"
			st.AuthHint = "复用环境变量 GEMINI_API_KEY"
		}
	case "aider":
		if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "" || strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != "" || strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")) != "" {
			st.AuthMode = "env_key"
			st.AuthHint = "复用环境变量模型 API Key"
		}
	case "cline":
		if fileExists(filepath.Join(home, ".cline", "config.json")) || strings.TrimSpace(os.Getenv("CLINE_API_KEY")) != "" || strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != "" {
			st.AuthMode = "env_key"
			st.AuthHint = "复用本机 Cline 配置/环境变量"
		}
	case "qwen-code":
		if fileExists(filepath.Join(home, ".qwen", "settings.json")) || strings.TrimSpace(os.Getenv("DASHSCOPE_API_KEY")) != "" {
			st.AuthMode = "oauth"
			st.AuthHint = "复用 ~/.qwen 或 DASHSCOPE_API_KEY"
		}
	case "trae":
		if fileExists(filepath.Join(home, ".trae", "config.json")) || strings.TrimSpace(os.Getenv("TRAE_API_KEY")) != "" {
			st.AuthMode = "oauth"
			st.AuthHint = "复用本机 Trae 登录态"
		}
	case "copilot":
		if fileExists(filepath.Join(home, ".config", "github-copilot", "hosts.json")) || strings.TrimSpace(os.Getenv("GITHUB_TOKEN")) != "" || strings.TrimSpace(os.Getenv("GH_TOKEN")) != "" {
			st.AuthMode = "oauth"
			st.AuthHint = "复用 GitHub Copilot / GH Token"
		}
	case "amp":
		if fileExists(filepath.Join(home, ".config", "amp", "settings.json")) || strings.TrimSpace(os.Getenv("AMP_API_KEY")) != "" {
			st.AuthMode = "oauth"
			st.AuthHint = "复用本机 Sourcegraph Amp 配置"
		}
	case "goose":
		if fileExists(filepath.Join(home, ".config", "goose", "config.yaml")) || strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "" || strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != "" {
			st.AuthMode = "oauth"
			st.AuthHint = "复用 ~/.config/goose/config.yaml"
		}
	case "windsurf":
		if fileExists(filepath.Join(home, ".codeium", "windsurf", "mcp_config.json")) || strings.TrimSpace(os.Getenv("WINDSURF_API_KEY")) != "" {
			st.AuthMode = "oauth"
			st.AuthHint = "复用 ~/.codeium/windsurf 配置"
		}
	case "auggie":
		if fileExists(filepath.Join(home, ".augment", "settings.json")) || strings.TrimSpace(os.Getenv("AUGMENT_API_TOKEN")) != "" {
			st.AuthMode = "oauth"
			st.AuthHint = "复用 ~/.augment 配置"
		}
	}
}

// sanitizeURL strips embedded userinfo, query parameters, and fragments from any URL.
func sanitizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return raw
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

var sensitiveArgFlagRe = regexp.MustCompile(`(?i)(token|secret|apikey|api[_-]?key|password|passwd|auth|bearer)`)

func sanitizeMCPArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, 0, len(args))
	redactNext := false
	for _, a := range args {
		if redactNext {
			out = append(out, "[REDACTED]")
			redactNext = false
			continue
		}
		if strings.HasPrefix(a, "-") && !strings.Contains(a, "=") && sensitiveArgFlagRe.MatchString(a) {
			out = append(out, a)
			redactNext = true
			continue
		}
		if eq := strings.IndexByte(a, '='); eq > 0 && sensitiveArgFlagRe.MatchString(a[:eq]) {
			out = append(out, a[:eq+1]+"[REDACTED]")
			continue
		}
		cleaned := secretTokenRe.ReplaceAllString(a, "[REDACTED]")
		if strings.HasPrefix(cleaned, "http://") || strings.HasPrefix(cleaned, "https://") {
			cleaned = sanitizeURL(cleaned)
		}
		out = append(out, cleaned)
	}
	return out
}

func parseGitCredentials(content string) []GitRemoteStatus {
	seen := make(map[string]bool)
	var out []GitRemoteStatus
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil || u.Host == "" {
			continue
		}
		scheme := u.Scheme
		if scheme == "" {
			scheme = "https"
		}
		cleanURL := fmt.Sprintf("%s://%s", scheme, u.Host)
		if seen[cleanURL] {
			continue
		}
		seen[cleanURL] = true
		out = append(out, GitRemoteStatus{
			Host:   u.Host,
			URL:    cleanURL,
			Source: ".git-credentials",
		})
	}
	return out
}

func scanGitRemotes(kbs []DiscoveredKB) []GitRemoteStatus {
	seen := make(map[string]bool)
	var out []GitRemoteStatus
	home, _ := os.UserHomeDir()
	if home != "" {
		if raw, err := os.ReadFile(filepath.Join(home, ".git-credentials")); err == nil {
			for _, r := range parseGitCredentials(string(raw)) {
				seen[r.URL] = true
				out = append(out, r)
			}
		}
	}
	for _, kb := range kbs {
		if kb.GitRemote == "" {
			continue
		}
		clean := sanitizeURL(kb.GitRemote)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		host := clean
		if u, err := url.Parse(clean); err == nil && u.Host != "" {
			host = u.Host
		}
		out = append(out, GitRemoteStatus{
			Host:   host,
			URL:    clean,
			Source: "repo",
		})
	}
	return out
}

func scanKnowledgeBases(ctx context.Context) []DiscoveredKB {
	home, _ := os.UserHomeDir()
	var roots []string
	roots = append(roots,
		"/opt/webuddy-demo/data/repositories",
		"/opt/webuddy/data/repositories",
		"/opt/webuddy/repos",
	)
	if home != "" {
		roots = append(roots,
			filepath.Join(home, "workspace"),
			filepath.Join(home, "Desktop", "workspace"),
		)
	}
	if extra := strings.TrimSpace(os.Getenv("WEBUDDY_KB_SCAN_DIRS")); extra != "" {
		for _, d := range strings.Split(extra, string(os.PathListSeparator)) {
			if d = strings.TrimSpace(d); d != "" {
				roots = append(roots, d)
			}
		}
	}

	seen := make(map[string]bool)
	var found []DiscoveredKB
	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "vendor": true, ".tmh": true,
		"target": true, "dist": true, "build": true, ".venv": true, "__pycache__": true,
	}

	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > 3 || ctx.Err() != nil {
			return
		}
		abs, err := filepath.Abs(dir)
		if err != nil || seen[abs] {
			return
		}
		st, err := os.Stat(abs)
		if err != nil || !st.IsDir() {
			return
		}
		// Check if this directory itself is a .tmh repo
		hasGraph := fileExists(filepath.Join(abs, ".tmh", "cache", "graph.db")) || fileExists(filepath.Join(abs, ".tmh", "graph.db"))
		hasKB := false
		if stKB, err := os.Stat(filepath.Join(abs, ".tmh", "knowledge")); err == nil && stKB.IsDir() {
			hasKB = true
		}
		if hasGraph || hasKB {
			seen[abs] = true
			kb := DiscoveredKB{
				Name:     filepath.Base(abs),
				Path:     abs,
				HasGraph: hasGraph,
				HasKB:    hasKB,
			}
			if _, err := os.Stat(filepath.Join(abs, ".git")); err == nil {
				kb.IsGit = true
				kb.GitRemote, kb.DefaultBranch = readGitRepoMeta(abs)
			}
			found = append(found, kb)
			return
		}

		entries, err := os.ReadDir(abs)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if skipDirs[name] || strings.HasPrefix(name, ".") {
				continue
			}
			walk(filepath.Join(abs, name), depth+1)
		}
	}

	for _, root := range roots {
		walk(root, 0)
	}
	sort.Slice(found, func(i, j int) bool {
		return found[i].Path < found[j].Path
	})
	return found
}

// readGitRepoMeta parses .git/HEAD and .git/config directly (supporting both
// normal repos and linked git worktrees where .git is a gitdir pointer file).
func readGitRepoMeta(repoPath string) (remoteURL, branch string) {
	gitDir := filepath.Join(repoPath, ".git")
	if st, err := os.Stat(gitDir); err == nil && !st.IsDir() {
		if raw, err := os.ReadFile(gitDir); err == nil {
			line := strings.TrimSpace(string(raw))
			if strings.HasPrefix(line, "gitdir:") {
				target := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
				if !filepath.IsAbs(target) {
					target = filepath.Join(repoPath, target)
				}
				gitDir = filepath.Clean(target)
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(gitDir, "HEAD")); err == nil {
		line := strings.TrimSpace(string(raw))
		if strings.HasPrefix(line, "ref: refs/heads/") {
			branch = strings.TrimPrefix(line, "ref: refs/heads/")
		}
	}
	cfgPath := filepath.Join(gitDir, "config")
	if !fileExists(cfgPath) {
		// Linked worktree stores shared config under commondir
		if cdRaw, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
			cd := strings.TrimSpace(string(cdRaw))
			if cd != "" {
				if !filepath.IsAbs(cd) {
					cd = filepath.Join(gitDir, cd)
				}
				cfgPath = filepath.Join(filepath.Clean(cd), "config")
			}
		}
	}
	if raw, err := os.ReadFile(cfgPath); err == nil {
		inOrigin := false
		for _, line := range strings.Split(string(raw), "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "[") {
				inOrigin = strings.EqualFold(t, `[remote "origin"]`)
				continue
			}
			if inOrigin && strings.HasPrefix(t, "url") {
				if eq := strings.IndexByte(t, '='); eq >= 0 {
					remoteURL = sanitizeURL(strings.TrimSpace(t[eq+1:]))
					break
				}
			}
		}
	}
	return remoteURL, branch
}

func scanExternalMCPs() []DiscoveredMCP {
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	var all []DiscoveredMCP
	seen := make(map[string]bool)
	add := func(list []DiscoveredMCP) {
		for _, m := range list {
			key := m.Name + "|" + m.Target
			if seen[key] {
				continue
			}
			seen[key] = true
			all = append(all, m)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml")); err == nil {
		add(parseCodexConfig(string(raw)).MCPs)
	}
	if raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json")); err == nil {
		add(parseClaudeSettings(raw, "~/.claude/settings.json").MCPs)
	}
	if raw, err := os.ReadFile(filepath.Join(home, ".claude.json")); err == nil {
		add(parseClaudeSettings(raw, "~/.claude.json").MCPs)
	}
	return all
}
