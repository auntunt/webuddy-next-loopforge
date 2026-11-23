/* Webuddy v2 Global Floating Code Review & Flow Anchor Dock
 * Stays available across ALL LoopForge views (Chat, SOP, Issues, Knowledge Blueprint)
 * Supports: minimized pill | right drawer | draggable floating window
 */
(function () {
  const fState = {
    mode: 'minimized', // minimized | drawer | window
    reviews: [],
    selectedId: '',
    detail: null,
    pos: { right: 24, bottom: 24 },
  };

  function esc(v) {
    return String(v == null ? '' : v)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  async function reqJSON(url) {
    const res = await fetch(url);
    if (!res.ok) throw new Error('HTTP ' + res.status);
    return res.json();
  }

  async function refreshFloating() {
    try {
      const pid = encodeURIComponent((window.state && window.state.projectFilter) || '');
      const data = await reqJSON('/api/codereview/reviews?projectId=' + pid + '&limit=10');
      fState.reviews = data.reviews || [];
      if (!fState.selectedId && fState.reviews.length) {
        fState.selectedId = fState.reviews[0].id;
      }
      if (fState.mode !== 'minimized' && fState.selectedId) {
        fState.detail = await reqJSON('/api/codereview/reviews/' + encodeURIComponent(fState.selectedId));
      }
    } catch (_) {
      // Ignore before login
    }
    renderDock();
  }

  function ensureHost() {
    let el = document.getElementById('wbFloatingDock');
    if (!el) {
      el = document.createElement('div');
      el.id = 'wbFloatingDock';
      document.body.appendChild(el);
    }
    return el;
  }

  function renderDock() {
    const host = ensureHost();
    const count = fState.reviews.length;
    const latest = fState.reviews[0];
    const failCount = fState.reviews.filter(r => r.verdict === 'fail').length;

    if (fState.mode === 'minimized') {
      host.className = 'wb-float-pill-wrap';
      host.innerHTML =
        '<button type="button" class="wb-float-pill" id="wbFloatOpenBtn" title="打开 Webuddy 浮动代码复核面板（可在看架构图/对话时同步复核）">' +
          '<span class="wb-float-pulse ' + (failCount > 0 ? 'fail' : 'pass') + '"></span>' +
          '<span>代码复核浮层</span>' +
          '<span class="wb-badge ' + (failCount > 0 ? 'fail' : 'info') + '">' + (failCount > 0 ? ('阻断 ' + failCount) : (count + ' 条')) + '</span>' +
        '</button>';
      host.querySelector('#wbFloatOpenBtn')?.addEventListener('click', () => {
        fState.mode = 'drawer';
        refreshFloating();
      });
      return;
    }

    const r = fState.detail && fState.detail.review ? fState.detail.review : latest;
    const bodyHTML = !r
      ? '<div class="wb-empty">当前项目暂无代码审查记录。点击下方「前往代码审查工作台」发起一次 14k 分批审查。</div>'
      : '<div style="display:flex;flex-direction:column;gap:10px">' +
          '<div class="wb-item-top">' +
            '<select id="wbFloatSelect" style="flex:1;max-width:260px">' +
              fState.reviews.map(x => '<option value="' + esc(x.id) + '" ' + (x.id === r.id ? 'selected' : '') + '>' + esc(x.title) + ' (' + esc(x.verdict) + ')</option>').join('') +
            '</select>' +
            '<span class="wb-badge ' + esc(r.verdict || 'unknown') + '">' + (r.verdict === 'pass' ? '通过' : r.verdict === 'fail' ? '阻断' : '待核实') + '</span>' +
          '</div>' +
          '<div class="wb-meta">' +
            '<span>Writer: <b>' + esc(r.writer) + '</b></span>' +
            '<span>Reviewer: <b>' + esc(r.reviewer) + '</b></span>' +
            '<span>' + r.totalParts + ' 批</span>' +
            '<span>' + (r.complete ? '完整覆盖' : '存在未覆盖') + '</span>' +
          '</div>' +
          ((r.blocking || []).length
            ? '<div><div class="wb-meta" style="margin-bottom:4px;color:#ef4444;font-weight:600">阻断项 (' + r.blocking.length + ')</div>' +
              r.blocking.map(b => '<div class="wb-finding-card" style="margin-bottom:6px;border-color:rgba(239,68,68,0.35);font-size:12.5px">' + esc(b) + '</div>').join('') + '</div>'
            : '<div class="wb-meta" style="color:#10b981">无阻断项</div>') +
          ((r.advisory || []).length
            ? '<div><div class="wb-meta" style="margin-bottom:4px;font-weight:600">建议项 (' + r.advisory.length + ')</div>' +
              r.advisory.map(a => '<div class="wb-finding-card" style="margin-bottom:6px;font-size:12px">' + esc(a) + '</div>').join('') + '</div>'
            : '') +
          '<div><div class="wb-meta" style="margin-bottom:4px;font-weight:600">覆盖批次</div>' +
            (r.parts || []).map(p => '<div class="wb-meta" style="justify-content:space-between;padding:3px 0;border-bottom:1px solid rgba(148,163,184,0.12)"><span>#' + p.part + ' ' + esc((p.file || '').replace('diff --git ', '')) + '</span><span>' + p.characters + ' 字</span></div>').join('') +
          '</div>' +
        '</div>';

    host.className = fState.mode === 'drawer' ? 'wb-float-drawer' : 'wb-float-window';
    host.innerHTML =
      '<div class="wb-float-head" id="wbFloatDragHead">' +
        '<strong>Webuddy 浮动代码复核</strong>' +
        '<div class="wb-actions">' +
          '<button type="button" class="button ghost small" id="wbFloatToggleMode">' + (fState.mode === 'drawer' ? '悬浮窗' : '停靠右侧') + '</button>' +
          '<button type="button" class="button ghost small" id="wbFloatJumpFull">完整工作台</button>' +
          '<button type="button" class="button ghost small" id="wbFloatMinBtn">收起</button>' +
        '</div>' +
      '</div>' +
      '<div class="wb-float-body">' + bodyHTML + '</div>';

    host.querySelector('#wbFloatMinBtn')?.addEventListener('click', () => { fState.mode = 'minimized'; renderDock(); });
    host.querySelector('#wbFloatToggleMode')?.addEventListener('click', () => {
      fState.mode = fState.mode === 'drawer' ? 'window' : 'drawer';
      renderDock();
    });
    host.querySelector('#wbFloatJumpFull')?.addEventListener('click', () => {
      fState.mode = 'minimized';
      renderDock();
      if (typeof window.switchView === 'function') window.switchView('codereview');
    });
    host.querySelector('#wbFloatSelect')?.addEventListener('change', async e => {
      fState.selectedId = e.target.value;
      await refreshFloating();
    });

    if (fState.mode === 'window') {
      enableDrag(host, host.querySelector('#wbFloatDragHead'));
    }
  }

  function enableDrag(el, handle) {
    if (!handle) return;
    handle.style.cursor = 'move';
    handle.onmousedown = e => {
      if (e.target.closest('button')) return;
      const rect = el.getBoundingClientRect();
      const ox = e.clientX - rect.left;
      const oy = e.clientY - rect.top;
      function onMove(ev) {
        el.style.left = Math.max(8, Math.min(window.innerWidth - rect.width - 8, ev.clientX - ox)) + 'px';
        el.style.top = Math.max(8, Math.min(window.innerHeight - rect.height - 8, ev.clientY - oy)) + 'px';
        el.style.right = 'auto';
        el.style.bottom = 'auto';
      }
      function onUp() {
        window.removeEventListener('mousemove', onMove);
        window.removeEventListener('mouseup', onUp);
      }
      window.addEventListener('mousemove', onMove);
      window.addEventListener('mouseup', onUp);
    };
  }

  window.refreshWebuddyFloatingReview = refreshFloating;
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', () => setTimeout(refreshFloating, 1200));
  } else {
    setTimeout(refreshFloating, 1200);
  }
})();
