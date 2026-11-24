/* Webuddy v2 Knowledge Base & Code Graph Workbench */
(function () {
  const kbState = {
    repos: [],
    repoPath: localStorage.getItem('webuddy.kb.repoPath') || '',
    tab: 'blueprint', // blueprint | er | statecharts | docs | chain | impact | questions | flows | tables | evidence
    overview: null,
    statecharts: null,
    searchQuery: '',
    searchHits: null,
    docScopeFilter: 'all',
    docStatusFilter: 'all',
    docQuickFilter: '',
    selectedDoc: null,
    docHistory: [],
    docRawMode: false,
    docContent: null,
    docInlinePin: null,
    chainQuery: '',
    chainLoading: false,
    chainResult: null,
    impactTarget: '',
    impactDepth: 3,
    impactLoading: false,
    impactResult: null,
    questions: null,
    questionsLoading: false,
    newQuestionText: '',
    newQuestionPages: '',
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
    kbState.chainQuery = q;
    kbState.tab = 'chain';
    await runChain();
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

  async function runChainQuery() {
    if (!kbState.repoPath.trim() || !kbState.chainQuery.trim()) {
      notify('请先选择仓库并输入业务问题或需求描述');
      return;
    }
    kbState.chainLoading = true;
    render();
    try {
      kbState.chainResult = await reqJSON('/api/webuddy/kb/chain', {
        method: 'POST',
        body: JSON.stringify({ repoPath: kbState.repoPath.trim(), query: kbState.chainQuery.trim(), flows: 6 }),
      });
    } catch (err) {
      kbState.chainResult = { error: err.message };
    } finally {
      kbState.chainLoading = false;
      render();
    }
  }

  async function runImpactAnalysis() {
    if (!kbState.repoPath.trim() || !kbState.impactTarget.trim()) {
      notify('请先选择仓库并输入文件、表名、符号或接口关键词');
      return;
    }
    kbState.impactLoading = true;
    render();
    try {
      kbState.impactResult = await reqJSON('/api/webuddy/kb/impact', {
        method: 'POST',
        body: JSON.stringify({ repoPath: kbState.repoPath.trim(), target: kbState.impactTarget.trim(), depth: kbState.impactDepth || 3 }),
      });
    } catch (err) {
      kbState.impactResult = { error: err.message };
    } finally {
      kbState.impactLoading = false;
      render();
    }
  }

  async function loadQuestions() {
    if (!kbState.repoPath.trim()) {
      kbState.questions = [];
      return;
    }
    kbState.questionsLoading = true;
    render();
    try {
      const data = await reqJSON('/api/webuddy/kb/questions?repoPath=' + encodeURIComponent(kbState.repoPath.trim()));
      kbState.questions = data.questions || [];
    } catch (err) {
      notify(err.message);
      kbState.questions = [];
    } finally {
      kbState.questionsLoading = false;
      render();
    }
  }

  async function submitNewQuestion() {
    if (!kbState.repoPath.trim() || !kbState.newQuestionText.trim()) {
      notify('请输入待澄清问题内容');
      return;
    }
    try {
      const data = await reqJSON('/api/webuddy/kb/questions/ask', {
        method: 'POST',
        body: JSON.stringify({
          repoPath: kbState.repoPath.trim(),
          question: kbState.newQuestionText.trim(),
          pages: kbState.newQuestionPages.trim(),
        }),
      });
      kbState.questions = data.questions || [];
      kbState.newQuestionText = '';
      kbState.newQuestionPages = '';
      notify('已记录待澄清问题 #' + (data.id || ''));
    } catch (err) {
      notify(err.message);
    }
    render();
  }

  async function submitAnswerQuestion(num, text) {
    if (!kbState.repoPath.trim() || !num || !String(text || '').trim()) {
      notify('请输入回答内容');
      return;
    }
    try {
      const data = await reqJSON('/api/webuddy/kb/questions/answer', {
        method: 'POST',
        body: JSON.stringify({ repoPath: kbState.repoPath.trim(), num: Number(num), text: String(text).trim() }),
      });
      kbState.questions = data.questions || [];
      notify('已保存问题 #' + num + ' 的人工澄清结论');
    } catch (err) {
      notify(err.message);
    }
    render();
  }

  async function confirmCurrentPage(pageId) {
    if (!kbState.repoPath.trim() || !pageId) return;
    try {
      await reqJSON('/api/webuddy/kb/confirm-page', {
        method: 'POST',
        body: JSON.stringify({ repoPath: kbState.repoPath.trim(), pageId }),
      });
      notify('已人工确认知识页：' + pageId);
      const q = '?repoPath=' + encodeURIComponent(kbState.repoPath.trim());
      kbState.overview = await reqJSON('/api/webuddy/kb/overview' + q).catch(() => kbState.overview);
    } catch (err) {
      notify(err.message);
    }
    render();
  }

  function renderDocsTab(ov) {
    const builtins = (ov && ov.builtinDocs) || [];
    const allPages = (ov && ov.pages) || [];
    const specialDocs = (ov && ov.specialDocs) || [];
    const hits = kbState.searchHits;

    const filteredPages = allPages.filter(p => {
      if (kbState.docScopeFilter !== 'all' && (p.scope || '') !== kbState.docScopeFilter) return false;
      if (kbState.docStatusFilter !== 'all' && (p.status || '待确认') !== kbState.docStatusFilter) return false;
      if (kbState.docQuickFilter.trim()) {
        const q = kbState.docQuickFilter.trim().toLowerCase();
        const hay = ((p.title || '') + ' ' + (p.name || '') + ' ' + (p.path || '') + ' ' + (p.summary || '')).toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });

    const specialPills = specialDocs.length
      ? '<div style="margin-bottom:12px"><div class="wb-meta" style="margin-bottom:6px"><strong>项目核心文档</strong></div><div style="display:flex;flex-wrap:wrap;gap:6px">' +
          specialDocs.map(sd =>
            '<button type="button" class="button ghost small" data-kb-doc="' + esc(sd.path) + '">' + esc(sd.title) + '</button>'
          ).join('') +
        '</div></div>'
      : '';

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
        specialPills +
        '<div class="wb-item-top" style="margin-bottom:6px"><h3 style="margin:0">项目知识页 (' + filteredPages.length + '/' + (ov.pageCount || allPages.length) + ')</h3></div>' +
        '<div class="wb-toolbar" style="margin-bottom:8px;gap:6px">' +
          '<select id="wbDocScopeFilter" style="min-width:96px">' +
            '<option value="all"' + (kbState.docScopeFilter === 'all' ? ' selected' : '') + '>全部类型</option>' +
            '<option value="业务域"' + (kbState.docScopeFilter === '业务域' ? ' selected' : '') + '>业务域</option>' +
            '<option value="业务流程"' + (kbState.docScopeFilter === '业务流程' ? ' selected' : '') + '>业务流程</option>' +
            '<option value="数据实体"' + (kbState.docScopeFilter === '数据实体' ? ' selected' : '') + '>数据实体</option>' +
            '<option value="总览与规范"' + (kbState.docScopeFilter === '总览与规范' ? ' selected' : '') + '>总览与规范</option>' +
          '</select>' +
          '<select id="wbDocStatusFilter" style="min-width:90px">' +
            '<option value="all"' + (kbState.docStatusFilter === 'all' ? ' selected' : '') + '>全部状态</option>' +
            '<option value="已确认"' + (kbState.docStatusFilter === '已确认' ? ' selected' : '') + '>已确认</option>' +
            '<option value="待确认"' + (kbState.docStatusFilter === '待确认' ? ' selected' : '') + '>待确认</option>' +
          '</select>' +
          '<input type="search" id="wbDocQuickFilter" value="' + esc(kbState.docQuickFilter) + '" placeholder="过滤标题/路径…" style="flex:1;min-width:100px">' +
        '</div>' +
        '<div class="wb-list" style="max-height:32vh;margin-bottom:14px">' +
        (filteredPages.length === 0 ? '<div class="wb-empty" style="padding:12px">当前筛选下无知识页</div>' : filteredPages.slice(0, 120).map(p =>
          '<button type="button" class="wb-list-item ' + (p.path === kbState.selectedDoc ? 'active' : '') + '" data-kb-doc="' + esc(p.path) + '">' +
            '<div class="wb-item-top"><span class="wb-item-title">' + esc(p.title || p.name) + '</span><span class="wb-badge ' + (p.status === '已确认' ? 'pass' : 'info') + '">' + esc(p.status || '待确认') + '</span></div>' +
            '<div class="wb-meta"><span>' + esc(p.scope || '项目') + '</span><span>' + esc(p.path) + '</span></div>' +
          '</button>'
        ).join('')) +
        '</div>' +
        '<h3>内置领域知识库 (' + builtins.length + ')</h3>' +
        '<div class="wb-list" style="max-height:26vh">' +
        builtins.map(d =>
          '<button type="button" class="wb-list-item ' + (d.path === kbState.selectedDoc ? 'active' : '') + '" data-kb-doc="' + esc(d.path) + '">' +
            '<div class="wb-item-top"><span class="wb-item-title">' + esc(d.title) + '</span><span class="wb-badge pass">内置</span></div>' +
            '<div class="wb-meta"><span>' + esc(d.name) + '</span><span>' + d.size + ' 字符</span></div>' +
          '</button>'
        ).join('') +
        '</div>';
    }

    const doc = kbState.docContent;
    const currentPageMeta = doc ? allPages.find(p => p.path === doc.path) : null;
    const canConfirm = Boolean(currentPageMeta && currentPageMeta.pageId && currentPageMeta.status !== '已确认');
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
              '<h3 style="margin:0">' + esc((currentPageMeta && currentPageMeta.title) || doc.path) + '</h3>' +
              (currentPageMeta ? '<span class="wb-badge ' + (currentPageMeta.status === '已确认' ? 'pass' : 'info') + '">' + esc(currentPageMeta.status || '待确认') + '</span>' : '') +
            '</div>' +
            '<div class="wb-actions">' +
              (canConfirm ? '<button type="button" class="button primary small" id="wbConfirmPageBtn" data-page-id="' + esc(currentPageMeta.pageId) + '">✓ 人工确认此页知识</button>' : '') +
              '<span class="wb-meta">共 ' + doc.totalLines + ' 行</span>' +
              '<button type="button" class="button ghost small" id="wbToggleDocRaw">' + (kbState.docRawMode ? '富文本阅读' : '原始 Markdown') + '</button>' +
            '</div>' +
          '</div>' +
          pinBox +
          (kbState.docRawMode
            ? '<pre class="wb-code">' + esc(doc.content) + '</pre>'
            : renderWikiMarkdown(doc.content, doc.path)) +
        '</div>'
      : '<div class="wb-panel wb-empty">点击左侧核心文档、项目知识页或内置领域知识（国密改造、数据库方言、信创 .NET 等）进行富文本 Wiki 阅读，支持人工确认、相对链接跳转与源码行号直跳。</div>';

    return '<div class="wb-split"><div class="wb-panel">' + leftHTML + '</div><div>' + rightHTML + '</div></div>';
  }

  function renderChainTab() {
    const res = kbState.chainResult;
    const inlinePin = kbState.docInlinePin;
    const pinBox = inlinePin
      ? '<div class="wb-finding-card" style="margin-bottom:12px;border-color:rgba(16,185,129,0.4)">' +
          '<div class="wb-item-top"><strong>⌖ 源码定位：' + esc(inlinePin.where) + '</strong><button type="button" class="button ghost small" id="wbCloseDocPin">关闭</button></div>' +
          (inlinePin.error ? '<span class="wb-badge fail">' + esc(inlinePin.error) + '</span>' : '<pre class="wb-code" style="max-height:220px">' + esc(inlinePin.snippet || '') + '</pre>') +
        '</div>'
      : '';
    let chainResultHTML = '<div class="wb-empty" style="padding:20px">输入一句业务问题，AI 结合代码图谱 3 句话直接回答，并附可点击的源码位置。</div>';
    if (res && res.error) {
      chainResultHTML = '<div class="wb-badge fail">查询失败：' + esc(res.error) + '</div>';
    } else if (res) {
      const cites = Array.isArray(res.aiCitations) ? res.aiCitations : [];
      const aiBox = res.aiAnswer
        ? '<div class="wb-finding-card" style="margin-bottom:12px;border-color:rgba(37,99,235,0.4);background:var(--panel)">' +
            '<div class="wb-item-top" style="margin-bottom:6px">' +
              '<strong style="font-size:14px">✨ AI 回答</strong>' +
              '<span class="wb-meta" style="font-size:12px">' + esc(res.aiModel || 'AI') + (res.aiLatencyMs ? ' · ' + (res.aiLatencyMs / 1000).toFixed(1) + 's' : '') + '</span>' +
            '</div>' +
            '<div style="font-size:14px;line-height:1.65;white-space:pre-line">' + esc(res.aiAnswer) + '</div>' +
            (cites.length
              ? '<div style="margin-top:8px;display:flex;flex-wrap:wrap;gap:6px;align-items:center">' +
                  '<span class="wb-meta" style="font-size:12px">对应源码：</span>' +
                  cites.map(c => '<button type="button" class="wb-cite-pill" data-kb-cite="' + esc(c) + '">⌖ ' + esc(c) + '</button>').join('') +
                '</div>'
              : '') +
          '</div>'
        : (res.aiError ? '<div class="wb-badge unknown" style="margin-bottom:10px">AI 总结暂不可用（' + esc(res.aiError) + '），可展开下方原始证据查看。</div>' : '');
      const rawDetails =
        '<details style="margin-top:8px">' +
          '<summary style="cursor:pointer;font-size:12.5px;color:var(--muted,#6b7280)">查看原始知识库证据（供核查 / AI 用）</summary>' +
          '<div style="margin-top:10px">' + renderWikiMarkdown(res.markdown || '未找到匹配链路', '.tmh/knowledge/index.md') + '</div>' +
        '</details>';
      chainResultHTML = aiBox + rawDetails;
    }
    return (
      '<div class="wb-panel">' +
        '<h3>问业务链路</h3>' +
        '<p class="wb-subtitle" style="margin-bottom:12px">输入一句业务问题，AI 结合图谱 3 句话直答并定位源码。</p>' +
        '<div class="wb-toolbar" style="margin-bottom:12px">' +
          '<input type="text" id="wbChainQueryInput" value="' + esc(kbState.chainQuery) + '" placeholder="例如：怎么验证输入格式是对的、审批驳回怎么走…" style="min-width:320px;flex:1">' +
          '<button type="button" class="button primary" id="wbRunChainBtn" ' + (kbState.chainLoading ? 'disabled' : '') + '>' + (kbState.chainLoading ? 'AI 思考中…' : '✨ 问 AI') + '</button>' +
        '</div>' +
        pinBox +
        chainResultHTML +
      '</div>'
    );
  }

  function renderImpactTab() {
    const res = kbState.impactResult;
    let outHTML = '<div class="wb-empty" style="padding:20px">输入要修改的文件名、Controller、表名或接口关键词（例如 <code>ApprovaController</code> 或 <code>approva</code>），立即评估改动波及的业务域、流程与数据表。</div>';
    if (res && res.error) {
      outHTML = '<div class="wb-badge fail">分析失败：' + esc(res.error) + '</div>';
    } else if (res) {
      const doms = res.affectedDomains || [];
      const flows = res.affectedFlows || [];
      const tbls = res.affectedTables || [];
      const ci = res.codeImpact || {};
      const callers = Array.isArray(ci.callers) ? ci.callers : (Array.isArray(ci.files) ? ci.files : []);
      outHTML =
        (res.aiSummary
          ? '<div class="wb-finding-card" style="margin-bottom:12px;border-color:rgba(37,99,235,0.4);background:var(--panel)">' +
              '<div class="wb-item-top" style="margin-bottom:4px"><strong style="font-size:13.5px">✨ AI 影响结论</strong><span class="wb-meta" style="font-size:12px">' + esc(res.aiModel || 'AI') + '</span></div>' +
              '<div style="font-size:13.5px;line-height:1.6;white-space:pre-line">' + esc(res.aiSummary) + '</div>' +
            '</div>'
          : '') +
        '<div class="wb-kpi-grid" style="margin-bottom:14px">' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">波及业务域</span><span class="wb-kpi-val">' + doms.length + '</span></div>' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">受影响业务流程</span><span class="wb-kpi-val">' + flows.length + '</span></div>' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">关联数据表</span><span class="wb-kpi-val">' + tbls.length + '</span></div>' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">调用链波及文件</span><span class="wb-kpi-val">' + callers.length + '</span></div>' +
        '</div>' +
        (doms.length ? '<div style="margin-bottom:12px;display:flex;flex-wrap:wrap;gap:6px">' + doms.map(d => '<span class="wb-badge info">' + esc(d.domain) + ' (' + d.flowCount + ' 流程)</span>').join('') + '</div>' : '') +
        (flows.length
          ? '<table class="wb-table" style="margin-bottom:14px"><thead><tr><th>受影响流程</th><th>所属域</th><th>涉及数据表</th><th>源码定位</th></tr></thead><tbody>' +
              flows.map(f =>
                '<tr><td><b>' + esc(f.name) + '</b></td><td><span class="wb-badge info">' + esc(f.domain) + '</span></td><td><code>' + esc((f.tables || []).join(', ') || '—') + '</code></td><td>' +
                (f.file && f.line ? '<button type="button" class="wb-cite-pill" data-impact-cite="' + esc(f.file + ':' + f.line) + '">⌖ ' + esc(f.file + ':' + f.line) + '</button>' : '—') +
                '</td></tr>'
              ).join('') +
            '</tbody></table>'
          : '<div class="wb-meta" style="margin-bottom:12px">未在业务流程图中直接命中该关键词。</div>') +
        (callers.length
          ? '<div><h4 style="margin:0 0 8px">调用链波及明细</h4><pre class="wb-code" style="max-height:240px">' + esc(JSON.stringify(callers.slice(0, 40), null, 2)) + '</pre></div>'
          : '');
    }
    return (
      '<div class="wb-panel">' +
        '<h3>查改动影响</h3>' +
        '<p class="wb-subtitle" style="margin-bottom:12px">输入要改的文件、接口或表名，先看会波及哪些业务。</p>' +
        '<div class="wb-toolbar" style="margin-bottom:12px">' +
          '<input type="text" id="wbImpactTargetInput" value="' + esc(kbState.impactTarget) + '" placeholder="例如：ApprovaController、approva、t_bill…" style="min-width:320px;flex:1">' +
          '<select id="wbImpactDepthSelect" style="width:110px">' +
            [1, 2, 3, 4].map(d => '<option value="' + d + '"' + (Number(kbState.impactDepth) === d ? ' selected' : '') + '>深度 ' + d + ' 层</option>').join('') +
          '</select>' +
          '<button type="button" class="button primary" id="wbRunImpactBtn" ' + (kbState.impactLoading ? 'disabled' : '') + '>' + (kbState.impactLoading ? '正在分析影响面…' : '分析变更影响') + '</button>' +
        '</div>' +
        outHTML +
      '</div>'
    );
  }

  function renderQuestionsTab() {
    const qs = kbState.questions;
    const listHTML = kbState.questionsLoading
      ? '<div class="wb-empty">正在加载待澄清问题池…</div>'
      : !qs || qs.length === 0
        ? '<div class="wb-empty" style="padding:18px">当前项目暂无待澄清问题，可在上方新增一条供团队或业务方确认。</div>'
        : '<div style="display:flex;flex-direction:column;gap:10px">' + qs.map((q, idx) => {
            const num = q.id || q.num || (idx + 1);
            const status = String(q.status || 'open');
            const answered = status === 'answered' || status === '已回答' || status === '已确认' || Boolean(q.answer);
            return (
              '<div class="wb-finding-card">' +
                '<div class="wb-item-top"><strong>#' + esc(num) + ' · ' + esc(q.question || q.text || '') + '</strong><span class="wb-badge ' + (answered ? 'pass' : 'fail') + '">' + (answered ? '已澄清' : '待澄清') + '</span></div>' +
                '<div class="wb-meta">' +
                  (q.pages ? '<span>关联页：<code>' + esc(Array.isArray(q.pages) ? q.pages.join(', ') : q.pages) + '</code></span>' : '') +
                  (q.who ? '<span>提问人：' + esc(q.who) + '</span>' : '') +
                '</div>' +
                (q.answer ? '<div style="font-size:13px;margin-top:4px"><strong>澄清结论：</strong>' + esc(q.answer) + '</div>' : '') +
                (!answered
                  ? '<div class="wb-toolbar" style="margin-top:8px"><input type="text" data-answer-input="' + esc(num) + '" placeholder="输入业务确认结论…" style="flex:1"><button type="button" class="button primary small" data-answer-submit="' + esc(num) + '">保存人工回答</button></div>'
                  : '') +
              '</div>'
            );
          }).join('') + '</div>';

    return (
      '<div style="display:flex;flex-direction:column;gap:14px">' +
        '<div class="wb-panel">' +
          '<h3>新增待澄清问题（写入 .tmh/knowledge/open-questions.md）</h3>' +
          '<div class="wb-toolbar">' +
            '<input type="text" id="wbNewQText" value="' + esc(kbState.newQuestionText) + '" placeholder="例如：跨租户审批驳回后是否需要重置财务凭证状态？" style="flex:2;min-width:260px">' +
            '<input type="text" id="wbNewQPages" value="' + esc(kbState.newQuestionPages) + '" placeholder="关联知识页（可选，逗号分隔）" style="flex:1;min-width:160px">' +
            '<button type="button" class="button primary" id="wbAskQBtn">记录问题</button>' +
          '</div>' +
        '</div>' +
        '<div class="wb-panel">' +
          '<div class="wb-item-top" style="margin-bottom:10px"><h3 style="margin:0">项目待澄清问题池 (' + ((qs || []).length) + ')</h3><button type="button" class="button ghost small" id="wbReloadQBtn">刷新</button></div>' +
          listHTML +
        '</div>' +
      '</div>'
    );
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
    else if (kbState.tab === 'chain') bodyHTML = renderChainTab();
    else if (kbState.tab === 'impact') bodyHTML = renderImpactTab();
    else if (kbState.tab === 'questions') bodyHTML = renderQuestionsTab();
    else if (kbState.tab === 'flows') bodyHTML = renderFlowsTab(ov);
    else if (kbState.tab === 'tables') bodyHTML = renderTablesTab(ov);
    else if (kbState.tab === 'evidence') bodyHTML = renderEvidenceTab();
    else bodyHTML = renderDocsTab(ov);

    const ps = ov.pageStats || {};
    const confirmedPages = ps.confirmed || 0;
    const totalPages = ov.pageCount || (ov.pages || []).length;
    const openQs = ov.openQuestionCount || 0;

    const repoSelectHTML =
      '<select id="wbKbProjectSelect" aria-label="选择已导入项目仓库" style="min-width:240px">' +
        (kbState.repos.length === 0
          ? '<option value="">尚未导入项目，点击右侧选择目录…</option>'
          : kbState.repos.map(r => {
              const tag = !r.exists ? '（目录不存在）' : r.hasGraph ? ' · 已建图谱' : r.hasKb ? ' · 有知识页' : ' · 待扫描';
              return '<option value="' + esc(r.localPath) + '" ' + (r.localPath === kbState.repoPath ? 'selected' : '') + '>' + esc(r.projectName + tag) + '</option>';
            }).join('')) +
      '</select>';

    const extraTabs = [
      { id: 'statecharts', label: '业务状态图' },
      { id: 'er', label: '数据 ER 图' },
      { id: 'flows', label: '业务流程列表' },
      { id: 'tables', label: '数据表清单' },
      { id: 'questions', label: '待澄清问题' + (openQs ? ' (' + openQs + ')' : '') },
      { id: 'docs', label: '知识文档（AI/参考）' },
      { id: 'evidence', label: '源码行号核验' },
    ];
    const activeExtra = extraTabs.find(t => t.id === kbState.tab);

    root.innerHTML =
      '<div class="wb-page">' +
        '<div class="wb-header">' +
          '<div><h1>知识库</h1><p class="wb-subtitle">看架构、查改动影响、问业务逻辑，3 秒定位到代码。</p></div>' +
          '<div class="wb-toolbar">' +
            repoSelectHTML +
            '<button type="button" class="button primary" id="wbKbScanBtn" ' + (kbState.scanning ? 'disabled' : '') + '>' + (kbState.scanning ? '扫描中…' : (ov.built ? '更新图谱' : '⚡ 扫描图谱')) + '</button>' +
            '<input type="search" id="wbKbSearchInput" value="' + esc(kbState.searchQuery) + '" placeholder="搜业务 / 流程 / 表名…">' +
            '<button type="button" class="button ghost" id="wbKbSearchBtn">搜索</button>' +
          '</div>' +
        '</div>' +
        '<div class="wb-tabs" style="align-items:center">' +
          '<button type="button" class="wb-tab ' + (kbState.tab === 'blueprint' ? 'active' : '') + '" data-kb-tab="blueprint">🗺️ 看架构</button>' +
          '<button type="button" class="wb-tab ' + (kbState.tab === 'impact' ? 'active' : '') + '" data-kb-tab="impact">💥 查改动影响</button>' +
          '<button type="button" class="wb-tab ' + (kbState.tab === 'chain' ? 'active' : '') + '" data-kb-tab="chain">❓ 问业务链路</button>' +
          '<select id="wbKbMoreTabSelect" aria-label="更多视角" style="margin-left:auto;min-width:150px;height:32px;font-size:13px">' +
            '<option value="">更多视角 ▾</option>' +
            extraTabs.map(t => '<option value="' + t.id + '"' + (activeExtra && activeExtra.id === t.id ? ' selected' : '') + '>' + esc(t.label) + '</option>').join('') +
          '</select>' +
          '<span class="wb-meta" style="font-size:12px">' + ((ov.domains || []).length) + ' 域 · ' + (ov.flowCount || 0) + ' 流程 · ' + (ov.tableCount || 0) + ' 表</span>' +
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
      btn.addEventListener('click', () => {
        kbState.tab = btn.dataset.kbTab;
        if (kbState.tab === 'questions' && kbState.questions === null) loadQuestions();
        else render();
      });
    });
    root.querySelector('#wbKbMoreTabSelect')?.addEventListener('change', e => {
      if (!e.target.value) return;
      kbState.tab = e.target.value;
      if (kbState.tab === 'questions' && kbState.questions === null) loadQuestions();
      else render();
    });
    root.querySelector('#wbDocScopeFilter')?.addEventListener('change', e => { kbState.docScopeFilter = e.target.value; render(); });
    root.querySelector('#wbDocStatusFilter')?.addEventListener('change', e => { kbState.docStatusFilter = e.target.value; render(); });
    root.querySelector('#wbDocQuickFilter')?.addEventListener('input', e => { kbState.docQuickFilter = e.target.value; render(); });
    root.querySelector('#wbConfirmPageBtn')?.addEventListener('click', e => confirmCurrentPage(e.currentTarget.dataset.pageId));

    const chainInput = root.querySelector('#wbChainQueryInput');
    if (chainInput) {
      chainInput.addEventListener('input', e => { kbState.chainQuery = e.target.value; });
      chainInput.addEventListener('keydown', e => { if (e.key === 'Enter') runChainQuery(); });
    }
    root.querySelector('#wbRunChainBtn')?.addEventListener('click', runChainQuery);

    const impInput = root.querySelector('#wbImpactTargetInput');
    if (impInput) {
      impInput.addEventListener('input', e => { kbState.impactTarget = e.target.value; });
      impInput.addEventListener('keydown', e => { if (e.key === 'Enter') runImpactAnalysis(); });
    }
    root.querySelector('#wbImpactDepthSelect')?.addEventListener('change', e => { kbState.impactDepth = Number(e.target.value) || 3; });
    root.querySelector('#wbRunImpactBtn')?.addEventListener('click', runImpactAnalysis);
    root.querySelectorAll('[data-impact-cite]').forEach(btn => {
      btn.addEventListener('click', () => {
        kbState.evidenceRef = btn.dataset.impactCite;
        kbState.tab = 'evidence';
        verifyEvidence();
      });
    });

    root.querySelector('#wbNewQText')?.addEventListener('input', e => { kbState.newQuestionText = e.target.value; });
    root.querySelector('#wbNewQPages')?.addEventListener('input', e => { kbState.newQuestionPages = e.target.value; });
    root.querySelector('#wbAskQBtn')?.addEventListener('click', submitNewQuestion);
    root.querySelector('#wbReloadQBtn')?.addEventListener('click', loadQuestions);
    root.querySelectorAll('[data-answer-submit]').forEach(btn => {
      btn.addEventListener('click', () => {
        const num = btn.dataset.answerSubmit;
        const inp = root.querySelector('[data-answer-input="' + num + '"]');
        submitAnswerQuestion(num, inp ? inp.value : '');
      });
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
