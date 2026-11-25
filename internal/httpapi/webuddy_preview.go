package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
)

type previewInstance struct {
	ProjectID   string
	ProjectName string
	RepoPath    string
	Port        int
	Command     string
	QuickPaths  []string
	StartedAt   time.Time
	cmd         *exec.Cmd
	staticSrv   *http.Server
	mu          sync.Mutex
	logs        []string
	exited      bool
	exitErr     string
}

func (p *previewInstance) appendLog(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	p.mu.Lock()
	p.logs = append(p.logs, line)
	if len(p.logs) > 40 {
		p.logs = p.logs[len(p.logs)-40:]
	}
	p.mu.Unlock()
}

func (p *previewInstance) snapshotLogs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.logs))
	copy(out, p.logs)
	return out
}

func (p *previewInstance) alive() bool {
	p.mu.Lock()
	exited := p.exited
	p.mu.Unlock()
	if exited {
		return false
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p.Port), 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (p *previewInstance) stop() {
	if p.staticSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = p.staticSrv.Shutdown(ctx)
		cancel()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
		time.Sleep(300 * time.Millisecond)
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	}
	p.mu.Lock()
	p.exited = true
	p.mu.Unlock()
}

type previewStatusDTO struct {
	OK          bool     `json:"ok"`
	Running     bool     `json:"running"`
	ProjectID   string   `json:"projectId"`
	ProjectName string   `json:"projectName"`
	Port        int      `json:"port,omitempty"`
	PreviewURL  string   `json:"previewUrl,omitempty"`
	QuickPaths  []string `json:"quickPaths,omitempty"`
	Command     string   `json:"command,omitempty"`
	StartedAt   string   `json:"startedAt,omitempty"`
	Logs        []string `json:"logs,omitempty"`
	Error       string   `json:"error,omitempty"`
}

type webuddyPreviewManager struct {
	mu        sync.Mutex
	instances map[string]*previewInstance
}

var defaultPreviewMgr = &webuddyPreviewManager{
	instances: make(map[string]*previewInstance),
}

func allocFreeLocalPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("failed to allocate local port")
	}
	return addr.Port, nil
}

func detectPreviewQuickPaths(repoPath string) []string {
	paths := []string{"/"}
	candidates := []struct {
		file string
		url  string
	}{
		{"public/admin.html", "/admin.html"},
		{"public/login.html", "/login.html"},
		{"admin.html", "/admin.html"},
		{"login.html", "/login.html"},
	}
	seen := map[string]bool{"/": true}
	for _, c := range candidates {
		if info, err := os.Stat(filepath.Join(repoPath, c.file)); err == nil && !info.IsDir() {
			if !seen[c.url] {
				seen[c.url] = true
				paths = append(paths, c.url)
			}
		}
	}
	return paths
}

func (m *webuddyPreviewManager) status(pid, projName string) previewStatusDTO {
	m.mu.Lock()
	inst := m.instances[pid]
	m.mu.Unlock()
	if inst == nil {
		return previewStatusDTO{
			OK:          true,
			Running:     false,
			ProjectID:   pid,
			ProjectName: projName,
		}
	}
	alive := inst.alive()
	dto := previewStatusDTO{
		OK:          true,
		Running:     alive,
		ProjectID:   inst.ProjectID,
		ProjectName: inst.ProjectName,
		Port:        inst.Port,
		PreviewURL:  fmt.Sprintf("/app-preview/%s/", url.PathEscape(inst.ProjectID)),
		QuickPaths:  inst.QuickPaths,
		Command:     inst.Command,
		StartedAt:   inst.StartedAt.Format(time.RFC3339),
		Logs:        inst.snapshotLogs(),
	}
	inst.mu.Lock()
	if inst.exitErr != "" && !alive {
		dto.Error = inst.exitErr
	}
	inst.mu.Unlock()
	return dto
}

func (m *webuddyPreviewManager) stop(pid string) {
	m.mu.Lock()
	inst := m.instances[pid]
	delete(m.instances, pid)
	m.mu.Unlock()
	if inst != nil {
		inst.stop()
	}
}

func (m *webuddyPreviewManager) start(ctx context.Context, pid, projName, repoPath string) (previewStatusDTO, error) {
	m.mu.Lock()
	if cur := m.instances[pid]; cur != nil && cur.alive() {
		m.mu.Unlock()
		return m.status(pid, projName), nil
	} else if cur != nil {
		delete(m.instances, pid)
		go cur.stop()
	}
	m.mu.Unlock()

	if info, err := os.Stat(repoPath); err != nil || !info.IsDir() {
		return previewStatusDTO{}, fmt.Errorf("项目代码目录不存在: %s", repoPath)
	}

	port, err := allocFreeLocalPort()
	if err != nil {
		return previewStatusDTO{}, err
	}
	quickPaths := detectPreviewQuickPaths(repoPath)

	inst := &previewInstance{
		ProjectID:   pid,
		ProjectName: projName,
		RepoPath:    repoPath,
		Port:        port,
		QuickPaths:  quickPaths,
		StartedAt:   time.Now(),
	}

	pkgPath := filepath.Join(repoPath, "package.json")
	if _, err := os.Stat(pkgPath); err == nil {
		// Node.js project: install dependencies if node_modules missing
		if _, nmErr := os.Stat(filepath.Join(repoPath, "node_modules")); nmErr != nil {
			inst.appendLog("正在安装 Node.js 依赖 (npm install --omit=dev)...")
			icCtx, icCancel := context.WithTimeout(ctx, 2*time.Minute)
			ic := exec.CommandContext(icCtx, "npm", "install", "--omit=dev", "--no-audit", "--no-fund")
			ic.Dir = repoPath
			out, iErr := ic.CombinedOutput()
			icCancel()
			if iErr != nil {
				tail := strings.TrimSpace(string(out))
				if len(tail) > 500 {
					tail = tail[len(tail)-500:]
				}
				return previewStatusDTO{}, fmt.Errorf("npm install 失败: %v\n%s", iErr, tail)
			}
			inst.appendLog("npm install 完成")
		}

		// Determine start command
		startCmd := []string{"npm", "start"}
		if rawPkg, rErr := os.ReadFile(pkgPath); rErr == nil {
			var pkg struct {
				Scripts map[string]string `json:"scripts"`
			}
			if json.Unmarshal(rawPkg, &pkg) == nil {
				if strings.TrimSpace(pkg.Scripts["start"]) != "" {
					startCmd = []string{"npm", "start"}
				} else if strings.TrimSpace(pkg.Scripts["dev"]) != "" {
					startCmd = []string{"npm", "run", "dev"}
				} else if _, err := os.Stat(filepath.Join(repoPath, "server/index.js")); err == nil {
					startCmd = []string{"node", "server/index.js"}
				}
			}
		}
		inst.Command = strings.Join(startCmd, " ")
		cmd := exec.Command(startCmd[0], startCmd[1:]...)
		cmd.Dir = repoPath
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Env = append(os.Environ(),
			fmt.Sprintf("PORT=%d", port),
			"HOST=127.0.0.1",
			"NODE_ENV=development",
			"DEV_LOGIN=1",
			"DEV_AUTH=1",
			"SESSION_SECRET=webuddy-preview-session-secret-2026",
			fmt.Sprintf("PUBLIC_URL=/app-preview/%s", url.PathEscape(pid)),
		)
		stdout, _ := cmd.StdoutPipe()
		stderr, _ := cmd.StderrPipe()
		if err := cmd.Start(); err != nil {
			return previewStatusDTO{}, fmt.Errorf("启动预览服务失败: %w", err)
		}
		inst.cmd = cmd
		scanPipe := func(r io.Reader) {
			if r == nil {
				return
			}
			sc := bufio.NewScanner(r)
			for sc.Scan() {
				inst.appendLog(sc.Text())
			}
		}
		go scanPipe(stdout)
		go scanPipe(stderr)
		go func() {
			waitErr := cmd.Wait()
			inst.mu.Lock()
			inst.exited = true
			if waitErr != nil {
				inst.exitErr = waitErr.Error()
			}
			inst.mu.Unlock()
		}()
	} else {
		// Fallback: serve static directory (public/ or repo root) directly via Go http.FileServer
		staticRoot := repoPath
		if info, err := os.Stat(filepath.Join(repoPath, "public")); err == nil && info.IsDir() {
			staticRoot = filepath.Join(repoPath, "public")
		}
		inst.Command = "static-http-server (" + filepath.Base(staticRoot) + ")"
		srv := &http.Server{
			Addr:    fmt.Sprintf("127.0.0.1:%d", port),
			Handler: http.FileServer(http.Dir(staticRoot)),
		}
		inst.staticSrv = srv
		go func() {
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				inst.mu.Lock()
				inst.exited = true
				inst.exitErr = err.Error()
				inst.mu.Unlock()
			}
		}()
	}

	// Wait up to 15s for the local TCP port to start listening
	deadline := time.Now().Add(15 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		if inst.alive() {
			ready = true
			break
		}
		inst.mu.Lock()
		exited := inst.exited
		exitErr := inst.exitErr
		inst.mu.Unlock()
		if exited {
			return previewStatusDTO{}, fmt.Errorf("预览进程提前退出 (%s)：%s", exitErr, strings.Join(inst.snapshotLogs(), " | "))
		}
		time.Sleep(300 * time.Millisecond)
	}
	if !ready {
		inst.stop()
		return previewStatusDTO{}, fmt.Errorf("预览服务在 15 秒内未监听端口 %d，最近日志：%s", port, strings.Join(inst.snapshotLogs(), " | "))
	}

	m.mu.Lock()
	m.instances[pid] = inst
	m.mu.Unlock()
	return m.status(pid, projName), nil
}

// ---------- HTTP Handlers ----------

func (h *webuddyEnvHandlers) previewStatus(c *gin.Context) {
	pid, projName, _ := h.resolveProjectAndRepoPath(c.Query("projectId"))
	c.JSON(http.StatusOK, defaultPreviewMgr.status(pid, projName))
}

func (h *webuddyEnvHandlers) previewStart(c *gin.Context) {
	var req struct {
		ProjectID string `json:"projectId"`
	}
	_ = c.ShouldBindJSON(&req)
	pid, projName, repoPath := h.resolveProjectAndRepoPath(req.ProjectID)
	if pid == "" || repoPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "当前项目尚未挂载本地代码仓库，无法启动实时预览"})
		return
	}
	if authRequired() && !h.requireProjectWriter(c, pid) {
		return
	}
	dto, err := defaultPreviewMgr.start(c.Request.Context(), pid, projName, repoPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, dto)
}

func (h *webuddyEnvHandlers) previewStop(c *gin.Context) {
	var req struct {
		ProjectID string `json:"projectId"`
	}
	_ = c.ShouldBindJSON(&req)
	pid, projName, _ := h.resolveProjectAndRepoPath(req.ProjectID)
	if authRequired() && pid != "" && !h.requireProjectWriter(c, pid) {
		return
	}
	defaultPreviewMgr.stop(pid)
	c.JSON(http.StatusOK, defaultPreviewMgr.status(pid, projName))
}

var htmlRootAttrRe = regexp.MustCompile(`(?i)(\b(?:href|src|action)\s*=\s*["'])(/(?:[^/"'][^"']*)?["'])`)
var jsRootNavRe = regexp.MustCompile(`(\b(?:window\.)?location(?:\.href\s*=|\s*=|\.(?:replace|assign)\s*\()\s*["'])(/(?:[^/"'][^"']*)?["'])`)

func rewriteJSRootNav(src, prefix string) string {
	return jsRootNavRe.ReplaceAllStringFunc(src, func(m string) string {
		sub := jsRootNavRe.FindStringSubmatch(m)
		if len(sub) != 3 {
			return m
		}
		if strings.HasPrefix(sub[2], prefix+"/") {
			return m
		}
		return sub[1] + prefix + sub[2]
	})
}

// ANY /app-preview/:projectId and /app-preview/:projectId/*path
func (h *webuddyEnvHandlers) previewProxy(c *gin.Context) {
	if authRequired() {
		if _, ok := h.requestUser(c.Request, true); !ok {
			c.String(http.StatusUnauthorized, "请先登录 Webuddy 后再访问项目实时预览")
			return
		}
	}
	pid := strings.TrimSpace(c.Param("projectId"))
	if pid == "" {
		c.String(http.StatusBadRequest, "missing projectId")
		return
	}
	// Ensure trailing slash on base URL so relative assets resolve cleanly
	prefix := "/app-preview/" + url.PathEscape(pid)
	if c.Request.URL.Path == prefix {
		c.Redirect(http.StatusFound, prefix+"/")
		return
	}

	defaultPreviewMgr.mu.Lock()
	inst := defaultPreviewMgr.instances[pid]
	defaultPreviewMgr.mu.Unlock()
	if inst == nil || !inst.alive() {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusServiceUnavailable, fmt.Sprintf(`<!doctype html><html><body style="font-family:sans-serif;padding:32px;color:#334155"><h3>项目预览服务尚未启动 (%s)</h3><p>请返回 Webuddy 对话框或知识库点击「一键启动实时预览」后再刷新本页面。</p></body></html>`, pid))
		return
	}

	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", inst.Port))
	proxy := httputil.NewSingleHostReverseProxy(target)
	restPath := c.Param("path")
	if restPath == "" {
		restPath = "/"
	}
	origDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		origDirector(r)
		r.URL.Path = restPath
		r.URL.RawPath = ""
		r.Host = target.Host
		// Disable upstream gzip & conditional 304 caching so ModifyResponse always rewrites HTML/JS cleanly
		r.Header.Del("Accept-Encoding")
		r.Header.Del("If-None-Match")
		r.Header.Del("If-Modified-Since")
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Rewrite root-relative redirect Location headers
		if loc := resp.Header.Get("Location"); strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, "//") && !strings.HasPrefix(loc, prefix+"/") {
			resp.Header.Set("Location", prefix+loc)
		}
		// Allow embedding in same-origin Webuddy chat preview iframe
		resp.Header.Del("X-Frame-Options")
		resp.Header.Del("Content-Security-Policy")

		ct := strings.ToLower(resp.Header.Get("Content-Type"))
		if strings.Contains(ct, "text/html") {
			bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
			resp.Body.Close()
			if err != nil {
				return err
			}
			htmlStr := string(bodyBytes)
			// Rewrite root-relative href="/...", src="/...", action="/..."
			htmlStr = htmlRootAttrRe.ReplaceAllStringFunc(htmlStr, func(m string) string {
				sub := htmlRootAttrRe.FindStringSubmatch(m)
				if len(sub) != 3 {
					return m
				}
				if strings.HasPrefix(sub[2], prefix+"/") {
					return m
				}
				return sub[1] + prefix + sub[2]
			})
			// Also rewrite inline JS root-relative location redirects (e.g. location.href = '/login.html')
			htmlStr = rewriteJSRootNav(htmlStr, prefix)
			// Inject fetch/XHR/history prefix shim so SPA /api/, /auth/, /files/ calls route through /app-preview/<pid>
			shim := fmt.Sprintf(`<script>(function(){var P=%q;function rw(u){return(typeof u==='string'&&u.startsWith('/')&&!u.startsWith('//')&&!u.startsWith(P+'/')&&u!==P)?P+u:u;}var f=window.fetch;if(f){window.fetch=function(u,o){return f.call(this,rw(u),o);};}if(window.XMLHttpRequest){var xo=XMLHttpRequest.prototype.open;XMLHttpRequest.prototype.open=function(m,u){var a=Array.prototype.slice.call(arguments);a[1]=rw(u);return xo.apply(this,a);};}if(window.history){['pushState','replaceState'].forEach(function(k){var orig=history[k];if(orig){history[k]=function(s,t,u){return orig.call(this,s,t,u!=null?rw(String(u)):u);};}});}})();</script>`, prefix)
			lower := strings.ToLower(htmlStr)
			if idx := strings.Index(lower, "<head>"); idx >= 0 {
				htmlStr = htmlStr[:idx+6] + shim + htmlStr[idx+6:]
			} else {
				htmlStr = shim + htmlStr
			}
			newBytes := []byte(htmlStr)
			resp.Body = io.NopCloser(bytes.NewReader(newBytes))
			resp.ContentLength = int64(len(newBytes))
			resp.Header.Set("Content-Length", strconv.Itoa(len(newBytes)))
			resp.Header.Del("ETag")
			resp.Header.Set("Cache-Control", "no-store")
		} else if strings.Contains(ct, "javascript") {
			bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
			resp.Body.Close()
			if err != nil {
				return err
			}
			jsStr := rewriteJSRootNav(string(bodyBytes), prefix)
			newBytes := []byte(jsStr)
			resp.Body = io.NopCloser(bytes.NewReader(newBytes))
			resp.ContentLength = int64(len(newBytes))
			resp.Header.Set("Content-Length", strconv.Itoa(len(newBytes)))
			resp.Header.Del("ETag")
			resp.Header.Set("Cache-Control", "no-store")
		}
		return nil
	}
	proxy.ServeHTTP(c.Writer, c.Request)
}
