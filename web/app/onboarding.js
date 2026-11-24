/* Webuddy 2.0 Zero-Friction Environment Hub (红绿灯一键环境中心) */
(function () {
  const envState = {
    overview: null,
    overviewError: '',
    mcp: { status: 'checking', detail: '检测中…' },
    loading: false,
    probing: false,
    busyKey: '',
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
        if (body && body.error) msg = body.error;
      } catch (_) {}
      throw new Error(msg);
    }
    return res.json();
  }

  async function loadOnboardingView() {
    const root = document.getElementById('onboardingRoot');
    if (!root) return;
    envState.loading = true;
    envState.mcp = { status: 'checking', detail: '检测中…' };
    render();

    const [ovR, mcpR] = await Promise.allSettled([
      fetchChecked('/api/webuddy/env/overview'),
      fetchChecked('/api/webuddy/kb/mcp', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          jsonrpc: '2.0',
          id: 1,
          method: 'tools/call',
          params: { name: 'kb_search', arguments: { query: 'SM4', limit: 1 } },
        }),
      }),
    ]);

    if (ovR.status === 'fulfilled') {
      envState.overview = ovR.value;
      envState.overviewError = '';
    } else {
      envState.overview = null;
      envState.overviewError = ovR.reason.message;
    }

    if (mcpR.status === 'fulfilled' && !mcpR.value.error && !mcpR.value.result?.isError) {
      envState.mcp = { status: 'ok', detail: '正常' };
    } else {
      envState.mcp = { status: 'error', detail: '异常' };
    }

    envState.loading = false;
    render();
  }

  function fmtGB(mb) {
    if (!mb || mb <= 0) return '-';
    return Math.round(mb / 1024) + 'G';
  }

  function cliTrafficPill(cli) {
    if (!cli.available) return '';
    if (cli.authMode === 'none') {
      return '<span class="wb-badge unknown">' + esc(cli.id) + ' · 未登录</span>';
    }
    if (cli.probeStatus === 'ok') {
      const sec = cli.probeLatencyMs ? ' ' + (cli.probeLatencyMs / 1000).toFixed(1) + 's' : '';
      return '<span class="wb-badge pass">✅ ' + esc(cli.id) + (cli.defaultModel ? ' (' + esc(cli.defaultModel) + ')' : '') + sec + '</span>';
    }
    if (cli.probeStatus === 'auth_error' || cli.probeStatus === 'model_error' || cli.probeStatus === 'error') {
      const shortErr = cli.probeStatus === 'auth_error' ? '账号异常' : cli.probeStatus === 'model_error' ? '模型不可用' : '不可用';
      return '<span class="wb-badge fail" title="' + esc(cli.probeError || '') + '">❌ ' + esc(cli.id) + ' · ' + shortErr + '</span>';
    }
    if (cli.probeStatus === 'probing') {
      return '<span class="wb-badge info">⏳ ' + esc(cli.id) + ' 检测中</span>';
    }
    return '<span class="wb-badge info">● ' + esc(cli.id) + '</span>';
  }

  function renderMachineCard(name, online, isDefault, runnerId, prof) {
    const hw = (prof && prof.hardware) || {};
    const tc = (prof && prof.toolchains) || {};
    const clis = ((prof && prof.clis) || []).filter(c => c.available);
    const hasErr = clis.some(c => c.probeStatus === 'auth_error' || c.probeStatus === 'model_error' || c.probeStatus === 'error');
    const light = !online ? '⚪' : hasErr ? '🟡' : '🟢';

    const oneLineSpec = prof
      ? (fmtGB(hw.memTotalMB) + ' 内存 · ' + (tc.docker && tc.docker.available ? 'Docker 正常' : '本地模式'))
      : '等待连接上报…';

    const errNotes = clis
      .filter(c => c.probeError)
      .map(c => '<div style="font-size:12px;color:#dc2626;margin-top:6px">❌ ' + esc(c.id) + '：' + esc(c.probeError) + '</div>')
      .join('');

    const detailsHtml = prof
      ? '<details style="margin-top:8px"><summary style="cursor:pointer;font-size:12px;color:var(--muted,#6b7280)">查看详细参数（供排查）</summary>' +
          '<div style="margin-top:6px;font-size:12px;color:var(--muted,#6b7280);display:flex;flex-wrap:wrap;gap:6px">' +
            '<span>CPU ' + esc(hw.cpuCores || '?') + ' 核</span>' +
            '<span>可用内存 ' + fmtGB(hw.memAvailMB) + '</span>' +
            '<span>可用磁盘 ' + esc(hw.diskAvailGB || '-') + 'G</span>' +
            (tc.git && tc.git.available ? '<span>Git ✓</span>' : '') +
            (tc.node && tc.node.available ? '<span>Node ✓</span>' : '') +
            (tc.python && tc.python.available ? '<span>Python ✓</span>' : '') +
            (tc.playwright && tc.playwright.available ? '<span>Playwright ✓</span>' : '') +
          '</div>' +
          clis.map(c => '<div style="font-size:12px;margin-top:4px"><code>' + esc(c.id) + '</code> ' + esc(c.gatewayUrl || c.authHint || '') + (c.models && c.models.length ? ' · 模型: ' + esc(c.models.slice(0, 5).join(', ')) : '') + '</div>').join('') +
        '</details>'
      : '';

    return (
      '<div class="wb-panel" style="margin-bottom:10px">' +
        '<div class="wb-item-top" style="gap:8px;flex-wrap:wrap">' +
          '<div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap">' +
            '<span style="font-size:16px">' + light + '</span>' +
            '<b style="font-size:15px">' + esc(name) + '</b>' +
            (isDefault ? '<span class="wb-badge pass">★ 默认</span>' : '') +
            '<span style="font-size:12px;color:var(--muted,#6b7280)">' + esc(oneLineSpec) + '</span>' +
          '</div>' +
          '<div class="wb-actions">' +
            (runnerId && online && !isDefault
              ? '<button type="button" class="button ghost small" data-set-default-runner="' + esc(runnerId) + '">设为默认</button>'
              : '') +
          '</div>' +
        '</div>' +
        (clis.length ? '<div class="wb-meta" style="margin-top:8px;gap:6px;flex-wrap:wrap">' + clis.map(cliTrafficPill).join('') + '</div>' : '') +
        errNotes +
        detailsHtml +
      '</div>'
    );
  }

  function render() {
    const root = document.getElementById('onboardingRoot');
    if (!root) return;

    const ov = envState.overview;
    const server = ov ? ov.server : null;
    const runners = (ov && Array.isArray(ov.runners)) ? ov.runners : [];
    const onlineRunners = runners.filter(r => r.status === 'online');
    const importedMcpSet = new Set((ov && ov.importedMcps) || []);

    const hostEntries = [];
    runners.forEach(r => { if (r.hostProfile) hostEntries.push({ hostLabel: r.name, isServer: false, prof: r.hostProfile }); });
    if (server) hostEntries.push({ hostLabel: '服务器', isServer: true, prof: server });

    let okCLIs = 0;
    let badCLIs = 0;
    const allKBs = [];
    const kbSeen = new Set();
    const allMCPs = [];
    const mcpSeen = new Set();

    hostEntries.forEach(entry => {
      (entry.prof.clis || []).forEach(c => {
        if (!c.available) return;
        if (c.probeStatus === 'ok') okCLIs++;
        if (c.probeStatus === 'auth_error' || c.probeStatus === 'model_error' || c.probeStatus === 'error') badCLIs++;
      });
      (entry.prof.knowledgeBases || []).forEach(kb => {
        const key = (entry.isServer ? 'srv:' : entry.hostLabel + ':') + kb.path;
        if (!kbSeen.has(key)) {
          kbSeen.add(key);
          allKBs.push({ ...kb, hostLabel: entry.hostLabel, isServer: entry.isServer });
        }
      });
      (entry.prof.externalMcps || []).forEach(m => {
        const key = m.name + '|' + m.target;
        if (!mcpSeen.has(key)) {
          mcpSeen.add(key);
          allMCPs.push({ ...m, hostLabel: entry.hostLabel });
        }
      });
    });

    const unimportedMCPs = allMCPs.filter(m => !importedMcpSet.has(m.name.toLowerCase()) && !importedMcpSet.has(('ext-' + m.name).toLowerCase()));
    const ready = !envState.overviewError && onlineRunners.length > 0 && (okCLIs > 0 || badCLIs === 0);

    const statusBanner =
      '<div class="wb-panel" style="display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap;border-color:' + (ready ? 'rgba(16,185,129,0.45)' : 'rgba(245,158,11,0.5)') + '">' +
        '<div>' +
          '<div style="font-size:17px;font-weight:700">' +
            (envState.overviewError
              ? '🔴 环境读取失败'
              : ready
                ? (badCLIs > 0 ? '🟡 可直接干活（有 ' + badCLIs + ' 个备用工具异常）' : '🟢 环境已就绪，可以直接干活')
                : '🟡 请先连接一台执行机器') +
          '</div>' +
          '<div class="wb-subtitle" style="margin-top:2px">在线机器 ' + onlineRunners.length + ' 台 · 已挂载项目 ' + (ov ? ov.importedRepos : 0) + ' 个 · 知识库服务' + esc(envState.mcp.detail) + '</div>' +
        '</div>' +
        '<div class="wb-actions">' +
          '<button type="button" class="button ghost" id="wbEnvProbeNow"' + (envState.probing ? ' disabled' : '') + '>' + (envState.probing ? '体检中…' : '⚡ 一键体检') + '</button>' +
          '<button type="button" class="button primary" data-ob-jump="collaboration">去工作台干活 →</button>' +
        '</div>' +
      '</div>';

    const machinesHtml =
      runners.map(r => renderMachineCard(r.name, r.status === 'online', r.isDefault, r.id, r.hostProfile)).join('') +
      (server ? renderMachineCard('服务器本机', true, false, '', server) : '');

    const kbsHtml = allKBs.length
      ? allKBs.map((kb, idx) => {
          const btn = kb.alreadyImported
            ? '<button type="button" class="button ghost small" data-open-kb="' + esc(kb.path) + '">打开 →</button>'
            : '<button type="button" class="button primary small" data-mount-kb="' + idx + '" ' + (envState.busyKey === 'kb:' + idx ? 'disabled' : '') + '>' + (envState.busyKey === 'kb:' + idx ? '挂载中…' : '+ 一键挂载') + '</button>';
          return (
            '<div style="display:flex;align-items:center;justify-content:space-between;gap:10px;padding:8px 10px;border:1px solid var(--border,#e5e7eb);border-radius:8px;margin-bottom:6px">' +
              '<div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap" title="' + esc(kb.path) + '">' +
                '<b style="font-size:14px">' + esc(kb.name) + '</b>' +
                (kb.alreadyImported ? '<span class="wb-badge pass">已挂载</span>' : '<span class="wb-badge unknown">未挂载</span>') +
                (kb.hasGraph ? '<span class="wb-badge info">有图谱</span>' : '') +
              '</div>' +
              '<div class="wb-actions">' + btn + '</div>' +
            '</div>'
          );
        }).join('')
      : '<div class="wb-subtitle">未发现新项目</div>';

    const mcpUrl = location.origin + '/api/webuddy/kb/mcp';
    const mcpSnippet = JSON.stringify({
      mcpServers: {
        'webuddy-kb': {
          url: mcpUrl,
          headers: { Authorization: 'Bearer <替换为访问密钥>' },
        },
      },
    }, null, 2);

    root.innerHTML =
      '<div class="wb-page">' +
        '<div class="wb-header">' +
          '<div><h1>环境接入</h1><p class="wb-subtitle">看红绿灯，点一下就能挂载项目和切换机器。</p></div>' +
          '<div class="wb-actions">' +
            '<button type="button" class="button ghost" data-ob-jump="runners">+ 连接新电脑</button>' +
            '<button type="button" class="button ghost" id="wbEnvRefresh"' + (envState.loading ? ' disabled' : '') + '>' + (envState.loading ? '刷新中…' : '刷新') + '</button>' +
          '</div>' +
        '</div>' +

        statusBanner +

        '<div><h3 style="margin:6px 0 10px">1. 我的电脑与 AI 工具</h3>' + machinesHtml + '</div>' +

        '<div class="wb-panel">' +
          '<div class="wb-item-top" style="margin-bottom:10px"><h3 style="margin:0">2. 一键挂载项目（' + allKBs.length + '）</h3><button type="button" class="button ghost small" data-ob-jump="knowledgebase">看知识库 →</button></div>' +
          kbsHtml +
        '</div>' +

        (unimportedMCPs.length
          ? '<div class="wb-panel"><h3 style="margin:0 0 8px">3. 发现可导入工具（' + unimportedMCPs.length + '）</h3>' +
              unimportedMCPs.map((m, idx) =>
                '<div style="display:flex;align-items:center;justify-content:space-between;padding:6px 0">' +
                  '<b>' + esc(m.name) + '</b>' +
                  '<button type="button" class="button primary small" data-import-mcp="' + idx + '">+ 一键导入</button>' +
                '</div>'
              ).join('') +
            '</div>'
          : '') +

        '<details class="wb-panel" style="cursor:pointer">' +
          '<summary style="font-weight:600;font-size:13px">高级：给外部 AI 工具接入知识库（普通使用无需配置）</summary>' +
          '<div style="margin-top:10px;display:flex;gap:8px;margin-bottom:8px">' +
            '<button type="button" class="button ghost small" data-ob-jump="guide">生成密钥</button>' +
            '<button type="button" class="button primary small" id="wbObCopyMcp">复制配置</button>' +
          '</div>' +
          '<pre class="wb-code">' + esc(mcpSnippet) + '</pre>' +
        '</details>' +
      '</div>';

    root.querySelector('#wbEnvRefresh')?.addEventListener('click', loadOnboardingView);
    root.querySelector('#wbEnvProbeNow')?.addEventListener('click', async () => {
      envState.probing = true;
      render();
      try {
        await fetchChecked('/api/webuddy/env/probe?sync=1', { method: 'POST' });
        notify('体检完成');
        await loadOnboardingView();
      } catch (err) {
        notify('体检失败：' + err.message);
      } finally {
        envState.probing = false;
        render();
      }
    });

    root.querySelectorAll('[data-set-default-runner]').forEach(btn => {
      btn.addEventListener('click', async () => {
        try {
          await fetchChecked('/api/webuddy/env/set-default-runner', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ runnerId: btn.dataset.setDefaultRunner }),
          });
          notify('已设为默认机器');
          await loadOnboardingView();
        } catch (err) {
          notify(err.message);
        }
      });
    });

    root.querySelectorAll('[data-mount-kb]').forEach(btn => {
      btn.addEventListener('click', async () => {
        const idx = Number(btn.dataset.mountKb);
        const kb = allKBs[idx];
        if (!kb) return;
        envState.busyKey = 'kb:' + idx;
        render();
        try {
          await fetchChecked('/api/webuddy/env/mount-kb', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              localPath: kb.path,
              name: kb.name,
              cloneUrl: kb.gitRemote || '',
              defaultBranch: kb.defaultBranch || 'main',
              allowRemote: !kb.isServer,
            }),
          });
          notify('已挂载：' + kb.name);
          await loadOnboardingView();
        } catch (err) {
          notify(err.message);
        } finally {
          envState.busyKey = '';
          render();
        }
      });
    });

    root.querySelectorAll('[data-open-kb]').forEach(btn => {
      btn.addEventListener('click', () => {
        localStorage.setItem('webuddy.kb.repoPath', btn.dataset.openKb);
        if (typeof window.switchView === 'function') window.switchView('knowledgebase');
      });
    });

    root.querySelectorAll('[data-import-mcp]').forEach(btn => {
      btn.addEventListener('click', async () => {
        const m = unimportedMCPs[Number(btn.dataset.importMcp)];
        if (!m) return;
        try {
          await fetchChecked('/api/webuddy/env/import-mcp', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name: m.name, transport: m.transport, target: m.target, args: m.args || [], source: m.source }),
          });
          notify('已导入：' + m.name);
          await loadOnboardingView();
        } catch (err) {
          notify(err.message);
        }
      });
    });

    root.querySelectorAll('[data-ob-jump]').forEach(btn => {
      btn.addEventListener('click', () => {
        if (typeof window.switchView === 'function') window.switchView(btn.dataset.obJump);
      });
    });

    root.querySelector('#wbObCopyMcp')?.addEventListener('click', () => {
      navigator.clipboard?.writeText(mcpSnippet).then(() => notify('已复制配置')).catch(() => notify(mcpUrl));
    });
  }

  window.loadOnboardingView = loadOnboardingView;
})();
