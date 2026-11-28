package httpapi

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// autotestAllowLoopbackForTests allows 127.0.0.1 httptest servers during Go unit tests.
var autotestAllowLoopbackForTests atomic.Bool

func validateAutoTestTargetURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("目标 URL 不能为空")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("目标 URL 格式非法: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, errors.New("仅支持 http:// 或 https:// 协议的目标地址")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return nil, errors.New("目标 URL 缺少主机名")
	}
	// Block cloud metadata endpoints unconditionally
	if host == "169.254.169.254" || host == "100.100.100.200" || host == "metadata.google.internal" {
		return nil, errors.New("安全策略禁止访问云元数据地址")
	}
	if autotestAllowLoopbackForTests.Load() {
		return u, nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return nil, errors.New("安全策略禁止访问链路本地或未指定地址")
		}
		if ip.IsLoopback() || ip.IsPrivate() {
			port := u.Port()
			// Allow Webuddy local app preview / demo ports (18000-19999) if explicitly tested locally
			if port == "" || !strings.HasPrefix(port, "18") && !strings.HasPrefix(port, "19") {
				return nil, errors.New("安全策略禁止扫描内网私有端口，请使用公网域名或平台预览地址")
			}
		}
	} else if host == "localhost" {
		port := u.Port()
		if port == "" || (!strings.HasPrefix(port, "18") && !strings.HasPrefix(port, "19")) {
			return nil, errors.New("安全策略禁止直接访问非预览区的 localhost 端口")
		}
	}
	return u, nil
}

func resolveRelativeTargetURL(baseURL, stepTarget string) (string, error) {
	base, err := validateAutoTestTargetURL(baseURL)
	if err != nil {
		return "", err
	}
	st := strings.TrimSpace(stepTarget)
	if st == "" {
		return base.String(), nil
	}
	if strings.HasPrefix(st, "http://") || strings.HasPrefix(st, "https://") {
		u, err := validateAutoTestTargetURL(st)
		if err != nil {
			return "", err
		}
		return u.String(), nil
	}
	rel, err := url.Parse(st)
	if err != nil {
		return "", fmt.Errorf("步骤路径非法: %w", err)
	}
	resolved := base.ResolveReference(rel)
	if _, err := validateAutoTestTargetURL(resolved.String()); err != nil {
		return "", err
	}
	return resolved.String(), nil
}

func findLocalChromeBinary() string {
	if custom := strings.TrimSpace(os.Getenv("AGENTFLOW_CHROME_BIN")); custom != "" {
		if st, err := os.Stat(custom); err == nil && !st.IsDir() {
			return custom
		}
	}
	candidates := []string{
		"google-chrome",
		"google-chrome-stable",
		"chromium-browser",
		"chromium",
		"/usr/bin/google-chrome",
		"/usr/bin/chromium-browser",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	}
	for _, c := range candidates {
		if filepath.IsAbs(c) {
			if st, err := os.Stat(c); err == nil && !st.IsDir() {
				return c
			}
			continue
		}
		if p, err := exec.LookPath(c); err == nil && p != "" {
			return p
		}
	}
	return ""
}

// ---------- Lightweight Direct WebSocket CDP Session ----------

type autoCDPMessage struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type autoChromeSession struct {
	cmd         *exec.Cmd
	userDataDir string
	conn        *websocket.Conn
	writeMu     sync.Mutex
	mu          sync.Mutex
	pending     map[int64]chan autoCDPMessage
	nextID      atomic.Int64
	sessionID   string
	done        chan struct{}
	closeOnce   sync.Once
}

var devtoolsWSRegex = regexp.MustCompile(`DevTools listening on (ws://\S+)`)

func startHeadlessChromeSession(ctx context.Context) (*autoChromeSession, error) {
	bin := findLocalChromeBinary()
	if bin == "" {
		return nil, errors.New("未检测到本机 Chrome / Chromium 浏览器可执行文件")
	}
	userDir, err := os.MkdirTemp("", "webuddy-autotest-chrome-*")
	if err != nil {
		return nil, err
	}
	args := []string{
		"--headless=new",
		"--remote-debugging-port=0",
		"--no-sandbox",
		"--disable-setuid-sandbox",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--disable-background-networking",
		"--disable-sync",
		"--no-first-run",
		"--no-default-browser-check",
		"--hide-scrollbars",
		"--mute-audio",
		"--window-size=1440,900",
		"--user-data-dir=" + userDir,
		"about:blank",
	}
	cmd := exec.Command(bin, args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = os.RemoveAll(userDir)
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(userDir)
		return nil, fmt.Errorf("启动 Headless Chrome 失败: %w", err)
	}

	wsCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			line := sc.Text()
			if m := devtoolsWSRegex.FindStringSubmatch(line); len(m) == 2 {
				select {
				case wsCh <- m[1]:
				default:
				}
				// Continue draining stderr so Chrome never blocks on pipe buffer
				_, _ = io.Copy(io.Discard, stderr)
				return
			}
		}
		select {
		case errCh <- errors.New("Chrome 未输出 DevTools WebSocket 地址"):
		default:
		}
	}()

	var wsURL string
	select {
	case wsURL = <-wsCh:
	case e := <-errCh:
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.RemoveAll(userDir)
		return nil, e
	case <-time.After(22 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.RemoveAll(userDir)
		return nil, errors.New("等待 Headless Chrome 启动超时")
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.RemoveAll(userDir)
		return nil, ctx.Err()
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: 6 * time.Second,
		ReadBufferSize:   1024 * 1024,
		WriteBufferSize:  256 * 1024,
	}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.RemoveAll(userDir)
		return nil, fmt.Errorf("连接 Chrome CDP 失败: %w", err)
	}
	conn.SetReadLimit(32 << 20) // 32MB for base64 screenshots

	s := &autoChromeSession{
		cmd:         cmd,
		userDataDir: userDir,
		conn:        conn,
		pending:     make(map[int64]chan autoCDPMessage),
		done:        make(chan struct{}),
	}
	go s.readLoop()

	if err := s.attachPageTarget(ctx); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *autoChromeSession) readLoop() {
	defer s.Close()
	for {
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			return
		}
		var msg autoCDPMessage
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		if msg.ID != 0 {
			s.mu.Lock()
			ch := s.pending[msg.ID]
			delete(s.pending, msg.ID)
			s.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
		}
	}
}

func (s *autoChromeSession) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		if s.conn != nil {
			_ = s.conn.Close()
		}
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
			_, _ = s.cmd.Process.Wait()
		}
		if s.userDataDir != "" {
			_ = os.RemoveAll(s.userDataDir)
		}
	})
}

func (s *autoChromeSession) call(ctx context.Context, usePageSession bool, method string, params any, out any) error {
	id := s.nextID.Add(1)
	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		rawParams = b
	}
	sess := ""
	if usePageSession {
		sess = s.sessionID
	}
	payload, err := json.Marshal(autoCDPMessage{
		ID:        id,
		Method:    method,
		SessionID: sess,
		Params:    rawParams,
	})
	if err != nil {
		return err
	}
	ch := make(chan autoCDPMessage, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()

	s.writeMu.Lock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err = s.conn.WriteMessage(websocket.TextMessage, payload)
	s.writeMu.Unlock()
	if err != nil {
		return err
	}

	select {
	case msg := <-ch:
		if msg.Error != nil {
			return fmt.Errorf("%s: %s", method, msg.Error.Message)
		}
		if out != nil && len(msg.Result) > 0 {
			return json.Unmarshal(msg.Result, out)
		}
		return nil
	case <-s.done:
		return errors.New("Chrome CDP 会话已断开")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *autoChromeSession) attachPageTarget(ctx context.Context) error {
	var targetsResp struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
		} `json:"targetInfos"`
	}
	var targetID string
	if err := s.call(ctx, false, "Target.getTargets", nil, &targetsResp); err == nil {
		for _, t := range targetsResp.TargetInfos {
			if t.Type == "page" {
				targetID = t.TargetID
				break
			}
		}
	}
	if targetID == "" {
		var created struct {
			TargetID string `json:"targetId"`
		}
		if err := s.call(ctx, false, "Target.createTarget", map[string]any{"url": "about:blank"}, &created); err != nil {
			return err
		}
		targetID = created.TargetID
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := s.call(ctx, false, "Target.attachToTarget", map[string]any{
		"targetId": targetID,
		"flatten":  true,
	}, &attached); err != nil {
		return err
	}
	s.sessionID = attached.SessionID
	_ = s.call(ctx, true, "Page.enable", nil, nil)
	_ = s.call(ctx, true, "Runtime.enable", nil, nil)
	_ = s.call(ctx, true, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width":             1440,
		"height":            900,
		"deviceScaleFactor": 1,
		"mobile":            false,
	}, nil)
	// Auto-accept native JS alert/confirm dialogs so tests never hang
	_ = s.call(ctx, true, "Runtime.evaluate", map[string]any{
		"expression": `window.alert = function(){}; window.confirm = function(){ return true; };`,
	}, nil)
	return nil
}

func (s *autoChromeSession) evalJSON(ctx context.Context, jsExpr string, out any) error {
	var resp struct {
		Result struct {
			Type        string          `json:"type"`
			Value       json.RawMessage `json:"value"`
			Description string          `json:"description"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	err := s.call(ctx, true, "Runtime.evaluate", map[string]any{
		"expression":    jsExpr,
		"returnByValue": true,
		"awaitPromise":  true,
	}, &resp)
	if err != nil {
		return err
	}
	if resp.ExceptionDetails != nil {
		return fmt.Errorf("页面脚本执行异常: %s", resp.ExceptionDetails.Text)
	}
	if out != nil && len(resp.Result.Value) > 0 && string(resp.Result.Value) != "null" {
		return json.Unmarshal(resp.Result.Value, out)
	}
	return nil
}

func (s *autoChromeSession) navigateAndSettle(ctx context.Context, targetURL string) (string, error) {
	if err := s.call(ctx, true, "Page.navigate", map[string]any{"url": targetURL}, nil); err != nil {
		return "", err
	}
	// Poll readyState up to 8s
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		if err := s.evalJSON(ctx, `document.readyState`, &state); err == nil && (state == "complete" || state == "interactive") {
			if state == "complete" {
				break
			}
		}
		select {
		case <-time.After(150 * time.Millisecond):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	// Give async SPA fetch calls 450ms to render initial data
	select {
	case <-time.After(450 * time.Millisecond):
	case <-ctx.Done():
		return "", ctx.Err()
	}
	_ = s.evalJSON(ctx, `window.alert = function(){}; window.confirm = function(){ return true; }; true`, nil)
	var curURL string
	_ = s.evalJSON(ctx, `location.href`, &curURL)
	return curURL, nil
}

func (s *autoChromeSession) capturePNG(ctx context.Context, destPath string) error {
	var res struct {
		Data string `json:"data"`
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.call(cctx, true, "Page.captureScreenshot", map[string]any{
		"format": "png",
	}, &res); err != nil {
		return err
	}
	raw, err := base64.StdEncoding.DecodeString(res.Data)
	if err != nil || len(raw) == 0 {
		return errors.New("解码截图数据失败")
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(destPath, raw, 0o644)
}

// ---------- In-Browser DOM Helpers (JS Snippets) ----------

const jsElementHelpers = `
(function() {
  if (window.__wbAutoTest) return true;
  function norm(s) { return String(s || '').replace(/\s+/g, ' ').trim(); }
  function isVisible(el) {
    if (!el || !el.getBoundingClientRect) return false;
    const st = window.getComputedStyle(el);
    if (st.display === 'none' || st.visibility === 'hidden' || parseFloat(st.opacity || '1') === 0) return false;
    const r = el.getBoundingClientRect();
    return r.width > 1 && r.height > 1;
  }
  function cssEscapeIdent(s) {
    if (window.CSS && CSS.escape) return CSS.escape(s);
    return String(s).replace(/([^a-zA-Z0-9_-])/g, '\\$1');
  }
  function buildUniqueSelector(el) {
    if (!el || el.nodeType !== 1) return '';
    if (el.id) return '#' + cssEscapeIdent(el.id);
    for (const attr of ['data-testid', 'data-test', 'data-action', 'data-tab', 'data-view']) {
      const v = el.getAttribute(attr);
      if (v) {
        const cand = el.tagName.toLowerCase() + '[' + attr + '="' + v.replace(/"/g, '\\"') + '"]';
        if (document.querySelectorAll(cand).length === 1) return cand;
      }
    }
    if (el.tagName === 'A' && el.getAttribute('href')) {
      const href = el.getAttribute('href');
      const cand = 'a[href="' + href.replace(/"/g, '\\"') + '"]';
      if (document.querySelectorAll(cand).length === 1) return cand;
    }
    if (el.getAttribute('name')) {
      const cand = el.tagName.toLowerCase() + '[name="' + el.getAttribute('name').replace(/"/g, '\\"') + '"]';
      if (document.querySelectorAll(cand).length === 1) return cand;
    }
    const parts = [];
    let cur = el;
    while (cur && cur.nodeType === 1 && cur !== document.body && parts.length < 5) {
      let seg = cur.tagName.toLowerCase();
      if (cur.id) {
        parts.unshift('#' + cssEscapeIdent(cur.id));
        break;
      }
      const parent = cur.parentElement;
      if (parent) {
        const sameTag = Array.from(parent.children).filter(c => c.tagName === cur.tagName);
        if (sameTag.length > 1) {
          seg += ':nth-of-type(' + (sameTag.indexOf(cur) + 1) + ')';
        }
      }
      parts.unshift(seg);
      cur = parent;
    }
    return parts.join(' > ');
  }
  function extractParentContext(el) {
    let cur = el ? el.parentElement : null;
    const ctxBits = [];
    while (cur && cur !== document.body && ctxBits.length < 2) {
      const heading = cur.querySelector ? cur.querySelector('h1,h2,h3,h4,legend,caption,th') : null;
      if (heading && heading !== el) {
        const ht = norm(heading.innerText || heading.textContent).slice(0, 40);
        if (ht && !ctxBits.includes(ht)) ctxBits.push(ht);
      }
      const aria = norm(cur.getAttribute && (cur.getAttribute('aria-label') || cur.getAttribute('data-section')));
      if (aria && !ctxBits.includes(aria)) ctxBits.push(aria.slice(0, 40));
      cur = cur.parentElement;
    }
    if (ctxBits.length === 0 && el && el.parentElement) {
      ctxBits.push(norm(el.parentElement.innerText || '').slice(0, 48));
    }
    return ctxBits.join(' · ');
  }
  function extractDOMPath(el) {
    const tags = [];
    let cur = el;
    while (cur && cur.nodeType === 1 && tags.length < 6) {
      let t = cur.tagName.toLowerCase();
      const role = cur.getAttribute('role');
      if (role) t += '[' + role + ']';
      tags.unshift(t);
      if (cur === document.body) break;
      cur = cur.parentElement;
    }
    return tags.join(' > ');
  }
  function inferRole(el) {
    const explicit = norm(el.getAttribute('role'));
    if (explicit) return explicit;
    const tag = el.tagName.toLowerCase();
    if (tag === 'button' || (tag === 'input' && ['button','submit'].includes(el.type))) return 'button';
    if (tag === 'a') return 'link';
    if (tag === 'input' || tag === 'textarea' || tag === 'select') return 'input';
    if (/^h[1-6]$/.test(tag)) return 'heading';
    if (tag === 'th' || tag === 'td') return 'cell';
    return tag;
  }
  function extractPortrait(el) {
    const r = el.getBoundingClientRect();
    const fallbacks = [];
    const txt = norm(el.innerText || el.value || el.textContent).slice(0, 80);
    if (txt && txt.length <= 40) fallbacks.push('text=' + txt);
    if (el.getAttribute('href')) fallbacks.push('a[href="' + el.getAttribute('href').replace(/"/g, '\\"') + '"]');
    return {
      tag: el.tagName.toLowerCase(),
      role: inferRole(el),
      text: txt,
      ariaLabel: norm(el.getAttribute('aria-label')).slice(0, 60),
      placeholder: norm(el.getAttribute('placeholder')).slice(0, 60),
      nameAttr: norm(el.getAttribute('name')).slice(0, 40),
      href: norm(el.getAttribute('href')).slice(0, 80),
      elementId: norm(el.id),
      classes: Array.from(el.classList || []).slice(0, 5),
      parentContext: extractParentContext(el),
      domPath: extractDOMPath(el),
      pagePathPattern: location.pathname || '/',
      primarySelector: buildUniqueSelector(el),
      fallbackSelectors: fallbacks,
      rect: { x: Math.round(r.x), y: Math.round(r.y), width: Math.round(r.width), height: Math.round(r.height) }
    };
  }
  function locateBySelector(sel) {
    sel = String(sel || '').trim();
    if (!sel) return null;
    if (sel.startsWith('text=')) {
      const needle = norm(sel.slice(5));
      if (!needle) return null;
      const all = Array.from(document.querySelectorAll('button,a,[role="button"],[role="tab"],input[type="submit"],h1,h2,h3,h4,th,td,span,div,label'));
      let best = null;
      for (const el of all) {
        if (!isVisible(el)) continue;
        const t = norm(el.innerText || el.value || el.textContent);
        if (t === needle) return el;
        if (t.includes(needle) && (!best || t.length < norm(best.innerText).length)) best = el;
      }
      return best;
    }
    try {
      const list = Array.from(document.querySelectorAll(sel));
      for (const el of list) {
        if (isVisible(el)) return el;
      }
      return list[0] || null;
    } catch (e) {
      return null;
    }
  }
  window.__wbAutoTest = { norm, isVisible, buildUniqueSelector, extractPortrait, locateBySelector };
  return true;
})()
`

type domPageInspection struct {
	URL         string            `json:"url"`
	Pathname    string            `json:"pathname"`
	Title       string            `json:"title"`
	Headings    []string          `json:"headings"`
	BodySnippet string            `json:"bodySnippet"`
	Elements    []ElementPortrait `json:"elements"`
}

func (s *autoChromeSession) inspectCurrentDOM(ctx context.Context) (domPageInspection, error) {
	_ = s.evalJSON(ctx, jsElementHelpers, nil)
	expr := `
(() => {
  const H = window.__wbAutoTest;
  const headings = Array.from(document.querySelectorAll('h1,h2,h3,th')).filter(H.isVisible).map(e => H.norm(e.innerText)).filter(Boolean).slice(0, 18);
  const candidates = Array.from(document.querySelectorAll('button,a[href],[role="button"],[role="tab"],input:not([type="hidden"]),select,textarea,h1,h2,h3,.stat-value,.kpi-value'));
  const seen = new Set();
  const elements = [];
  for (const el of candidates) {
    if (!H.isVisible(el)) continue;
    const p = H.extractPortrait(el);
    if (!p.primarySelector || seen.has(p.primarySelector)) continue;
    if (!p.text && !p.placeholder && !p.ariaLabel && !p.href) continue;
    seen.add(p.primarySelector);
    elements.push(p);
    if (elements.length >= 45) break;
  }
  return {
    url: location.href,
    pathname: location.pathname || '/',
    title: document.title || '',
    headings,
    bodySnippet: H.norm(document.body ? document.body.innerText : '').slice(0, 1800),
    elements
  };
})()
`
	var snap domPageInspection
	err := s.evalJSON(ctx, expr, &snap)
	return snap, err
}

func (s *autoChromeSession) scanAndDismissPopup(ctx context.Context) (handled bool, detail string) {
	_ = s.evalJSON(ctx, jsElementHelpers, nil)
	expr := `
(() => {
  const H = window.__wbAutoTest;
  const dialogs = Array.from(document.querySelectorAll('dialog[open],[role="dialog"],[aria-modal="true"],.modal.open,.popup.open,.ant-modal-wrap,.el-dialog__wrapper')).filter(H.isVisible);
  if (dialogs.length === 0) return { handled: false, detail: '未检测到遮挡弹窗' };
  const allowWords = ['允许', '同意', '接受', '确认', '我知道了', '知道了'];
  const dismissWords = ['跳过', '关闭', '稍后', '以后再说', '取消', '×', '✕'];
  for (const dlg of dialogs) {
    const btns = Array.from(dlg.querySelectorAll('button,a,[role="button"],.close,.modal-close')).filter(H.isVisible);
    for (const b of btns) {
      const t = H.norm(b.innerText || b.getAttribute('aria-label') || '');
      if (allowWords.some(w => t === w || t.includes(w))) {
        b.click();
        return { handled: true, detail: '弹窗引擎识别权限/合规类弹窗，已点击「' + t + '」放行' };
      }
      if (dismissWords.some(w => t === w || t.includes(w))) {
        b.click();
        return { handled: true, detail: '弹窗引擎识别通知/引导类遮挡层，已点击「' + t + '」关闭' };
      }
    }
  }
  return { handled: false, detail: '检测到浮动层但无安全关闭按钮，保持原状避免误操作' };
})()
`
	var res struct {
		Handled bool   `json:"handled"`
		Detail  string `json:"detail"`
	}
	if err := s.evalJSON(ctx, expr, &res); err != nil {
		return false, "弹窗扫描跳过"
	}
	if res.Handled {
		time.Sleep(250 * time.Millisecond)
	}
	return res.Handled, res.Detail
}

func (s *autoChromeSession) performDOMStep(ctx context.Context, step AutoTestStep) (portrait *ElementPortrait, actual string, err error) {
	_ = s.evalJSON(ctx, jsElementHelpers, nil)
	selJSON, _ := json.Marshal(step.PrimarySelector)
	valJSON, _ := json.Marshal(step.Value)
	expJSON, _ := json.Marshal(step.Expected)

	switch step.Action {
	case "click":
		expr := fmt.Sprintf(`
(() => {
  const H = window.__wbAutoTest;
  const el = H.locateBySelector(%s);
  if (!el) return { ok: false, error: '未找到目标控件: ' + %s };
  el.scrollIntoView({ block: 'center', inline: 'center' });
  const p = H.extractPortrait(el);
  el.click();
  return { ok: true, portrait: p, actual: '已点击控件「' + (p.text || p.primarySelector) + '」' };
})()
`, string(selJSON), string(selJSON))
		var res struct {
			OK       bool             `json:"ok"`
			Error    string           `json:"error"`
			Portrait *ElementPortrait `json:"portrait"`
			Actual   string           `json:"actual"`
		}
		if err := s.evalJSON(ctx, expr, &res); err != nil {
			return nil, "", err
		}
		if !res.OK {
			return nil, "", errors.New(res.Error)
		}
		// Wait briefly for navigation or state update
		select {
		case <-time.After(380 * time.Millisecond):
		case <-ctx.Done():
			return res.Portrait, res.Actual, ctx.Err()
		}
		return res.Portrait, res.Actual, nil

	case "fill":
		expr := fmt.Sprintf(`
(() => {
  const H = window.__wbAutoTest;
  const el = H.locateBySelector(%s);
  if (!el) return { ok: false, error: '未找到输入框控件: ' + %s };
  el.scrollIntoView({ block: 'center', inline: 'center' });
  el.focus();
  const p = H.extractPortrait(el);
  const proto = el.tagName === 'TEXTAREA' ? window.HTMLTextAreaElement.prototype : window.HTMLInputElement.prototype;
  const desc = Object.getOwnPropertyDescriptor(proto, 'value');
  if (desc && desc.set) desc.set.call(el, %s); else el.value = %s;
  el.dispatchEvent(new Event('input', { bubbles: true }));
  el.dispatchEvent(new Event('change', { bubbles: true }));
  return { ok: true, portrait: p, actual: '已输入文本 (' + String(%s).length + ' 字符)' };
})()
`, string(selJSON), string(selJSON), string(valJSON), string(valJSON), string(valJSON))
		var res struct {
			OK       bool             `json:"ok"`
			Error    string           `json:"error"`
			Portrait *ElementPortrait `json:"portrait"`
			Actual   string           `json:"actual"`
		}
		if err := s.evalJSON(ctx, expr, &res); err != nil {
			return nil, "", err
		}
		if !res.OK {
			return nil, "", errors.New(res.Error)
		}
		return res.Portrait, res.Actual, nil

	case "assert_text":
		deadline := time.Now().Add(2600 * time.Millisecond)
		var lastErr string
		for {
			expr := fmt.Sprintf(`
(() => {
  const H = window.__wbAutoTest;
  const sel = %s;
  const want = %s;
  if (sel) {
    const el = H.locateBySelector(sel);
    if (!el) return { ok: false, selectorMissing: true, error: '未找到断言目标元素: ' + sel };
    const got = H.norm(el.innerText || el.value || el.textContent);
    const p = H.extractPortrait(el);
    if (!want || got.includes(want)) {
      return { ok: true, portrait: p, actual: '元素文本匹配「' + got.slice(0, 90) + '」' };
    }
    return { ok: false, selectorMissing: false, portrait: p, error: '业务断言失败：期望包含「' + want + '」，实际为「' + got.slice(0, 90) + '」' };
  }
  const body = H.norm(document.body ? document.body.innerText : '');
  if (!want || body.includes(want)) {
    return { ok: true, actual: '页面可见内容已包含「' + want + '」' };
  }
  return { ok: false, selectorMissing: false, error: '业务断言失败：页面可见文本未出现「' + want + '」（当前摘要：' + body.slice(0, 100) + '）' };
})()
`, string(selJSON), string(expJSON))
			var res struct {
				OK              bool             `json:"ok"`
				SelectorMissing bool             `json:"selectorMissing"`
				Error           string           `json:"error"`
				Portrait        *ElementPortrait `json:"portrait"`
				Actual          string           `json:"actual"`
			}
			if err := s.evalJSON(ctx, expr, &res); err != nil {
				return nil, "", err
			}
			if res.OK {
				return res.Portrait, res.Actual, nil
			}
			lastErr = res.Error
			if res.SelectorMissing || time.Now().After(deadline) {
				if res.SelectorMissing {
					return nil, "", &selectorNotFoundError{Selector: step.PrimarySelector, Msg: res.Error}
				}
				return res.Portrait, "", errors.New(lastErr)
			}
			select {
			case <-time.After(250 * time.Millisecond):
			case <-ctx.Done():
				return nil, "", ctx.Err()
			}
		}

	case "assert_url":
		var cur string
		if err := s.evalJSON(ctx, `location.href`, &cur); err != nil {
			return nil, "", err
		}
		want := strings.TrimSpace(step.Expected)
		if want != "" && !strings.Contains(cur, want) {
			return nil, cur, fmt.Errorf("URL 断言失败：期望包含 %q，当前实际 URL 为 %q", want, cur)
		}
		return nil, "当前 URL 匹配：" + cur, nil

	case "assert_api":
		apiPath := strings.TrimSpace(step.TargetURL)
		if apiPath == "" {
			apiPath = strings.TrimSpace(step.Value)
		}
		pathJSON, _ := json.Marshal(apiPath)
		expr := fmt.Sprintf(`
(async () => {
  const resp = await fetch(%s, { credentials: 'include', headers: { 'Accept': 'application/json, text/plain, */*' } });
  const text = await resp.text();
  return { status: resp.status, body: text.slice(0, 1500) };
})()
`, string(pathJSON))
		var res struct {
			Status int    `json:"status"`
			Body   string `json:"body"`
		}
		if err := s.evalJSON(ctx, expr, &res); err != nil {
			return nil, "", fmt.Errorf("接口请求执行失败: %w", err)
		}
		if res.Status < 200 || res.Status >= 400 {
			return nil, fmt.Sprintf("HTTP %d", res.Status), fmt.Errorf("接口状态码断言失败：%s 返回 HTTP %d", apiPath, res.Status)
		}
		want := strings.TrimSpace(step.Expected)
		if want != "" && !strings.Contains(res.Body, want) {
			return nil, fmt.Sprintf("HTTP %d (%s)", res.Status, clipRunText(res.Body, 90)), fmt.Errorf("接口响应断言失败：期望包含 %q，实际响应摘要 %q", want, clipRunText(res.Body, 110))
		}
		return nil, fmt.Sprintf("接口 %s 返回 HTTP %d 且命中契约特征 %q", apiPath, res.Status, want), nil
	}
	return nil, "", fmt.Errorf("不支持的步骤动作: %s", step.Action)
}

type selectorNotFoundError struct {
	Selector string
	Msg      string
}

func (e *selectorNotFoundError) Error() string { return e.Msg }

func isSelectorLocateFailure(step AutoTestStep, err error) bool {
	if err == nil {
		return false
	}
	var snf *selectorNotFoundError
	if errors.As(err, &snf) {
		return true
	}
	if step.Action == "click" || step.Action == "fill" {
		return strings.Contains(err.Error(), "未找到")
	}
	return false
}

// ---------- Lightweight HTTP+DOM Fallback (for CI environments without Chrome) ----------

type httpFallbackSession struct {
	client  *http.Client
	curURL  string
	curHTML string
}

func newHTTPFallbackSession() *httpFallbackSession {
	jar, _ := cookiejar.New(nil)
	return &httpFallbackSession{
		client: &http.Client{
			Jar:     jar,
			Timeout: 10 * time.Second,
		},
	}
}

var (
	htmlTagStripRe = regexp.MustCompile(`(?s)<script.*?</script>|<style.*?</style>|<[^>]+>`)
	htmlIDAttrRe   = regexp.MustCompile(`(?i)<([a-z0-9]+)([^>]*?)>(.*?)</([a-z0-9]+)>`)
	attrExtractRe  = regexp.MustCompile(`(?i)([a-z0-9_-]+)\s*=\s*["']([^"']*)["']`)
)

func (h *httpFallbackSession) navigate(ctx context.Context, targetURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	h.curURL = resp.Request.URL.String()
	h.curHTML = string(raw)
	if resp.StatusCode >= 500 {
		return h.curURL, fmt.Errorf("目标页面返回 HTTP %d", resp.StatusCode)
	}
	return h.curURL, nil
}

func (h *httpFallbackSession) visibleText() string {
	txt := htmlTagStripRe.ReplaceAllString(h.curHTML, " ")
	return strings.Join(strings.Fields(txt), " ")
}

func (h *httpFallbackSession) inspectElements() domPageInspection {
	u, _ := url.Parse(h.curURL)
	pathname := "/"
	if u != nil && u.Path != "" {
		pathname = u.Path
	}
	var elems []ElementPortrait
	matches := htmlIDAttrRe.FindAllStringSubmatch(h.curHTML, 80)
	for _, m := range matches {
		tag := strings.ToLower(m[1])
		if tag != "button" && tag != "a" && tag != "h1" && tag != "h2" && tag != "h3" && tag != "span" && tag != "div" {
			continue
		}
		attrs := map[string]string{}
		for _, am := range attrExtractRe.FindAllStringSubmatch(m[2], -1) {
			attrs[strings.ToLower(am[1])] = am[2]
		}
		inner := strings.Join(strings.Fields(htmlTagStripRe.ReplaceAllString(m[3], " ")), " ")
		if inner == "" && attrs["aria-label"] == "" {
			continue
		}
		sel := ""
		if id := attrs["id"]; id != "" {
			sel = "#" + id
		} else if dt := attrs["data-testid"]; dt != "" {
			sel = tag + `[data-testid="` + dt + `"]`
		} else if href := attrs["href"]; href != "" && tag == "a" {
			sel = `a[href="` + href + `"]`
		} else if inner != "" {
			sel = "text=" + inner
		}
		if sel == "" {
			continue
		}
		role := attrs["role"]
		if role == "" {
			if tag == "a" {
				role = "link"
			} else if tag == "button" {
				role = "button"
			} else {
				role = tag
			}
		}
		var classes []string
		if cls := attrs["class"]; cls != "" {
			classes = strings.Fields(cls)
		}
		elems = append(elems, ElementPortrait{
			Tag:             tag,
			Role:            role,
			Text:            clipRunText(inner, 80),
			AriaLabel:       attrs["aria-label"],
			Href:            attrs["href"],
			ElementID:       attrs["id"],
			Classes:         classes,
			ParentContext:   attrs["data-section"],
			DOMPath:         "body > " + tag,
			PagePathPattern: pathname,
			PrimarySelector: sel,
		})
	}
	return domPageInspection{
		URL:         h.curURL,
		Pathname:    pathname,
		BodySnippet: clipRunText(h.visibleText(), 1500),
		Elements:    elems,
	}
}
