/* Webuddy v2 Interactive Visuals:
 *   1. Spatial Dot-Grid Architecture Blueprint Canvas (Entry Gateways -> Colored Domain Territories -> Shared Tables)
 *   2. Interactive ER & CRUD Heatmap Visual
 *   3. 4-Stage Code Review Gate Track & Effectiveness Donut Ring
 */
(function () {
  const DOMAIN_PALETTE = [
    { stroke: '#2563eb', soft: 'rgba(37,99,235,0.12)', bar: '#3b82f6' },
    { stroke: '#4f46e5', soft: 'rgba(79,70,229,0.12)', bar: '#6366f1' },
    { stroke: '#059669', soft: 'rgba(5,150,105,0.12)', bar: '#10b981' },
    { stroke: '#0891b2', soft: 'rgba(8,145,178,0.12)', bar: '#06b6d4' },
    { stroke: '#7c3aed', soft: 'rgba(124,58,237,0.12)', bar: '#8b5cf6' },
    { stroke: '#d97706', soft: 'rgba(217,119,6,0.12)', bar: '#f59e0b' },
    { stroke: '#e11d48', soft: 'rgba(225,29,72,0.12)', bar: '#f43f5e' },
    { stroke: '#0d9488', soft: 'rgba(13,148,136,0.12)', bar: '#14b8a6' },
  ];

  function esc(v) {
    return String(v == null ? '' : v)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  function trunc(text, max) {
    const s = String(text || '');
    return s.length > max ? s.slice(0, max - 1) + '…' : s;
  }

  /**
   * Renders the interactive dot-grid SVG Architecture Blueprint Canvas.
   */
  function renderArchitectureBlueprint(container, ov, opts) {
    if (!container) return;
    if (!ov || !ov.built) {
      container.innerHTML = '<div class="wb-panel wb-empty">选择上方已导入项目（如 <b>together-project</b>）或扫描代码图谱后，这里将展示可交互的 3 层空间架构蓝图。</div>';
      return;
    }

    const domains = (ov.domains || []).slice(0, 12);
    const flows = ov.flows || [];
    const tables = ov.tables || [];
    const crudMatrix = ov.crudMatrix || [];

    // Compute entry gateway kinds
    const kindCounts = {};
    const domainKinds = {};
    const domainTables = {};
    const tableDomains = {};

    flows.forEach(f => {
      const k = f.kind || (f.trigger ? '接口/入口' : '业务流程');
      kindCounts[k] = (kindCounts[k] || 0) + 1;
      const d = f.domain || '（未划分）';
      if (!domainKinds[d]) domainKinds[d] = new Set();
      domainKinds[d].add(k);
    });

    crudMatrix.forEach(c => {
      const d = c.domain || '（未划分）';
      if (!domainTables[d]) domainTables[d] = new Set();
      Object.keys(c.tables || {}).forEach(t => {
        domainTables[d].add(t);
        if (!tableDomains[t]) tableDomains[t] = new Set();
        tableDomains[t].add(d);
      });
    });

    const topEntries = Object.entries(kindCounts).sort((a, b) => b[1] - a[1]).slice(0, 5);
    const rankedTables = tables
      .slice()
      .sort((a, b) => ((tableDomains[b.name]?.size || 0) * 10 + (b.users || 0)) - ((tableDomains[a.name]?.size || 0) * 10 + (a.users || 0)))
      .slice(0, 10);

    const bpState = container._wbBpState || { zoom: 1, selectedDomain: '', selectedTable: '', hoverDomain: '', hoverTable: '' };
    container._wbBpState = bpState;

    function draw() {
      const W = 1120;
      const cols = 4;
      const domRows = Math.max(1, Math.ceil(domains.length / cols));
      const entryY = 44;
      const domStartY = 148;
      const domCardW = 244;
      const domCardH = 74;
      const domGapX = 24;
      const domGapY = 18;
      const tblStartY = domStartY + domRows * (domCardH + domGapY) + 56;
      const H = tblStartY + 110;

      const focusDom = bpState.hoverDomain || bpState.selectedDomain;
      const focusTbl = bpState.hoverTable || bpState.selectedTable;
      const hasSpot = !!(focusDom || focusTbl);

      const litDoms = new Set();
      const litTbls = new Set();
      const litEntries = new Set();

      if (focusDom) {
        litDoms.add(focusDom);
        (domainKinds[focusDom] || []).forEach(k => litEntries.add(k));
        (domainTables[focusDom] || []).forEach(t => litTbls.add(t));
      } else if (focusTbl) {
        litTbls.add(focusTbl);
        (tableDomains[focusTbl] || []).forEach(d => {
          litDoms.add(d);
          (domainKinds[d] || []).forEach(k => litEntries.add(k));
        });
      }

      // Compute node coordinates
      const entryPos = {};
      const entryW = 176;
      const entryGap = 20;
      const entryTotalW = topEntries.length * entryW + Math.max(0, topEntries.length - 1) * entryGap;
      const entryX0 = (W - entryTotalW) / 2;
      topEntries.forEach(([k], i) => {
        entryPos[k] = { x: entryX0 + i * (entryW + entryGap), y: entryY, w: entryW, h: 42 };
      });

      const domPos = {};
      const domTotalW = Math.min(cols, domains.length) * domCardW + Math.max(0, Math.min(cols, domains.length) - 1) * domGapX;
      const domX0 = (W - domTotalW) / 2;
      domains.forEach((d, i) => {
        const r = Math.floor(i / cols);
        const c = i % cols;
        domPos[d.key] = {
          x: domX0 + c * (domCardW + domGapX),
          y: domStartY + r * (domCardH + domGapY),
          w: domCardW,
          h: domCardH,
          tone: DOMAIN_PALETTE[i % DOMAIN_PALETTE.length],
        };
      });

      const tblPos = {};
      const tblCols = Math.min(5, Math.max(1, rankedTables.length));
      const tblW = 196;
      const tblH = 44;
      const tblGapX = 18;
      const tblGapY = 14;
      const tblTotalW = tblCols * tblW + (tblCols - 1) * tblGapX;
      const tblX0 = (W - tblTotalW) / 2;
      rankedTables.forEach((t, i) => {
        const r = Math.floor(i / tblCols);
        const c = i % tblCols;
        tblPos[t.name] = { x: tblX0 + c * (tblW + tblGapX), y: tblStartY + r * (tblH + tblGapY), w: tblW, h: tblH };
      });

      // Build curved connector edges
      const edges = [];
      domains.forEach(d => {
        const dp = domPos[d.key];
        (domainKinds[d.key] || []).forEach(k => {
          const ep = entryPos[k];
          if (!ep) return;
          const active = !hasSpot || (litDoms.has(d.key) && litEntries.has(k));
          const x1 = ep.x + ep.w / 2, y1 = ep.y + ep.h;
          const x2 = dp.x + dp.w / 2, y2 = dp.y;
          const my = (y1 + y2) / 2;
          edges.push(
            '<path d="M ' + x1 + ' ' + y1 + ' C ' + x1 + ' ' + my + ', ' + x2 + ' ' + my + ', ' + x2 + ' ' + y2 + '" ' +
            'fill="none" stroke="' + (active && hasSpot ? dp.tone.stroke : 'rgba(148,163,184,0.22)') + '" ' +
            'stroke-width="' + (active && hasSpot ? '2.2' : '1.1') + '" ' +
            'opacity="' + (active ? '0.9' : '0.18') + '" />'
          );
        });
        (domainTables[d.key] || []).forEach(tname => {
          const tp = tblPos[tname];
          if (!tp) return;
          const active = !hasSpot || (litDoms.has(d.key) && litTbls.has(tname));
          const x1 = dp.x + dp.w / 2, y1 = dp.y + dp.h;
          const x2 = tp.x + tp.w / 2, y2 = tp.y;
          const my = (y1 + y2) / 2;
          edges.push(
            '<path d="M ' + x1 + ' ' + y1 + ' C ' + x1 + ' ' + my + ', ' + x2 + ' ' + my + ', ' + x2 + ' ' + y2 + '" ' +
            'fill="none" stroke="' + (active && hasSpot ? dp.tone.stroke : 'rgba(148,163,184,0.2)') + '" ' +
            'stroke-width="' + (active && hasSpot ? '2.2' : '1.1') + '" ' +
            'stroke-dasharray="' + (active && hasSpot ? '' : '4 3') + '" ' +
            'opacity="' + (active ? '0.9' : '0.16') + '" />'
          );
        });
      });

      const entryNodes = topEntries.map(([k, count]) => {
        const p = entryPos[k];
        const dim = hasSpot && !litEntries.has(k);
        return (
          '<g opacity="' + (dim ? '0.3' : '1') + '">' +
            '<rect x="' + p.x + '" y="' + p.y + '" width="' + p.w + '" height="' + p.h + '" rx="9" fill="var(--panel)" stroke="rgba(148,163,184,0.35)" stroke-width="1.2"/>' +
            '<text x="' + (p.x + 12) + '" y="' + (p.y + 20) + '" font-size="12" font-weight="600" fill="currentColor">' + esc(trunc(k, 14)) + '</text>' +
            '<text x="' + (p.x + 12) + '" y="' + (p.y + 34) + '" font-size="10.5" fill="var(--muted,#64748b)">' + count + ' 条入口</text>' +
          '</g>'
        );
      }).join('');

      const domainNodes = domains.map(d => {
        const p = domPos[d.key];
        const dim = hasSpot && !litDoms.has(d.key);
        const sel = bpState.selectedDomain === d.key;
        const tCount = (domainTables[d.key] || new Set()).size;
        return (
          '<g class="wb-svg-node" data-bp-domain="' + esc(d.key) + '" opacity="' + (dim ? '0.26' : '1') + '" style="cursor:pointer">' +
            '<rect x="' + p.x + '" y="' + p.y + '" width="' + p.w + '" height="' + p.h + '" rx="10" fill="' + p.tone.soft + '" stroke="' + p.tone.stroke + '" stroke-width="' + (sel ? '2.6' : '1.4') + '"/>' +
            '<rect x="' + p.x + '" y="' + p.y + '" width="5" height="' + p.h + '" rx="2.5" fill="' + p.tone.bar + '"/>' +
            '<text x="' + (p.x + 16) + '" y="' + (p.y + 26) + '" font-size="13" font-weight="600" fill="currentColor">' + esc(trunc(d.title || d.key, 17)) + '</text>' +
            '<text x="' + (p.x + 16) + '" y="' + (p.y + 46) + '" font-size="11" fill="var(--muted,#64748b)">域标识: ' + esc(trunc(d.key, 18)) + '</text>' +
            '<text x="' + (p.x + 16) + '" y="' + (p.y + 63) + '" font-size="11" font-weight="600" fill="' + p.tone.stroke + '">' + d.flowCount + ' 流程 · ' + tCount + ' 关联表</text>' +
          '</g>'
        );
      }).join('');

      const tableNodes = rankedTables.map(t => {
        const p = tblPos[t.name];
        const dim = hasSpot && !litTbls.has(t.name);
        const sel = bpState.selectedTable === t.name;
        const sharedBy = (tableDomains[t.name] || new Set()).size;
        return (
          '<g class="wb-svg-node" data-bp-table="' + esc(t.name) + '" opacity="' + (dim ? '0.26' : '1') + '" style="cursor:pointer">' +
            '<rect x="' + p.x + '" y="' + p.y + '" width="' + p.w + '" height="' + p.h + '" rx="8" fill="var(--panel)" stroke="' + (sel || sharedBy > 1 ? '#0ea5e9' : 'rgba(148,163,184,0.35)') + '" stroke-width="' + (sel ? '2.4' : '1.2') + '"/>' +
            '<text x="' + (p.x + 10) + '" y="' + (p.y + 19) + '" font-size="11.5" font-weight="600" fill="currentColor">' + esc(trunc(t.name, 20)) + '</text>' +
            '<text x="' + (p.x + 10) + '" y="' + (p.y + 35) + '" font-size="10" fill="var(--muted,#64748b)">' + (t.users || 0) + ' 引用' + (sharedBy > 1 ? ' · 跨 ' + sharedBy + ' 域共享' : '') + '</text>' +
          '</g>'
        );
      }).join('');

      // Drill-down inspector panel when a domain or table is selected
      let inspectorHTML = '';
      if (bpState.selectedDomain) {
        const domObj = domains.find(d => d.key === bpState.selectedDomain) || { key: bpState.selectedDomain, title: bpState.selectedDomain };
        const domFlows = flows.filter(f => (f.domain || '（未划分）') === bpState.selectedDomain).slice(0, 20);
        inspectorHTML =
          '<div class="wb-panel" style="margin-top:12px">' +
            '<div class="wb-item-top" style="margin-bottom:8px">' +
              '<h3 style="margin:0">业务域下钻：' + esc(domObj.title) + '（' + domFlows.length + ' 条示例流程）</h3>' +
              '<button type="button" class="button ghost small" id="wbBpClearSelect">清除选中</button>' +
            '</div>' +
            '<table class="wb-table"><thead><tr><th>流程名称</th><th>触发入口</th><th>读写表</th><th>源码证据（点击查看）</th></tr></thead><tbody>' +
            domFlows.map(f => {
              const cite = f.file ? (f.file + (f.line ? ':' + f.line : '')) : '';
              return '<tr><td><b>' + esc(f.name) + '</b></td><td><code>' + esc(f.trigger || f.kind || '—') + '</code></td><td>' + renderCrudChips(f.crud) + '</td><td>' +
                (cite ? '<button type="button" class="button ghost small" data-bp-cite="' + esc(cite) + '">' + esc(cite) + '</button>' : '—') +
              '</td></tr>';
            }).join('') +
            '</tbody></table>' +
          '</div>';
      } else if (bpState.selectedTable) {
        const tFlows = crudMatrix.filter(c => c.tables && Object.prototype.hasOwnProperty.call(c.tables, bpState.selectedTable));
        inspectorHTML =
          '<div class="wb-panel" style="margin-top:12px">' +
            '<div class="wb-item-top" style="margin-bottom:8px">' +
              '<h3 style="margin:0">数据表影响面反查：' + esc(bpState.selectedTable) + '（' + tFlows.length + ' 条读写流程）</h3>' +
              '<button type="button" class="button ghost small" id="wbBpClearSelect">清除选中</button>' +
            '</div>' +
            '<table class="wb-table"><thead><tr><th>业务流程</th><th>所属域</th><th>操作类型</th></tr></thead><tbody>' +
            (tFlows.map(c => '<tr><td><b>' + esc(c.flowName) + '</b></td><td><span class="wb-badge info">' + esc(c.domain) + '</span></td><td>' + renderCrudOp(c.tables[bpState.selectedTable]) + '</td></tr>').join('') || '<tr><td colspan="3" class="wb-empty">暂无 CRUD 记录</td></tr>') +
            '</tbody></table>' +
          '</div>';
      }

      // For non-Java/Go/Python repos (e.g. Node.js/TypeScript) where static flows/tables are 0
      // but module knowledge pages or sourceFiles exist, render an interactive Module Knowledge Map
      // instead of an empty dot grid.
      const modPages = (ov.pages || []).filter(p => p && p.path);
      if (domains.length === 0 && flows.length === 0 && (modPages.length > 0 || (ov.sourceFiles || []).length > 0)) {
        const analyzedCount = modPages.filter(p => p.status && p.status !== '未分析').length;
        const pid = (window.state && window.state.projectFilter) || '';
        const cardsHTML = modPages.map(p => {
          const isDone = p.status && p.status !== '未分析';
          const badgeCls = p.status === '已确认' ? 'pass' : isDone ? 'info' : '';
          const badgeStyle = !isDone ? 'background:#f1f5f9;color:#64748b' : '';
          return (
            '<div class="wb-finding-card" data-mod-open="' + esc(p.path) + '" style="cursor:pointer;display:flex;flex-direction:column;gap:6px;padding:12px 14px;border-color:' + (isDone ? 'rgba(34,197,94,0.45)' : 'var(--line,#e2e8f0)') + ';background:' + (isDone ? 'linear-gradient(180deg,#f0fdf4 0%,#fff 100%)' : '#fff') + '">' +
              '<div style="display:flex;justify-content:space-between;align-items:center;gap:8px">' +
                '<strong style="font-size:13.5px;color:#0f172a">' + (isDone ? '✅ ' : '📄 ') + esc(p.title || p.name || p.path) + '</strong>' +
                '<span class="wb-badge ' + badgeCls + '" style="' + badgeStyle + '">' + esc(p.status || '未分析') + '</span>' +
              '</div>' +
              (p.summary ? '<div style="font-size:12.5px;color:#334155;line-height:1.5">' + esc(p.summary) + '</div>' : '<div style="font-size:12px;color:#94a3b8">尚未分析，点击上方「后台继续分析」自动生成带源码证据的业务说明</div>') +
              '<div style="display:flex;justify-content:space-between;align-items:center;font-size:11px;color:#64748b;margin-top:auto">' +
                '<code>' + esc(p.path) + '</code>' +
                (p.updated ? '<span>' + esc(p.updated) + '</span>' : '') +
              '</div>' +
            '</div>'
          );
        }).join('');

        container.innerHTML =
          '<div class="wb-panel">' +
            '<div class="wb-item-top" style="margin-bottom:12px;flex-wrap:wrap;gap:10px">' +
              '<div>' +
                '<h3 style="margin:0">模块知识地图（已分析 ' + analyzedCount + ' / 共 ' + modPages.length + ' 页 · 收录 ' + Number(ov.sourceFileCount || 0) + ' 个源码文件）</h3>' +
                '<p class="wb-subtitle" style="margin:3px 0 0">点击任意模块卡片可查看完整业务说明与真实源码 file:line 证据。</p>' +
              '</div>' +
              '<div class="wb-actions">' +
                '<button type="button" class="button primary small" id="wbModBgUpdateBtn">🚀 后台继续分析 3 页（不阻塞）</button>' +
              '</div>' +
            '</div>' +
            '<div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(300px,1fr));gap:10px">' +
              (cardsHTML || '<div class="wb-empty">已收录源码文件，点击上方按钮开始生成模块知识页。</div>') +
            '</div>' +
          '</div>' +
          '<div id="wbModDocViewer" style="margin-top:12px"></div>';

        const bgBtn = container.querySelector('#wbModBgUpdateBtn');
        if (bgBtn) {
          bgBtn.onclick = async () => {
            bgBtn.disabled = true;
            try {
              const res = await fetch('/api/webuddy/kb/batch-update', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                credentials: 'same-origin',
                body: JSON.stringify({ projectId: pid, limit: 3, async: true }),
              });
              if (res.ok) {
                window.dispatchEvent(new CustomEvent('webuddy:tasks-changed'));
                if (typeof window.toast === 'function') window.toast('已转入后台持续分析，右下角浮动胶囊实时显示进度', 'ok');
              }
            } finally {
              setTimeout(() => { bgBtn.disabled = false; }, 1500);
            }
          };
        }
        container.querySelectorAll('[data-mod-open]').forEach(el => {
          el.onclick = async () => {
            const docPath = el.getAttribute('data-mod-open');
            const viewer = container.querySelector('#wbModDocViewer');
            if (!viewer || !docPath) return;
            viewer.innerHTML = '<div class="wb-panel"><p class="wb-subtitle">正在读取 ' + esc(docPath) + ' ...</p></div>';
            try {
              const url = '/api/webuddy/kb/file?repoPath=' + encodeURIComponent(ov.repoPath || '') + '&path=' + encodeURIComponent(docPath);
              const r = await fetch(url, { credentials: 'same-origin' });
              const d = await r.json();
              viewer.innerHTML =
                '<div class="wb-panel">' +
                  '<div class="wb-item-top" style="margin-bottom:8px"><strong>📄 ' + esc(docPath) + '</strong><button type="button" class="button ghost small" id="wbModDocClose">关闭</button></div>' +
                  '<pre style="white-space:pre-wrap;word-break:break-word;font-size:12.5px;line-height:1.6;background:#f8fafc;padding:12px;border-radius:10px;border:1px solid #e2e8f0;max-height:480px;overflow:auto">' + esc(d.content || d.error || '无内容') + '</pre>' +
                '</div>';
              viewer.querySelector('#wbModDocClose')?.addEventListener('click', () => { viewer.innerHTML = ''; });
              viewer.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
            } catch (err) {
              viewer.innerHTML = '<div class="wb-panel"><p style="color:#dc2626">读取失败：' + esc(err.message) + '</p></div>';
            }
          };
        });
        return;
      }

      container.innerHTML =
        '<div class="wb-panel">' +
          '<div class="wb-item-top" style="margin-bottom:10px">' +
            '<div><h3 style="margin:0">空间架构蓝图（入口网关 → 业务域领地 → 共享数据核心）</h3><p class="wb-subtitle" style="margin:2px 0 0">悬停任意业务域或数据表触发聚光灯联动，点击节点下钻流程与真实源码证据。</p></div>' +
            '<div class="wb-actions">' +
              '<button type="button" class="button ghost small" id="wbBpZoomOut">−</button>' +
              '<span class="wb-meta">' + Math.round(bpState.zoom * 100) + '%</span>' +
              '<button type="button" class="button ghost small" id="wbBpZoomIn">+</button>' +
              '<button type="button" class="button ghost small" id="wbBpZoomReset">重置</button>' +
            '</div>' +
          '</div>' +
          '<div class="wb-dot-canvas">' +
            '<svg width="' + Math.round(W * bpState.zoom) + '" height="' + Math.round(H * bpState.zoom) + '" viewBox="0 0 ' + W + ' ' + H + '">' +
              '<text x="20" y="24" font-size="11" font-weight="600" fill="var(--muted,#64748b)">ENTRY GATEWAYS · 入口网关</text>' +
              '<text x="20" y="132" font-size="11" font-weight="600" fill="var(--muted,#64748b)">DOMAIN TERRITORIES · 核心业务域（Top ' + domains.length + ' / 共 ' + (ov.domains || []).length + ' 域）</text>' +
              '<text x="20" y="' + (tblStartY - 14) + '" font-size="11" font-weight="600" fill="var(--muted,#64748b)">SHARED DATA CORE · 高频与跨城共享数据表</text>' +
              edges.join('') +
              entryNodes +
              domainNodes +
              tableNodes +
            '</svg>' +
          '</div>' +
        '</div>' +
        inspectorHTML;

      container.querySelector('#wbBpZoomIn')?.addEventListener('click', () => { bpState.zoom = Math.min(1.6, +(bpState.zoom + 0.15).toFixed(2)); draw(); });
      container.querySelector('#wbBpZoomOut')?.addEventListener('click', () => { bpState.zoom = Math.max(0.65, +(bpState.zoom - 0.15).toFixed(2)); draw(); });
      container.querySelector('#wbBpZoomReset')?.addEventListener('click', () => { bpState.zoom = 1; bpState.selectedDomain = ''; bpState.selectedTable = ''; draw(); });
      container.querySelector('#wbBpClearSelect')?.addEventListener('click', () => { bpState.selectedDomain = ''; bpState.selectedTable = ''; draw(); });

      container.querySelectorAll('[data-bp-domain]').forEach(el => {
        el.addEventListener('mouseenter', () => { if (bpState.hoverDomain !== el.dataset.bpDomain) { bpState.hoverDomain = el.dataset.bpDomain; bpState.hoverTable = ''; draw(); } });
        el.addEventListener('mouseleave', () => { if (bpState.hoverDomain) { bpState.hoverDomain = ''; draw(); } });
        el.addEventListener('click', () => { bpState.selectedDomain = el.dataset.bpDomain; bpState.selectedTable = ''; draw(); });
      });
      container.querySelectorAll('[data-bp-table]').forEach(el => {
        el.addEventListener('mouseenter', () => { if (bpState.hoverTable !== el.dataset.bpTable) { bpState.hoverTable = el.dataset.bpTable; bpState.hoverDomain = ''; draw(); } });
        el.addEventListener('mouseleave', () => { if (bpState.hoverTable) { bpState.hoverTable = ''; draw(); } });
        el.addEventListener('click', () => { bpState.selectedTable = el.dataset.bpTable; bpState.selectedDomain = ''; draw(); });
      });
      container.querySelectorAll('[data-bp-cite]').forEach(btn => {
        btn.addEventListener('click', () => {
          if (opts && typeof opts.onCite === 'function') opts.onCite(btn.dataset.bpCite);
        });
      });
    }

    draw();
  }

  function renderCrudOp(op) {
    const s = String(op || '').toUpperCase();
    return s.split('').map(ch => {
      const cls = ch === 'C' ? 'crud-c' : ch === 'R' ? 'crud-r' : ch === 'U' ? 'crud-u' : ch === 'D' ? 'crud-d' : 'crud-r';
      return '<span class="wb-crud-chip ' + cls + '">' + esc(ch) + '</span>';
    }).join('');
  }

  function renderCrudChips(crudObj) {
    const entries = Object.entries(crudObj || {}).slice(0, 4);
    if (!entries.length) return '<span class="wb-meta">—</span>';
    return entries.map(([t, op]) =>
      '<span class="wb-meta" style="display:inline-flex;align-items:center;gap:3px;margin-right:6px"><code>' + esc(t) + '</code>' + renderCrudOp(op) + '</span>'
    ).join('');
  }

  /**
   * Renders the 4-stage visual review gate track + SVG effectiveness donut ring.
   */
  function renderReviewGateTrack(r, summary) {
    if (!r) return '';
    const steps = [
      { label: '1. 全量 14k 分批', ok: r.totalParts > 0 && r.complete, detail: r.totalParts + ' 批 / ' + (r.complete ? '100% 覆盖' : '存在未覆盖') },
      { label: '2. 独立执行器', ok: !!r.differentExecutor, detail: (r.writer || '?') + ' ≠ ' + (r.reviewer || '?') },
      { label: '3. 防篡改与二进制闸门', ok: !r.binaryDetected && !(r.uncovered || []).some(u => u.includes('基准')), detail: r.binaryDetected ? '含二进制需专项核验' : 'Base & Diff SHA256 锁定' },
      { label: '4. 结论与人工闭环', ok: r.verdict === 'pass', warn: r.verdict === 'fail', detail: r.verdict === 'pass' ? '放行通过' : r.verdict === 'fail' ? ('阻断 ' + (r.blocking || []).length + ' 项') : '待核实' },
    ];

    const rate = summary && summary.validRate != null ? summary.validRate : null;
    const pct = rate == null ? 0 : Math.max(0, Math.min(100, rate));
    const circ = 2 * Math.PI * 22;
    const dash = ((pct / 100) * circ).toFixed(1);

    return (
      '<div class="wb-panel wb-gate-strip">' +
        '<div class="wb-gate-steps">' +
          steps.map(st =>
            '<div class="wb-gate-step ' + (st.ok ? 'ok' : st.warn ? 'fail' : 'warn') + '">' +
              '<div class="wb-gate-dot"></div>' +
              '<div><div class="wb-gate-title">' + esc(st.label) + '</div><div class="wb-gate-sub">' + esc(st.detail) + '</div></div>' +
            '</div>'
          ).join('') +
        '</div>' +
        '<div class="wb-ring-box" title="项目 AI Review 有效率">' +
          '<svg width="56" height="56" viewBox="0 0 56 56">' +
            '<circle cx="28" cy="28" r="22" fill="none" stroke="rgba(148,163,184,0.2)" stroke-width="5"/>' +
            '<circle cx="28" cy="28" r="22" fill="none" stroke="#10b981" stroke-width="5" stroke-linecap="round" stroke-dasharray="' + dash + ' ' + circ.toFixed(1) + '" transform="rotate(-90 28 28)"/>' +
            '<text x="28" y="32" text-anchor="middle" font-size="11" font-weight="700" fill="currentColor">' + (rate == null ? '—' : Math.round(rate) + '%') + '</text>' +
          '</svg>' +
          '<div class="wb-meta">有效率</div>' +
        '</div>' +
      '</div>'
    );
  }

  /**
   * Renders an interactive dot-grid SVG ER Entity Relation Graph.
   * Solid blue edges = DDL Foreign Keys; Dashed cyan edges = Co-accessed in same business flow (explicitly marked inferred).
   */
  function renderERGraph(container, ov, opts) {
    if (!container) return;
    if (!ov || !ov.built || !(ov.tables || []).length) {
      container.innerHTML = '<div class="wb-panel wb-empty">扫描代码图谱后，这里将展示数据表实体卡片、外键连线与同流程共现关系图。</div>';
      return;
    }

    const crudMatrix = ov.crudMatrix || [];
    const fkEdges = ov.erEdges || [];
    const tables = (ov.tables || []).slice().sort((a, b) => (b.users || 0) - (a.users || 0)).slice(0, 16);
    const tableSet = new Set(tables.map(t => t.name));

    // Build real FK edges + inferred co-occurrence edges within top 16 tables
    const relEdges = [];
    const seenEdge = new Set();
    fkEdges.forEach(e => {
      if (tableSet.has(e.fromTable) && tableSet.has(e.toTable) && e.fromTable !== e.toTable) {
        const key = 'fk:' + e.fromTable + '->' + e.toTable;
        if (!seenEdge.has(key)) {
          seenEdge.add(key);
          relEdges.push({ from: e.fromTable, to: e.toTable, kind: 'fk', label: e.fromColumn + ' → ' + e.toColumn });
        }
      }
    });

    const coCount = {};
    crudMatrix.forEach(c => {
      const ts = Object.keys(c.tables || {}).filter(t => tableSet.has(t));
      for (let i = 0; i < ts.length; i++) {
        for (let j = i + 1; j < ts.length; j++) {
          const pair = ts[i] < ts[j] ? (ts[i] + '|' + ts[j]) : (ts[j] + '|' + ts[i]);
          coCount[pair] = (coCount[pair] || 0) + 1;
        }
      }
    });
    Object.entries(coCount)
      .sort((a, b) => b[1] - a[1])
      .slice(0, 28)
      .forEach(([pair, cnt]) => {
        const [a, b] = pair.split('|');
        if (!seenEdge.has('fk:' + a + '->' + b) && !seenEdge.has('fk:' + b + '->' + a)) {
          relEdges.push({ from: a, to: b, kind: 'co', label: '共现 ' + cnt + ' 流程（推断）' });
        }
      });

    const erState = container._wbErState || { zoom: 1, selected: '', hover: '' };
    container._wbErState = erState;

    function draw() {
      const cols = 4;
      const cardW = 236;
      const cardH = 112;
      const gapX = 34;
      const gapY = 34;
      const rows = Math.max(1, Math.ceil(tables.length / cols));
      const W = cols * cardW + (cols - 1) * gapX + 80;
      const H = rows * cardH + (rows - 1) * gapY + 80;

      const pos = {};
      tables.forEach((t, i) => {
        const r = Math.floor(i / cols);
        const c = i % cols;
        pos[t.name] = { x: 40 + c * (cardW + gapX), y: 40 + r * (cardH + gapY), w: cardW, h: cardH };
      });

      const focus = erState.hover || erState.selected;
      const lit = new Set();
      if (focus) {
        lit.add(focus);
        relEdges.forEach(e => {
          if (e.from === focus) lit.add(e.to);
          if (e.to === focus) lit.add(e.from);
        });
      }

      const edgeSVG = relEdges.map(e => {
        const p1 = pos[e.from], p2 = pos[e.to];
        if (!p1 || !p2) return '';
        const active = !focus || e.from === focus || e.to === focus;
        const x1 = p1.x + p1.w / 2, y1 = p1.y + p1.h / 2;
        const x2 = p2.x + p2.w / 2, y2 = p2.y + p2.h / 2;
        const mx = (x1 + x2) / 2, my = (y1 + y2) / 2 - 22;
        const color = e.kind === 'fk' ? '#3b82f6' : '#06b6d4';
        return (
          '<path d="M ' + x1 + ' ' + y1 + ' Q ' + mx + ' ' + my + ' ' + x2 + ' ' + y2 + '" ' +
          'fill="none" stroke="' + (active ? color : 'rgba(148,163,184,0.22)') + '" ' +
          'stroke-width="' + (active && focus ? '2.4' : '1.3') + '" ' +
          (e.kind === 'co' ? 'stroke-dasharray="5 4" ' : '') +
          'opacity="' + (active ? '0.88' : '0.16') + '"><title>' + esc(e.from + ' ↔ ' + e.to + ' : ' + e.label) + '</title></path>'
        );
      }).join('');

      const cardSVG = tables.map((t, idx) => {
        const p = pos[t.name];
        const dim = focus && !lit.has(t.name);
        const sel = erState.selected === t.name;
        const tone = DOMAIN_PALETTE[idx % DOMAIN_PALETTE.length];
        const colsPreview = (t.columns || []).slice(0, 3);
        return (
          '<g class="wb-svg-node" data-er-table="' + esc(t.name) + '" opacity="' + (dim ? '0.26' : '1') + '" style="cursor:pointer">' +
            '<rect x="' + p.x + '" y="' + p.y + '" width="' + p.w + '" height="' + p.h + '" rx="9" fill="var(--panel)" stroke="' + (sel ? tone.stroke : 'rgba(148,163,184,0.34)') + '" stroke-width="' + (sel ? '2.4' : '1.2') + '"/>' +
            '<rect x="' + p.x + '" y="' + p.y + '" width="' + p.w + '" height="28" rx="9" fill="' + tone.soft + '"/>' +
            '<text x="' + (p.x + 10) + '" y="' + (p.y + 18) + '" font-size="12" font-weight="700" fill="currentColor">' + esc(trunc(t.name, 22)) + '</text>' +
            '<text x="' + (p.x + p.w - 10) + '" y="' + (p.y + 18) + '" text-anchor="end" font-size="10.5" fill="' + tone.stroke + '">' + (t.users || 0) + ' 引用</text>' +
            (colsPreview.length
              ? colsPreview.map((c, ci) =>
                  '<text x="' + (p.x + 10) + '" y="' + (p.y + 46 + ci * 18) + '" font-size="10.5" fill="var(--muted,#64748b)">' +
                    (c.pk ? '[PK] ' : c.fk ? '[FK] ' : '• ') + esc(trunc(c.name + ' : ' + c.type, 26)) +
                  '</text>'
                ).join('')
              : '<text x="' + (p.x + 10) + '" y="' + (p.y + 54) + '" font-size="10.5" fill="var(--muted,#64748b)">实体：' + esc(trunc(t.entity || '（由代码调用解析）', 22)) + '</text>' +
                '<text x="' + (p.x + 10) + '" y="' + (p.y + 74) + '" font-size="10" fill="var(--muted,#64748b)">位置：' + esc(trunc(t.file || '—', 26)) + '</text>') +
          '</g>'
        );
      }).join('');

      let detailHTML = '';
      if (erState.selected) {
        const tbl = tables.find(x => x.name === erState.selected);
        const relFlows = crudMatrix.filter(c => c.tables && Object.prototype.hasOwnProperty.call(c.tables, erState.selected));
        const rels = relEdges.filter(e => e.from === erState.selected || e.to === erState.selected);
        detailHTML =
          '<div class="wb-panel" style="margin-top:12px">' +
            '<div class="wb-item-top" style="margin-bottom:8px">' +
              '<h3 style="margin:0">数据表详情：' + esc(erState.selected) + '（关联 ' + rels.length + ' 张表 · ' + relFlows.length + ' 条读写流程）</h3>' +
              '<div class="wb-actions">' +
                (tbl && tbl.file ? '<button type="button" class="button ghost small" data-er-cite="' + esc(tbl.file + (tbl.line ? ':' + tbl.line : ':1')) + '">定位定义源码</button>' : '') +
                '<button type="button" class="button ghost small" id="wbErClear">清除选中</button>' +
              '</div>' +
            '</div>' +
            (rels.length ? '<div class="wb-meta" style="margin-bottom:10px">' + rels.map(e => '<span class="wb-badge ' + (e.kind === 'fk' ? 'pass' : 'info') + '">' + esc((e.from === erState.selected ? e.to : e.from) + ' · ' + e.label) + '</span>').join('') + '</div>' : '') +
            '<table class="wb-table"><thead><tr><th>读写业务流程</th><th>所属域</th><th>CRUD 操作</th></tr></thead><tbody>' +
            (relFlows.map(c => '<tr><td><b>' + esc(c.flowName) + '</b></td><td><span class="wb-badge info">' + esc(c.domain) + '</span></td><td>' + renderCrudOp(c.tables[erState.selected]) + '</td></tr>').join('') || '<tr><td colspan="3" class="wb-empty">暂无 CRUD 记录</td></tr>') +
            '</tbody></table>' +
          '</div>';
      }

      container.innerHTML =
        '<div class="wb-panel">' +
          '<div class="wb-item-top" style="margin-bottom:10px">' +
            '<div><h3 style="margin:0">ER 实体关系图（Top ' + tables.length + ' 核心表 / 共 ' + (ov.tableCount || tables.length) + ' 表）</h3><p class="wb-subtitle" style="margin:2px 0 0">蓝色实线 = DDL 外键约束；青色虚线 = 同流程共现（推断）。点击任意表卡片下钻字段与 CRUD 影响面。</p></div>' +
            '<div class="wb-actions">' +
              '<button type="button" class="button ghost small" id="wbErZoomOut">−</button>' +
              '<span class="wb-meta">' + Math.round(erState.zoom * 100) + '%</span>' +
              '<button type="button" class="button ghost small" id="wbErZoomIn">+</button>' +
              '<button type="button" class="button ghost small" id="wbErZoomReset">重置</button>' +
            '</div>' +
          '</div>' +
          '<div class="wb-dot-canvas">' +
            '<svg width="' + Math.round(W * erState.zoom) + '" height="' + Math.round(H * erState.zoom) + '" viewBox="0 0 ' + W + ' ' + H + '">' +
              edgeSVG + cardSVG +
            '</svg>' +
          '</div>' +
        '</div>' +
        detailHTML;

      container.querySelector('#wbErZoomIn')?.addEventListener('click', () => { erState.zoom = Math.min(1.6, +(erState.zoom + 0.15).toFixed(2)); draw(); });
      container.querySelector('#wbErZoomOut')?.addEventListener('click', () => { erState.zoom = Math.max(0.65, +(erState.zoom - 0.15).toFixed(2)); draw(); });
      container.querySelector('#wbErZoomReset')?.addEventListener('click', () => { erState.zoom = 1; erState.selected = ''; draw(); });
      container.querySelector('#wbErClear')?.addEventListener('click', () => { erState.selected = ''; draw(); });
      container.querySelectorAll('[data-er-table]').forEach(el => {
        el.addEventListener('mouseenter', () => { if (erState.hover !== el.dataset.erTable) { erState.hover = el.dataset.erTable; draw(); } });
        el.addEventListener('mouseleave', () => { if (erState.hover) { erState.hover = ''; draw(); } });
        el.addEventListener('click', () => { erState.selected = el.dataset.erTable; draw(); });
      });
      container.querySelectorAll('[data-er-cite]').forEach(btn => {
        btn.addEventListener('click', () => { if (opts && typeof opts.onCite === 'function') opts.onCite(btn.dataset.erCite); });
      });
    }

    draw();
  }

  /**
   * Renders interactive Business Statecharts + live StatechartCodePinpoint viewer.
   * Uses clean non-overlapping routing: adjacent forward edges run in generous gaps
   * with pill labels; skip-forward edges arc above nodes; backward/cycle edges arc
   * below nodes; node cards use opaque panel fills so edges never bleed through.
   */
  function renderStatechartsView(container, data, opts) {
    if (!container) return;
    const charts = (data && data.charts) || [];
    if (!charts.length) {
      container.innerHTML = '<div class="wb-panel wb-empty">当前项目暂无状态图。请先选择已导入项目或扫描代码图谱（同时支持 .tmh/knowledge/statecharts/*.json 与图谱实测流转推导）。</div>';
      return;
    }

    const scState = container._wbScState || {
      selectedChartId: charts[0].id,
      selectedNodeId: '',
      filter: '',
      zoom: 1,
      pinpoint: null,
      loadingPin: false,
    };
    if (!charts.some(c => c.id === scState.selectedChartId)) scState.selectedChartId = charts[0].id;
    container._wbScState = scState;

    async function pinpointWhere(where, label) {
      if (!where || !opts || !opts.repoPath) return;
      scState.loadingPin = true;
      scState.pinpoint = { where, label };
      draw();
      try {
        const parts = where.split(':');
        const file = parts[0];
        const lineSpec = (parts[1] || '1').split('-');
        const line = Math.max(1, parseInt(lineSpec[0], 10) || 1);
        const start = Math.max(1, line - 8);
        const end = line + 16;
        const params = new URLSearchParams({ repoPath: opts.repoPath, path: file, start: String(start), end: String(end) });
        const res = await fetch('/api/webuddy/kb/file?' + params.toString());
        const body = await res.json();
        if (!res.ok) throw new Error(body.error || '读取源码失败');
        scState.pinpoint = { where, label, focusLine: line, file: body.path, start: body.start, end: body.end, content: body.content };
      } catch (err) {
        scState.pinpoint = { where, label, error: err.message };
      } finally {
        scState.loadingPin = false;
        draw();
      }
    }

    function draw() {
      const chart = charts.find(c => c.id === scState.selectedChartId) || charts[0];
      const nodes = chart.nodes || [];
      const edges = chart.edges || [];
      const rules = chart.rules || [];
      const nodeLabelMap = { '[*]': '开始 / 结束' };
      nodes.forEach(n => { nodeLabelMap[n.id] = n.label || n.id; });

      // Generous horizontal spacing so labels fit cleanly between cards
      const nodeW = 224, nodeH = 84, gapX = 124;
      const startX = 96;
      const idxMap = { '[*]': -1 };
      nodes.forEach((n, i) => { idxMap[n.id] = i; });

      // Count skip-forward (above) & backward/cycle (below) arcs to size vertical margins cleanly
      let topArcCount = 0;
      let bottomArcCount = 0;
      const routedEdges = edges.map(e => {
        const i1 = idxMap[e.from];
        const i2 = idxMap[e.to];
        if (i1 === undefined || i2 === undefined) return null;
        if (e.from === '[*]' && i2 === 0) return { edge: e, kind: 'start', lane: 0 };
        if (e.to === '[*]') return { edge: e, kind: 'end', lane: 0 };
        if (i2 === i1 + 1) return { edge: e, kind: 'adjacent', lane: 0 };
        if (i2 > i1 + 1) {
          topArcCount++;
          return { edge: e, kind: 'skip', lane: topArcCount };
        }
        bottomArcCount++;
        return { edge: e, kind: 'back', lane: bottomArcCount };
      }).filter(Boolean);

      const topMargin = 48 + topArcCount * 46;
      const bottomMargin = 48 + bottomArcCount * 46;
      const nodeY = topMargin;
      const centerY = nodeY + nodeH / 2;
      const hasEndMarker = routedEdges.some(r => r.kind === 'end');
      const W = Math.max(820, startX + nodes.length * (nodeW + gapX) - gapX + (hasEndMarker ? 110 : 60));
      const H = Math.max(240, topMargin + nodeH + bottomMargin);

      const nPos = {
        '[*]': { x: 24, y: centerY - 10, w: 20, h: 20 },
      };
      nodes.forEach((n, i) => {
        nPos[n.id] = {
          x: startX + i * (nodeW + gapX),
          y: nodeY,
          w: nodeW,
          h: nodeH,
          tone: DOMAIN_PALETTE[i % DOMAIN_PALETTE.length],
        };
      });
      const endPos = { x: startX + nodes.length * (nodeW + gapX) - gapX + 56, y: centerY - 10, w: 20, h: 20 };

      function pillLabel(x, y, text, toneColor) {
        if (!text) return '';
        const clean = trunc(text, 16);
        const estW = Math.max(44, clean.length * 10.5 + 18);
        return (
          '<g>' +
            '<rect x="' + (x - estW / 2) + '" y="' + (y - 11) + '" width="' + estW + '" height="20" rx="10" fill="var(--panel, #ffffff)" stroke="' + (toneColor || 'rgba(148,163,184,0.38)') + '" stroke-width="1"/>' +
            '<text x="' + x + '" y="' + (y + 3) + '" text-anchor="middle" font-size="10.5" font-weight="500" fill="var(--text, #334155)">' + esc(clean) + '</text>' +
          '</g>'
        );
      }

      const edgeSVG = routedEdges.map(item => {
        const e = item.edge;
        const p1 = nPos[e.from];
        const p2 = item.kind === 'end' ? endPos : nPos[e.to];
        if (!p1 || !p2) return '';

        if (item.kind === 'start' || item.kind === 'adjacent' || item.kind === 'end') {
          const x1 = p1.x + p1.w;
          const y1 = p1.y + p1.h / 2;
          const x2 = p2.x - 2;
          const y2 = p2.y + p2.h / 2;
          const mx = (x1 + x2) / 2;
          return (
            '<g>' +
              '<path d="M ' + x1 + ' ' + y1 + ' L ' + x2 + ' ' + y2 + '" fill="none" stroke="rgba(100,116,139,0.55)" stroke-width="1.8" marker-end="url(#wbScArrow)"/>' +
              pillLabel(mx, y1 - 18, e.label, 'rgba(148,163,184,0.45)') +
            '</g>'
          );
        }

        if (item.kind === 'skip') {
          const x1 = p1.x + p1.w * 0.65;
          const y1 = p1.y;
          const x2 = p2.x + p2.w * 0.35;
          const y2 = p2.y - 2;
          const arcY = nodeY - item.lane * 42;
          const mx = (x1 + x2) / 2;
          const labelY = 0.25 * y1 + 0.5 * arcY + 0.25 * y2;
          return (
            '<g>' +
              '<path d="M ' + x1 + ' ' + y1 + ' Q ' + mx + ' ' + arcY + ' ' + x2 + ' ' + y2 + '" fill="none" stroke="rgba(59,130,246,0.58)" stroke-width="1.6" marker-end="url(#wbScArrowBlue)"/>' +
              pillLabel(mx, labelY, e.label, 'rgba(59,130,246,0.38)') +
            '</g>'
          );
        }

        // Back / cycle edge routed cleanly below the cards
        const x1 = p1.x + p1.w * 0.4;
        const y1 = p1.y + p1.h;
        const x2 = p2.x + p2.w * 0.6;
        const y2 = p2.y + p2.h + 2;
        const arcY = nodeY + nodeH + item.lane * 44;
        const mx = (x1 + x2) / 2;
        const labelY = 0.25 * y1 + 0.5 * arcY + 0.25 * y2;
        return (
          '<g>' +
            '<path d="M ' + x1 + ' ' + y1 + ' Q ' + mx + ' ' + arcY + ' ' + x2 + ' ' + y2 + '" fill="none" stroke="rgba(139,92,246,0.58)" stroke-width="1.6" stroke-dasharray="5 4" marker-end="url(#wbScArrowPurple)"/>' +
            pillLabel(mx, labelY, e.label, 'rgba(139,92,246,0.38)') +
          '</g>'
        );
      }).join('');

      const nodeSVG = nodes.map(n => {
        const p = nPos[n.id];
        const sel = scState.selectedNodeId === n.id;
        return (
          '<g class="wb-svg-node" data-sc-node="' + esc(n.id) + '" data-sc-where="' + esc(n.where || '') + '" data-sc-label="' + esc(n.label) + '" style="cursor:pointer">' +
            '<rect x="' + p.x + '" y="' + p.y + '" width="' + p.w + '" height="' + p.h + '" rx="12" fill="var(--panel, #ffffff)" stroke="' + p.tone.stroke + '" stroke-width="' + (sel ? '2.6' : '1.4') + '"/>' +
            '<rect x="' + p.x + '" y="' + p.y + '" width="6" height="' + p.h + '" rx="3" fill="' + p.tone.stroke + '"/>' +
            '<text x="' + (p.x + 16) + '" y="' + (p.y + 25) + '" font-size="13" font-weight="700" fill="var(--text-strong, #0f172a)">' + esc(trunc(n.label, 16)) + '</text>' +
            '<text x="' + (p.x + 16) + '" y="' + (p.y + 46) + '" font-size="11" fill="var(--muted, #64748b)">' + esc(trunc(n.summary || '点击查看关联源码', 20)) + '</text>' +
            '<text x="' + (p.x + 16) + '" y="' + (p.y + 67) + '" font-size="10.5" fill="' + p.tone.stroke + '">' + (n.where ? '⌖ ' + esc(trunc(n.where, 24)) : '状态节点') + '</text>' +
          '</g>'
        );
      }).join('');

      const endMarkerSVG = hasEndMarker
        ? '<circle cx="' + (endPos.x + 10) + '" cy="' + centerY + '" r="9" fill="none" stroke="#10b981" stroke-width="2"/><circle cx="' + (endPos.x + 10) + '" cy="' + centerY + '" r="5" fill="#10b981"/>'
        : '';

      // Code pinpoint viewer
      let pinHTML = '<div class="wb-panel wb-empty" style="padding:16px">点击上方任意状态节点，或下方任意业务规则的 <code>文件:行号</code>，在此实时展开精准定位的源码上下文。</div>';
      if (scState.loadingPin) {
        pinHTML = '<div class="wb-panel wb-empty">正在读取源码精准定位…</div>';
      } else if (scState.pinpoint) {
        const pin = scState.pinpoint;
        if (pin.error) {
          pinHTML = '<div class="wb-panel"><span class="wb-badge fail">定位失败：' + esc(pin.error) + '</span></div>';
        } else if (pin.content != null) {
          const linesHTML = String(pin.content).split('\n').map((ln, idx) => {
            const num = (pin.start || 1) + idx;
            const hi = num === pin.focusLine;
            return '<div class="wb-pin-line' + (hi ? ' focus' : '') + '"><span class="wb-pin-num">' + num + '</span><span>' + esc(ln) + '</span></div>';
          }).join('');
          pinHTML =
            '<div class="wb-panel">' +
              '<div class="wb-item-top" style="margin-bottom:8px"><strong>⌖ 源码精准定位 · ' + esc(pin.label || '') + '</strong><span class="wb-badge pass">' + esc(pin.where) + '</span></div>' +
              '<div class="wb-code" style="padding:8px 0">' + linesHTML + '</div>' +
            '</div>';
        }
      }

      const q = (scState.filter || '').trim().toLowerCase();
      const filteredCharts = q
        ? charts.filter(c => ((c.title || '') + ' ' + (c.domain || '') + ' ' + (c.id || '')).toLowerCase().includes(q))
        : charts;

      const listHTML = filteredCharts.map(c =>
        '<button type="button" class="wb-list-item ' + (c.id === chart.id ? 'active' : '') + '" data-sc-chart="' + esc(c.id) + '">' +
          '<div class="wb-item-top"><span class="wb-item-title">' + esc(c.title) + '</span><span class="wb-badge ' + (c.sourceKind === 'native' ? 'pass' : 'info') + '">' + esc(c.status) + '</span></div>' +
          '<div class="wb-meta"><span>' + (c.nodes || []).length + ' 状态</span><span>' + (c.edges || []).length + ' 流转</span><span>' + (c.rules || []).length + ' 规则</span></div>' +
        '</button>'
      ).join('') || '<div class="wb-empty" style="padding:12px">无匹配状态图</div>';

      const transitionsHTML = edges.length
        ? '<div class="wb-panel">' +
            '<h3>状态流转清单 (' + edges.length + ')</h3>' +
            '<table class="wb-table"><thead><tr><th>起始状态</th><th>触发条件 / 动作</th><th>目标状态</th></tr></thead><tbody>' +
              edges.map(e =>
                '<tr><td><b>' + esc(nodeLabelMap[e.from] || e.from) + '</b></td><td><span class="wb-badge info">' + esc(e.label || '流转') + '</span></td><td><b>' + esc(nodeLabelMap[e.to] || e.to) + '</b></td></tr>'
              ).join('') +
            '</tbody></table>' +
          '</div>'
        : '';

      const scaledW = Math.round(W * scState.zoom);
      const scaledH = Math.round(H * scState.zoom);

      container.innerHTML =
        '<div class="wb-split">' +
          '<div class="wb-panel">' +
            '<h3 style="margin:0 0 8px">业务状态图 (' + filteredCharts.length + '/' + charts.length + ')</h3>' +
            '<input type="search" id="wbScFilterInput" value="' + esc(scState.filter) + '" placeholder="筛选业务域 / 状态图…" style="width:100%;margin-bottom:10px">' +
            '<div class="wb-list" style="max-height:62vh">' + listHTML + '</div>' +
          '</div>' +
          '<div style="display:flex;flex-direction:column;gap:12px;min-width:0">' +
            '<div class="wb-panel">' +
              '<div class="wb-item-top" style="margin-bottom:10px">' +
                '<div><h3 style="margin:0">' + esc(chart.title) + '</h3><p class="wb-subtitle" style="margin:2px 0 0">' + esc(chart.sourceLabel) + ' · 点击任意状态节点精准定位代码行</p></div>' +
                '<div class="wb-actions">' +
                  '<button type="button" class="button ghost small" id="wbScZoomOut" title="缩小">−</button>' +
                  '<span class="wb-meta">' + Math.round(scState.zoom * 100) + '%</span>' +
                  '<button type="button" class="button ghost small" id="wbScZoomIn" title="放大">+</button>' +
                  '<button type="button" class="button ghost small" id="wbScZoomReset">重置</button>' +
                  '<span class="wb-badge ' + (chart.sourceKind === 'native' ? 'pass' : 'info') + '">' + esc(chart.sourceKind === 'native' ? '真实状态图文件' : '图谱实测推导') + '</span>' +
                '</div>' +
              '</div>' +
              '<div class="wb-dot-canvas" style="overflow:auto;max-height:460px;padding:8px">' +
                '<svg width="' + scaledW + '" height="' + scaledH + '" viewBox="0 0 ' + W + ' ' + H + '" style="display:block;min-width:' + scaledW + 'px">' +
                  '<defs>' +
                    '<marker id="wbScArrow" markerWidth="8" markerHeight="8" refX="7" refY="3.5" orient="auto"><path d="M0,0.5 L0,6.5 L7.5,3.5 z" fill="rgba(100,116,139,0.8)"/></marker>' +
                    '<marker id="wbScArrowBlue" markerWidth="8" markerHeight="8" refX="7" refY="3.5" orient="auto"><path d="M0,0.5 L0,6.5 L7.5,3.5 z" fill="rgba(59,130,246,0.8)"/></marker>' +
                    '<marker id="wbScArrowPurple" markerWidth="8" markerHeight="8" refX="7" refY="3.5" orient="auto"><path d="M0,0.5 L0,6.5 L7.5,3.5 z" fill="rgba(139,92,246,0.8)"/></marker>' +
                  '</defs>' +
                  '<circle cx="34" cy="' + centerY + '" r="8" fill="#3b82f6"/>' +
                  endMarkerSVG +
                  edgeSVG + nodeSVG +
                '</svg>' +
              '</div>' +
            '</div>' +
            pinHTML +
            transitionsHTML +
            (rules.length
              ? '<div class="wb-panel">' +
                  '<h3>业务规则与代码锚点 (' + rules.length + ')</h3>' +
                  '<div style="display:flex;flex-direction:column;gap:6px">' + rules.map(rl =>
                    '<div class="wb-finding-card" style="flex-direction:row;align-items:center;justify-content:space-between;gap:10px">' +
                      '<span style="font-size:12.5px">' + esc(rl.text) + '</span>' +
                      (rl.where ? '<button type="button" class="button ghost small" data-sc-rule-where="' + esc(rl.where) + '" data-sc-rule-label="' + esc(rl.text) + '">⌖ ' + esc(rl.where) + '</button>' : '') +
                    '</div>'
                  ).join('') + '</div>' +
                '</div>'
              : '') +
          '</div>' +
        '</div>';

      const filterEl = container.querySelector('#wbScFilterInput');
      if (filterEl) {
        filterEl.addEventListener('input', e => {
          scState.filter = e.target.value;
          const caret = e.target.selectionStart;
          draw();
          const next = container.querySelector('#wbScFilterInput');
          if (next) { next.focus(); if (caret != null) next.setSelectionRange(caret, caret); }
        });
      }
      container.querySelector('#wbScZoomOut')?.addEventListener('click', () => { scState.zoom = Math.max(0.55, +(scState.zoom - 0.15).toFixed(2)); draw(); });
      container.querySelector('#wbScZoomIn')?.addEventListener('click', () => { scState.zoom = Math.min(1.6, +(scState.zoom + 0.15).toFixed(2)); draw(); });
      container.querySelector('#wbScZoomReset')?.addEventListener('click', () => { scState.zoom = 1; draw(); });

      container.querySelectorAll('[data-sc-chart]').forEach(btn => {
        btn.addEventListener('click', () => {
          scState.selectedChartId = btn.dataset.scChart;
          scState.selectedNodeId = '';
          scState.pinpoint = null;
          draw();
        });
      });
      container.querySelectorAll('[data-sc-node]').forEach(el => {
        el.addEventListener('click', () => {
          scState.selectedNodeId = el.dataset.scNode;
          if (el.dataset.scWhere) pinpointWhere(el.dataset.scWhere, el.dataset.scLabel);
          else draw();
        });
      });
      container.querySelectorAll('[data-sc-rule-where]').forEach(btn => {
        btn.addEventListener('click', () => pinpointWhere(btn.dataset.scRuleWhere, btn.dataset.scRuleLabel));
      });
    }

    draw();
  }

  window.WebuddyVisuals = {
    renderArchitectureBlueprint,
    renderERGraph,
    renderStatechartsView,
    renderCrudOp,
    renderCrudChips,
    renderReviewGateTrack,
  };
})();
