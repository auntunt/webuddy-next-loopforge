/* Webuddy AI Automated Testing & 5-Step Self-Healing Workbench */
(function () {
  const atState = {
    loading: false,
    busy: false,
    busyText: '',
    tab: 'cases', // cases | runs | precedents
    overview: null,
    cases: [],
    runs: [],
    precedents: [],
    selectedCaseId: '',
    selectedRunId: '',
    showCreateModal: false,
    errorMsg: '',
    toastMsg: '',
    draft: {
      title: '',
      baseUrl: '',
      preconditionUrl: '',
      naturalRequirement: '',
      customStepsJson: '',
      advancedSteps: false,
    },
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
    const res = await fetch(
      url,
      Object.assign({ headers: { 'Content-Type': 'application/json' }, credentials: 'same-origin' }, opts || {})
    );
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data.error || '请求失败 (' + res.status + ')');
    }
    return data;
  }

  function ensureStyles() {
    if (document.getElementById('wbAutotestStyles')) return;
    const st = document.createElement('style');
    st.id = 'wbAutotestStyles';
    st.textContent = `
      .wb-at-wrap { padding: 22px 28px 48px; max-width: 1440px; margin: 0 auto; color: var(--fg, #18181b); }
      .wb-at-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; flex-wrap: wrap; margin-bottom: 18px; }
      .wb-at-head h1 { font-size: 21px; font-weight: 700; margin: 0 0 6px; display: flex; align-items: center; gap: 10px; }
      .wb-at-head p { margin: 0; font-size: 13px; color: var(--muted, #71717a); line-height: 1.55; max-width: 780px; }
      .wb-at-actions { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
      .wb-at-pill { display: inline-flex; align-items: center; gap: 5px; font-size: 11.5px; font-weight: 600; padding: 3px 9px; border-radius: 999px; border: 1px solid var(--line, #e4e4e7); background: var(--panel, #fff); }
      .wb-at-pill.ok { color: #059669; border-color: rgba(16,185,129,.35); background: rgba(16,185,129,.08); }
      .wb-at-pill.heal { color: #d97706; border-color: rgba(245,158,11,.4); background: rgba(245,158,11,.1); }
      .wb-at-pill.err { color: #dc2626; border-color: rgba(239,68,68,.38); background: rgba(239,68,68,.09); }
      .wb-at-pill.info { color: #2563eb; border-color: rgba(37,99,235,.32); background: rgba(37,99,235,.08); }
      .wb-at-kpis { display: grid; grid-template-columns: repeat(5, minmax(150px, 1fr)); gap: 12px; margin-bottom: 18px; }
      @media (max-width: 1024px) { .wb-at-kpis { grid-template-columns: repeat(2, 1fr); } }
      .wb-at-kpi { background: var(--panel, #fff); border: 1px solid var(--line, #e4e4e7); border-radius: 12px; padding: 13px 15px; }
      .wb-at-kpi .k-label { font-size: 12px; color: var(--muted, #71717a); margin-bottom: 4px; }
      .wb-at-kpi .k-val { font-size: 22px; font-weight: 700; letter-spacing: -0.02em; }
      .wb-at-kpi .k-sub { font-size: 11px; color: var(--muted, #71717a); margin-top: 3px; }
      .wb-at-tabs { display: flex; gap: 6px; border-bottom: 1px solid var(--line, #e4e4e7); margin-bottom: 16px; }
      .wb-at-tab { padding: 9px 14px; font-size: 13px; font-weight: 600; background: none; border: none; border-bottom: 2px solid transparent; color: var(--muted, #71717a); cursor: pointer; }
      .wb-at-tab.active { color: var(--fg, #18181b); border-bottom-color: var(--accent, #2563eb); }
      .wb-at-grid { display: grid; grid-template-columns: 340px 1fr; gap: 16px; align-items: start; }
      @media (max-width: 980px) { .wb-at-grid { grid-template-columns: 1fr; } }
      .wb-at-list { background: var(--panel, #fff); border: 1px solid var(--line, #e4e4e7); border-radius: 12px; overflow: hidden; }
      .wb-at-item { padding: 13px 15px; border-bottom: 1px solid var(--line, #e4e4e7); cursor: pointer; transition: background .12s; }
      .wb-at-item:last-child { border-bottom: none; }
      .wb-at-item:hover { background: rgba(37,99,235,.04); }
      .wb-at-item.active { background: rgba(37,99,235,.08); border-left: 3px solid var(--accent, #2563eb); padding-left: 12px; }
      .wb-at-item-title { font-size: 13.5px; font-weight: 600; margin-bottom: 5px; display: flex; justify-content: space-between; gap: 8px; }
      .wb-at-item-meta { font-size: 11.5px; color: var(--muted, #71717a); display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
      .wb-at-card { background: var(--panel, #fff); border: 1px solid var(--line, #e4e4e7); border-radius: 12px; padding: 18px 20px; margin-bottom: 14px; }
      .wb-at-card h3 { margin: 0 0 10px; font-size: 15px; font-weight: 650; display: flex; align-items: center; justify-content: space-between; gap: 10px; flex-wrap: wrap; }
      .wb-at-step { border: 1px solid var(--line, #e4e4e7); border-radius: 10px; padding: 11px 13px; margin-bottom: 9px; background: var(--bg-subtle, rgba(0,0,0,.015)); }
      .wb-at-step-top { display: flex; justify-content: space-between; align-items: center; gap: 8px; flex-wrap: wrap; font-size: 13px; font-weight: 600; }
      .wb-at-portrait { margin-top: 7px; display: flex; flex-wrap: wrap; gap: 6px; font-size: 11.5px; color: var(--muted, #71717a); }
      .wb-at-portrait code, .wb-at-mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 11.5px; background: rgba(0,0,0,.05); padding: 1px 6px; border-radius: 5px; }
      .wb-at-diff { border: 1px solid rgba(245,158,11,.45); background: rgba(245,158,11,.08); border-radius: 10px; padding: 12px 14px; margin-bottom: 14px; font-size: 12.5px; line-height: 1.6; }
      .wb-at-diag { margin-top: 8px; border-left: 3px solid #f59e0b; padding: 8px 12px; background: rgba(245,158,11,.06); border-radius: 0 8px 8px 0; font-size: 12px; }
      .wb-at-diag-row { margin: 3px 0; display: flex; gap: 6px; align-items: baseline; }
      .wb-at-shots { display: flex; gap: 10px; overflow-x: auto; padding-top: 8px; }
      .wb-at-shot { border: 1px solid var(--line, #e4e4e7); border-radius: 8px; width: 210px; flex-shrink: 0; overflow: hidden; background: #09090b; }
      .wb-at-shot img { width: 100%; height: 124px; object-fit: cover; display: block; cursor: zoom-in; }
      .wb-at-shot span { display: block; padding: 4px 8px; font-size: 11px; color: #e4e4e7; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
      .wb-at-modal-mask { position: fixed; inset: 0; background: rgba(0,0,0,.45); z-index: 1200; display: flex; align-items: center; justify-content: center; padding: 16px; }
      .wb-at-modal { background: var(--panel, #fff); border: 1px solid var(--line, #e4e4e7); border-radius: 14px; width: 100%; max-width: 680px; max-height: 90vh; overflow-y: auto; padding: 22px 24px; box-shadow: 0 20px 50px rgba(0,0,0,.22); }
      .wb-at-field { margin-bottom: 13px; }
      .wb-at-field label { display: block; font-size: 12.5px; font-weight: 600; margin-bottom: 5px; }
      .wb-at-field input, .wb-at-field textarea { width: 100%; box-sizing: border-box; padding: 8px 11px; font-size: 13px; border: 1px solid var(--line, #d4d4d8); border-radius: 8px; background: var(--bg, #fff); color: var(--fg, #18181b); }
      .wb-at-btn { padding: 7px 13px; border-radius: 8px; font-size: 12.5px; font-weight: 600; border: 1px solid var(--line, #d4d4d8); background: var(--panel, #fff); color: var(--fg, #18181b); cursor: pointer; display: inline-flex; align-items: center; gap: 6px; }
      .wb-at-btn:hover:not(:disabled) { border-color: var(--accent, #2563eb); color: var(--accent, #2563eb); }
      .wb-at-btn.primary { background: var(--accent, #2563eb); border-color: var(--accent, #2563eb); color: #fff; }
      .wb-at-btn.primary:hover:not(:disabled) { opacity: .92; color: #fff; }
      .wb-at-btn.warn { border-color: rgba(245,158,11,.5); color: #b45309; background: rgba(245,158,11,.08); }
      .wb-at-btn.danger { border-color: rgba(239,68,68,.45); color: #dc2626; }
      .wb-at-btn:disabled { opacity: .55; cursor: not-allowed; }
      .wb-at-banner { padding: 10px 14px; border-radius: 10px; margin-bottom: 14px; font-size: 12.5px; display: flex; justify-content: space-between; align-items: center; }
      .wb-at-banner.busy { background: rgba(37,99,235,.09); border: 1px solid rgba(37,99,235,.3); color: #1d4ed8; }
      .wb-at-banner.ok { background: rgba(16,185,129,.1); border: 1px solid rgba(16,185,129,.35); color: #047857; }
      .wb-at-banner.err { background: rgba(239,68,68,.09); border: 1px solid rgba(239,68,68,.35); color: #b91c1c; }
    `;
    document.head.appendChild(st);
  }

  function statusPill(st) {
    if (st === 'passed') return '<span class="wb-at-pill ok">● 回归通过</span>';
    if (st === 'healed') return '<span class="wb-at-pill heal">🩹 五步自愈通过</span>';
    if (st === 'failed') return '<span class="wb-at-pill err">✕ 异常待裁定</span>';
    return '<span class="wb-at-pill">⚪ 待运行</span>';
  }

  async function refreshOverview() {
    ensureStyles();
    atState.loading = true;
    atState.errorMsg = '';
    render();
    try {
      const pid = currentProjectId();
      const q = pid ? '?projectId=' + encodeURIComponent(pid) : '';
      const data = await reqJSON('/api/webuddy/autotest/overview' + q);
      atState.overview = data;
      atState.cases = data.cases || [];
      atState.runs = data.recentRuns || [];
      atState.precedents = data.precedents || [];
      if (!atState.selectedCaseId && atState.cases.length > 0) {
        atState.selectedCaseId = atState.cases[0].id;
      } else if (atState.selectedCaseId && !atState.cases.some(c => c.id === atState.selectedCaseId)) {
        atState.selectedCaseId = atState.cases[0] ? atState.cases[0].id : '';
      }
      if (!atState.draft.baseUrl && data.suggestedBaseUrl) {
        atState.draft.baseUrl = data.suggestedBaseUrl;
      }
      if (!atState.draft.preconditionUrl && data.suggestedAuthPath) {
        atState.draft.preconditionUrl = data.suggestedAuthPath;
      }
    } catch (e) {
      atState.errorMsg = e.message || String(e);
    } finally {
      atState.loading = false;
      render();
    }
  }

  async function runSingleCase(caseId) {
    if (atState.busy) return;
    atState.busy = true;
    atState.busyText = '正在启动 Headless Chrome 执行 0-Token 确定性回归（遇定位漂移自动进入五步诊断自愈）…';
    atState.errorMsg = '';
    atState.toastMsg = '';
    render();
    try {
      const res = await reqJSON('/api/webuddy/autotest/cases/' + encodeURIComponent(caseId) + '/run', {
        method: 'POST',
        body: '{}',
      });
      const r = res.run || {};
      if (r.status === 'healed' && r.healDiff) {
        atState.toastMsg = `🩹 五步诊断自愈成功！定位器 ${r.healDiff.oldSelector} → ${r.healDiff.newSelector}（置信度 ${r.healDiff.confidence} >= 0.80），已自动生成快照 Rev ${r.healDiff.toRevision}。`;
      } else if (r.status === 'passed') {
        atState.toastMsg = `✅ 0-Token 真机回归通过（耗时 ${r.durationMs} ms，消耗 ${r.tokensUsed} Token）。`;
      } else {
        atState.errorMsg = r.escalationReason || '测试未通过，请查看五步诊断结果或一键转缺陷单。';
      }
      await refreshOverview();
    } catch (e) {
      atState.errorMsg = e.message;
    } finally {
      atState.busy = false;
      atState.busyText = '';
      render();
    }
  }

  async function runAllSuite() {
    if (atState.busy) return;
    atState.busy = true;
    atState.busyText = '正在批量执行全项目固化用例集真机回归…';
    atState.errorMsg = '';
    atState.toastMsg = '';
    render();
    try {
      const res = await reqJSON('/api/webuddy/autotest/suites/run', {
        method: 'POST',
        body: JSON.stringify({ projectId: currentProjectId() }),
      });
      atState.toastMsg = `⚡ 全量回归完成：共执行 ${res.total} 条，通过 ${res.passed} 条，AI 自愈 ${res.healed} 条，待裁定异常 ${res.failed} 条。`;
      await refreshOverview();
    } catch (e) {
      atState.errorMsg = e.message;
    } finally {
      atState.busy = false;
      atState.busyText = '';
      render();
    }
  }

  async function injectDrift(caseId) {
    if (atState.busy) return;
    atState.busy = true;
    atState.busyText = '正在模拟单周 UI 改版导致旧选择器失效（保留三维控件数字身份证）…';
    atState.errorMsg = '';
    render();
    try {
      const res = await reqJSON('/api/webuddy/autotest/cases/' + encodeURIComponent(caseId) + '/inject-drift', {
        method: 'POST',
        body: '{}',
      });
      atState.toastMsg = `🧪 已模拟 UI 迭代破坏步骤 #${res.mutatedStepIdx} 定位器：${res.oldSelector} → ${res.brokenSelector}。现在点击「▶ 0-Token 极速复放」即可观看真机五步自愈与自动快照！`;
      await refreshOverview();
    } catch (e) {
      atState.errorMsg = e.message;
    } finally {
      atState.busy = false;
      atState.busyText = '';
      render();
    }
  }

  async function rollbackCase(caseId, targetRev) {
    if (atState.busy) return;
    atState.busy = true;
    atState.busyText = '正在回滚用例快照并写入防误修判例库…';
    atState.errorMsg = '';
    render();
    try {
      const res = await reqJSON('/api/webuddy/autotest/cases/' + encodeURIComponent(caseId) + '/rollback', {
        method: 'POST',
        body: JSON.stringify({ targetRevision: targetRev || 0 }),
      });
      atState.toastMsg = `⏪ 已一键回滚至历史快照 Rev ${res.restoredFromRevision}，并将被回滚的自愈变更记入判例防误修护栏。`;
      await refreshOverview();
    } catch (e) {
      atState.errorMsg = e.message;
    } finally {
      atState.busy = false;
      atState.busyText = '';
      render();
    }
  }

  async function deleteCase(caseId) {
    if (!window.confirm('确认删除该固化回归用例吗？')) return;
    try {
      await reqJSON('/api/webuddy/autotest/cases/' + encodeURIComponent(caseId), { method: 'DELETE' });
      atState.toastMsg = '已删除测试用例。';
      if (atState.selectedCaseId === caseId) atState.selectedCaseId = '';
      await refreshOverview();
    } catch (e) {
      atState.errorMsg = e.message;
      render();
    }
  }

  async function escalateRunToIssue(runId) {
    if (atState.busy) return;
    atState.busy = true;
    atState.busyText = '正在将异常现场与五步诊断明细打包升级为项目缺陷单…';
    render();
    try {
      const res = await reqJSON('/api/webuddy/autotest/runs/' + encodeURIComponent(runId) + '/create-issue', {
        method: 'POST',
        body: '{}',
      });
      const iss = res.issue || {};
      atState.toastMsg = `🚨 已创建待裁定缺陷单「${iss.title || iss.id}」（可在左侧「需求」列表直接指派修复 SOP）。`;
      await refreshOverview();
    } catch (e) {
      atState.errorMsg = e.message;
    } finally {
      atState.busy = false;
      atState.busyText = '';
      render();
    }
  }

  async function submitGenerateCase(ev) {
    if (ev) ev.preventDefault();
    if (atState.busy) return;
    const d = atState.draft;
    let parsedSteps = undefined;
    if (d.advancedSteps && d.customStepsJson.trim()) {
      try {
        parsedSteps = JSON.parse(d.customStepsJson);
      } catch (e) {
        atState.errorMsg = '自定义步骤 JSON 格式错误: ' + e.message;
        render();
        return;
      }
    }
    atState.busy = true;
    atState.busyText = '正在启动 Headless Chrome 注入前置状态、结合知识图谱探索真实 DOM 并固化首版确定性用例…';
    atState.errorMsg = '';
    render();
    try {
      const res = await reqJSON('/api/webuddy/autotest/cases/generate', {
        method: 'POST',
        body: JSON.stringify({
          projectId: currentProjectId(),
          title: d.title,
          baseUrl: d.baseUrl,
          preconditionUrl: d.preconditionUrl,
          naturalRequirement: d.naturalRequirement,
          steps: parsedSteps,
        }),
      });
      atState.showCreateModal = false;
      atState.selectedCaseId = res.case ? res.case.id : '';
      atState.toastMsg = `✨ 已真机验证并固化确定性用例「${res.case?.title || ''}」（Rev 1，采集控件数字身份证并完成首轮截图存证）！后续回归 0 Token 秒级执行。`;
      await refreshOverview();
    } catch (e) {
      atState.errorMsg = e.message;
    } finally {
      atState.busy = false;
      atState.busyText = '';
      render();
    }
  }

  function applyPresetScenario(idx) {
    if (idx === 1) {
      atState.draft.title = 'CEO 月度核算台 2026-11 算薪与报销契约核验';
      atState.draft.baseUrl = 'https://earn-demo.bbstack.date';
      atState.draft.preconditionUrl = '/auth/dev?id=1&to=admin';
      atState.draft.naturalRequirement =
        '通过前置登录态直接进入 we-earn 管理后台 (/admin.html)，核验月度核算台主标题控件与品牌文案，并校验 /api/admin/month/2026-11 月度核算接口正常返回 2026-11 账期与员工明细契约。';
      atState.draft.advancedSteps = true;
      atState.draft.customStepsJson = JSON.stringify(
        [
          { name: '校验已进入管理后台页面', action: 'assert_url', expected: '/admin' },
          { name: '核验管理后台主标题控件与品牌文案', action: 'assert_text', primarySelector: 'h1', expected: '月度核算台' },
          { name: '核验 2026-11 月度核算与对账 API 契约数据', action: 'assert_api', targetUrl: '/api/admin/month/2026-11', expected: '2026-11' },
        ],
        null,
        2
      );
    } else if (idx === 2) {
      atState.draft.title = '演示角色切换门户与员工端一键进入闭环核验';
      atState.draft.baseUrl = 'https://earn-demo.bbstack.date/auth/dev';
      atState.draft.preconditionUrl = '';
      atState.draft.naturalRequirement =
        '打开 earn-demo 角色体验门户 (/auth/dev)，核验角色切换入口渲染正常，点击员工登录链接后进入员工报销工作台页面。';
      atState.draft.advancedSteps = false;
      atState.draft.customStepsJson = '';
    }
    render();
  }

  function renderCaseDetail(tc) {
    if (!tc) {
      return `<div class="wb-at-card"><p style="color:var(--muted);margin:0;">请从左侧选择一条固化用例，或点击右上角「✨ AI 自然语言生成并真机固化」创建首条自动化回归资产。</p></div>`;
    }
    const caseRuns = atState.runs.filter(r => r.caseId === tc.id);
    const latestRun = caseRuns[0] || null;

    const stepsHtml = (tc.steps || [])
      .map((st, idx) => {
        const p = st.portrait;
        return `
        <div class="wb-at-step">
          <div class="wb-at-step-top">
            <span>#${idx + 1} · ${esc(st.name || st.action)} <span class="wb-at-pill info">${esc(st.action)}</span></span>
            ${st.primarySelector ? `<code class="wb-at-mono">${esc(st.primarySelector)}</code>` : ''}
          </div>
          ${st.expected ? `<div style="font-size:12px;margin-top:5px;color:var(--muted);">期望契约：<code>${esc(st.expected)}</code> ${st.targetUrl ? ` · 目标：<code>${esc(st.targetUrl)}</code>` : ''}</div>` : ''}
          ${
            p
              ? `<div class="wb-at-portrait">
                  <span>🪪 控件身份证：<code>[${esc(p.role || p.tag)}] ${esc(p.text || p.ariaLabel || '无文案')}</code></span>
                  <span>🛡️ 语义防火墙：<code>${esc(p.pagePathPattern || '/')}</code></span>
                  ${p.parentContext ? `<span>📍 业务上下文：<code>${esc(p.parentContext)}</code></span>` : ''}
                  ${p.domPath ? `<span>🌳 层级：<code>${esc(p.domPath)}</code></span>` : ''}
                </div>`
              : ''
          }
        </div>`;
      })
      .join('');

    let latestRunHtml = '';
    if (latestRun) {
      const diffBanner = latestRun.healDiff
        ? `<div class="wb-at-diff">
            <strong>🩹 AI 五步诊断已完成步骤级自愈并自动保存快照（Rev ${latestRun.healDiff.fromRevision} → Rev ${latestRun.healDiff.toRevision}）</strong><br/>
            失效定位器 <code>${esc(latestRun.healDiff.oldSelector)}</code> → 新定位器 <code>${esc(latestRun.healDiff.newSelector)}</code>
            · 三维画像置信度 <strong>${latestRun.healDiff.confidence}</strong>（门禁 ≥ 0.80） · 锚点：${esc(latestRun.healDiff.portraitAnchor)}
            <div style="margin-top:8px;">
              <button class="wb-at-btn warn" data-at-rollback="${esc(tc.id)}" data-at-rev="${latestRun.healDiff.fromRevision}">⏪ 觉得修得不对？一键回滚至 Rev ${latestRun.healDiff.fromRevision}</button>
            </div>
          </div>`
        : '';

      const stepResHtml = (latestRun.stepResults || [])
        .map(sr => {
          const diagHtml =
            sr.diagnosis && sr.diagnosis.checks
              ? `<div class="wb-at-diag">
                  <div style="font-weight:600;margin-bottom:4px;">🔍 五步诊断结论（${esc(sr.diagnosis.rootCauseCategory)} · 置信度 ${sr.diagnosis.confidence} / 门禁 ${sr.diagnosis.threshold}）：${esc(sr.diagnosis.decision)}</div>
                  ${sr.diagnosis.checks
                    .map(
                      ck => `<div class="wb-at-diag-row"><span>${ck.passed ? '✅' : '❌'}</span><span><strong>${esc(ck.title)}</strong>：${esc(ck.summary)}</span></div>`
                    )
                    .join('')}
                </div>`
              : '';
          return `
            <div class="wb-at-step">
              <div class="wb-at-step-top">
                <span>${sr.status === 'passed' ? '✅' : sr.status === 'healed' ? '🩹' : '❌'} 步骤 #${sr.stepIndex} · ${esc(sr.name)}</span>
                <span style="font-size:11.5px;color:var(--muted);">${sr.durationMs} ms</span>
              </div>
              <div style="font-size:12px;margin-top:4px;color:${sr.error ? '#dc2626' : 'var(--muted)'};">${esc(sr.error || sr.actualSummary || '')}</div>
              ${diagHtml}
            </div>`;
        })
        .join('');

      const shots = (latestRun.stepResults || []).filter(s => s.screenshotUrl);
      const shotsHtml =
        shots.length > 0
          ? `<div class="wb-at-shots">
              ${shots
                .map(
                  s => `<a class="wb-at-shot" href="${esc(s.screenshotUrl)}" target="_blank" rel="noopener">
                    <img src="${esc(s.screenshotUrl)}" alt="${esc(s.name)}" loading="lazy"/>
                    <span>#${s.stepIndex} ${esc(s.name)}</span>
                  </a>`
                )
                .join('')}
            </div>`
          : '';

      latestRunHtml = `
        <div class="wb-at-card">
          <h3>
            <span>最近一次执行存证 · <code class="wb-at-mono">${esc(latestRun.id)}</code></span>
            <span style="display:flex;gap:8px;align-items:center;">
              ${statusPill(latestRun.status)}
              <span class="wb-at-pill">${esc(latestRun.engine || 'cdp')} · ${latestRun.durationMs} ms · ${latestRun.tokensUsed} Token</span>
              ${
                latestRun.status === 'failed'
                  ? `<button class="wb-at-btn danger" data-at-escalate="${esc(latestRun.id)}">🚨 一键转缺陷单 (Issue)</button>`
                  : ''
              }
            </span>
          </h3>
          ${diffBanner}
          ${shotsHtml}
          <div style="margin-top:12px;">${stepResHtml}</div>
        </div>`;
    }

    const revsHtml = (tc.revisions || [])
      .slice()
      .reverse()
      .map(
        rv => `
        <div style="display:flex;justify-content:space-between;align-items:center;padding:8px 0;border-bottom:1px solid var(--line,#e4e4e7);font-size:12.5px;gap:10px;">
          <div>
            <strong>Rev ${rv.revision}</strong> ${rv.autoHealed ? '<span class="wb-at-pill heal">AI自愈快照</span>' : '<span class="wb-at-pill">基线快照</span>'}
            <span style="margin-left:6px;">${esc(rv.reason)}</span>
            ${rv.diffNote ? `<div style="color:var(--muted);font-size:11.5px;margin-top:2px;">${esc(rv.diffNote)}</div>` : ''}
          </div>
          ${
            rv.revision !== tc.currentRevision
              ? `<button class="wb-at-btn" data-at-rollback="${esc(tc.id)}" data-at-rev="${rv.revision}">恢复此版</button>`
              : '<span style="font-size:11.5px;color:#059669;font-weight:600;">当前生效</span>'
          }
        </div>`
      )
      .join('');

    return `
      <div>
        <div class="wb-at-card">
          <h3>
            <span>${esc(tc.title)} <span class="wb-at-pill">Rev ${tc.currentRevision || 1}</span></span>
            <div style="display:flex;gap:8px;flex-wrap:wrap;">
              <button class="wb-at-btn primary" data-at-run="${esc(tc.id)}" ${atState.busy ? 'disabled' : ''}>▶ 0-Token 极速复放</button>
              <button class="wb-at-btn warn" data-at-drift="${esc(tc.id)}" ${atState.busy ? 'disabled' : ''} title="故意改坏一个选择器，验证真机五步诊断与三维画像自愈">🧪 模拟 UI 改版定位失效</button>
              ${
                (tc.revisions || []).length > 1
                  ? `<button class="wb-at-btn" data-at-rollback="${esc(tc.id)}" ${atState.busy ? 'disabled' : ''}>⏪ 一键回滚上一版</button>`
                  : ''
              }
              <button class="wb-at-btn danger" data-at-del="${esc(tc.id)}" ${atState.busy ? 'disabled' : ''}>删除</button>
            </div>
          </h3>
          <div style="font-size:12.5px;color:var(--muted);line-height:1.65;margin-bottom:12px;">
            <div>🎯 <strong>目标站点</strong>：<a href="${esc(tc.baseUrl)}" target="_blank" rel="noopener">${esc(tc.baseUrl)}</a>
              ${tc.preconditionUrl ? ` · 🔑 <strong>前置状态直接注入</strong>：<code class="wb-at-mono">${esc(tc.preconditionUrl)}</code>` : ''}
            </div>
            <div>📋 <strong>产技测验收契约</strong>：${esc(tc.naturalRequirement)}</div>
          </div>
          <div style="font-size:13px;font-weight:600;margin-bottom:8px;">固化确定性步骤与控件三维数字身份证 (${(tc.steps || []).length} 步)</div>
          ${stepsHtml}
        </div>
        ${latestRunHtml}
        <div class="wb-at-card">
          <h3>双层安全护栏 · 版本快照历史 (支持毫秒级回滚)</h3>
          ${revsHtml || '<div style="font-size:12px;color:var(--muted);">暂无历史快照</div>'}
        </div>
      </div>`;
  }

  function renderRunsTab() {
    if (!atState.runs.length) {
      return `<div class="wb-at-card"><p style="margin:0;color:var(--muted);">暂无回归执行记录。</p></div>`;
    }
    return `<div class="wb-at-card">
      <h3>全量回归与五步自愈轨迹（最近 ${atState.runs.length} 次）</h3>
      ${atState.runs
        .map(
          r => `
        <div class="wb-at-step" style="cursor:pointer;" data-at-jump-case="${esc(r.caseId)}">
          <div class="wb-at-step-top">
            <span>${statusPill(r.status)} <strong>${esc(r.caseTitle)}</strong> <span class="wb-at-pill">Rev ${r.caseRevision}</span></span>
            <span style="font-size:12px;color:var(--muted);">${esc(r.engine)} · ${r.durationMs} ms · 消耗 ${r.tokensUsed} Token</span>
          </div>
          ${
            r.healDiff
              ? `<div style="font-size:12px;color:#b45309;margin-top:5px;">🩹 自愈变更：<code>${esc(r.healDiff.oldSelector)}</code> → <code>${esc(r.healDiff.newSelector)}</code> (置信度 ${r.healDiff.confidence})</div>`
              : ''
          }
          ${
            r.escalationReason
              ? `<div style="font-size:12px;color:#dc2626;margin-top:5px;display:flex;justify-content:space-between;align-items:center;gap:8px;">
                  <span>${esc(r.escalationReason)}</span>
                  <button class="wb-at-btn danger" data-at-escalate="${esc(r.id)}">🚨 一键转缺陷单</button>
                </div>`
              : ''
          }
        </div>`
        )
        .join('')}
    </div>`;
  }

  function renderPrecedentsTab() {
    if (!atState.precedents.length) {
      return `<div class="wb-at-card"><p style="margin:0;color:var(--muted);">暂无判例资产。每当真机固化成功、AI 自愈成功或人工回滚误修后，系统都会像「数字员工判例库」一样自动沉淀为项目上下文资产（零人工录入）。</p></div>`;
    }
    return `<div class="wb-at-card">
      <h3>上下文资产层 · 自动化验收与自愈判例库 (${atState.precedents.length} 条，零人工录入自动沉淀)</h3>
      ${atState.precedents
        .map(p => {
          let badge = '<span class="wb-at-pill info">📘 验收基线判例</span>';
          if (p.kind === 'heal_pattern') badge = '<span class="wb-at-pill heal">🩹 自愈演化判例</span>';
          if (p.kind === 'bad_case_guard') badge = '<span class="wb-at-pill err">🛑 回滚防误修护栏</span>';
          return `<div class="wb-at-step">
            <div class="wb-at-step-top">
              <span>${badge} ${esc(p.title)}</span>
              <span style="font-size:11.5px;color:var(--muted);">${p.confidence ? `置信度 ${p.confidence}` : ''}</span>
            </div>
            <div style="font-size:12.5px;color:var(--muted);margin-top:5px;line-height:1.55;">${esc(p.summary)}</div>
          </div>`;
        })
        .join('')}
    </div>`;
  }

  function renderCreateModal() {
    if (!atState.showCreateModal) return '';
    const d = atState.draft;
    return `
      <div class="wb-at-modal-mask" id="wbAtModalMask">
        <div class="wb-at-modal">
          <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:12px;">
            <h2 style="margin:0;font-size:17px;">✨ AI 自然语言生成并真机固化确定性用例</h2>
            <button class="wb-at-btn" id="wbAtCloseModal">关闭</button>
          </div>
          <p style="font-size:12.5px;color:var(--muted);margin:0 0 12px;line-height:1.55;">
            采用「AI 真机探索降噪 → 固化确定性脚本 → 日常 0-Token 秒级回归 → 失败触发五步自愈」闭环。可一键填入演示场景立即体验：
          </p>
          <div style="display:flex;gap:8px;flex-wrap:wrap;margin-bottom:14px;">
            <button type="button" class="wb-at-btn" data-at-preset="1">⚡ 一键填入：we-earn 算薪与对账后台核验</button>
            <button type="button" class="wb-at-btn" data-at-preset="2">⚡ 一键填入：earn-demo 角色门户与员工端核验</button>
          </div>
          <form id="wbAtCreateForm">
            <div class="wb-at-field">
              <label>用例标题</label>
              <input name="title" value="${esc(d.title)}" placeholder="例如：CEO 管理后台 2026-11 算薪批次与 AI 对账核验" required />
            </div>
            <div class="wb-at-field">
              <label>目标站点 Base URL</label>
              <input name="baseUrl" value="${esc(d.baseUrl)}" placeholder="https://earn-demo.bbstack.date" required />
            </div>
            <div class="wb-at-field">
              <label>前置登录态 / 角色状态直接注入路径（可选，跳过繁琐登录页直接进入业务态）</label>
              <input name="preconditionUrl" value="${esc(d.preconditionUrl)}" placeholder="例如：/auth/dev?id=1&to=admin" />
            </div>
            <div class="wb-at-field">
              <label>自然语言验收场景描述（产技测一体契约）</label>
              <textarea name="naturalRequirement" rows="3" placeholder="描述要验证的页面核心流程、按钮交互或关键业务数据断言…">${esc(d.naturalRequirement)}</textarea>
            </div>
            <div class="wb-at-field">
              <label style="display:flex;align-items:center;gap:6px;cursor:pointer;">
                <input type="checkbox" id="wbAtToggleSteps" style="width:auto;" ${d.advancedSteps ? 'checked' : ''} />
                <span>高级：查看或指定确定性原子步骤 JSON（留空则由 AI 自动探测真实页面 DOM 生成）</span>
              </label>
              ${
                d.advancedSteps
                  ? `<textarea name="customStepsJson" rows="6" style="font-family:monospace;font-size:12px;margin-top:6px;" placeholder='[{"name":"核验标题","action":"assert_text","primarySelector":"h1","expected":"..."}]'>${esc(d.customStepsJson)}</textarea>`
                  : ''
              }
            </div>
            <div style="display:flex;justify-content:flex-end;gap:10px;margin-top:16px;">
              <button type="button" class="wb-at-btn" id="wbAtCancelBtn">取消</button>
              <button type="submit" class="wb-at-btn primary" ${atState.busy ? 'disabled' : ''}>🚀 启动 Chrome 真机验证并固化 (Rev 1)</button>
            </div>
          </form>
        </div>
      </div>`;
  }

  function render() {
    const root = document.getElementById('autotestRoot');
    if (!root) return;
    ensureStyles();

    const m = (atState.overview && atState.overview.metrics) || {
      totalCases: 0,
      zeroTokenRuns: 0,
      passRate: 0,
      healedRuns: 0,
      precedentsCount: 0,
    };
    const chromeOK = atState.overview ? atState.overview.chromeAvailable : true;
    const selectedCase = atState.cases.find(c => c.id === atState.selectedCaseId) || atState.cases[0] || null;

    root.innerHTML = `
      <div class="wb-at-wrap">
        <div class="wb-at-head">
          <div>
            <h1>
              <span>自动化测试 · AI 固化与五步自愈工作台</span>
              <span class="wb-at-pill ${chromeOK ? 'ok' : ''}">${chromeOK ? '🟢 Chromium CDP 真机引擎就绪' : '⚡ HTTP+DOM 轻量引擎'}</span>
              <span class="wb-at-pill info">🛡️ 自愈置信度门禁 ≥ 0.80</span>
            </h1>
            <p>融合「AI 真机探索降噪 → 固化确定性脚本（日常 0-Token 秒级回归）→ 失败触发五步诊断与三维控件身份证自愈 → 快照回滚与判例库自动沉淀」，一人指挥数字员工跑通验收闭环，异常才找人拍板。</p>
          </div>
          <div class="wb-at-actions">
            <button class="wb-at-btn" id="wbAtRefreshBtn" ${atState.busy ? 'disabled' : ''}>刷新</button>
            <button class="wb-at-btn" id="wbAtRunSuiteBtn" ${atState.busy || !atState.cases.length ? 'disabled' : ''}>▶ 一键全量回归 (0 Token)</button>
            <button class="wb-at-btn primary" id="wbAtOpenCreateBtn" ${atState.busy ? 'disabled' : ''}>✨ AI 自然语言生成并真机固化</button>
          </div>
        </div>

        ${atState.busy ? `<div class="wb-at-banner busy"><span>⏳ ${esc(atState.busyText)}</span></div>` : ''}
        ${atState.toastMsg ? `<div class="wb-at-banner ok"><span>${esc(atState.toastMsg)}</span><button class="wb-at-btn" id="wbAtClearToast" style="padding:2px 8px;">知道了</button></div>` : ''}
        ${atState.errorMsg ? `<div class="wb-at-banner err"><span>⚠️ ${esc(atState.errorMsg)}</span></div>` : ''}

        <div class="wb-at-kpis">
          <div class="wb-at-kpi">
            <div class="k-label">固化确定性用例</div>
            <div class="k-val">${m.totalCases}</div>
            <div class="k-sub">带控件三维数字身份证</div>
          </div>
          <div class="wb-at-kpi">
            <div class="k-label">0-Token 极速回归次数</div>
            <div class="k-val">${m.zeroTokenRuns}</div>
            <div class="k-sub">日常回归零大模型开销</div>
          </div>
          <div class="wb-at-kpi">
            <div class="k-label">综合回归通过率</div>
            <div class="k-val">${m.passRate}%</div>
            <div class="k-sub">真机截图与契约断言</div>
          </div>
          <div class="wb-at-kpi">
            <div class="k-label">五步诊断自愈成功</div>
            <div class="k-val" style="color:#d97706;">${m.healedRuns}</div>
            <div class="k-sub">跨页防火墙 + 快照可回滚</div>
          </div>
          <div class="wb-at-kpi">
            <div class="k-label">沉淀上下文判例库</div>
            <div class="k-val" style="color:#2563eb;">${m.precedentsCount}</div>
            <div class="k-sub">聊天框可发 /test 秒跑</div>
          </div>
        </div>

        <div class="wb-at-tabs">
          <button class="wb-at-tab ${atState.tab === 'cases' ? 'active' : ''}" data-at-tab="cases">固化用例与控件画像 (${atState.cases.length})</button>
          <button class="wb-at-tab ${atState.tab === 'runs' ? 'active' : ''}" data-at-tab="runs">执行与五步自愈轨迹 (${atState.runs.length})</button>
          <button class="wb-at-tab ${atState.tab === 'precedents' ? 'active' : ''}" data-at-tab="precedents">上下文判例资产 (${atState.precedents.length})</button>
        </div>

        ${
          atState.tab === 'cases'
            ? `<div class="wb-at-grid">
                <div class="wb-at-list">
                  ${
                    atState.cases.length
                      ? atState.cases
                          .map(
                            c => `
                        <div class="wb-at-item ${selectedCase && selectedCase.id === c.id ? 'active' : ''}" data-at-select="${esc(c.id)}">
                          <div class="wb-at-item-title">
                            <span>${esc(c.title)}</span>
                            <span class="wb-at-pill">Rev ${c.currentRevision || 1}</span>
                          </div>
                          <div class="wb-at-item-meta">
                            ${statusPill(c.lastStatus)}
                            <span>通过率 ${c.stats?.passRate ?? 0}%</span>
                            <span>${(c.steps || []).length} 步</span>
                          </div>
                        </div>`
                          )
                          .join('')
                      : `<div style="padding:20px;font-size:12.5px;color:var(--muted);">当前项目还没有固化用例。点击右上角「✨ AI 自然语言生成并真机固化」立即跑通首条用例！</div>`
                  }
                </div>
                ${renderCaseDetail(selectedCase)}
              </div>`
            : atState.tab === 'runs'
            ? renderRunsTab()
            : renderPrecedentsTab()
        }
      </div>
      ${renderCreateModal()}
    `;

    bindEvents(root);
  }

  function syncDraftInputs(root) {
    const form = root.querySelector('#wbAtCreateForm');
    if (!form) return;
    ['title', 'baseUrl', 'preconditionUrl', 'naturalRequirement', 'customStepsJson'].forEach(k => {
      const el = form.elements.namedItem(k);
      if (el) atState.draft[k] = el.value;
    });
  }

  function bindEvents(root) {
    const on = (sel, fn) => {
      const el = root.querySelector(sel);
      if (el) el.onclick = fn;
    };
    on('#wbAtRefreshBtn', () => refreshOverview());
    on('#wbAtRunSuiteBtn', () => runAllSuite());
    on('#wbAtOpenCreateBtn', () => {
      atState.showCreateModal = true;
      render();
    });
    on('#wbAtCloseModal', () => {
      syncDraftInputs(root);
      atState.showCreateModal = false;
      render();
    });
    on('#wbAtCancelBtn', () => {
      syncDraftInputs(root);
      atState.showCreateModal = false;
      render();
    });
    on('#wbAtClearToast', () => {
      atState.toastMsg = '';
      render();
    });

    const toggleSteps = root.querySelector('#wbAtToggleSteps');
    if (toggleSteps) {
      toggleSteps.onchange = e => {
        syncDraftInputs(root);
        atState.draft.advancedSteps = !!e.target.checked;
        render();
      };
    }

    const form = root.querySelector('#wbAtCreateForm');
    if (form) {
      form.oninput = () => syncDraftInputs(root);
      form.onsubmit = submitGenerateCase;
    }

    root.querySelectorAll('[data-at-preset]').forEach(btn => {
      btn.onclick = () => applyPresetScenario(Number(btn.dataset.atPreset));
    });
    root.querySelectorAll('[data-at-tab]').forEach(btn => {
      btn.onclick = () => {
        atState.tab = btn.dataset.atTab;
        render();
      };
    });
    root.querySelectorAll('[data-at-select]').forEach(el => {
      el.onclick = () => {
        atState.selectedCaseId = el.dataset.atSelect;
        render();
      };
    });
    root.querySelectorAll('[data-at-jump-case]').forEach(el => {
      el.onclick = ev => {
        if (ev.target.closest('[data-at-escalate]')) return;
        atState.selectedCaseId = el.dataset.atJumpCase;
        atState.tab = 'cases';
        render();
      };
    });
    root.querySelectorAll('[data-at-run]').forEach(btn => {
      btn.onclick = () => runSingleCase(btn.dataset.atRun);
    });
    root.querySelectorAll('[data-at-drift]').forEach(btn => {
      btn.onclick = () => injectDrift(btn.dataset.atDrift);
    });
    root.querySelectorAll('[data-at-rollback]').forEach(btn => {
      btn.onclick = () => rollbackCase(btn.dataset.atRollback, Number(btn.dataset.atRev || 0));
    });
    root.querySelectorAll('[data-at-del]').forEach(btn => {
      btn.onclick = () => deleteCase(btn.dataset.atDel);
    });
    root.querySelectorAll('[data-at-escalate]').forEach(btn => {
      btn.onclick = ev => {
        ev.stopPropagation();
        escalateRunToIssue(btn.dataset.atEscalate);
      };
    });
  }

  window.loadAutoTestView = function () {
    refreshOverview();
  };
})();
