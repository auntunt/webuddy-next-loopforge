/* Webuddy 2.0 Task-First Onboarding & Live Health Check Guide */
(function () {
  const obState = {
    serverHost: '',
    executors: [],
    execError: '',
    runners: [],
    runnersError: '',
    repos: [],
    reposError: '',
    mcp: { status: 'checking', detail: '正在探测 /api/webuddy/kb/mcp…' },
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

  async function fetchChecked(url, opts) {
    const res = await fetch(url, opts);
    if (!res.ok) {
      let msg = 'HTTP ' + res.status;
      try {
        const body = await res.json();
        if (body && body.error) msg = body.error + ' (HTTP ' + res.status + ')';
      } catch (_) {}
      throw new Error(msg);
    }
    return res.json();
  }

  async function loadOnboardingView() {
    const root = document.getElementById('onboardingRoot');
    if (!root) return;
    obState.loading = true;
    obState.mcp = { status: 'checking', detail: '正在探测 MCP 接口…' };
    render();

    const [exR, runR, repoR, mcpR] = await Promise.allSettled([
      fetchChecked('/api/codereview/executors'),
      fetchChecked('/api/runners'),
      fetchChecked('/api/webuddy/kb/repos'),
      fetchChecked('/api/webuddy/kb/mcp', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          jsonrpc: '2.0',
          id: 1,
          method: 'tools/call',
          params: { name: 'kb_search', arguments: { query: 'SM4', limit: 3 } },
        }),
      }),
    ]);

    if (exR.status === 'fulfilled') {
      obState.executors = exR.value.executors || [];
      obState.serverHost = exR.value.host || '';
      obState.execError = '';
    } else {
      obState.executors = [];
      obState.execError = exR.reason.message;
    }

    if (runR.status === 'fulfilled') {
      obState.runners = Array.isArray(runR.value) ? runR.value : [];
      obState.runnersError = '';
    } else {
      obState.runners = [];
      obState.runnersError = runR.reason.message;
    }

    if (repoR.status === 'fulfilled') {
      obState.repos = repoR.value.repos || [];
      if (!obState.serverHost && repoR.value.host) obState.serverHost = repoR.value.host;
      obState.reposError = '';
    } else {
      obState.repos = [];
      obState.reposError = repoR.reason.message;
    }

    if (mcpR.status === 'fulfilled' && !mcpR.value.error && !mcpR.value.result?.isError) {
      obState.mcp = { status: 'ok', detail: 'kb_search 实测调用通过（Python 引擎正常）' };
    } else {
      const reason = mcpR.status === 'rejected'
        ? mcpR.reason.message
        : (mcpR.value.error?.message || mcpR.value.result?.content?.[0]?.text || 'MCP 返回错误');
      obState.mcp = { status: 'error', detail: reason };
    }

    obState.loading = false;
    render();
  }

  function render() {
    const root = document.getElementById('onboardingRoot');
    if (!root) return;

    const installed = obState.executors.filter(e => e.available);
    const onlineRunners = obState.runners.filter(r => r.status === 'online');
    const repos = obState.repos;
    const activePid = (window.state && window.state.projectFilter) || '';
    const activeRepo = (activePid && repos.find(r => r.projectId === activePid)) || repos.find(r => r.hasGraph) || repos[0];

    const mcpUrl = location.origin + '/api/webuddy/kb/mcp';
    const mcpSnippet = JSON.stringify({
      mcpServers: {
        'webuddy-kb': {
          url: mcpUrl,
          headers: {
            Authorization: 'Bearer <替换为你在「访问密钥」生成的 Token>',
          },
        },
      },
    }, null, 2);

    const mcpCardVal = obState.mcp.status === 'ok'
      ? '<span style="color:#059669">已连通</span>'
      : obState.mcp.status === 'checking'
        ? '检测中…'
        : '<span style="color:#dc2626">不可用</span>';

    root.innerHTML =
      '<div class="wb-page">' +
        '<div class="wb-header">' +
          '<div><h1>开始一项真实工作 · Webuddy 指引</h1><p class="wb-subtitle">选择一个项目，说明这次要解决什么问题——知识图谱、代码审查和执行机都会围绕当前任务展开。</p></div>' +
          '<div class="wb-actions">' +
            '<a class="button ghost" href="/landing" target="_blank" rel="noopener">产品官网 ↗</a>' +
            '<button type="button" class="button primary" id="wbObRefresh">重新自检</button>' +
          '</div>' +
        '</div>' +

        '<div class="wb-kpi-grid">' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">当前选中项目</span><span class="wb-kpi-val" style="font-size:16px">' + esc(activeRepo ? activeRepo.projectName : '未选择') + '</span></div>' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">服务端已扫 CLI' + (obState.serverHost ? ' (' + esc(obState.serverHost) + ')' : '') + '</span><span class="wb-kpi-val">' + (obState.execError ? '异常' : installed.length) + '</span></div>' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">在线个人执行机</span><span class="wb-kpi-val">' + (obState.runnersError ? '异常' : onlineRunners.length) + '</span></div>' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">知识库 MCP 实时状态</span><span class="wb-kpi-val" style="font-size:16px">' + mcpCardVal + '</span></div>' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">已导入本地项目</span><span class="wb-kpi-val">' + (obState.reposError ? '异常' : repos.length) + '</span></div>' +
        '</div>' +

        '<div class="wb-panel">' +
          '<h3>第 1 步 · 选择项目并确认影响范围</h3>' +
          '<p class="wb-subtitle" style="margin-bottom:10px">先在顶栏或下方选定目标项目，打开知识库看清这次改动涉及哪些业务域、流程和数据表，再开始修改。</p>' +
          (obState.reposError
            ? '<div class="wb-badge fail" style="margin-bottom:10px">读取项目列表失败：' + esc(obState.reposError) + '</div>'
            : '<div class="wb-meta" style="margin-bottom:12px">' +
                (repos.length
                  ? repos.map(r => '<span class="wb-badge ' + (r.hasGraph ? 'pass' : 'info') + '">' + esc(r.projectName + (r.hasGraph ? ' · 已建图谱' : ' · 待扫描')) + '</span>').join('')
                  : '<span class="wb-badge unknown">尚未导入本地项目</span>') +
              '</div>') +
          '<div class="wb-actions">' +
            '<button type="button" class="button primary" data-ob-jump="knowledgebase">打开知识库与架构蓝图 →</button>' +
            '<button type="button" class="button ghost" id="wbObPickRepo">+ 选择本地文件夹导入</button>' +
          '</div>' +
        '</div>' +

        '<div class="wb-panel">' +
          '<h3>第 2 步 · 开始修改（确认代码与执行所在机器）</h3>' +
          '<p class="wb-subtitle" style="margin-bottom:10px">个人电脑上的执行机器可直接复用你本机已登录的 Claude Code / Codex；服务端主机（<code>' + esc(obState.serverHost || 'local') + '</code>）上的操作会读取服务端文件系统与服务端 CLI。</p>' +
          (obState.execError ? '<div class="wb-badge fail" style="margin-bottom:8px">服务端 CLI 检测失败：' + esc(obState.execError) + '</div>' : '') +
          (obState.runnersError ? '<div class="wb-badge fail" style="margin-bottom:8px">执行机器列表读取失败：' + esc(obState.runnersError) + '</div>' : '') +
          '<div class="wb-meta" style="margin-bottom:12px">' +
            (onlineRunners.length
              ? '<span class="wb-badge pass">在线执行机：' + esc(onlineRunners.map(r => r.name).join('、')) + '</span>'
              : '<span class="wb-badge unknown">暂无在线个人执行机</span>') +
            installed.map(e => '<span class="wb-badge ' + (e.loggedIn ? 'pass' : 'info') + '">服务端 ' + esc(e.name) + '</span>').join('') +
          '</div>' +
          '<div class="wb-actions">' +
            '<button type="button" class="button primary" data-ob-jump="collaboration">前往工作台提出任务 →</button>' +
            '<button type="button" class="button ghost" data-ob-jump="runners">管理执行机器</button>' +
            '<button type="button" class="button ghost" data-ob-jump="settings">个人配置（API Key）</button>' +
          '</div>' +
        '</div>' +

        '<div class="wb-panel">' +
          '<h3>第 3 步 · 独立代码审查与人工确认</h3>' +
          '<p class="wb-subtitle" style="margin-bottom:10px">改动完成后提交独立审查：逐行检查彩色 Diff 与阻断项，确认无误报后在「待我确认」完成签收交付。</p>' +
          '<div class="wb-actions">' +
            '<button type="button" class="button primary" data-ob-jump="codereview">打开代码审查 →</button>' +
            '<button type="button" class="button ghost" data-ob-jump="approvals">查看待我确认</button>' +
          '</div>' +
        '</div>' +

        '<div class="wb-panel">' +
          '<div class="wb-item-top" style="margin-bottom:8px">' +
            '<div><h3 style="margin:0">第 4 步 · 在外部助手（Codex / Claude Code / Cursor）挂载知识库 MCP</h3>' +
            '<p class="wb-subtitle" style="margin:2px 0 0">当前实时状态：<b>' + esc(obState.mcp.detail) + '</b>。开启认证的实例需要携带访问密钥（Bearer Token）。</p></div>' +
            '<div class="wb-actions">' +
              '<button type="button" class="button ghost small" data-ob-jump="guide">前往生成访问密钥</button>' +
              '<button type="button" class="button primary small" id="wbObCopyMcp">复制含鉴权的 MCP 配置</button>' +
            '</div>' +
          '</div>' +
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
      navigator.clipboard?.writeText(mcpSnippet).then(() => notify('已复制含 Authorization 头的 MCP 配置 JSON')).catch(() => notify(mcpUrl));
    });
    root.querySelector('#wbObPickRepo')?.addEventListener('click', async () => {
      try {
        const picked = await fetchChecked('/api/webuddy/kb/pick-folder', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' });
        if (picked.cancelled || !picked.path) return;
        const imp = await fetchChecked('/api/webuddy/kb/import-local', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ localPath: picked.path, name: picked.name }) });
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
