/* Webuddy v2 Knowledge Base & Code Graph Workbench */
(function () {
  const kbState = {
    repos: [],
    repoPath: localStorage.getItem('webuddy.kb.repoPath') || '',
    tab: 'blueprint', // blueprint | er | statecharts | docs | flows | tables | evidence
    overview: null,
    statecharts: null,
    searchQuery: '',
    searchHits: null,
    selectedDoc: null,
    docHistory: [],
    docRawMode: false,
    docContent: null,
    docInlinePin: null,
    evidenceRef: '',
    evidenceResult: null,
    scanning: false,
    picking: false,
  };

  function esc(v) {
    return String(v == null ? '' : v)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  async function reqJSON(url, opts) {
    const res = await fetch(url, Object.assign({ headers: { 'Content-Type': 'application/json' } }, opts));
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || ('请求失败: ' + res.status));
    return data;
  }

  function notify(msg) {
    if (typeof window.toast === 'function') window.toast(msg);
    else console.log('[webuddy-kb]', msg);
  }

  async function pickAndImportLocalRepo() {
    kbState.picking = true;
    render();
    try {
      const picked = await reqJSON('/api/webuddy/kb/pick-folder', { method: 'POST', body: '{}' });
      if (picked.cancelled || !picked.path) return;
      const imported = await reqJSON('/api/webuddy/kb/import-local', {
        method: 'POST',
        body: JSON.stringify({ localPath: picked.path, name: picked.name }),
      });
      const r = imported.repo;
      if (r && r.localPath) {
        kbState.repoPath = r.localPath;
        localStorage.setItem('webuddy.kb.repoPath', kbState.repoPath);
        notify((imported.reused ? '已切换到本地项目：' : '已导入本地项目：') + r.projectName);
        if (typeof window.boot === 'function') {
          try { await window.boot(); } catch (_) {}
        }
        await loadKnowledgeBaseView();
      }
    } catch (err) {
      notify(err.message);
    } finally {
      kbState.picking = false;
      render();
    }
  }

  async function loadKnowledgeBaseView() {
    const root = document.getElementById('knowledgebaseRoot');
    if (!root) return;
    try {
      const repoData = await reqJSON('/api/webuddy/kb/repos');
      kbState.repos = repoData.repos || [];
    } catch (_) {
      kbState.repos = [];
    }
    const activePid = (window.state && window.state.projectFilter) || '';
    const matchActive = activePid && kbState.repos.find(r => r.projectId === activePid && r.exists);
    const currentValid = kbState.repoPath && kbState.repos.some(r => r.localPath === kbState.repoPath && r.exists);
    if (matchActive) {
      kbState.repoPath = matchActive.localPath;
      localStorage.setItem('webuddy.kb.repoPath', kbState.repoPath);
    } else if (!currentValid && kbState.repos.length > 0) {
      const preferred = kbState.repos.find(r => r.exists && r.hasGraph) || kbState.repos.find(r => r.exists) || kbState.repos[0];
      kbState.repoPath = preferred.localPath;
      localStorage.setItem('webuddy.kb.repoPath', kbState.repoPath);
    }
    try {
      const q = kbState.repoPath ? ('?repoPath=' + encodeURIComponent(kbState.repoPath)) : '';
      const [ov, sc] = await Promise.all([
        reqJSON('/api/webuddy/kb/overview' + q),
        reqJSON('/api/webuddy/kb/statecharts' + q).catch(() => ({ charts: [] })),
      ]);
      kbState.overview = ov;
      kbState.statecharts = sc;
    } catch (err) {
      notify(err.message);
    }
    render();
  }

  async function scanRepo() {
    if (!kbState.repoPath.trim()) {
      notify('请先输入本地代码仓库路径');
      return;
    }
    kbState.scanning = true;
    render();
    try {
      localStorage.setItem('webuddy.kb.repoPath', kbState.repoPath.trim());
      kbState.overview = await reqJSON('/api/webuddy/kb/scan', {
        method: 'POST',
        body: JSON.stringify({ repoPath: kbState.repoPath.trim() }),
      });
      notify('代码图谱扫描完成：发现 ' + (kbState.overview.flowCount || 0) + ' 条业务流程，' + (kbState.overview.tableCount || 0) + ' 张数据表');
    } catch (err) {
      notify(err.message);
    } finally {
      kbState.scanning = false;
      render();
    }
  }

  async function runSearch() {
    const q = kbState.searchQuery.trim();
    if (!q) {
      kbState.searchHits = null;
      render();
      return;
    }
    try {
      const data = await reqJSON('/api/webuddy/kb/search', {
        method: 'POST',
        body: JSON.stringify({ repoPath: kbState.repoPath.trim(), query: q, limit: 30 }),
      });
      kbState.searchHits = data.hits || [];
      kbState.tab = 'docs';
    } catch (err) {
      notify(err.message);
    }
    render();
  }

  function resolveRelativePath(basePath, rel) {
    if (!rel) return '';
    if (rel.startsWith('builtin:') || rel.startsWith('.tmh/')) return rel;
    const baseParts = String(basePath || '').split('/');
    baseParts.pop();
    const parts = baseParts.concat(String(rel).split('/'));
    const out = [];
    for (const p of parts) {
      if (!p || p === '.') continue;
      if (p === '..') out.pop();
      else out.push(p);
    }
    return out.join('/');
  }

  async function openDoc(path, pushHistory) {
    if (pushHistory !== false && kbState.selectedDoc && kbState.selectedDoc !== path) {
      kbState.docHistory.push(kbState.selectedDoc);
      if (kbState.docHistory.length > 20) kbState.docHistory.shift();
    }
    kbState.selectedDoc = path;
    kbState.docInlinePin = null;
    try {
      const params = new URLSearchParams({ path });
      if (kbState.repoPath.trim()) params.set('repoPath', kbState.repoPath.trim());
      kbState.docContent = await reqJSON('/api/webuddy/kb/file?' + params.toString());
    } catch (err) {
      notify(err.message);
    }
    render();
  }

  function formatInlineMd(raw, currentPath) {
    let s = esc(raw);
    // Markdown links [label](target)
    s = s.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_, label, href) => {
      if (/^https?:\/\//i.test(href)) {
        return '<a class="wb-wiki-link" href="' + href + '" target="_blank" rel="noopener">' + label + '</a>';
      }
      const target = resolveRelativePath(currentPath, href);
      return '<a href="#" class="wb-wiki-link" data-wiki-open="' + esc(target) + '">' + label + '</a>';
    });
    // Inline code with optional file:line citation detection
    s = s.replace(/`([^`]+)`/g, (_, code) => {
      if (/^[A-Za-z0-9_./-]+\.[A-Za-z0-9]{1,8}:\d+(?:-\d+)?$/.test(code)) {
        return '<button type="button" class="wb-cite-pill" data-wiki-cite="' + code + '">⌖ ' + code + '</button>';
      }
      return '<code>' + code + '</code>';
    });
    s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
    return s;
  }

  function renderWikiMarkdown(content, currentPath) {
    let text = String(content || '').replace(/\r\n/g, '\n');
    let fmHTML = '';
    const fmMatch = text.match(/^---\n([\s\S]*?)\n---\n?/);
    if (fmMatch) {
      text = text.slice(fmMatch[0].length);
      const meta = {};
      fmMatch[1].split('\n').forEach(ln => {
        const idx = ln.indexOf(':');
        if (idx > 0) meta[ln.slice(0, idx).trim()] = ln.slice(idx + 1).trim();
      });
      fmHTML =
        '<div class="wb-md-frontmatter">' +
          '<div class="wb-item-top">' +
            '<strong>' + esc(meta.title || meta.id || currentPath) + '</strong>' +
            '<div class="wb-actions">' +
              (meta.status ? '<span class="wb-badge ' + (meta.status === '已确认' ? 'pass' : 'info') + '">' + esc(meta.status) + '</span>' : '') +
              (meta.updated ? '<span class="wb-meta">更新于 ' + esc(meta.updated) + '</span>' : '') +
            '</div>' +
          '</div>' +
          (meta.summary ? '<div style="font-size:13px">' + esc(meta.summary) + '</div>' : '') +
        '</div>';
    }
    // Strip HTML comments like <!-- tmh:facts:start ... -->
    text = text.replace(/<!--[\s\S]*?-->/g, '');

    const lines = text.split('\n');
    const out = [fmHTML, '<div class="wb-md">'];
    let i = 0;
    while (i < lines.length) {
      const line = lines[i];
      if (/^```/.test(line.trim())) {
        const lang = line.trim().slice(3).trim().toLowerCase();
        i++;
        const buf = [];
        while (i < lines.length && !/^```/.test(lines[i].trim())) {
          buf.push(lines[i]);
          i++;
        }
        i++;
        out.push('<pre class="wb-code" style="margin:10px 0">' + (lang ? '<div class="wb-meta" style="margin-bottom:4px">' + esc(lang) + '</div>' : '') + esc(buf.join('\n')) + '</pre>');
        continue;
      }
      // Table block
      if (line.trim().startsWith('|') && i + 1 < lines.length && /^\|[\s:|-]+\|/.test(lines[i + 1].trim())) {
        const parseRow = r => r.trim().replace(/^\|/, '').replace(/\|$/, '').split('|').map(c => c.trim());
        const headers = parseRow(line);
        i += 2;
        const rows = [];
        while (i < lines.length && lines[i].trim().startsWith('|')) {
          rows.push(parseRow(lines[i]));
          i++;
        }
        out.push(
          '<div style="overflow-x:auto;margin:10px 0"><table class="wb-table"><thead><tr>' +
          headers.map(h => '<th>' + formatInlineMd(h, currentPath) + '</th>').join('') +
          '</tr></thead><tbody>' +
          rows.map(r => '<tr>' + r.map(c => '<td>' + formatInlineMd(c, currentPath) + '</td>').join('') + '</tr>').join('') +
          '</tbody></table></div>'
        );
        continue;
      }
      if (/^###\s+/.test(line)) { out.push('<h3>' + formatInlineMd(line.replace(/^###\s+/, ''), currentPath) + '</h3>'); i++; continue; }
      if (/^##\s+/.test(line)) { out.push('<h2>' + formatInlineMd(line.replace(/^##\s+/, ''), currentPath) + '</h2>'); i++; continue; }
      if (/^#\s+/.test(line)) { out.push('<h1>' + formatInlineMd(line.replace(/^#\s+/, ''), currentPath) + '</h1>'); i++; continue; }
      if (/^>\s?/.test(line)) {
        const qbuf = [];
        while (i < lines.length && /^>\s?/.test(lines[i])) {
          qbuf.push(formatInlineMd(lines[i].replace(/^>\s?/, ''), currentPath));
          i++;
        }
        out.push('<blockquote>' + qbuf.join('<br>') + '</blockquote>');
        continue;
      }
      if (/^[-*]\s+/.test(line)) {
        const lbuf = [];
        while (i < lines.length && /^[-*]\s+/.test(lines[i])) {
          lbuf.push('<li>' + formatInlineMd(lines[i].replace(/^[-*]\s+/, ''), currentPath) + '</li>');
          i++;
        }
        out.push('<ul>' + lbuf.join('') + '</ul>');
        continue;
      }
      if (!line.trim()) { i++; continue; }
      out.push('<p>' + formatInlineMd(line, currentPath) + '</p>');
      i++;
    }
    out.push('</div>');
    return out.join('');
  }

  async function verifyEvidence() {
    if (!kbState.repoPath.trim() || !kbState.evidenceRef.trim()) {
      notify('请输入仓库路径和源码引用（例如 go.mod:1-5）');
      return;
    }
    try {
      kbState.evidenceResult = await reqJSON('/api/webuddy/kb/verify-evidence', {
        method: 'POST',
        body: JSON.stringify({ repoPath: kbState.repoPath.trim(), ref: kbState.evidenceRef.trim() }),
      });
    } catch (err) {
      kbState.evidenceResult = { valid: false, error: err.message };
    }
    render();
  }

  function renderDocsTab(ov) {
    const builtins = (ov && ov.builtinDocs) || [];
    const pages = (ov && ov.pages) || [];
    const hits = kbState.searchHits;

    let leftHTML = '';
    if (hits) {
      leftHTML =
        '<h3>搜索结果 (' + hits.length + ') <button type="button" class="button ghost small" id="wbClearSearch">清除</button></h3>' +
        '<div class="wb-list">' +
        (hits.length === 0 ? '<div class="wb-empty">未找到匹配结果</div>' : hits.map(h =>
          '<button type="button" class="wb-list-item ' + (h.path === kbState.selectedDoc ? 'active' : '') + '" data-kb-doc="' + esc(h.path) + '">' +
            '<div class="wb-item-top"><span class="wb-item-title">' + esc(h.title || h.path) + '</span><span class="wb-badge info">' + esc(h.source) + '</span></div>' +
            '<div class="wb-meta"><span>' + esc(h.path) + (h.line ? ':' + h.line : '') + '</span></div>' +
            (h.snippet ? '<div style="font-size:12px;color:var(--muted,#64748b)">' + esc(h.snippet) + '</div>' : '') +
          '</button>'
        ).join('')) +
        '</div>';
    } else {
      leftHTML =
        '<h3>项目知识页 (' + (ov.pageCount || pages.length) + ')</h3>' +
        '<div class="wb-list" style="max-height:28vh;margin-bottom:14px">' +
        (pages.length === 0 ? '<div class="wb-empty" style="padding:12px">扫描仓库后展示 .tmh/knowledge 知识页</div>' : pages.map(p =>
          '<button type="button" class="wb-list-item ' + (p.path === kbState.selectedDoc ? 'active' : '') + '" data-kb-doc="' + esc(p.path) + '">' +
            '<div class="wb-item-top"><span class="wb-item-title">' + esc(p.name) + '</span><span class="wb-badge info">项目</span></div>' +
            '<div class="wb-meta"><span>' + esc(p.path) + '</span></div>' +
          '</button>'
        ).join('')) +
        '</div>' +
        '<h3>内置领域知识库 (' + builtins.length + ')</h3>' +
        '<div class="wb-list" style="max-height:36vh">' +
        builtins.map(d =>
          '<button type="button" class="wb-list-item ' + (d.path === kbState.selectedDoc ? 'active' : '') + '" data-kb-doc="' + esc(d.path) + '">' +
            '<div class="wb-item-top"><span class="wb-item-title">' + esc(d.title) + '</span><span class="wb-badge pass">内置</span></div>' +
            '<div class="wb-meta"><span>' + esc(d.name) + '</span><span>' + d.size + ' 字符</span></div>' +
          '</button>'
        ).join('') +
        '</div>';
    }

    const doc = kbState.docContent;
    const inlinePin = kbState.docInlinePin;
    const pinBox = inlinePin
      ? '<div class="wb-finding-card" style="margin-bottom:12px;border-color:rgba(16,185,129,0.4)">' +
          '<div class="wb-item-top"><strong>⌖ 源码定位：' + esc(inlinePin.where) + '</strong><button type="button" class="button ghost small" id="wbCloseDocPin">关闭</button></div>' +
          (inlinePin.error
            ? '<span class="wb-badge fail">' + esc(inlinePin.error) + '</span>'
            : '<pre class="wb-code" style="max-height:220px">' + esc(inlinePin.snippet || '') + '</pre>') +
        '</div>'
      : '';

    const rightHTML = doc
      ? '<div class="wb-panel">' +
          '<div class="wb-item-top" style="margin-bottom:12px">' +
            '<div class="wb-actions">' +
              (kbState.docHistory.length ? '<button type="button" class="button ghost small" id="wbDocBackBtn">← 返回上一页</button>' : '') +
              '<h3 style="margin:0">' + esc(doc.path) + '</h3>' +
            '</div>' +
            '<div class="wb-actions">' +
              '<span class="wb-meta">共 ' + doc.totalLines + ' 行</span>' +
              '<button type="button" class="button ghost small" id="wbToggleDocRaw">' + (kbState.docRawMode ? '富文本阅读' : '原始 Markdown') + '</button>' +
            '</div>' +
          '</div>' +
          pinBox +
          (kbState.docRawMode
            ? '<pre class="wb-code">' + esc(doc.content) + '</pre>'
            : renderWikiMarkdown(doc.content, doc.path)) +
        '</div>'
      : '<div class="wb-panel wb-empty">点击左侧项目知识页或内置领域知识文档（国密改造、数据库方言、信创 .NET 等）进行富文本 Wiki 阅读，支持内部相对链接跳转与源码行号直跳。</div>';

    return '<div class="wb-split"><div class="wb-panel">' + leftHTML + '</div><div>' + rightHTML + '</div></div>';
  }

  function renderFlowsTab(ov) {
    const flows = (ov && ov.flows) || [];
    if (!ov || !ov.built) return '<div class="wb-panel wb-empty">请先在上方输入本地代码仓库路径并点击「扫描代码图谱」。</div>';
    const rows = flows.map(f =>
      '<tr><td><b>' + esc(f.name) + '</b></td><td><span class="wb-badge info">' + esc(f.domainTitle || f.domain) + '</span></td><td><code>' + esc(f.trigger || f.kind) + '</code></td><td><code>' + esc(f.file) + (f.line ? ':' + f.line : '') + '</code></td></tr>'
    ).join('');
    return (
      '<div class="wb-panel">' +
        '<h3>业务域与流程清单 (' + flows.length + ')</h3>' +
        '<table class="wb-table"><thead><tr><th>业务流程</th><th>所属域</th><th>入口 / 触发器</th><th>源码证据</th></tr></thead><tbody>' +
        (rows || '<tr><td colspan="4" class="wb-empty">未解析到业务流程</td></tr>') +
        '</tbody></table>' +
      '</div>'
    );
  }

  function renderTablesTab(ov) {
    if (!ov || !ov.built) return '<div class="wb-panel wb-empty">请先扫描代码图谱以提取数据表、外键 ER 关系与 CRUD 矩阵。</div>';
    const tables = ov.tables || [];
    const er = ov.erEdges || [];
    const crud = ov.crudMatrix || [];
    return (
      '<div style="display:flex;flex-direction:column;gap:14px">' +
        '<div class="wb-panel">' +
          '<h3>数据表与列结构 (' + tables.length + ')</h3>' +
          '<table class="wb-table"><thead><tr><th>表名</th><th>实体</th><th>列数</th><th>引用流程数</th><th>定义位置</th></tr></thead><tbody>' +
          (tables.map(t => '<tr><td><b>' + esc(t.name) + '</b></td><td>' + esc(t.entity || '—') + '</td><td>' + (t.columns || []).length + '</td><td>' + (t.users || 0) + '</td><td><code>' + esc(t.file) + (t.line ? ':' + t.line : '') + '</code></td></tr>').join('') || '<tr><td colspan="5" class="wb-empty">未解析到数据表</td></tr>') +
          '</tbody></table>' +
        '</div>' +
        '<div class="wb-panel">' +
          '<h3>外键 ER 关系 (' + er.length + ') · CRUD 矩阵 (' + crud.length + ')</h3>' +
          (er.length ? '<div class="wb-meta" style="margin-bottom:10px">' + er.map(e => '<span class="wb-badge info">' + esc(e.fromTable + '.' + e.fromColumn + ' → ' + e.toTable + '.' + e.toColumn) + '</span>').join('') + '</div>' : '') +
          '<table class="wb-table"><thead><tr><th>业务流程</th><th>所属域</th><th>表读写操作 (CRUD)</th></tr></thead><tbody>' +
          (crud.map(c => '<tr><td><b>' + esc(c.flowName) + '</b></td><td>' + esc(c.domain) + '</td><td><code>' + esc(JSON.stringify(c.tables)) + '</code></td></tr>').join('') || '<tr><td colspan="3" class="wb-empty">暂无 CRUD 映射</td></tr>') +
          '</tbody></table>' +
        '</div>' +
      '</div>'
    );
  }

  function renderEvidenceTab() {
    const res = kbState.evidenceResult;
    return (
      '<div class="wb-panel">' +
        '<h3>源码证据防幻觉核验（kb_structure.evidence）</h3>' +
        '<p class="wb-subtitle" style="margin-bottom:12px">校验知识库或 AI 回答中的 <code>文件:行号</code> / <code>文件:起行-止行</code> 引用是否真实存在于仓库且行号未越界。</p>' +
        '<div class="wb-toolbar" style="margin-bottom:12px">' +
          '<input type="text" id="wbEvidenceRefInput" value="' + esc(kbState.evidenceRef) + '" placeholder="例如：go.mod:1-5 或 internal/httpapi/server.go:1-20" style="min-width:360px;flex:1">' +
          '<button type="button" class="button primary" id="wbVerifyEvidenceBtn">核验源码证据</button>' +
        '</div>' +
        (res ? (res.valid
          ? '<div><div class="wb-meta" style="margin-bottom:8px"><span class="wb-badge pass">证据有效</span><code>' + esc(res.file) + ':' + res.line + '-' + res.end + '</code></div><pre class="wb-code">' + esc(res.snippet) + '</pre></div>'
          : '<div class="wb-badge fail">核验失败：' + esc(res.error) + '</div>') : '') +
      '</div>'
    );
  }

  function render() {
    const root = document.getElementById('knowledgebaseRoot');
    if (!root) return;
    const ov = kbState.overview || {};

    let bodyHTML = '';
    if (kbState.tab === 'blueprint') bodyHTML = '<div id="wbBlueprintMount"></div>';
    else if (kbState.tab === 'er') bodyHTML = '<div id="wbERMount"></div>';
    else if (kbState.tab === 'statecharts') bodyHTML = '<div id="wbStatechartsMount"></div>';
    else if (kbState.tab === 'flows') bodyHTML = renderFlowsTab(ov);
    else if (kbState.tab === 'tables') bodyHTML = renderTablesTab(ov);
    else if (kbState.tab === 'evidence') bodyHTML = renderEvidenceTab();
    else bodyHTML = renderDocsTab(ov);

    const repoSelectHTML =
      '<select id="wbKbProjectSelect" aria-label="选择已导入项目仓库" style="min-width:240px">' +
        (kbState.repos.length === 0
          ? '<option value="">尚未导入项目，点击右侧选择目录…</option>'
          : kbState.repos.map(r => {
              const tag = !r.exists ? '（目录不存在）' : r.hasGraph ? ' · 已建图谱' : r.hasKb ? ' · 有知识页' : ' · 待扫描';
              return '<option value="' + esc(r.localPath) + '" ' + (r.localPath === kbState.repoPath ? 'selected' : '') + '>' + esc(r.projectName + tag) + '</option>';
            }).join('')) +
      '</select>';

    root.innerHTML =
      '<div class="wb-page">' +
        '<div class="wb-header">' +
          '<div><h1>知识库与代码图谱 · Webuddy Core</h1><p class="wb-subtitle">代码图谱解析（业务域 / 流程 / ER / CRUD）+ 源码证据防幻觉核验 + 内置领域知识库 · Streamable HTTP MCP: <code>' + esc(location.origin + '/api/webuddy/kb/mcp') + '</code> <button type="button" class="button ghost small" id="wbCopyMcpBtn">复制 MCP 地址</button></p></div>' +
          '<div class="wb-toolbar">' +
            repoSelectHTML +
            '<button type="button" class="button ghost" id="wbKbPickFolderBtn" ' + (kbState.picking ? 'disabled' : '') + '>' + (kbState.picking ? '正在选择目录…' : '+ 选择本地文件夹导入') + '</button>' +
            '<button type="button" class="button primary" id="wbKbScanBtn" ' + (kbState.scanning ? 'disabled' : '') + '>' + (kbState.scanning ? '正在扫描…' : (ov.built ? '更新代码图谱' : '扫描代码图谱')) + '</button>' +
            '<input type="search" id="wbKbSearchInput" value="' + esc(kbState.searchQuery) + '" placeholder="搜索知识库 / 流程 / 表 / SM4…">' +
            '<button type="button" class="button ghost" id="wbKbSearchBtn">搜索</button>' +
          '</div>' +
        '</div>' +
        '<div class="wb-kpi-grid">' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">图谱状态</span><span class="wb-kpi-val">' + (ov.built ? '已构建' : '未扫描') + '</span></div>' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">业务域</span><span class="wb-kpi-val">' + ((ov.domains || []).length) + '</span></div>' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">业务流程</span><span class="wb-kpi-val">' + (ov.flowCount || 0) + '</span></div>' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">数据表 / 项目知识页</span><span class="wb-kpi-val">' + (ov.tableCount || 0) + ' / ' + (ov.pageCount || (ov.pages || []).length) + '</span></div>' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">内置领域知识</span><span class="wb-kpi-val">' + ((ov.builtinDocs || []).length) + '</span></div>' +
        '</div>' +
        '<div class="wb-tabs">' +
          '<button type="button" class="wb-tab ' + (kbState.tab === 'blueprint' ? 'active' : '') + '" data-kb-tab="blueprint">空间架构蓝图</button>' +
          '<button type="button" class="wb-tab ' + (kbState.tab === 'er' ? 'active' : '') + '" data-kb-tab="er">ER 实体关系图</button>' +
          '<button type="button" class="wb-tab ' + (kbState.tab === 'statecharts' ? 'active' : '') + '" data-kb-tab="statecharts">业务状态图与源码定位</button>' +
          '<button type="button" class="wb-tab ' + (kbState.tab === 'docs' ? 'active' : '') + '" data-kb-tab="docs">知识导览与搜索</button>' +
          '<button type="button" class="wb-tab ' + (kbState.tab === 'flows' ? 'active' : '') + '" data-kb-tab="flows">业务域与流程</button>' +
          '<button type="button" class="wb-tab ' + (kbState.tab === 'tables' ? 'active' : '') + '" data-kb-tab="tables">数据表与 CRUD 矩阵</button>' +
          '<button type="button" class="wb-tab ' + (kbState.tab === 'evidence' ? 'active' : '') + '" data-kb-tab="evidence">源码证据核验</button>' +
        '</div>' +
        bodyHTML +
      '</div>';

    const projSel = root.querySelector('#wbKbProjectSelect');
    if (projSel) {
      projSel.addEventListener('change', e => {
        if (e.target.value) {
          kbState.repoPath = e.target.value;
          localStorage.setItem('webuddy.kb.repoPath', kbState.repoPath);
          kbState.searchHits = null;
          kbState.docContent = null;
          loadKnowledgeBaseView();
        }
      });
    }
    const repoInput = root.querySelector('#wbKbRepoInput');
    if (repoInput) repoInput.addEventListener('input', e => { kbState.repoPath = e.target.value; });
    const searchInput = root.querySelector('#wbKbSearchInput');
    if (searchInput) {
      searchInput.addEventListener('input', e => { kbState.searchQuery = e.target.value; });
      searchInput.addEventListener('keydown', e => { if (e.key === 'Enter') runSearch(); });
    }
    root.querySelector('#wbKbPickFolderBtn')?.addEventListener('click', pickAndImportLocalRepo);
    root.querySelector('#wbKbScanBtn')?.addEventListener('click', scanRepo);
    root.querySelector('#wbCopyMcpBtn')?.addEventListener('click', () => {
      const url = location.origin + '/api/webuddy/kb/mcp';
      navigator.clipboard?.writeText(url).then(() => notify('已复制 MCP 地址：' + url)).catch(() => notify(url));
    });
    root.querySelector('#wbKbSearchBtn')?.addEventListener('click', runSearch);
    root.querySelector('#wbClearSearch')?.addEventListener('click', () => { kbState.searchQuery = ''; kbState.searchHits = null; render(); });
    root.querySelectorAll('[data-kb-tab]').forEach(btn => {
      btn.addEventListener('click', () => { kbState.tab = btn.dataset.kbTab; render(); });
    });
    root.querySelectorAll('[data-kb-doc]').forEach(btn => {
      btn.addEventListener('click', () => openDoc(btn.dataset.kbDoc, true));
    });
    root.querySelector('#wbDocBackBtn')?.addEventListener('click', () => {
      const prev = kbState.docHistory.pop();
      if (prev) openDoc(prev, false);
    });
    root.querySelector('#wbToggleDocRaw')?.addEventListener('click', () => {
      kbState.docRawMode = !kbState.docRawMode;
      render();
    });
    root.querySelector('#wbCloseDocPin')?.addEventListener('click', () => {
      kbState.docInlinePin = null;
      render();
    });
    root.querySelectorAll('[data-wiki-open]').forEach(link => {
      link.addEventListener('click', e => {
        e.preventDefault();
        openDoc(link.dataset.wikiOpen, true);
      });
    });
    root.querySelectorAll('[data-wiki-cite]').forEach(btn => {
      btn.addEventListener('click', async () => {
        const ref = btn.dataset.wikiCite;
        if (!kbState.repoPath.trim()) { notify('请先选择仓库路径'); return; }
        try {
          kbState.docInlinePin = await reqJSON('/api/webuddy/kb/verify-evidence', {
            method: 'POST',
            body: JSON.stringify({ repoPath: kbState.repoPath.trim(), ref }),
          });
        } catch (err) {
          kbState.docInlinePin = { where: ref, error: err.message };
        }
        render();
      });
    });
    const evInput = root.querySelector('#wbEvidenceRefInput');
    if (evInput) evInput.addEventListener('input', e => { kbState.evidenceRef = e.target.value; });
    root.querySelector('#wbVerifyEvidenceBtn')?.addEventListener('click', verifyEvidence);

    if (kbState.tab === 'blueprint' && window.WebuddyVisuals) {
      window.WebuddyVisuals.renderArchitectureBlueprint(root.querySelector('#wbBlueprintMount'), ov, {
        onCite: cite => {
          kbState.evidenceRef = cite;
          kbState.tab = 'evidence';
          verifyEvidence();
        },
      });
    }
    if (kbState.tab === 'er' && window.WebuddyVisuals) {
      window.WebuddyVisuals.renderERGraph(root.querySelector('#wbERMount'), ov, {
        onCite: cite => {
          kbState.evidenceRef = cite;
          kbState.tab = 'evidence';
          verifyEvidence();
        },
      });
    }
    if (kbState.tab === 'statecharts' && window.WebuddyVisuals) {
      window.WebuddyVisuals.renderStatechartsView(root.querySelector('#wbStatechartsMount'), kbState.statecharts, {
        repoPath: kbState.repoPath,
      });
    }
  }

  window.loadKnowledgeBaseView = loadKnowledgeBaseView;
})();
