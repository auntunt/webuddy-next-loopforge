/* Webuddy 2.0 Unified Environment & Integration Hub (环境与接入中心) */
(function () {
  const envState = {
    overview: null,
    overviewError: '',
    mcp: { status: 'checking', detail: '正在探测 /api/webuddy/kb/mcp…' },
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
        if (body && body.error) msg = body.error + ' (HTTP ' + res.status + ')';
      } catch (_) {}
      throw new Error(msg);
    }
    return res.json();
  }

  async function loadOnboardingView() {
    const root = document.getElementById('onboardingRoot');
    if (!root) return;
    envState.loading = true;
    envState.mcp = { status: 'checking', detail: '正在探测知识库 MCP 接口…' };
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
          params: { name: 'kb_search', arguments: { query: 'SM4', limit: 3 } },
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
      envState.mcp = { status: 'ok', detail: 'kb_search 实测调用通过（Python 知识库引擎正常）' };
    } else {
      const reason = mcpR.status === 'rejected'
        ? mcpR.reason.message
        : (mcpR.value.error?.message || mcpR.value.result?.content?.[0]?.text || 'MCP 返回错误');
      envState.mcp = { status: 'error', detail: reason };
    }

    envState.loading = false;
    render();
  }

  function fmtGB(mb) {
    if (!mb || mb <= 0) return '-';
    return (mb / 1024).toFixed(1) + 'G';
  }

  function renderProbeBadge(cli) {
    if (!cli.available) return '<span class="wb-badge unknown">未安装</span>';
    if (cli.authMode === 'none') return '<span class="wb-badge unknown">未配置凭据</span>';
    switch (cli.probeStatus) {
      case 'ok':
        return '<span class="wb-badge pass">✅ 实测可用' + (cli.probeLatencyMs ? ' (' + (cli.probeLatencyMs / 1000).toFixed(1) + 's)' : '') + '</span>';
      case 'auth_error':
        return '<span class="wb-badge fail">❌ 鉴权/封禁异常</span>';
      case 'model_error':
        return '<span class="wb-badge fail">⚠ 模型不可用</span>';
      case 'error':
        return '<span class="wb-badge fail">❌ 探活失败</span>';
      case 'probing':
        return '<span class="wb-badge info">⏳ 实测探活中…</span>';
      default:
        return '<span class="wb-badge info">○ 已配置 · 待探活</span>';
    }
  }

  function renderToolchainChip(label, tool) {
    if (!tool || !tool.available) {
      const tip = tool && tool.detail ? ' title="' + esc(tool.detail) + '"' : '';
      return '<span class="wb-badge unknown" style="opacity:0.65"' + tip + '>' + esc(label) + ' 未就绪</span>';
    }
    const ver = tool.version ? ' ' + tool.version.split(' ')[0] : (tool.detail ? ' ' + tool.detail : '');
    return '<span class="wb-badge pass" title="' + esc(tool.path || '') + '">' + esc(label + ver) + '</span>';
  }

  function renderHostCard(title, subtitle, badgesHtml, actionHtml, prof) {
    if (!prof) {
      return (
        '<div class="wb-panel" style="margin-bottom:12px">' +
          '<div class="wb-item-top" style="margin-bottom:6px">' +
            '<div><h4 style="margin:0;font-size:15px">' + esc(title) + '</h4><div class="wb-subtitle" style="margin:2px 0 0">' + esc(subtitle) + '</div></div>' +
            '<div class="wb-actions">' + badgesHtml + actionHtml + '</div>' +
          '</div>' +
          '<div class="wb-subtitle">等待该机器心跳上报环境画像（重启/升级 Runner 后将在 30 秒内自动上报）。</div>' +
        '</div>'
      );
    }

    const hw = prof.hardware || {};
    const tc = prof.toolchains || {};
    const clis = prof.clis || [];
    const installedCLIs = clis.filter(c => c.available);
    const missingCLIs = clis.filter(c => !c.available);
    const sandboxCls = hw.sandboxHint === 'docker_ready' ? 'pass' : 'info';

    const cliRows = installedCLIs.length
      ? installedCLIs.map(c => {
          const modelsHtml = (c.models || []).slice(0, 6).map(m =>
            '<span class="wb-badge ' + (m === c.defaultModel ? 'pass' : 'info') + '" style="font-size:11px;padding:1px 6px">' + esc(m) + '</span>'
          ).join('');
          const errHtml = c.probeError
            ? '<div style="margin-top:4px;font-size:12px;color:#dc2626;background:#fef2f2;border:1px solid #fecaca;border-radius:6px;padding:4px 8px;word-break:break-all">' + esc(c.probeError) + '</div>'
            : '';
          return (
            '<div style="padding:8px 10px;border:1px solid var(--border,#e5e7eb);border-radius:8px;margin-bottom:6px;background:var(--surface-2,#fafafa)">' +
              '<div class="wb-item-top" style="gap:8px;flex-wrap:wrap">' +
                '<div>' +
                  '<b style="font-size:13px">' + esc(c.name) + '</b> ' +
                  (c.version ? '<span style="font-size:12px;color:var(--muted,#6b7280)">' + esc(c.version) + '</span> ' : '') +
                  '<span style="font-size:12px;color:var(--muted,#6b7280)">' + esc(c.authHint || '') + '</span>' +
                '</div>' +
                '<div>' + renderProbeBadge(c) + '</div>' +
              '</div>' +
              (c.gatewayUrl || modelsHtml
                ? '<div style="margin-top:5px;display:flex;flex-wrap:wrap;align-items:center;gap:6px;font-size:12px">' +
                    (c.gatewayUrl ? '<span style="color:var(--muted,#6b7280)">网关: <code>' + esc(c.gatewayUrl) + '</code></span>' : '') +
                    (modelsHtml ? '<span style="display:inline-flex;flex-wrap:wrap;gap:4px;align-items:center">模型: ' + modelsHtml + '</span>' : '') +
                  '</div>'
                : '') +
              errHtml +
            '</div>'
          );
        }).join('')
      : '<div class="wb-subtitle">该机器未检测到已安装的 Coding CLI。</div>';

    return (
      '<div class="wb-panel" style="margin-bottom:12px">' +
        '<div class="wb-item-top" style="margin-bottom:10px;flex-wrap:wrap;gap:8px">' +
          '<div>' +
            '<h4 style="margin:0;font-size:15px">' + esc(title) + '</h4>' +
            '<div class="wb-subtitle" style="margin:2px 0 0">' + esc(subtitle) + ' · ' + esc(prof.os + '/' + prof.arch) + ' · ' + esc(hw.cpuCores || '?') + ' 核 / 内存 ' + fmtGB(hw.memAvailMB) + ' 可用 (共 ' + fmtGB(hw.memTotalMB) + ') / 磁盘 ' + esc(hw.diskAvailGB || '-') + 'G 可用</div>' +
          '</div>' +
          '<div class="wb-actions">' + badgesHtml + actionHtml + '</div>' +
        '</div>' +
        '<div class="wb-meta" style="margin-bottom:10px;gap:6px;flex-wrap:wrap">' +
          '<span class="wb-badge ' + sandboxCls + '">' + esc(hw.sandboxLabel || '本地直跑') + '</span>' +
          renderToolchainChip('Docker', tc.docker) +
          renderToolchainChip('Git', tc.git) +
          renderToolchainChip('Node', tc.node) +
          renderToolchainChip('Python', tc.python) +
          renderToolchainChip('Go', tc.go) +
          renderToolchainChip('Playwright', tc.playwright) +
        '</div>' +
        cliRows +
        (missingCLIs.length
          ? '<div style="font-size:12px;color:var(--muted,#6b7280);margin-top:4px">未安装 CLI：' + esc(missingCLIs.map(c => c.binary).join(' / ')) + '</div>'
          : '') +
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
    const defaultRunner = runners.find(r => r.isDefault);
    const importedMcpSet = new Set((ov && ov.importedMcps) || []);

    // Collect all profiles for KPI & aggregation
    const hostEntries = [];
    runners.forEach(r => {
      if (r.hostProfile) {
        hostEntries.push({ hostLabel: r.name, isServer: false, prof: r.hostProfile });
      }
    });
    if (server) {
      hostEntries.push({ hostLabel: '控制面 (' + (server.host || 'server') + ')', isServer: true, prof: server });
    }

    let totalCLIs = 0;
    let errorCLIs = 0;
    const allKBs = [];
    const kbSeen = new Set();
    const allGitRemotes = [];
    const gitSeen = new Set();
    const allMCPs = [];
    const mcpSeen = new Set();

    hostEntries.forEach(entry => {
      (entry.prof.clis || []).forEach(c => {
        if (c.available) {
          totalCLIs++;
          if (c.probeStatus === 'auth_error' || c.probeStatus === 'model_error' || c.probeStatus === 'error') {
            errorCLIs++;
          }
        }
      });
      (entry.prof.knowledgeBases || []).forEach(kb => {
        const key = (entry.isServer ? 'srv:' : entry.hostLabel + ':') + kb.path;
        if (!kbSeen.has(key)) {
          kbSeen.add(key);
          allKBs.push({ ...kb, hostLabel: entry.hostLabel, isServer: entry.isServer });
        }
      });
      (entry.prof.gitRemotes || []).forEach(g => {
        const key = entry.hostLabel + '|' + g.url;
        if (!gitSeen.has(key)) {
          gitSeen.add(key);
          allGitRemotes.push({ ...g, hostLabel: entry.hostLabel });
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

    const mcpCardVal = envState.mcp.status === 'ok'
      ? '<span style="color:#059669">已连通</span>'
      : envState.mcp.status === 'checking'
        ? '检测中…'
        : '<span style="color:#dc2626">不可用</span>';

    const machinesHtml =
      runners.map(r => {
        const statusBadge = r.status === 'online'
          ? '<span class="wb-badge pass">在线</span>'
          : '<span class="wb-badge unknown">离线</span>';
        const defBadge = r.isDefault
          ? '<span class="wb-badge pass">★ 默认执行机</span>'
          : '';
        const setDefBtn = (r.status === 'online' && !r.isDefault)
          ? '<button type="button" class="button ghost small" data-set-default-runner="' + esc(r.id) + '">设为默认</button>'
          : '';
        return renderHostCard(
          '执行机 · ' + r.name,
          'Runner ' + (r.version || '') + ' (' + r.id + ')',
          statusBadge + defBadge,
          setDefBtn,
          r.hostProfile
        );
      }).join('') +
      (server
        ? renderHostCard(
            '控制面主机 · ' + (server.host || 'local'),
            'Webuddy 服务端本机（提供知识库扫描、MCP 网关与服务端代码审查）',
            '<span class="wb-badge info">控制面</span>',
            '',
            server
          )
        : '');

    const kbsHtml = allKBs.length
      ? allKBs.map((kb, idx) => {
          const btn = kb.alreadyImported
            ? '<span class="wb-badge pass">✅ 已挂载</span><button type="button" class="button ghost small" data-open-kb="' + esc(kb.path) + '">打开知识库</button>'
            : '<button type="button" class="button primary small" data-mount-kb="' + idx + '" ' + (envState.busyKey === 'kb:' + idx ? 'disabled' : '') + '>' + (envState.busyKey === 'kb:' + idx ? '挂载中…' : '+ 一键挂载为项目') + '</button>';
          return (
            '<div style="display:flex;align-items:center;justify-content:space-between;gap:10px;padding:8px 10px;border:1px solid var(--border,#e5e7eb);border-radius:8px;margin-bottom:6px;flex-wrap:wrap">' +
              '<div>' +
                '<div style="display:flex;align-items:center;gap:6px;flex-wrap:wrap">' +
                  '<b style="font-size:13px">' + esc(kb.name) + '</b>' +
                  '<span class="wb-badge info" style="font-size:11px">' + esc(kb.hostLabel) + '</span>' +
                  (kb.hasGraph ? '<span class="wb-badge pass" style="font-size:11px">图谱</span>' : '') +
                  (kb.hasKb ? '<span class="wb-badge pass" style="font-size:11px">知识库</span>' : '') +
                  (kb.defaultBranch ? '<span class="wb-badge unknown" style="font-size:11px">' + esc(kb.defaultBranch) + '</span>' : '') +
                '</div>' +
                '<div style="font-size:12px;color:var(--muted,#6b7280);margin-top:2px"><code>' + esc(kb.path) + '</code>' + (kb.gitRemote ? ' · Git: <code>' + esc(kb.gitRemote) + '</code>' : '') + '</div>' +
              '</div>' +
              '<div class="wb-actions">' + btn + '</div>' +
            '</div>'
          );
        }).join('')
      : '<div class="wb-subtitle">暂未在扫描目录中发现含 <code>.tmh</code> 的代码仓库。</div>';

    const gitHtml = allGitRemotes.length
      ? '<div style="margin-top:10px"><div style="font-size:12px;font-weight:600;margin-bottom:6px">已自动识别的私有 Git 远端与凭证（已脱敏）</div><div class="wb-meta" style="gap:6px;flex-wrap:wrap">' +
          allGitRemotes.map(g => {
            const cls = g.reachable === true ? 'pass' : (g.reachable === false ? 'fail' : 'info');
            const tag = g.reachable === true ? '✅ ' : (g.reachable === false ? '❌ ' : '');
            return '<span class="wb-badge ' + cls + '" title="' + esc(g.error || g.source) + '">' + tag + esc(g.url) + ' (' + esc(g.hostLabel) + ')</span>';
          }).join('') +
        '</div></div>'
      : '';

    const extMcpsHtml = allMCPs.length
      ? allMCPs.map((m, idx) => {
          const isImported = importedMcpSet.has(m.name.toLowerCase()) || importedMcpSet.has(('ext-' + m.name).toLowerCase());
          const btn = isImported
            ? '<span class="wb-badge pass">✅ 已导入平台</span>'
            : '<button type="button" class="button primary small" data-import-mcp="' + idx + '" ' + (envState.busyKey === 'mcp:' + idx ? 'disabled' : '') + '>' + (envState.busyKey === 'mcp:' + idx ? '导入中…' : '+ 一键导入平台') + '</button>';
          return (
            '<div style="display:flex;align-items:center;justify-content:space-between;gap:10px;padding:8px 10px;border:1px solid var(--border,#e5e7eb);border-radius:8px;margin-bottom:6px;flex-wrap:wrap">' +
              '<div>' +
                '<div style="display:flex;align-items:center;gap:6px;flex-wrap:wrap">' +
                  '<b style="font-size:13px">' + esc(m.name) + '</b>' +
                  '<span class="wb-badge info" style="font-size:11px">' + esc(m.transport) + '</span>' +
                  '<span class="wb-badge unknown" style="font-size:11px">' + esc(m.hostLabel + ' · ' + m.source) + '</span>' +
                '</div>' +
                '<div style="font-size:12px;color:var(--muted,#6b7280);margin-top:2px"><code>' + esc(m.target + (m.args && m.args.length ? ' ' + m.args.join(' ') : '')) + '</code></div>' +
              '</div>' +
              '<div class="wb-actions">' + btn + '</div>' +
            '</div>'
          );
        }).join('')
      : '<div class="wb-subtitle">暂未在 <code>~/.codex/config.toml</code> 或 <code>~/.claude/settings.json</code> 中发现外部 MCP 配置。</div>';

    root.innerHTML =
      '<div class="wb-page">' +
        '<div class="wb-header">' +
          '<div>' +
            '<h1>环境与接入中心 · 外部组件自动发现与探活</h1>' +
            '<p class="wb-subtitle">统一查看控制面与所有执行机器的硬件/Docker 能力、Coding CLI 实测状态、私有 Git、本机知识库（.tmh）及外部 MCP，全部支持一键挂载与切换。</p>' +
          '</div>' +
          '<div class="wb-actions">' +
            '<button type="button" class="button ghost" id="wbEnvRefresh"' + (envState.loading ? ' disabled' : '') + '>' + (envState.loading ? '扫描中…' : '刷新扫描') + '</button>' +
            '<button type="button" class="button primary" id="wbEnvProbeNow"' + (envState.probing ? ' disabled' : '') + '>' + (envState.probing ? '正在 1-Token 实测探活…' : '⚡ 立即实测探活') + '</button>' +
          '</div>' +
        '</div>' +

        (envState.overviewError ? '<div class="wb-badge fail" style="margin-bottom:12px">读取环境总览失败：' + esc(envState.overviewError) + '</div>' : '') +

        '<div class="wb-kpi-grid">' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">在线执行机器</span><span class="wb-kpi-val">' + onlineRunners.length + ' / ' + runners.length + '</span><span style="font-size:11px;color:var(--muted,#6b7280)">默认: ' + esc(defaultRunner ? defaultRunner.name : '未指定') + '</span></div>' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">已扫 Coding CLI</span><span class="wb-kpi-val">' + totalCLIs + (errorCLIs ? ' <span style="font-size:13px;color:#dc2626">(' + errorCLIs + ' 异常)</span>' : '') + '</span></div>' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">已发现知识库 (.tmh)</span><span class="wb-kpi-val">' + allKBs.length + '</span><span style="font-size:11px;color:var(--muted,#6b7280)">已挂载项目: ' + esc(ov ? ov.importedRepos : 0) + '</span></div>' +
          '<div class="wb-kpi-card"><span class="wb-kpi-label">外部 MCP / 知识库 MCP</span><span class="wb-kpi-val" style="font-size:16px">' + allMCPs.length + ' 个 · ' + mcpCardVal + '</span></div>' +
        '</div>' +

        '<div style="margin-top:16px">' +
          '<div class="wb-item-top" style="margin-bottom:8px">' +
            '<h3 style="margin:0">1. 多机能力画像与 Coding CLI 实测探活</h3>' +
            '<div class="wb-actions">' +
              '<button type="button" class="button ghost small" data-ob-jump="runners">+ 绑定新执行机</button>' +
              '<button type="button" class="button ghost small" data-ob-jump="settings">个人 API Key 配置</button>' +
            '</div>' +
          '</div>' +
          machinesHtml +
        '</div>' +

        '<div class="wb-panel" style="margin-top:14px">' +
          '<div class="wb-item-top" style="margin-bottom:10px">' +
            '<div><h3 style="margin:0">2. 本机知识库（.tmh）与私有 Git 自动发现</h3><p class="wb-subtitle" style="margin:2px 0 0">自动扫描服务器与工作区目录，无需手填路径或依赖 GitHub，一键挂载即可用于架构蓝图、影响面分析与代码审查。</p></div>' +
            '<div class="wb-actions">' +
              '<button type="button" class="button ghost small" data-ob-jump="knowledgebase">打开知识库 →</button>' +
            '</div>' +
          '</div>' +
          kbsHtml +
          gitHtml +
        '</div>' +

        '<div class="wb-panel" style="margin-top:14px">' +
          '<div class="wb-item-top" style="margin-bottom:10px">' +
            '<div><h3 style="margin:0">3. 外部 MCP 自动发现 & Webuddy 知识库 MCP 自检</h3><p class="wb-subtitle" style="margin:2px 0 0">知识库 MCP 状态：<b>' + esc(envState.mcp.detail) + '</b></p></div>' +
            '<div class="wb-actions">' +
              '<button type="button" class="button ghost small" data-ob-jump="guide">生成访问密钥</button>' +
              '<button type="button" class="button primary small" id="wbObCopyMcp">复制知识库 MCP 配置</button>' +
            '</div>' +
          '</div>' +
          extMcpsHtml +
          '<details style="margin-top:10px"><summary style="cursor:pointer;font-size:12px;color:var(--muted,#6b7280)">查看给外部 Codex / Claude Code / Cursor 使用的 Webuddy 知识库 MCP JSON</summary><pre class="wb-code" style="margin-top:6px">' + esc(mcpSnippet) + '</pre></details>' +
        '</div>' +

        '<div class="wb-panel" style="margin-top:14px">' +
          '<div class="wb-item-top">' +
            '<div><h3 style="margin:0">4. 快速开始工作</h3><p class="wb-subtitle" style="margin:2px 0 0">环境与组件就绪后，可直接进入工作台提需求、编排 SOP 或提交独立代码审查。</p></div>' +
            '<div class="wb-actions">' +
              '<button type="button" class="button primary" data-ob-jump="collaboration">前往工作台 →</button>' +
              '<button type="button" class="button ghost" data-ob-jump="codereview">代码审查</button>' +
              '<button type="button" class="button ghost" data-ob-jump="approvals">待我确认</button>' +
            '</div>' +
          '</div>' +
        '</div>' +
      '</div>';

    // Wire actions
    root.querySelector('#wbEnvRefresh')?.addEventListener('click', loadOnboardingView);
    root.querySelector('#wbEnvProbeNow')?.addEventListener('click', async () => {
      envState.probing = true;
      render();
      try {
        await fetchChecked('/api/webuddy/env/probe?sync=1', { method: 'POST' });
        notify('实测探活完成，已更新最新状态');
        await loadOnboardingView();
      } catch (err) {
        notify('探活失败：' + err.message);
      } finally {
        envState.probing = false;
        render();
      }
    });

    root.querySelectorAll('[data-set-default-runner]').forEach(btn => {
      btn.addEventListener('click', async () => {
        const rid = btn.dataset.setDefaultRunner;
        try {
          await fetchChecked('/api/webuddy/env/set-default-runner', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ runnerId: rid }),
          });
          notify('已切换默认执行机器');
          await loadOnboardingView();
        } catch (err) {
          notify('切换失败：' + err.message);
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
          const res = await fetchChecked('/api/webuddy/env/mount-kb', {
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
          notify((res.reused ? '项目已存在：' : '已一键挂载项目：') + (res.projectName || kb.name));
          await loadOnboardingView();
        } catch (err) {
          notify('挂载失败：' + err.message);
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
        const idx = Number(btn.dataset.importMcp);
        const m = allMCPs[idx];
        if (!m) return;
        envState.busyKey = 'mcp:' + idx;
        render();
        try {
          await fetchChecked('/api/webuddy/env/import-mcp', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              name: m.name,
              transport: m.transport,
              target: m.target,
              args: m.args || [],
              source: m.source,
            }),
          });
          notify('已导入外部 MCP：' + m.name);
          await loadOnboardingView();
        } catch (err) {
          notify('导入失败：' + err.message);
        } finally {
          envState.busyKey = '';
          render();
        }
      });
    });

    root.querySelectorAll('[data-ob-jump]').forEach(btn => {
      btn.addEventListener('click', () => {
        if (typeof window.switchView === 'function') window.switchView(btn.dataset.obJump);
      });
    });

    root.querySelector('#wbObCopyMcp')?.addEventListener('click', () => {
      navigator.clipboard?.writeText(mcpSnippet).then(() => notify('已复制含 Authorization 头的 MCP 配置 JSON')).catch(() => notify(mcpUrl));
    });
  }

  window.loadOnboardingView = loadOnboardingView;
})();
