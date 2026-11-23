/* Webuddy 2.0 Interactive Onboarding & 3-Minute Usage Guide */
(function () {
  const obState = {
    executors: [],
    runners: [],
    repos: [],
    loading: false,
  };

  function esc(v) {
    return String(v == null ? '' : v)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  function notify(msg) {
    if (typeof window.toast === 'function') window.toast(msg);
  }

  async function loadOnboardingView() {
    const root = document.getElementById('onboardingRoot');
    if (!root) return;
    obState.loading = true;
    render();
    try {
      const [exRes, runRes, repoRes] = await Promise.all([
        fetch('/api/codereview/executors').then(r => r.ok ? r.json() : { executors: [] }).catch(() => ({ executors: [] })),
        fetch('/api/runners').then(r => r.ok ? r.json() : []).catch(() => []),
        fetch('/api/webuddy/kb/repos').then(r => r.ok ? r.json() : { repos: [] }).catch(() => ({ repos: [] })),
      ]);
      obState.executors = exRes.executors || [];
      obState.runners = Array.isArray(runRes) ? runRes : [];
      obState.repos = repoRes.repos || [];
    } finally {
      obState.loading = false;
      render();
    }
  }

  function render() {
    const root = document.getElementById('onboardingRoot');
    if (!root) return;

    const installed = obState.executors.filter(e => e.available);
    const onlineRunners = obState.runners.filter(r => r.status === 'online');
    const repos = obState.repos;
    const mcpUrl = location.origin + '/api/webuddy/kb/mcp';
    const mcpSnippet = JSON.stringify({ mcpServers: { 'webuddy-kb': { url: mcpUrl } } }, null, 2);

    root.innerHTML =
      '<div class="wb-page">' +
        '<div class="wb-header">' +
          '<div><h1>Webuddy 2.0 · 3 分钟上手指引</h1><p class="wb-subtitle">实时检查你的本机 CLI、项目代码图谱、全量代码审查与 MCP 接入状态</p></div>' +
          '<div class="wb-actions">' +
            '<a class="button ghost" href="/landing" target="_blank" rel="noopener">打开产品官网 ↗</a>' +
            '<button type="button" class="button primary" id="wbObRefresh">刷新状态</button>' +
          '</div>' +
        '</div>' +

        '<div class="wb-kpi-grid">' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">本机已安装 CLI</span><span class="wb-kpi-val">' + installed.length + '</span></div>' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">在线执行机器</span><span class="wb-kpi-val">' + onlineRunners.length + '</span></div>' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">已绑定本地项目</span><span class="wb-kpi-val">' + repos.length + '</span></div>' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">内置知识库 MCP</span><span class="wb-kpi-val">就绪</span></div>' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">产品官网</span><span class="wb-kpi-val"><a href="/landing" target="_blank" style="font-size:15px">/landing ↗</a></span></div>' +
        '</div>' +

        '<div class="wb-panel">' +
          '<h3>步骤 1 · 确认本机 CLI 与执行机（免填 Key 复用订阅）</h3>' +
          '<p class="wb-subtitle" style="margin-bottom:10px">Webuddy 支持自动扫描本机已登录的 Codex / Claude Code / Cursor，同时也保留了手动 API Key 模式。</p>' +
          '<div class="wb-meta" style="margin-bottom:12px">' +
            (installed.length
              ? installed.map(e => '<span class="wb-badge ' + (e.loggedIn ? 'pass' : 'info') + '">' + esc(e.name) + (e.loggedIn ? ' · 已登录' : ' · 已安装') + '</span>').join('')
              : '<span class="wb-badge unknown">未检测到本机 CLI</span>') +
            (onlineRunners.length
              ? '<span class="wb-badge pass">在线执行机：' + esc(onlineRunners.map(r => r.name).join('、')) + '</span>'
              : '<span class="wb-badge unknown">暂无在线执行机</span>') +
          '</div>' +
          '<div class="wb-actions">' +
            '<button type="button" class="button ghost" data-ob-jump="settings">前往个人配置（查看自动扫描 / 填 API Key）</button>' +
            '<button type="button" class="button ghost" data-ob-jump="runners">管理执行机器</button>' +
          '</div>' +
        '</div>' +

        '<div class="wb-panel">' +
          '<h3>步骤 2 · 打开知识库与空间架构图谱</h3>' +
          '<p class="wb-subtitle" style="margin-bottom:10px">支持空间架构蓝图、ER 实体关系图、业务状态图与源码精准定位、富文本 Wiki 互跳与防幻觉源码核验。</p>' +
          '<div class="wb-meta" style="margin-bottom:12px">' +
            (repos.length
              ? repos.map(r => '<span class="wb-badge ' + (r.hasGraph ? 'pass' : 'info') + '">' + esc(r.projectName + (r.hasGraph ? ' · 已建图谱' : ' · 待扫描')) + '</span>').join('')
              : '<span class="wb-badge unknown">尚未导入本地项目</span>') +
          '</div>' +
          '<div class="wb-actions"><button type="button" class="button primary" data-ob-jump="knowledgebase">打开知识库与架构蓝图 →</button><button type="button" class="button ghost" id="wbObPickRepo">+ 选择本地文件夹导入</button></div>' +
        '</div>' +

        '<div class="wb-panel">' +
          '<h3>步骤 3 · 发起 14k 全量分批独立代码审查</h3>' +
          '<p class="wb-subtitle" style="margin-bottom:10px">自动按文件与 14,000 字符上限分批、校验 Writer ≠ Reviewer 与基准提交防篡改、彩色 Diff 阅读，并支持人工逐条记录有效率与遗漏项。</p>' +
          '<div class="wb-actions"><button type="button" class="button primary" data-ob-jump="codereview">打开代码审查工作台 →</button></div>' +
        '</div>' +

        '<div class="wb-panel">' +
          '<div class="wb-item-top" style="margin-bottom:8px"><h3 style="margin:0">步骤 4 · 将 Webuddy 知识库 MCP 挂载到外部 IDE / CLI</h3><button type="button" class="button primary small" id="wbObCopyMcp">一键复制 JSON 配置</button></div>' +
          '<p class="wb-subtitle" style="margin-bottom:10px">粘贴到 Codex / Claude Code / Cursor 的 MCP 配置中，即可直接调用 <code>kb_search</code>、<code>kb_fetch</code>、<code>kb_overview</code>、<code>kb_verify_evidence</code>。</p>' +
          '<pre class="wb-code">' + esc(mcpSnippet) + '</pre>' +
        '</div>' +
      '</div>';

    root.querySelector('#wbObRefresh')?.addEventListener('click', loadOnboardingView);
    root.querySelectorAll('[data-ob-jump]').forEach(btn => {
      btn.addEventListener('click', () => {
        if (typeof window.switchView === 'function') window.switchView(btn.dataset.obJump);
      });
    });
    root.querySelector('#wbObCopyMcp')?.addEventListener('click', () => {
      navigator.clipboard?.writeText(mcpSnippet).then(() => notify('已复制 Webuddy MCP 配置 JSON')).catch(() => notify(mcpUrl));
    });
    root.querySelector('#wbObPickRepo')?.addEventListener('click', async () => {
      try {
        const picked = await fetch('/api/webuddy/kb/pick-folder', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' }).then(r => r.json());
        if (picked.cancelled || !picked.path) return;
        const imp = await fetch('/api/webuddy/kb/import-local', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ localPath: picked.path, name: picked.name }) }).then(r => r.json());
        if (imp.repo) {
          localStorage.setItem('webuddy.kb.repoPath', imp.repo.localPath);
          notify('已导入本地项目：' + imp.repo.projectName);
          await loadOnboardingView();
        }
      } catch (err) {
        notify(err.message);
      }
    });
  }

  window.loadOnboardingView = loadOnboardingView;
})();
