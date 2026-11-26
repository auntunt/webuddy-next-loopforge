/* Webuddy v2 Code Review & Human Effectiveness Feedback Workbench */
(function () {
  function emptyDraft() {
    return {
      title: '',
      issueId: '',
      runId: '',
      writer: 'developer',
      reviewer: '',
      model: '',
      baseRef: 'HEAD~1',
      headRef: 'HEAD',
      diff: '',
      blocking: '',
      advisory: '',
    };
  }

  const crState = {
    reviews: [],
    executors: [],
    canPickDir: false,
    serverHost: '',
    repos: [],
    issues: [],
    tasks: [],
    runs: [],
    linkedRun: null,
    selectedRepoPath: '',
    gitRefs: { isGit: false, branches: [], commits: [] },
    diffSource: 'repo', // repo | paste
    selectedId: '',
    detail: null,
    summary: null,
    showCreate: false,
    createMode: 'auto', // auto | manual
    draftsByProject: {},
    submitError: '',
    runningAI: false,
    activeJob: null,
    loading: false,
  };

  function draftKey() {
    return currentProjectId() || '__all__';
  }

  function activeDraft() {
    const k = draftKey();
    if (!crState.draftsByProject[k]) crState.draftsByProject[k] = emptyDraft();
    return crState.draftsByProject[k];
  }

  function syncDraftFromDOM(ev) {
    const form = document.getElementById('wbCreateReviewForm');
    if (!form) return;
    const d = activeDraft();
    const isRepoChange = ev && ev.target && ev.target.id === 'wbCrRepoSelect';
    const val = name => {
      const el = form.elements.namedItem(name);
      return el ? String(el.value || '') : undefined;
    };
    const keys = isRepoChange
      ? ['title', 'issueId', 'runId', 'writer', 'reviewer', 'model', 'diff', 'blocking', 'advisory']
      : ['title', 'issueId', 'runId', 'writer', 'reviewer', 'model', 'baseRef', 'headRef', 'diff', 'blocking', 'advisory'];
    keys.forEach(k => {
      const v = val(k);
      if (v !== undefined) d[k] = v;
    });
  }

  function resetDraft() {
    crState.draftsByProject[draftKey()] = emptyDraft();
    crState.submitError = '';
  }

  function esc(v) {
    return String(v == null ? '' : v)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  function currentProjectId() {
    return (window.state && window.state.projectFilter) || '';
  }

  async function reqJSON(url, opts) {
    const res = await fetch(url, Object.assign({ headers: { 'Content-Type': 'application/json' } }, opts));
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || ('请求失败: ' + res.status));
    return data;
  }

  function notify(msg) {
    if (typeof window.toast === 'function') window.toast(msg);
    else console.log('[codereview]', msg);
  }

  async function loadGitRefs(repoPath) {
    if (!repoPath) {
      crState.gitRefs = { isGit: false, branches: [], commits: [] };
      return;
    }
    try {
      crState.gitRefs = await reqJSON('/api/webuddy/kb/git-refs?repoPath=' + encodeURIComponent(repoPath));
    } catch (_) {
      crState.gitRefs = { isGit: false, branches: [], commits: [] };
    }
  }

  async function pickRepoForReview() {
    try {
      const picked = await reqJSON('/api/webuddy/kb/pick-folder', { method: 'POST', body: '{}' });
      if (picked.cancelled || !picked.path) return;
      const imported = await reqJSON('/api/webuddy/kb/import-local', {
        method: 'POST',
        body: JSON.stringify({ localPath: picked.path, name: picked.name }),
      });
      const repoData = await reqJSON('/api/webuddy/kb/repos').catch(() => ({ repos: [] }));
      crState.repos = repoData.repos || [];
      if (imported.repo && imported.repo.localPath) {
        crState.selectedRepoPath = imported.repo.localPath;
        await loadGitRefs(crState.selectedRepoPath);
        notify('已选择仓库：' + imported.repo.projectName);
      }
      render();
    } catch (err) {
      notify(err.message);
    }
  }

  function inspectApprovalGate(run, review) {
    if (!run) return { hasRun: false, waiting: false, nodeId: '', nodeName: '', status: '', isStale: false, latestGateTs: 0 };
    const status = String(run.status || '');
    const nodes = run.nodes || {};
    const nodeId = status === 'waiting'
      ? (Object.keys(nodes).find(id => nodes[id] && nodes[id].status === 'waiting_approval') || '')
      : '';
    const waiting = Boolean(nodeId);
    const wfNodes = (run.workflowSnapshot && run.workflowSnapshot.nodes) || [];
    const nodeDef = wfNodes.find(n => n.id === nodeId);
    const nodeName = (nodeDef && nodeDef.name) || nodeId;

    let latestGateTs = 0;
    if (waiting) {
      const wStart = Date.parse((nodes[nodeId] && nodes[nodeId].startedAt) || '');
      if (wStart > latestGateTs) latestGateTs = wStart;
      Object.entries(nodes).forEach(([id, nr]) => {
        if (id === nodeId || !nr) return;
        const fin = Date.parse(nr.finishedAt || '');
        if (fin > latestGateTs) latestGateTs = fin;
      });
    }
    const revTs = Date.parse((review && review.createdAt) || '');
    const isStale = Boolean(waiting && latestGateTs > 0 && revTs > 0 && revTs < latestGateTs);
    return { hasRun: true, waiting, nodeId, nodeName, status, isStale, latestGateTs };
  }

  async function resolveLinkedRun(review) {
    if (!review || (!review.runId && !review.issueId)) return null;
    let targetRunId = review.runId || '';
    if (!targetRunId && review.issueId) {
      const tasks = (crState.tasks && crState.tasks.length) ? crState.tasks : ((window.state && window.state.data && window.state.data.tasks) || []);
      const runs = (crState.runs && crState.runs.length) ? crState.runs : ((window.state && window.state.data && window.state.data.runs) || []);
      const matchingRunIds = new Set(tasks.filter(t => t.issueId === review.issueId && t.runId).map(t => t.runId));
      const matchingRuns = runs.filter(rn => matchingRunIds.has(rn.id));
      const waitingRun = matchingRuns.find(rn => rn.status === 'waiting');
      const picked = waitingRun || matchingRuns[0];
      if (picked) targetRunId = picked.id;
    }
    if (!targetRunId) return null;
    try {
      const full = await reqJSON('/api/runs/' + encodeURIComponent(targetRunId));
      if (full && full.id) return full;
    } catch (_) {}
    return crState.runs.find(rn => rn.id === targetRunId) || null;
  }

  async function loadCodeReviewView() {
    const root = document.getElementById('codereviewRoot');
    if (!root) return;
    crState.loading = true;
    render();
    try {
      const pid = encodeURIComponent(currentProjectId());
      const [listData, sumData, execData, repoData, issuesData, tasksData, runsData] = await Promise.all([
        reqJSON('/api/codereview/reviews?projectId=' + pid),
        reqJSON('/api/codereview/summary?projectId=' + pid),
        reqJSON('/api/codereview/executors').catch(() => ({ executors: [] })),
        reqJSON('/api/webuddy/kb/repos').catch(() => ({ repos: [] })),
        reqJSON('/api/issues').catch(() => []),
        reqJSON('/api/tasks').catch(() => []),
        reqJSON('/api/runs').catch(() => []),
      ]);
      crState.reviews = listData.reviews || [];
      crState.summary = sumData;
      crState.executors = execData.executors || [];
      crState.canPickDir = !!execData.canPickDir;
      crState.serverHost = execData.host || repoData.host || '';
      crState.repos = repoData.repos || [];
      // Dynamically pick the first available & logged-in reviewer (never hardcode a specific CLI).
      if (crState.executors.length > 0) {
        const d0 = activeDraft();
        const curExec = crState.executors.find(ex => ex.id === d0.reviewer);
        if (!curExec || !curExec.available || !curExec.loggedIn) {
          const defId = execData.defaultExecutor || '';
          const best =
            (defId && crState.executors.find(ex => ex.id === defId && ex.available)) ||
            crState.executors.find(ex => ex.available && ex.loggedIn) ||
            crState.executors.find(ex => ex.available);
          if (best) d0.reviewer = best.id;
        }
      }
      const rawPid = currentProjectId();
      crState.issues = (Array.isArray(issuesData) ? issuesData : []).filter(x => !rawPid || x.projectId === rawPid).slice(0, 30);
      crState.tasks = Array.isArray(tasksData) ? tasksData : [];
      const projectIssueIds = new Set(crState.issues.map(i => i.id));
      const projectRunIds = new Set(crState.tasks.filter(t => t.runId && projectIssueIds.has(t.issueId)).map(t => t.runId));
      crState.runs = (Array.isArray(runsData) ? runsData : []).filter(x => {
        if (!rawPid) return true;
        return x.projectId === rawPid
          || (x.executionContext && x.executionContext.projectId === rawPid)
          || (x.repository && x.repository.projectId === rawPid)
          || projectRunIds.has(x.id);
      }).slice(0, 30);
      const matchActive = rawPid && crState.repos.find(r => r.projectId === rawPid && r.exists);
      if (matchActive) {
        if (crState.selectedRepoPath !== matchActive.localPath) {
          crState.selectedRepoPath = matchActive.localPath;
          const d = activeDraft();
          d.baseRef = 'HEAD~1';
          d.headRef = 'HEAD';
          await loadGitRefs(crState.selectedRepoPath);
        }
      } else if (rawPid) {
        // Active project has no local repository: clear stale selection from previous project.
        crState.selectedRepoPath = '';
        crState.gitRefs = { isGit: false, branches: [], commits: [] };
      } else if (!crState.selectedRepoPath && crState.repos.length > 0) {
        const hit = crState.repos.find(r => r.exists && r.isGit) || crState.repos[0];
        if (hit) {
          crState.selectedRepoPath = hit.localPath;
          await loadGitRefs(crState.selectedRepoPath);
        }
      }
      if (crState.selectedId && !crState.reviews.some(r => r.id === crState.selectedId)) {
        crState.selectedId = '';
        crState.detail = null;
        crState.linkedRun = null;
      }
      if (!crState.selectedId && crState.reviews.length > 0) {
        crState.selectedId = crState.reviews[0].id;
      }
      if (crState.selectedId) {
        crState.detail = await reqJSON('/api/codereview/reviews/' + encodeURIComponent(crState.selectedId));
        crState.linkedRun = await resolveLinkedRun(crState.detail && crState.detail.review);
      } else {
        crState.detail = null;
        crState.linkedRun = null;
      }
    } catch (err) {
      notify(err.message);
    } finally {
      crState.loading = false;
      render();
    }
  }

  async function selectReview(id) {
    crState.selectedId = id;
    crState.showCreate = false;
    try {
      crState.detail = await reqJSON('/api/codereview/reviews/' + encodeURIComponent(id));
      crState.linkedRun = await resolveLinkedRun(crState.detail && crState.detail.review);
    } catch (err) {
      notify(err.message);
    }
    render();
  }

  function verdictBadge(v) {
    const norm = v || 'unknown';
    const label = norm === 'pass' ? '通过' : norm === 'fail' ? '阻断' : '未定/待核实';
    return '<span class="wb-badge ' + esc(norm) + '">' + label + '</span>';
  }

  function renderSummary() {
    const s = crState.summary || {};
    const rate = s.validRate == null ? '—' : s.validRate + '%';
    const mins = s.minutes == null ? '—' : s.minutes + ' 分钟';
    return (
      '<div class="wb-kpi-grid">' +
        '<div class="wb-kpi-card"><span class="wb-kpi-label">审查记录</span><span class="wb-kpi-val">' + crState.reviews.length + '</span></div>' +
        '<div class="wb-kpi-card"><span class="wb-kpi-label">AI 问题有效率</span><span class="wb-kpi-val">' + esc(rate) + '</span></div>' +
        '<div class="wb-kpi-card"><span class="wb-kpi-label">有效 / 误报 / 待核实</span><span class="wb-kpi-val">' + (s.valid || 0) + ' / ' + (s.falsePositive || 0) + ' / ' + (s.unassessed || 0) + '</span></div>' +
        '<div class="wb-kpi-card"><span class="wb-kpi-label">人工另行发现遗漏</span><span class="wb-kpi-val">' + (s.missed || 0) + '</span></div>' +
        '<div class="wb-kpi-card"><span class="wb-kpi-label">累计人工复核耗时</span><span class="wb-kpi-val">' + esc(mins) + '</span></div>' +
      '</div>'
    );
  }

  function defaultRepoPath() {
    const data = (window.state && window.state.data) || {};
    const repos = data.repositories || [];
    const pid = currentProjectId();
    const hit = (pid && repos.find(r => r.projectId === pid && r.localPath)) || repos.find(r => r.localPath);
    return (hit && hit.localPath) || '';
  }

  function renderCreateForm() {
    const isAuto = crState.createMode === 'auto';
    const d = activeDraft();
    const execList = crState.executors.length ? crState.executors : [
      { id: 'codex', name: 'Codex CLI', available: false },
      { id: 'claude', name: 'Claude Code CLI', available: false },
      { id: 'codebuddy', name: 'CodeBuddy CLI', available: false },
      { id: 'opencode', name: 'OpenCode CLI', available: false },
    ];
    const execOpts = execList.map(ex => {
      const statusTag = ex.available ? (ex.loggedIn ? '（已就绪）' : '（已安装·未登录）') : '（未安装）';
      return '<option value="' + esc(ex.id) + '" ' + (ex.id === d.reviewer ? 'selected' : '') + '>' + esc(ex.name) + statusTag + '</option>';
    }).join('');
    const modelCandidates = Array.from(new Set(execList.flatMap(ex => Array.isArray(ex.models) ? ex.models : [])));
    const modelDatalist = modelCandidates.length
      ? '<datalist id="wbCrModelList">' + modelCandidates.map(m => '<option value="' + esc(m) + '"></option>').join('') + '</datalist>'
      : '';

    const buildRefOptions = (defaultVal, defaultLabel, selectedVal) => {
      const items = [
        { ref: defaultVal, label: defaultLabel },
        ...crState.gitRefs.branches,
        ...crState.gitRefs.commits,
      ];
      return items.map(it =>
        '<option value="' + esc(it.ref) + '" ' + (it.ref === selectedVal ? 'selected' : '') + '>' + esc(it.label) + '</option>'
      ).join('');
    };

    return (
      '<div class="wb-panel">' +
        '<div class="wb-item-top" style="margin-bottom:10px">' +
          '<h3 style="margin:0">发起代码审查</h3>' +
          '<button type="button" class="button ghost small" id="wbCancelCreateReview">关闭</button>' +
        '</div>' +
        (crState.submitError ? '<div class="wb-finding-card" style="margin-bottom:10px;border-color:rgba(239,68,68,0.4);background:rgba(239,68,68,0.06);max-width:100%;box-sizing:border-box;"><div style="color:#dc2626;font-size:12.5px;line-height:1.5;white-space:normal;word-break:break-word;overflow-wrap:anywhere;"><strong>审查失败：</strong>' + esc(crState.submitError) + '</div></div>' : '') +
        (crState.runningAI && crState.activeJob ? (function () {
          const j = crState.activeJob;
          const total = j.totalBatches || 0;
          const done = j.completedBatches || 0;
          const pct = total > 0 ? Math.min(99, Math.max(5, Math.round((done / total) * 100))) : 8;
          const label = total > 0 ? ('正在审查第 ' + Math.min(done + 1, total) + ' / ' + total + ' 批') : '正在读取 Diff 并打包分批…';
          return (
            '<div class="wb-finding-card" style="margin-bottom:10px;border-color:rgba(59,130,246,0.35);background:rgba(59,130,246,0.05)">' +
              '<div class="wb-item-top" style="margin-bottom:6px"><strong style="color:#1d4ed8;font-size:13px">⚡ ' + esc(label) + '</strong><span class="wb-meta" style="margin:0">已耗时 ' + (j.elapsedSec || 0) + 's</span></div>' +
              '<div style="height:6px;border-radius:999px;background:rgba(59,130,246,0.14);overflow:hidden;margin-bottom:6px"><div style="height:100%;width:' + pct + '%;background:#2563eb;transition:width 0.35s ease"></div></div>' +
              '<div style="font-size:12px;color:var(--muted,#6b7280);white-space:normal;word-break:break-word">' + esc(j.currentHeader || '准备中…') + '</div>' +
            '</div>'
          );
        })() : '') +
        '<form id="wbCreateReviewForm" class="wb-form">' +
          '<div class="wb-toolbar" style="gap:8px;flex-wrap:wrap">' +
            '<select name="repoPath" id="wbCrRepoSelect" style="flex:1;min-width:200px">' +
              '<option value="">选一个项目仓库…</option>' +
              crState.repos.map(r => '<option value="' + esc(r.localPath) + '" ' + (r.localPath === crState.selectedRepoPath ? 'selected' : '') + '>' + esc(r.projectName) + '</option>').join('') +
            '</select>' +
            '<input type="text" name="title" value="' + esc(d.title) + '" placeholder="一句话说明改了啥（可不填）" style="flex:1;min-width:200px">' +
            '<button type="submit" class="button primary" ' + (crState.runningAI ? 'disabled' : '') + '>' +
              (crState.runningAI ? 'AI 正在审查…' : (isAuto ? '⚡ 一键审查最新改动' : '提交审查记录')) +
            '</button>' +
          '</div>' +
          '<details style="margin-top:10px"' + (!isAuto || d.diff || d.issueId || d.runId ? ' open' : '') + '>' +
            '<summary style="cursor:pointer;font-size:12.5px;color:var(--muted,#6b7280)">高级选项（换 AI / 选分支 / 关联需求 / 粘贴 Diff）</summary>' +
            '<div style="margin-top:10px;display:flex;flex-direction:column;gap:10px">' +
              '<div class="wb-tabs" style="border:none;padding:0">' +
                '<button type="button" class="wb-tab ' + (isAuto ? 'active' : '') + '" data-cr-mode="auto">AI 自动审查</button>' +
                '<button type="button" class="wb-tab ' + (!isAuto ? 'active' : '') + '" data-cr-mode="manual">手动录入</button>' +
                (crState.canPickDir ? '<button type="button" class="button ghost small" id="wbCrPickRepoBtn" style="margin-left:auto">+ 选本地目录</button>' : '') +
              '</div>' +
              '<div class="wb-form-row">' +
                '<div class="wb-field"><label>编写者</label><input type="text" name="writer" value="' + esc(d.writer) + '" placeholder="codex"></div>' +
                (isAuto
                  ? '<div class="wb-field"><label>审查 AI</label><select name="reviewer">' + execOpts + '</select></div>' +
                    '<div class="wb-field"><label>模型（留空自动）</label><input type="text" name="model" list="wbCrModelList" value="' + esc(d.model) + '" placeholder="自动默认">' + modelDatalist + '</div>'
                  : '<div class="wb-field"><label>审查者</label><input type="text" name="reviewer" value="' + esc(d.reviewer) + '" placeholder="reviewer"></div>') +
              '</div>' +
              '<div class="wb-form-row">' +
                '<div class="wb-field"><label>关联需求</label><select name="issueId">' +
                  '<option value="">不关联</option>' +
                  (d.issueId && !crState.issues.some(it => it.id === d.issueId)
                    ? '<option value="' + esc(d.issueId) + '" selected>' + esc(d.issueId) + '（已关联）</option>'
                    : '') +
                  crState.issues.map(it => '<option value="' + esc(it.id) + '" ' + (it.id === d.issueId ? 'selected' : '') + '>' + esc((it.key || it.id) + ' · ' + (it.title || '')) + '</option>').join('') +
                '</select></div>' +
                '<div class="wb-field"><label>关联运行</label><select name="runId">' +
                  '<option value="">不关联</option>' +
                  (d.runId && !crState.runs.some(rn => rn.id === d.runId)
                    ? '<option value="' + esc(d.runId) + '" selected>' + esc(d.runId.slice(0, 12)) + '（已关联）</option>'
                    : '') +
                  crState.runs.map(rn => '<option value="' + esc(rn.id) + '" ' + (rn.id === d.runId ? 'selected' : '') + '>' + esc(rn.id.slice(0, 12) + ' · ' + (rn.status || '')) + '</option>').join('') +
                '</select></div>' +
              '</div>' +
              (crState.gitRefs.isGit
                ? '<div class="wb-form-row">' +
                    '<div class="wb-field"><label>对比起点 (Base)</label><select name="baseRef">' + buildRefOptions('HEAD~1', '上一提交 (HEAD~1)', d.baseRef) + '</select></div>' +
                    '<div class="wb-field"><label>对比终点 (Head)</label><select name="headRef">' + buildRefOptions('HEAD', '最新提交 (HEAD)', d.headRef) + '</select></div>' +
                  '</div>'
                : '') +
              '<div class="wb-field"><label>手动粘贴 Diff（留空自动读取仓库）</label><textarea name="diff" rows="4" placeholder="diff --git a/... b/...">' + esc(d.diff) + '</textarea></div>' +
              (isAuto ? '' :
                '<div class="wb-form-row">' +
                  '<div class="wb-field"><label>阻断项（每行一条）</label><textarea name="blocking" rows="3" placeholder="auth.go:42 缺少校验">' + esc(d.blocking) + '</textarea></div>' +
                  '<div class="wb-field"><label>建议项（每行一条）</label><textarea name="advisory" rows="3" placeholder="建议补充测试">' + esc(d.advisory) + '</textarea></div>' +
                '</div>') +
            '</div>' +
          '</details>' +
        '</form>' +
      '</div>'
    );
  }

  const HUMAN_FILE_ROLES = [
    { key: 'production_code', label: '正式代码', hint: '核心业务与逻辑改动，建议重点复核', defaultOpen: true },
    { key: 'config', label: '配置与门禁', hint: '配置、构建、CI 与规则变更', defaultOpen: true },
    { key: 'process_artifact', label: '过程残余（应废弃）', hint: '临时日志/过程产物，合入前请确认清理', defaultOpen: true, warn: true },
    { key: 'test_code', label: '测试代码', hint: '测试文件改动，默认折叠可按需展开', defaultOpen: false },
    { key: 'spec_doc', label: '文档与说明', hint: 'Markdown / 说明文档，按需展开', defaultOpen: false },
  ];

  function classifyDiffFileRole(path) {
    const p = String(path || '').toLowerCase();
    if (/(^|\/)(tmp|temp|scratch|\.webuddy-local|logs?)(\/|$)|\.(log|tmp|bak|swp)$/.test(p)) return 'process_artifact';
    if (/(^|\/)(\.github\/workflows|dockerfile|docker-compose|makefile|eslint|prettier|tsconfig|golangci|pytest\.ini|pyproject\.toml|package\.json|go\.mod|go\.sum|vite\.config|webpack\.config|nginx)/.test(p) || /\.(ya?ml|toml|ini|env|conf)$/.test(p)) return 'config';
    if (/(^|\/)(tests?|__tests__|spec)(\/|$)|(_test\.go|\.test\.[jt]sx?|\.spec\.[jt]sx?|test_[^/]+\.py)$/.test(p)) return 'test_code';
    if (/\.(md|mdx|txt|rst)$|(^|\/)(readme|changelog|license|docs\/)/.test(p)) return 'spec_doc';
    return 'production_code';
  }

  function detectTamperSignals(fileBlocks) {
    const signals = [];
    for (const f of fileBlocks) {
      if (f.role === 'config' && /(workflow|eslint|golangci|tsconfig|dockerfile|gate|policy)/i.test(f.path)) {
        signals.push({ kind: '门禁/CI 配置被改', file: f.path, detail: '修改了构建、检查器或工作流配置，请人工确认未放宽门禁' });
      }
      for (const ln of f.lines) {
        if (!ln.startsWith('+') || ln.startsWith('+++')) continue;
        if (/@ts-ignore|@ts-nocheck|eslint-disable|nolint|#\s*noqa|#\s*type:\s*ignore|pragma:\s*no\s*cover/i.test(ln)) {
          signals.push({ kind: '新增抑制标记', file: f.path, detail: ln.slice(1).trim().slice(0, 90) });
          break;
        }
        if (f.role === 'test_code' && /\.(skip|only)\s*\(|t\.Skip\s*\(|@pytest\.mark\.skip|\bx(it|describe)\s*\(/.test(ln)) {
          signals.push({ kind: '测试被跳过或停用', file: f.path, detail: ln.slice(1).trim().slice(0, 90) });
          break;
        }
      }
    }
    return signals.slice(0, 8);
  }

  function renderColoredDiff(diffText) {
    if (!diffText || !diffText.trim()) return '';
    const rawBlocks = diffText.split(/(?=^diff --git )/m).filter(Boolean);
    const parsed = rawBlocks.map(block => {
      const lines = block.split('\n');
      const header = (lines[0] || '').replace(/^diff --git /, '');
      const m = header.match(/b\/(.+)$/);
      const path = (m && m[1]) || header || 'diff';
      const role = classifyDiffFileRole(path);
      let added = 0, removed = 0;
      const rendered = lines.slice(1).map(ln => {
        let cls = '';
        if (ln.startsWith('@@')) cls = 'hunk';
        else if (ln.startsWith('+') && !ln.startsWith('+++')) { cls = 'add'; added++; }
        else if (ln.startsWith('-') && !ln.startsWith('---')) { cls = 'del'; removed++; }
        return '<div class="wb-diff-line ' + cls + '">' + esc(ln) + '</div>';
      }).join('');
      return { path, header, role, added, removed, lines, rendered };
    });

    const signals = detectTamperSignals(parsed);
    const tamperBanner = signals.length
      ? '<div class="wb-finding-card" style="margin-bottom:12px;border-color:rgba(245,158,11,0.45);background:rgba(245,158,11,0.07)">' +
          '<div class="wb-item-top" style="margin-bottom:6px"><strong style="color:#b45309;font-size:13px">⚠ 人工重点关注 · 疑似门禁或测试弱化 (' + signals.length + ')</strong></div>' +
          signals.map(s =>
            '<div style="font-size:12.5px;line-height:1.5;margin-top:3px"><span class="wb-badge unknown" style="margin-right:6px">' + esc(s.kind) + '</span><code>' + esc(s.file) + '</code> — ' + esc(s.detail) + '</div>'
          ).join('') +
        '</div>'
      : '';

    const groupsHTML = HUMAN_FILE_ROLES.map(roleMeta => {
      const items = parsed.filter(f => f.role === roleMeta.key);
      if (!items.length) return '';
      const sumAdd = items.reduce((n, x) => n + x.added, 0);
      const sumDel = items.reduce((n, x) => n + x.removed, 0);
      const fileCards = items.map(f =>
        '<details class="wb-diff-file"' + (roleMeta.defaultOpen ? ' open' : '') + '>' +
          '<summary><code>' + esc(f.path) + '</code><span class="wb-actions"><span class="wb-badge pass">+' + f.added + '</span><span class="wb-badge fail">-' + f.removed + '</span></span></summary>' +
          '<div class="wb-diff-body">' + f.rendered + '</div>' +
        '</details>'
      ).join('');
      return (
        '<details class="wb-diff-role-group" style="margin-bottom:10px;border:1px solid var(--line,#e5e7eb);border-radius:10px;padding:8px 12px;' + (roleMeta.warn ? 'border-color:rgba(239,68,68,0.4);background:rgba(239,68,68,0.03);' : '') + '"' + (roleMeta.defaultOpen ? ' open' : '') + '>' +
          '<summary style="cursor:pointer;display:flex;align-items:center;justify-content:space-between;gap:8px;font-size:13px;font-weight:600;list-style:none">' +
            '<span>' + esc(roleMeta.label) + ' <span style="font-weight:400;color:var(--muted,#6b7280);font-size:12px">(' + items.length + ' 个文件 · ' + esc(roleMeta.hint) + ')</span></span>' +
            '<span class="wb-actions"><span class="wb-badge pass">+' + sumAdd + '</span><span class="wb-badge fail">-' + sumDel + '</span></span>' +
          '</summary>' +
          '<div class="wb-diff-files" style="margin-top:8px">' + fileCards + '</div>' +
        '</details>'
      );
    }).join('');

    return '<div class="wb-panel"><h3>人工分级 Diff 阅读器（按文件角色自动分组 · 共 ' + parsed.length + ' 个文件）</h3>' + tamperBanner + groupsHTML + '</div>';
  }

  async function createSampleReview() {
    const sampleDiff = [
      'diff --git a/internal/approval/handler.go b/internal/approval/handler.go',
      '--- a/internal/approval/handler.go',
      '+++ b/internal/approval/handler.go',
      '@@ -40,6 +40,12 @@ func (h *Handler) ApproveBill(c *gin.Context) {',
      ' \tbillID := c.Param("billid")',
      '+\t// 跳过租户校验直接更新审批状态',
      '+\tif err := h.repo.ForceApprove(billID); err != nil {',
      '+\t\tc.JSON(500, gin.H{"error": err.Error()})',
      '+\t\treturn',
      '+\t}',
      ' \tc.JSON(200, gin.H{"ok": true})',
      ' }',
      'diff --git a/internal/approval/audit.go b/internal/approval/audit.go',
      '--- a/internal/approval/audit.go',
      '+++ b/internal/approval/audit.go',
      '@@ -12,4 +12,6 @@ func RecordApproval(actor, billID string) {',
      '-\tlog.Printf("approved %s by %s", billID, actor)',
      '+\tlog.Printf("approved %s", billID)',
      ' }',
    ].join('\n');
    try {
      const created = await reqJSON('/api/codereview/reviews', {
        method: 'POST',
        body: JSON.stringify({
          projectId: currentProjectId(),
          title: '示例：单据审批权限边界审查',
          writer: 'codex',
          reviewer: 'claude-code',
          diff: sampleDiff,
          blocking: [
            'internal/approval/handler.go:42 越权风险：ForceApprove 未校验当前用户租户与待办归属，可跨租户直接审批任意单据',
          ],
          advisory: [
            'internal/approval/audit.go:13 审计日志移除了 actor 操作人字段，建议保留以便事后追溯',
          ],
        }),
      });
      notify('已生成示例审查记录，可体验 4 段闸门、彩色 Diff 与人工有效率反馈');
      crState.showCreate = false;
      crState.selectedId = created.id;
      await loadCodeReviewView();
    } catch (err) {
      notify(err.message);
    }
  }

  function renderDetail() {
    if (crState.showCreate) return renderCreateForm();
    if (!crState.detail || !crState.detail.review) {
      return (
        '<div class="wb-panel wb-empty">' +
          '<p style="margin:0 0 12px">选择左侧一条审查记录查看完整分批证据、彩色 Diff 与人工复核反馈，或立即体验一条示例审查。</p>' +
          '<div class="wb-actions" style="justify-content:center">' +
            '<button type="button" class="button primary" id="wbEmptyNewCR">发起代码审查</button>' +
            '<button type="button" class="button ghost" id="wbSampleCRBtn">一键生成示例审查体验</button>' +
          '</div>' +
        '</div>'
      );
    }
    const r = crState.detail.review;
    const snap = crState.detail.snapshot;
    const feedbacks = crState.detail.feedback || [];

    const partsRows = (r.parts || []).map(p =>
      '<tr><td>#' + p.part + '</td><td class="wb-code" style="border:none;padding:4px;background:transparent">' + esc(p.file) + '</td><td>' + p.characters + '</td><td>' + verdictBadge(p.verdict) + '</td><td><code>' + esc((p.sha256 || '').slice(0, 10)) + '</code></td></tr>'
    ).join('');

    const findings = (snap && snap.findings) || [];
    const findingInputs = findings.map(f =>
      '<div class="wb-finding-card" data-finding-id="' + esc(f.id) + '">' +
        '<div class="wb-item-top"><span class="wb-badge ' + (f.kind === 'blocking' ? 'fail' : 'info') + '">' + (f.kind === 'blocking' ? '阻断' : '建议') + ' · ' + esc(f.id) + '</span></div>' +
        '<div style="font-size:13px">' + esc(f.detail) + '</div>' +
        '<div class="wb-form-row">' +
          '<select class="wb-finding-verdict">' +
            '<option value="valid">有效问题 (valid)</option>' +
            '<option value="false_positive">误报 (false_positive)</option>' +
            '<option value="unassessed">待核实 (unassessed)</option>' +
          '</select>' +
          '<input type="text" class="wb-finding-reason" placeholder="填写判断依据（有效/误报必填）">' +
        '</div>' +
      '</div>'
    ).join('');

    const fbHistory = feedbacks.length === 0
      ? '<div class="wb-empty" style="padding:12px">暂无人工复核效果记录</div>'
      : feedbacks.map(fb =>
          '<div class="wb-finding-card">' +
            '<div class="wb-item-top"><strong>' + esc(fb.actor) + '</strong><span class="wb-badge ' + (fb.current ? 'pass' : 'unknown') + '">' + (fb.current ? '绑定当前快照' : '历史快照') + '</span></div>' +
            '<div class="wb-meta"><span>耗时 ' + fb.minutes + ' 分钟</span><span>遗漏 ' + (fb.missed || []).length + ' 项</span><span>' + esc(new Date(fb.createdAt).toLocaleString()) + '</span></div>' +
            '<div style="font-size:13px">' + esc(fb.note) + '</div>' +
          '</div>'
        ).join('');

    const gateTrackHTML = window.WebuddyVisuals ? window.WebuddyVisuals.renderReviewGateTrack(r, crState.summary) : '';
    const hasCurrentFb = feedbacks.some(fb => fb.current);
    const linkedTask = !!(r.issueId || r.runId);
    const gate = inspectApprovalGate(crState.linkedRun, r);
    const runStatusMap = {
      queued: '排队中',
      running: '执行中',
      waiting: '等待人工确认',
      succeeded: '已完成',
      failed: '已失败',
      cancelled: '已取消',
    };
    const runStatusText = runStatusMap[gate.status] || gate.status || '未知';
    const targetRunId = (crState.linkedRun && crState.linkedRun.id) || r.runId || '';

    let nextActionText = '';
    let nextBannerColor = r.verdict === 'fail' ? '#ef4444' : r.verdict === 'pass' ? '#10b981' : '#f59e0b';
    if (gate.waiting && gate.isStale) {
      nextBannerColor = '#f59e0b';
      nextActionText = '旧版本审查已过期：关联运行 ' + targetRunId.slice(0, 12) + ' 本轮进入人工确认节点「' + gate.nodeName + '」的时间晚于本次审查。旧版本结论不自动代替新版本确认，请基于最新改动重新发起代码审查，再前往「待我确认」签收。';
    } else if (r.verdict === 'fail') {
      nextActionText = gate.waiting
        ? '存在 ' + (r.blocking || []).length + ' 项阻断问题，且关联运行 ' + targetRunId.slice(0, 12) + ' 正停在人工确认节点「' + gate.nodeName + '」：请核对下方彩色 Diff 后前往该确认项退回上游返工，修复后重新审查。'
        : '存在 ' + (r.blocking || []).length + ' 项阻断问题：请先核对下方彩色 Diff 与阻断项，确认无误报后返工修复并重新发起审查。';
    } else if (!r.complete) {
      nextActionText = '审查存在未覆盖批次或二进制文件：请人工重点核对未覆盖项后记录结论。';
    } else if (!linkedTask) {
      nextActionText = hasCurrentFb
        ? '独立审查记录（未关联需求或运行）：AI 审查已通过且已完成人工复核反馈，结论已计入项目有效率统计。'
        : '独立审查记录（未关联需求或运行）：AI 审查已通过，请核对下方改动并在本页提交人工复核反馈。';
    } else if (gate.waiting) {
      nextActionText = hasCurrentFb
        ? '关联运行 ' + targetRunId.slice(0, 12) + ' 当前正停在人工确认节点「' + gate.nodeName + '」，本版本 AI 审查与人工复核均已完成，可立即前往「待我确认」执行批准或退回。'
        : '关联运行 ' + targetRunId.slice(0, 12) + ' 当前正停在人工确认节点「' + gate.nodeName + '」：建议先在本页提交人工复核反馈，再前往「待我确认」执行批准或退回。';
    } else if (gate.hasRun) {
      nextActionText = '审查已记录，流程尚未到达人工确认节点（关联运行 ' + targetRunId.slice(0, 12) + ' 当前状态：' + runStatusText + '）。待流程运行到人工确认节点后，即可在「待我确认」中携带本审查证据完成签收。';
    } else {
      nextActionText = '审查已记录（已关联需求 ' + r.issueId + '），但该需求当前尚无停在人工确认节点的运行，流程尚未到达人工确认阶段。';
    }

    const nextButtons =
      (gate.waiting && gate.isStale
        ? '<button type="button" class="button primary small" id="wbCrRerunReview">基于最新改动重新审查</button>'
        : '') +
      (gate.waiting
        ? '<button type="button" class="button ' + (gate.isStale ? 'ghost' : 'primary') + ' small" id="wbCrOpenApprovalCard" data-run-id="' + esc(targetRunId) + '">' + (r.verdict === 'fail' ? '前往此确认项（退回返工） →' : '前往此确认项 →') + '</button>'
        : '') +
      (r.issueId ? '<button type="button" class="button ghost small" id="wbCrOpenIssue">打开关联需求</button>' : '') +
      (targetRunId ? '<button type="button" class="button ghost small" id="wbCrOpenRun">打开关联运行</button>' : '') +
      '<button type="button" class="button ghost small" id="wbCrPublishSharedKB">📚 沉淀为团队共享知识</button>';

    const currentFb = feedbacks.find(fb => fb.current);
    const receiptHTML = currentFb
      ? '<div class="wb-panel" style="border-color:rgba(16,185,129,0.45);background:rgba(16,185,129,0.06)">' +
          '<div class="wb-item-top" style="margin-bottom:6px">' +
            '<div style="display:flex;align-items:center;gap:8px"><strong style="color:#065f46">✓ 已人工复核签收</strong><span class="wb-badge pass">绑定当前快照</span></div>' +
            '<button type="button" class="button ghost small" id="wbCrCopyBindingFp" data-fp="' + esc(r.binding || '') + '">复制凭证指纹 ' + esc((r.binding || '').slice(0, 10)) + '</button>' +
          '</div>' +
          '<div class="wb-meta" style="margin-bottom:6px"><span>签收人：<b>' + esc(currentFb.actor) + '</b></span><span>复核耗时：' + currentFb.minutes + ' 分钟</span><span>发现遗漏：' + (currentFb.missed || []).length + ' 项</span><span>' + esc(new Date(currentFb.createdAt).toLocaleString()) + '</span></div>' +
          (currentFb.note ? '<div style="font-size:13px;background:rgba(255,255,255,0.75);border:1px solid rgba(16,185,129,0.25);border-radius:8px;padding:8px 10px">' + esc(currentFb.note) + '</div>' : '') +
        '</div>'
      : '';

    const quickReasonPills = [
      { label: '核心逻辑已逐行核对通过', text: '已逐行核对正式代码与配置变更，边界条件与权限校验符合预期，确认放行。' },
      { label: 'AI 阻断项属实，需返工', text: '已核实 AI 提出的阻断问题属实，需退回修复后重新发起审查。' },
      { label: 'AI 提示为误报，业务允许', text: '已结合业务上下文核对，AI 提示属于误报，当前实现符合业务约定。' },
      { label: '发现 AI 遗漏边界条件', text: '已人工补充核查，发现 AI 遗漏边界场景，已在上方遗漏项中记录。' },
    ].map((p, idx) =>
      '<button type="button" class="button ghost small wb-cr-reason-pill" data-reason="' + esc(p.text) + '" style="border-style:dashed;font-size:12px">+ ' + esc(p.label) + '</button>'
    ).join('');

    return (
      '<div style="display:flex;flex-direction:column;gap:14px">' +
        receiptHTML +
        '<div class="wb-panel" style="border-left:4px solid ' + nextBannerColor + '">' +
          '<div class="wb-item-top"><strong>下一步动作</strong><div class="wb-actions">' + nextButtons + verdictBadge(r.verdict) + '</div></div>' +
          '<div style="font-size:13px;margin-top:4px">' + esc(nextActionText) + '</div>' +
        '</div>' +
        gateTrackHTML +
        '<div class="wb-panel">' +
          '<div class="wb-item-top" style="margin-bottom:10px">' +
            '<h3 style="margin:0;font-size:16px">' + esc(r.title) + '</h3>' +
            '<div class="wb-actions">' +
              verdictBadge(r.verdict) +
              '<span class="wb-badge ' + (r.differentExecutor ? 'pass' : 'unknown') + '">' + (r.differentExecutor ? '独立审查 (Writer≠Reviewer)' : '同执行器') + '</span>' +
              '<span class="wb-badge ' + (r.complete ? 'pass' : 'unknown') + '">' + (r.complete ? '完整覆盖' : '存在未覆盖/二进制') + '</span>' +
            '</div>' +
          '</div>' +
          '<div class="wb-meta">' +
            '<span>Writer: <b>' + esc(r.writer) + '</b></span>' +
            '<span>Reviewer: <b>' + esc(r.reviewer) + '</b></span>' +
            '<span>Base: <code>' + esc((r.baseCommit || '').slice(0, 12)) + '</code></span>' +
            '<span>Diff SHA256: <code>' + esc((r.diffSha256 || '').slice(0, 12)) + '</code></span>' +
            '<span>Binding: <code>' + esc((r.binding || '').slice(0, 12)) + '</code></span>' +
          '</div>' +
          ((r.uncovered || []).length ? '<div style="margin-top:10px;color:#f59e0b;font-size:12.5px">未覆盖提示：' + esc(r.uncovered.join('；')) + '</div>' : '') +
        '</div>' +

        '<div class="wb-panel">' +
          '<h3>分批覆盖清单（共 ' + r.totalParts + ' 批，单批上限 14,000 字符）</h3>' +
          '<table class="wb-table"><thead><tr><th>批次</th><th>文件头</th><th>字符数</th><th>结论</th><th>SHA256</th></tr></thead><tbody>' + partsRows + '</tbody></table>' +
        '</div>' +

        renderColoredDiff(r.diffPreview) +

        '<div class="wb-panel">' +
          '<h3>人工 Review 效果反馈（精确绑定当前快照）</h3>' +
          '<form id="wbFeedbackForm" class="wb-form">' +
            (findings.length ? findingInputs : '<div class="wb-meta">本条审查无阻断/建议项，可直接记录人工复核范围与遗漏项。</div>') +
            '<div class="wb-form-row">' +
              '<div class="wb-field"><label>实际人工复核分钟数 (0-1440)</label><input type="number" name="minutes" min="0" max="1440" value="10" required></div>' +
              '<div class="wb-field"><label>人工另行发现的遗漏问题（每行一条，可选）</label><textarea name="missed" rows="2" placeholder="AI 漏报的问题"></textarea></div>' +
            '</div>' +
            '<div class="wb-field"><label>复核范围与说明（必填）</label>' +
              '<div style="display:flex;flex-wrap:wrap;gap:6px;margin-bottom:6px">' + quickReasonPills + '</div>' +
              '<textarea name="note" id="wbCrFeedbackNote" rows="2" required placeholder="说明实际核对的文件范围与结论（也可点击上方常用理由快速填入）"></textarea>' +
            '</div>' +
            '<div class="wb-actions"><button type="submit" class="button primary">提交人工反馈</button></div>' +
          '</form>' +
        '</div>' +

        '<div class="wb-panel">' +
          '<h3>已提交的人工反馈记录 (' + feedbacks.length + ')</h3>' +
          '<div style="display:flex;flex-direction:column;gap:8px">' + fbHistory + '</div>' +
        '</div>' +
      '</div>'
    );
  }

  function render() {
    const root = document.getElementById('codereviewRoot');
    if (!root) return;
    const listHTML = crState.reviews.length === 0
      ? '<div class="wb-empty">暂无审查记录</div>'
      : crState.reviews.map(r =>
          '<button type="button" class="wb-list-item ' + (r.id === crState.selectedId && !crState.showCreate ? 'active' : '') + '" data-cr-id="' + esc(r.id) + '">' +
            '<div class="wb-item-top"><span class="wb-item-title">' + esc(r.title) + '</span>' + verdictBadge(r.verdict) + '</div>' +
            '<div class="wb-meta"><span>' + esc(r.writer) + ' → ' + esc(r.reviewer) + '</span><span>' + r.totalParts + ' 批</span><span>阻断 ' + (r.blocking || []).length + '</span></div>' +
          '</button>'
        ).join('');

    root.innerHTML =
      '<div class="wb-page">' +
        '<div class="wb-header">' +
          '<div><h1>代码审查</h1><p class="wb-subtitle">选好项目，点一下让 AI 自动检查最新代码改动。</p></div>' +
          '<div class="wb-actions">' +
            '<button type="button" class="button ghost" id="wbRefreshCR">刷新</button>' +
            '<button type="button" class="button primary" id="wbNewCRBtn">⚡ 发起审查</button>' +
          '</div>' +
        '</div>' +
        renderSummary() +
        '<div class="wb-split">' +
          '<div class="wb-panel"><h3>审查列表</h3><div class="wb-list">' + listHTML + '</div></div>' +
          '<div>' + renderDetail() + '</div>' +
        '</div>' +
      '</div>';

    root.querySelector('#wbRefreshCR')?.addEventListener('click', loadCodeReviewView);
    root.querySelector('#wbNewCRBtn')?.addEventListener('click', () => { crState.showCreate = true; render(); });
    root.querySelector('#wbEmptyNewCR')?.addEventListener('click', () => { crState.showCreate = true; render(); });
    root.querySelector('#wbSampleCRBtn')?.addEventListener('click', createSampleReview);
    root.querySelector('#wbCancelCreateReview')?.addEventListener('click', () => { resetDraft(); crState.showCreate = false; render(); });
    root.querySelectorAll('[data-cr-id]').forEach(btn => {
      btn.addEventListener('click', () => selectReview(btn.dataset.crId));
    });

    root.querySelectorAll('[data-cr-mode]').forEach(btn => {
      btn.addEventListener('click', () => { syncDraftFromDOM(); crState.createMode = btn.dataset.crMode; render(); });
    });
    root.querySelector('#wbCrOpenApprovalCard')?.addEventListener('click', ev => {
      const rid = ev.currentTarget.dataset.runId || '';
      if (rid && typeof window.openApprovalForRun === 'function') window.openApprovalForRun(rid);
      else if (typeof window.switchView === 'function') window.switchView('approvals');
    });
    root.querySelector('#wbCrRerunReview')?.addEventListener('click', () => {
      const r = crState.detail && crState.detail.review;
      const d = activeDraft();
      d.issueId = (r && r.issueId) || '';
      d.runId = (crState.linkedRun && crState.linkedRun.id) || (r && r.runId) || '';
      d.title = r && r.title ? (r.title.replace(/（返工复审）$/, '') + '（返工复审）') : '';
      d.diff = '';
      crState.showCreate = true;
      render();
    });
    root.querySelector('#wbCrOpenIssue')?.addEventListener('click', () => {
      const iid = crState.detail?.review?.issueId;
      if (iid && typeof window.switchView === 'function') {
        window.switchView('issues');
        if (typeof window.openIssueDetail === 'function') setTimeout(() => window.openIssueDetail(iid), 200);
      }
    });
    root.querySelector('#wbCrOpenRun')?.addEventListener('click', () => {
      const rid = (crState.linkedRun && crState.linkedRun.id) || crState.detail?.review?.runId;
      if (rid && typeof window.switchView === 'function') {
        window.switchView('runs');
        if (typeof window.showRunDetail === 'function') setTimeout(() => window.showRunDetail(rid), 200);
      }
    });
    root.querySelector('#wbCrPickRepoBtn')?.addEventListener('click', () => { syncDraftFromDOM(); pickRepoForReview(); });
    root.querySelector('#wbCrRepoSelect')?.addEventListener('change', async e => {
      syncDraftFromDOM(e);
      crState.selectedRepoPath = e.target.value;
      const d = activeDraft();
      d.baseRef = 'HEAD~1';
      d.headRef = 'HEAD';
      await loadGitRefs(crState.selectedRepoPath);
      render();
    });

    const createForm = root.querySelector('#wbCreateReviewForm');
    if (createForm) {
      createForm.addEventListener('input', syncDraftFromDOM);
      createForm.addEventListener('change', syncDraftFromDOM);
      createForm.addEventListener('submit', async e => {
        e.preventDefault();
        syncDraftFromDOM();
        crState.submitError = '';
        const d = activeDraft();
        const lines = s => String(s || '').split('\n').map(x => x.trim()).filter(Boolean);
        const isAuto = crState.createMode === 'auto';
        crState.runningAI = isAuto;
        if (isAuto) render();
        try {
          const url = isAuto ? '/api/codereview/reviews/run' : '/api/codereview/reviews';
          const isPasteOnly = !!(d.diff && d.diff.trim());
          const pickedRepo = !isPasteOnly ? crState.repos.find(r => r.localPath === crState.selectedRepoPath) : null;
          const payload = {
            projectId: isPasteOnly ? currentProjectId() : ((pickedRepo && pickedRepo.projectId) || currentProjectId()),
            issueId: d.issueId || '',
            runId: d.runId || '',
            title: (d.title || '').trim() || '最新代码改动审查',
            writer: d.writer || 'developer',
            reviewer: d.reviewer,
            model: d.model || '',
            repoPath: isPasteOnly ? '' : crState.selectedRepoPath,
            baseRef: !isPasteOnly && crState.gitRefs.isGit ? d.baseRef : '',
            headRef: !isPasteOnly && crState.gitRefs.isGit ? d.headRef : '',
            diff: d.diff,
          };
          if (isAuto) {
            payload.async = true;
          } else {
            payload.blocking = lines(d.blocking);
            payload.advisory = lines(d.advisory);
          }
          const resp = await reqJSON(url, {
            method: 'POST',
            body: JSON.stringify(payload),
          });
          let created = resp;
          if (isAuto && resp && resp.jobId) {
            crState.activeJob = resp;
            render();
            while (true) {
              await new Promise(resolve => setTimeout(resolve, 2000));
              const st = await reqJSON('/api/codereview/jobs/' + encodeURIComponent(resp.jobId));
              crState.activeJob = st;
              if (st.status === 'succeeded' && st.review) {
                created = st.review;
                break;
              }
              if (st.status === 'failed') {
                throw new Error(st.error || 'AI 审查执行失败');
              }
              render();
            }
          }
          notify('审查完成（' + created.verdict + '），共 ' + created.totalParts + ' 批');
          resetDraft();
          crState.activeJob = null;
          crState.showCreate = false;
          crState.selectedId = created.id;
          await loadCodeReviewView();
        } catch (err) {
          crState.submitError = err.message;
          crState.activeJob = null;
          notify(err.message);
        } finally {
          crState.runningAI = false;
          render();
        }
      });
    }

    root.querySelector('#wbCrCopyBindingFp')?.addEventListener('click', e => {
      const fp = e.currentTarget.dataset.fp || '';
      if (fp && navigator.clipboard) {
        navigator.clipboard.writeText(fp).then(() => notify('已复制复核凭证指纹 ' + fp.slice(0, 12))).catch(() => {});
      }
    });
    root.querySelectorAll('.wb-cr-reason-pill').forEach(btn => {
      btn.addEventListener('click', () => {
        const ta = root.querySelector('#wbCrFeedbackNote');
        if (ta) {
          ta.value = btn.dataset.reason || '';
          ta.focus();
        }
      });
    });

    const fbForm = root.querySelector('#wbFeedbackForm');
    root.querySelector('#wbCrPublishSharedKB')?.addEventListener('click', async () => {
      if (!crState.detail || !crState.detail.review) return;
      const rev = crState.detail.review;
      const blockingText = (rev.blocking || []).map((b, i) => `${i + 1}. ${b}`).join('\n');
      const uncoveredText = (rev.uncovered || []).map((u, i) => `- ${u}`).join('\n');
      let bodyText = blockingText;
      if (!bodyText) {
        if (rev.verdict === 'pass' && rev.complete) {
          bodyText = `审查通过（共 ${rev.totalParts} 批 Diff 全量覆盖，Base: ${rev.baseCommit || '-'}）。`;
        } else {
          const statusLabel = rev.verdict === 'fail' ? '阻断未通过' : '待核实 / 未完全覆盖';
          bodyText = `审查结论：${statusLabel}（共 ${rev.totalParts} 批，Base: ${rev.baseCommit || '-'}）。` + (uncoveredText ? `\n\n未覆盖/异常说明：\n${uncoveredText}` : '');
        }
      }
      try {
        await reqJSON('/api/webuddy/kb/shared', {
          method: 'POST',
          body: JSON.stringify({
            title: `代码审查沉淀：${rev.title}`,
            summary: `结论: ${rev.verdict} · Writer(${rev.writer}) / Reviewer(${rev.reviewer}) · 阻断项 ${(rev.blocking || []).length} 个`,
            content: bodyText,
            sourceType: 'code_review',
            sourceProjectId: rev.projectId || '',
            commit: rev.baseCommit || '',
            tags: ['代码审查', rev.verdict || 'review'],
          }),
        });
        notify('✅ 已沉淀至当前租户「团队共享知识库」，AI 对话与团队成员均可检索');
      } catch (err) {
        notify(err.message || '沉淀失败');
      }
    });
    if (fbForm && crState.detail && crState.detail.snapshot) {
      fbForm.addEventListener('submit', async e => {
        e.preventDefault();
        const fd = new FormData(fbForm);
        const findings = [];
        fbForm.querySelectorAll('[data-finding-id]').forEach(card => {
          findings.push({
            id: card.dataset.findingId,
            verdict: card.querySelector('.wb-finding-verdict').value,
            reason: card.querySelector('.wb-finding-reason').value,
          });
        });
        const missed = String(fd.get('missed') || '').split('\n').map(x => x.trim()).filter(Boolean);
        try {
          await reqJSON('/api/codereview/reviews/' + encodeURIComponent(crState.selectedId) + '/feedback', {
            method: 'POST',
            body: JSON.stringify({
              requestId: 'ui-' + Date.now() + '-' + Math.random().toString(36).slice(2, 8),
              binding: crState.detail.snapshot.binding,
              minutes: parseInt(fd.get('minutes'), 10),
              findings,
              missed,
              note: fd.get('note'),
            }),
          });
          notify('人工 Review 反馈已记录');
          await loadCodeReviewView();
        } catch (err) {
          notify(err.message);
        }
      });
    }
  }

  window.loadCodeReviewView = loadCodeReviewView;
  window.openWebuddyCodeReview = async function (reviewId) {
    crState.selectedId = reviewId || '';
    crState.showCreate = false;
    if (typeof window.switchView === 'function') window.switchView('codereview');
    else await loadCodeReviewView();
  };
  window.openNewWebuddyCodeReview = async function (opts) {
    const o = opts || {};
    const d = activeDraft();
    if (o.issueId !== undefined) d.issueId = o.issueId || '';
    if (o.runId !== undefined) d.runId = o.runId || '';
    if (o.title) d.title = o.title;
    crState.showCreate = true;
    crState.submitError = '';
    if (typeof window.switchView === 'function') window.switchView('codereview');
    else await loadCodeReviewView();
  };
})();
