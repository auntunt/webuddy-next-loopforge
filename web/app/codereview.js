/* Webuddy v2 Code Review & Human Effectiveness Feedback Workbench */
(function () {
  const crState = {
    reviews: [],
    executors: [],
    repos: [],
    selectedRepoPath: '',
    gitRefs: { isGit: false, branches: [], commits: [] },
    diffSource: 'repo', // repo | paste
    selectedId: '',
    detail: null,
    summary: null,
    showCreate: false,
    createMode: 'auto', // auto | manual
    runningAI: false,
    loading: false,
  };

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

  async function loadCodeReviewView() {
    const root = document.getElementById('codereviewRoot');
    if (!root) return;
    crState.loading = true;
    render();
    try {
      const pid = encodeURIComponent(currentProjectId());
      const [listData, sumData, execData, repoData] = await Promise.all([
        reqJSON('/api/codereview/reviews?projectId=' + pid),
        reqJSON('/api/codereview/summary?projectId=' + pid),
        reqJSON('/api/codereview/executors').catch(() => ({ executors: [] })),
        reqJSON('/api/webuddy/kb/repos').catch(() => ({ repos: [] })),
      ]);
      crState.reviews = listData.reviews || [];
      crState.summary = sumData;
      crState.executors = execData.executors || [];
      crState.repos = repoData.repos || [];
      if (!crState.selectedRepoPath && crState.repos.length > 0) {
        const activePid = currentProjectId();
        const hit = (activePid && crState.repos.find(r => r.projectId === activePid && r.exists)) || crState.repos.find(r => r.exists && r.isGit) || crState.repos[0];
        if (hit) {
          crState.selectedRepoPath = hit.localPath;
          await loadGitRefs(crState.selectedRepoPath);
        }
      }
      if (crState.selectedId && !crState.reviews.some(r => r.id === crState.selectedId)) {
        crState.selectedId = '';
        crState.detail = null;
      }
      if (!crState.selectedId && crState.reviews.length > 0) {
        crState.selectedId = crState.reviews[0].id;
      }
      if (crState.selectedId) {
        crState.detail = await reqJSON('/api/codereview/reviews/' + encodeURIComponent(crState.selectedId));
      } else {
        crState.detail = null;
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
    const execOpts = (crState.executors.length ? crState.executors : [
      { id: 'codex', name: 'Codex CLI', available: false },
      { id: 'claude', name: 'Claude Code CLI', available: false },
      { id: 'codebuddy', name: 'CodeBuddy CLI', available: false },
      { id: 'opencode', name: 'OpenCode CLI', available: false },
    ]).map(ex =>
      '<option value="' + esc(ex.id) + '">' + esc(ex.name) + (ex.available ? '（本机已安装）' : '（未检测到）') + '</option>'
    ).join('');

    return (
      '<div class="wb-panel">' +
        '<div class="wb-item-top" style="margin-bottom:12px">' +
          '<h3 style="margin:0">发起完整 Diff 分批审查</h3>' +
          '<div class="wb-tabs" style="border:none;padding:0">' +
            '<button type="button" class="wb-tab ' + (isAuto ? 'active' : '') + '" data-cr-mode="auto">自动 AI 分批审查（CLI）</button>' +
            '<button type="button" class="wb-tab ' + (!isAuto ? 'active' : '') + '" data-cr-mode="manual">手动录入审查结果</button>' +
          '</div>' +
        '</div>' +
        '<form id="wbCreateReviewForm" class="wb-form">' +
          '<div class="wb-form-row">' +
            '<div class="wb-field"><label>审查标题</label><input type="text" name="title" placeholder="例如：订单支付权限边界审查"></div>' +
            '<div class="wb-field"><label>编写者 (Writer)</label><input type="text" name="writer" value="codex" placeholder="codex / developer"></div>' +
            (isAuto
              ? '<div class="wb-field"><label>独立审查 CLI 执行器 (Reviewer)</label><select name="reviewer">' + execOpts + '</select></div>' +
                '<div class="wb-field"><label>模型（可选，留空走 CLI 默认）</label><input type="text" name="model" placeholder="例如 gpt-5 / claude-sonnet-4"></div>'
              : '<div class="wb-field"><label>独立审查者 (Reviewer)</label><input type="text" name="reviewer" value="claude-code" placeholder="与编写者分离"></div>') +
          '</div>' +
          '<div class="wb-form-row">' +
            '<div class="wb-field" style="grid-column:span 2"><label>选择本地项目仓库（免手填路径）</label>' +
              '<div class="wb-toolbar">' +
                '<select name="repoPath" id="wbCrRepoSelect" style="flex:1;min-width:220px">' +
                  '<option value="">不使用本地仓库（改用下方粘贴 Diff）</option>' +
                  crState.repos.map(r => '<option value="' + esc(r.localPath) + '" ' + (r.localPath === crState.selectedRepoPath ? 'selected' : '') + '>' + esc(r.projectName + (r.isGit ? ' · Git' : ' · 非 Git 目录')) + '</option>').join('') +
                '</select>' +
                '<button type="button" class="button ghost" id="wbCrPickRepoBtn">+ 选择本地目录</button>' +
              '</div>' +
            '</div>' +
          '</div>' +
          (crState.gitRefs.isGit
            ? '<div class="wb-form-row">' +
                '<div class="wb-field"><label>基准提交 / 分支 (Base)</label><select name="baseRef">' +
                  '<option value="HEAD~1">HEAD~1（上一提交）</option>' +
                  crState.gitRefs.branches.map(b => '<option value="' + esc(b.ref) + '">' + esc(b.label) + '</option>').join('') +
                  crState.gitRefs.commits.map(c => '<option value="' + esc(c.ref) + '">' + esc(c.label) + '</option>').join('') +
                '</select></div>' +
                '<div class="wb-field"><label>目标提交 / 分支 (Head)</label><select name="headRef">' +
                  '<option value="HEAD">HEAD（当前最新提交）</option>' +
                  crState.gitRefs.branches.map(b => '<option value="' + esc(b.ref) + '">' + esc(b.label) + '</option>').join('') +
                  crState.gitRefs.commits.map(c => '<option value="' + esc(c.ref) + '">' + esc(c.label) + '</option>').join('') +
                '</select></div>' +
              '</div>'
            : '<div class="wb-meta">当前所选目录不是 Git 仓库（或未选择仓库），可直接在下方粘贴 Unified Diff 进行 14k 分批审查。</div>') +
          '<div class="wb-field"><label>或直接粘贴 Unified Diff（留空则自动读取上方所选仓库的 Git Diff）</label><textarea name="diff" rows="5" placeholder="diff --git a/... b/..."></textarea></div>' +
          (isAuto ? '' :
            '<div class="wb-form-row">' +
              '<div class="wb-field"><label>阻断项（每行一条：文件:行号 + 触发条件与影响）</label><textarea name="blocking" rows="3" placeholder="auth.go:42 缺少租户隔离校验"></textarea></div>' +
              '<div class="wb-field"><label>建议项（每行一条，不阻断交付）</label><textarea name="advisory" rows="3" placeholder="建议补充边界测试"></textarea></div>' +
            '</div>') +
          '<div class="wb-actions">' +
            '<button type="submit" class="button primary" ' + (crState.runningAI ? 'disabled' : '') + '>' +
              (crState.runningAI ? '正在分批调用 CLI 审查…' : (isAuto ? '运行自动分批 AI 审查' : '提交并生成防篡改审查快照')) +
            '</button>' +
            '<button type="button" class="button ghost" id="wbCancelCreateReview">取消</button>' +
          '</div>' +
        '</form>' +
      '</div>'
    );
  }

  function renderColoredDiff(diffText) {
    if (!diffText || !diffText.trim()) return '';
    const blocks = diffText.split(/(?=^diff --git )/m).filter(Boolean);
    const cards = blocks.map(block => {
      const lines = block.split('\n');
      const header = (lines[0] || '').replace(/^diff --git /, '');
      let added = 0, removed = 0;
      const rendered = lines.slice(1).map(ln => {
        let cls = '';
        if (ln.startsWith('@@')) cls = 'hunk';
        else if (ln.startsWith('+') && !ln.startsWith('+++')) { cls = 'add'; added++; }
        else if (ln.startsWith('-') && !ln.startsWith('---')) { cls = 'del'; removed++; }
        return '<div class="wb-diff-line ' + cls + '">' + esc(ln) + '</div>';
      }).join('');
      return (
        '<details class="wb-diff-file" open>' +
          '<summary><code>' + esc(header || 'diff') + '</code><span class="wb-actions"><span class="wb-badge pass">+' + added + '</span><span class="wb-badge fail">-' + removed + '</span></span></summary>' +
          '<div class="wb-diff-body">' + rendered + '</div>' +
        '</details>'
      );
    }).join('');
    return '<div class="wb-panel"><h3>彩色 Unified Diff 逐行阅读器</h3><div class="wb-diff-files">' + cards + '</div></div>';
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

    return (
      '<div style="display:flex;flex-direction:column;gap:14px">' +
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
            '<div class="wb-field"><label>复核范围与说明（必填）</label><textarea name="note" rows="2" required placeholder="说明实际核对的文件范围与结论"></textarea></div>' +
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
          '<div><h1>代码审查 · Webuddy Core</h1><p class="wb-subtitle">完整 Diff 14k 分批独立审查（Writer≠Reviewer）+ 防篡改快照 + 人工有效率反馈闭环</p></div>' +
          '<div class="wb-actions">' +
            '<button type="button" class="button ghost" id="wbRefreshCR">刷新</button>' +
            '<button type="button" class="button primary" id="wbNewCRBtn">发起代码审查</button>' +
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
    root.querySelector('#wbCancelCreateReview')?.addEventListener('click', () => { crState.showCreate = false; render(); });
    root.querySelectorAll('[data-cr-id]').forEach(btn => {
      btn.addEventListener('click', () => selectReview(btn.dataset.crId));
    });

    root.querySelectorAll('[data-cr-mode]').forEach(btn => {
      btn.addEventListener('click', () => { crState.createMode = btn.dataset.crMode; render(); });
    });
    root.querySelector('#wbCrPickRepoBtn')?.addEventListener('click', pickRepoForReview);
    root.querySelector('#wbCrRepoSelect')?.addEventListener('change', async e => {
      crState.selectedRepoPath = e.target.value;
      await loadGitRefs(crState.selectedRepoPath);
      render();
    });

    const createForm = root.querySelector('#wbCreateReviewForm');
    if (createForm) {
      createForm.addEventListener('submit', async e => {
        e.preventDefault();
        const fd = new FormData(createForm);
        const lines = s => String(s || '').split('\n').map(x => x.trim()).filter(Boolean);
        const isAuto = crState.createMode === 'auto';
        crState.runningAI = isAuto;
        if (isAuto) render();
        try {
          const url = isAuto ? '/api/codereview/reviews/run' : '/api/codereview/reviews';
          const payload = {
            projectId: currentProjectId(),
            title: fd.get('title'),
            writer: fd.get('writer'),
            reviewer: fd.get('reviewer'),
            model: fd.get('model') || '',
            repoPath: fd.get('repoPath'),
            baseRef: fd.get('baseRef'),
            headRef: fd.get('headRef'),
            diff: fd.get('diff'),
          };
          if (!isAuto) {
            payload.blocking = lines(fd.get('blocking'));
            payload.advisory = lines(fd.get('advisory'));
          }
          const created = await reqJSON(url, {
            method: 'POST',
            body: JSON.stringify(payload),
          });
          notify('审查完成（' + created.verdict + '），共 ' + created.totalParts + ' 批');
          crState.showCreate = false;
          crState.selectedId = created.id;
          await loadCodeReviewView();
        } catch (err) {
          notify(err.message);
        } finally {
          crState.runningAI = false;
          render();
        }
      });
    }

    const fbForm = root.querySelector('#wbFeedbackForm');
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
})();
