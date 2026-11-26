/* Webuddy v2 Global Floating Live Task Pill & Code Review Dock
 * Stays active across ALL views (Workbench Chat, SOP, Issues, Knowledgebase, Code Review, Platform)
 * so long-running KB updates, AI generations, and SOP runs NEVER block the user's current work.
 */
(function () {
  const fState = {
    mode: 'minimized', // minimized | panel
    tasks: [],
    chatRunning: [],
    reviews: [],
    dismissedDone: {},
    lastRunningIds: new Set(),
  };

  function esc(v) {
    return String(v == null ? '' : v)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  function fmtSec(sec) {
    const s = Math.max(0, Number(sec || 0));
    if (s < 60) return s + 's';
    return Math.floor(s / 60) + 'm ' + (s % 60) + 's';
  }

  async function reqJSON(url) {
    const res = await fetch(url, { credentials: 'same-origin' });
    if (!res.ok) throw new Error('HTTP ' + res.status);
    return res.json();
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

  function ensureStyle() {
    if (document.getElementById('wbLiveTaskPillStyle')) return;
    const st = document.createElement('style');
    st.id = 'wbLiveTaskPillStyle';
    st.textContent = `
      #wbFloatingDock.wb-live-dock {
        position: fixed;
        right: 20px;
        bottom: 20px;
        z-index: 9999;
        font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      }
      .wb-live-pill {
        display: inline-flex;
        align-items: center;
        gap: 8px;
        padding: 9px 14px;
        border-radius: 999px;
        border: 1px solid rgba(99, 102, 241, 0.35);
        background: rgba(15, 23, 42, 0.92);
        color: #f8fafc;
        font-size: 12.5px;
        font-weight: 600;
        cursor: pointer;
        box-shadow: 0 8px 24px rgba(15, 23, 42, 0.24);
        backdrop-filter: blur(8px);
        max-width: 380px;
        transition: transform 0.15s ease, box-shadow 0.15s ease;
      }
      .wb-live-pill:hover { transform: translateY(-1px); box-shadow: 0 10px 28px rgba(15, 23, 42, 0.3); }
      .wb-live-pill.is-done {
        background: rgba(20, 83, 45, 0.94);
        border-color: rgba(134, 239, 172, 0.45);
      }
      .wb-live-dot {
        width: 9px;
        height: 9px;
        border-radius: 50%;
        background: #38bdf8;
        flex-shrink: 0;
        box-shadow: 0 0 0 0 rgba(56, 189, 248, 0.7);
        animation: wbLivePulse 1.5s infinite;
      }
      .wb-live-pill.is-done .wb-live-dot {
        background: #4ade80;
        animation: none;
        box-shadow: 0 0 8px rgba(74, 222, 128, 0.75);
      }
      @keyframes wbLivePulse {
        0% { box-shadow: 0 0 0 0 rgba(56, 189, 248, 0.7); }
        70% { box-shadow: 0 0 0 8px rgba(56, 189, 248, 0); }
        100% { box-shadow: 0 0 0 0 rgba(56, 189, 248, 0); }
      }
      .wb-live-pill-text {
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
      }
      .wb-live-badge {
        background: rgba(99, 102, 241, 0.35);
        color: #e0e7ff;
        padding: 1px 7px;
        border-radius: 999px;
        font-size: 11px;
        flex-shrink: 0;
      }
      .wb-live-panel {
        width: 360px;
        max-width: calc(100vw - 32px);
        max-height: 440px;
        border-radius: 14px;
        border: 1px solid #cbd5e1;
        background: #ffffff;
        color: #0f172a;
        box-shadow: 0 14px 36px rgba(15, 23, 42, 0.18);
        overflow: hidden;
        display: flex;
        flex-direction: column;
      }
      .wb-live-panel-head {
        display: flex;
        align-items: center;
        justify-content: space-between;
        padding: 11px 14px;
        background: #0f172a;
        color: #f8fafc;
        font-size: 13px;
        font-weight: 600;
      }
      .wb-live-panel-body {
        padding: 10px;
        overflow-y: auto;
        display: grid;
        gap: 8px;
        background: #f8fafc;
      }
      .wb-live-item {
        background: #ffffff;
        border: 1px solid #e2e8f0;
        border-radius: 10px;
        padding: 9px 11px;
        font-size: 12px;
      }
      .wb-live-item.running { border-left: 3px solid #4f46e5; }
      .wb-live-item.done { border-left: 3px solid #16a34a; }
      .wb-live-item.error { border-left: 3px solid #dc2626; }
    `;
    document.head.appendChild(st);
  }

  function combinedTasks() {
    const list = [];
    (fState.tasks || []).forEach(t => list.push(t));
    (fState.chatRunning || []).forEach(t => list.push(t));
    return list;
  }

  async function refreshFloating() {
    try {
      const [taskRes, sessRes] = await Promise.all([
        reqJSON('/api/webuddy/tasks/active').catch(() => null),
        reqJSON('/api/assistant/sessions').catch(() => null),
      ]);

      if (taskRes && Array.isArray(taskRes.tasks)) {
        const nextRunning = new Set();
        taskRes.tasks.forEach(t => {
          if (t.status === 'running') nextRunning.add(t.id);
          if (t.status === 'done' && fState.lastRunningIds.has(t.id) && t.kind === 'kb_update') {
            window.dispatchEvent(new CustomEvent('webuddy:kb-updated', { detail: { projectId: t.projectId, status: 'done' } }));
            const frame = document.getElementById('collaborationFrame') || document.querySelector('iframe[src*="native-chat"]');
            try {
              frame?.contentWindow?.postMessage({ type: 'webuddy-kb-updated', projectId: t.projectId }, location.origin);
            } catch (_) {}
          }
        });
        fState.lastRunningIds = nextRunning;
        fState.tasks = taskRes.tasks;
      }

      const activeChats = [];
      const sessions = Array.isArray(sessRes?.sessions) ? sessRes.sessions : (Array.isArray(sessRes) ? sessRes : []);
      sessions.forEach(s => {
        const gen = s && (s.generating || s.status === 'running' || s.activeGeneration);
        if (gen) {
          activeChats.push({
            id: 'chat-' + s.id,
            kind: 'chat',
            projectId: s.projectId || '',
            projectName: s.projectId || '',
            title: 'AI 对话处理中：' + (s.title || '当前会话'),
            status: 'running',
            progress: '后台持续推理中，可自由切换其他页面',
            urlHash: '#/collaboration',
            elapsedSec: 0,
          });
        }
      });
      fState.chatRunning = activeChats;
    } catch (_) {
      // Ignore before login
    }
    renderDock();
  }

  function renderDock() {
    ensureStyle();
    const host = ensureHost();
    const all = combinedTasks();
    const running = all.filter(t => t.status === 'running');
    const recentDone = all.filter(t => t.status !== 'running' && !fState.dismissedDone[t.id]);

    if (running.length === 0 && recentDone.length === 0) {
      host.className = '';
      host.innerHTML = '';
      return;
    }

    host.className = 'wb-live-dock';

    if (fState.mode === 'minimized') {
      const top = running[0] || recentDone[0];
      const isDone = running.length === 0;
      const label = isDone
        ? (top.status === 'error' ? '⚠️ 任务异常 · ' : '✅ 已完成 · ') + top.title
        : top.title + (top.elapsedSec > 0 ? ' · ' + fmtSec(top.elapsedSec) : '');
      host.innerHTML =
        '<button type="button" class="wb-live-pill' + (isDone ? ' is-done' : '') + '" id="wbLivePillBtn" title="点击展开后台活跃任务（不阻塞当前工作）">' +
          '<span class="wb-live-dot"></span>' +
          '<span class="wb-live-pill-text">' + esc(label) + '</span>' +
          (running.length > 0 ? '<span class="wb-live-badge">' + running.length + ' 进行中</span>' : '') +
        '</button>';
      host.querySelector('#wbLivePillBtn')?.addEventListener('click', () => {
        fState.mode = 'panel';
        renderDock();
      });
      return;
    }

    const itemsHTML = all.map(t => {
      const stLabel = t.status === 'running'
        ? '<span style="color:#4f46e5;font-weight:600">● 执行中' + (t.elapsedSec > 0 ? ' · ' + fmtSec(t.elapsedSec) : '') + '</span>'
        : t.status === 'done'
          ? '<span style="color:#15803d;font-weight:600">✓ 已完成</span>'
          : '<span style="color:#dc2626;font-weight:600">✕ 异常</span>';
      return (
        '<div class="wb-live-item ' + esc(t.status) + '">' +
          '<div style="display:flex;justify-content:space-between;align-items:center;gap:6px;margin-bottom:4px">' +
            '<strong style="font-size:12.5px;color:#0f172a">' + esc(t.title) + '</strong>' +
            stLabel +
          '</div>' +
          (t.progress ? '<div style="color:#475569;margin-bottom:4px">' + esc(t.progress) + '</div>' : '') +
          (t.detail ? '<div style="color:#64748b;font-size:11px;margin-bottom:5px">' + esc(t.detail) + '</div>' : '') +
          '<div style="display:flex;justify-content:space-between;align-items:center;gap:6px">' +
            '<span style="font-size:11px;color:#94a3b8">' + esc(t.projectName || t.projectId || '') + '</span>' +
            (t.urlHash ? '<button type="button" data-wb-jump="' + esc(t.urlHash) + '" style="border:1px solid #cbd5e1;background:#fff;border-radius:6px;padding:2px 8px;font-size:11px;cursor:pointer;color:#334155">前往查看 ↗</button>' : '') +
          '</div>' +
        '</div>'
      );
    }).join('');

    host.innerHTML =
      '<div class="wb-live-panel">' +
        '<div class="wb-live-panel-head">' +
          '<span>⚡ 后台活跃任务（' + running.length + ' 进行中）</span>' +
          '<div style="display:flex;gap:6px">' +
            (recentDone.length > 0 ? '<button type="button" id="wbLiveClearDone" style="border:none;background:rgba(255,255,255,0.14);color:#e2e8f0;border-radius:6px;padding:2px 8px;font-size:11px;cursor:pointer">清除已完成</button>' : '') +
            '<button type="button" id="wbLiveMinBtn" style="border:none;background:rgba(255,255,255,0.14);color:#f8fafc;border-radius:6px;padding:2px 8px;font-size:11px;cursor:pointer">收起</button>' +
          '</div>' +
        '</div>' +
        '<div class="wb-live-panel-body">' + itemsHTML + '</div>' +
      '</div>';

    host.querySelector('#wbLiveMinBtn')?.addEventListener('click', () => {
      fState.mode = 'minimized';
      renderDock();
    });
    host.querySelector('#wbLiveClearDone')?.addEventListener('click', () => {
      recentDone.forEach(t => { fState.dismissedDone[t.id] = true; });
      if (running.length === 0) fState.mode = 'minimized';
      renderDock();
    });
    host.querySelectorAll('[data-wb-jump]').forEach(btn => {
      btn.addEventListener('click', () => {
        const hash = btn.getAttribute('data-wb-jump');
        if (hash) location.hash = hash;
        fState.mode = 'minimized';
        renderDock();
      });
    });
  }

  window.addEventListener('webuddy:tasks-changed', () => {
    refreshFloating();
  });

  window.WebuddyFloating = {
    refresh: refreshFloating,
    open: () => { fState.mode = 'panel'; refreshFloating(); },
  };

  setTimeout(refreshFloating, 1200);
  setInterval(refreshFloating, 3000);
})();
