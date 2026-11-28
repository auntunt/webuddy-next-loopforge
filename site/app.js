(function () {
  // ---------- Theme Toggle ----------
  const root = document.documentElement;
  const saved = localStorage.getItem('webuddy.site.theme');
  if (saved === 'dark' || (!saved && window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches)) {
    root.setAttribute('data-theme', 'dark');
  }
  const themeBtn = document.getElementById('themeToggle');
  if (themeBtn) {
    themeBtn.addEventListener('click', () => {
      const next = root.getAttribute('data-theme') === 'dark' ? 'light' : 'dark';
      root.setAttribute('data-theme', next);
      localStorage.setItem('webuddy.site.theme', next);
    });
  }

  // ---------- Landing Page Interactive Terminal ----------
  const termSnippets = {
    deploy: `# 1. 克隆真实工作仓并 3 分钟一键部署（自动测速选择最快镜像源）
git clone https://github.com/auntunt/webuddy-next-loopforge.git
cd webuddy-next-loopforge
./start.sh

# 常用运维命令
./start.sh status    # 查看容器健康状态
./start.sh pull      # 无损拉取最新镜像并增量迁移
./start.sh logs      # 实时查看平台日志`,
    runner: `# 在工作台聊天框直接发送（0 Token 毫秒级管控测试机）：
/runner                                   # 查看在线测试机与 CLI 认证状态
/runner add                               # 生成接入新机器（Linux/macOS/WSL2）单行命令
/runner sync all                          # 一键下发网关配置并隔离冲突 auth.json
/runner config WSL2 codex model=gpt-6-sol # 远程切换默认模型
/runner config WSL2 codex mode=official   # 零耦合一键切回官方 OAuth 登录
/runner upgrade all                       # 远程 OTA 热升级所有在线 Runner`,
    test: `# 在工作台聊天框直接发送（0 Token 真机极速回归 + 五步自愈）：
/test                                     # 查看当前项目固化用例、版本与通过率
/test run                                 # Headless Chrome 真机跑完当前项目全量回归
/test run 算薪                            # 按关键词单独极速复放指定用例

# 遇到 UI 改版定位失效？
# 五步诊断自动核验服务健康 -> 驱动存活 -> 弹窗消解 -> 跨页语义防火墙 -> 三维控件画像 (>=0.80)
# 达标原地自愈并保存版本快照；不达标或真业务异常自动拦截，一键转缺陷单 (Issue)！`,
  };

  const termCode = document.getElementById('termCode');
  const copyBtn = document.getElementById('copyTermBtn');
  let activeTerm = 'deploy';
  if (termCode) {
    termCode.textContent = termSnippets[activeTerm];
    document.querySelectorAll('.term-tab').forEach(btn => {
      btn.addEventListener('click', () => {
        activeTerm = btn.dataset.term;
        document.querySelectorAll('.term-tab').forEach(b => b.classList.toggle('active', b === btn));
        termCode.textContent = termSnippets[activeTerm];
      });
    });
    if (copyBtn) {
      copyBtn.addEventListener('click', async () => {
        try {
          await navigator.clipboard.writeText(termSnippets[activeTerm]);
          copyBtn.textContent = '已复制 ✓';
          setTimeout(() => (copyBtn.textContent = '复制命令'), 1600);
        } catch {
          copyBtn.textContent = '请手动选中复制';
        }
      });
    }
  }

  // ---------- Interactive Docs Portal ----------
  const docsNav = document.getElementById('docsNav');
  const docsContent = document.getElementById('docsContent');
  const docsSearch = document.getElementById('docsSearch');
  if (!docsNav || !docsContent) return;

  const docsPages = [
    {
      id: 'quickstart',
      title: '1. 快速部署与首次上手',
      keywords: 'docker start.sh 部署 安装 ghcr 南京大学 首次登录 仓库',
      html: `
        <h1>Webuddy 2.0 快速部署与首次上手指南</h1>
        <blockquote>适用对象：个人超级开发者、研发团队负责人、DevOps 工程师 · 预计耗时 3 分钟完成自托管部署。</blockquote>
        <h2>三分钟一键部署</h2>
        <pre><code>git clone https://github.com/auntunt/webuddy-next-loopforge.git
cd webuddy-next-loopforge
./start.sh</code></pre>
        <p><code>./start.sh</code> 会自动并行测速官方 <code>ghcr.io</code> 与南京大学镜像源 <code>ghcr.nju.edu.cn</code>（严格校验 SHA-256 摘要），自动生成 <code>.env</code> 密钥与数据卷，拉起 MySQL 与平台容器并完成增量 Schema 迁移。</p>
        <h2>常用生命周期命令</h2>
        <pre><code>./start.sh status   # 查看服务与健康检查
./start.sh pull     # 拉取最新镜像并无损升级
./start.sh logs     # 查看实时运行日志
./start.sh dev      # 切换至本地源码构建模式</code></pre>
        <h2>四种代码仓库接入方式</h2>
        <ul>
          <li><strong>GitHub 仓库</strong>：配置 Personal Access Token 即可镜像扫描知识库并支持控制面一键创建 PR。</li>
          <li><strong>私有 GitLab / Gitea HTTP(S)</strong>：原生支持内网域名与 IPv4 端口地址（如 <code>http://10.0.0.8:8929/group/repo.git</code>）。</li>
          <li><strong>Git SSH</strong>：内置 SSH 主机指纹信任管理，支持非标端口。</li>
          <li><strong>零 Git 压缩包直传</strong>：直接上传 <code>.zip</code> / <code>.tar.gz</code> 源码包，自动构建全量 TMH 图谱。</li>
        </ul>`,
    },
    {
      id: 'autotest',
      title: '2. AI 自动化测试与五步自愈',
      keywords: '自动化测试 自愈 chrome cdp 0 token 语义防火墙 置信度 0.80 回滚 判例 /test',
      html: `
        <h1>AI 自动化测试、真机固化与五步自愈指南</h1>
        <blockquote>核心范式：AI 真机探索降噪 → 固化确定性脚本（日常 0-Token 秒级复放）→ 失败触发五步诊断自愈（Confidence ≥ 0.80）→ 快照一键回滚 + 上下文判例库零人工沉淀。</blockquote>
        <h2>四大工程护栏</h2>
        <ul>
          <li><strong>前置状态直接注入</strong>：通过 <code>preconditionUrl</code>（如 <code>/auth/dev?id=1&amp;to=admin</code>）跳过脆弱的登录输入流程，直达业务态。</li>
          <li><strong>三维控件数字身份证</strong>：真机固化时自动记录控件的 <em>语义角色与文案</em> + <em>页面业务父容器上下文</em> + <em>DOM 层级拓扑</em> 与路由基线。</li>
          <li><strong>五步诊断 + 跨页语义防火墙</strong>：① 目标站点 HTTP 探活（502 绝不误修）→ ② CDP 会话存活 → ③ 视觉与语义弹窗消解 → ④ 跨页语义防火墙（掉登录跳回 <code>/login</code> 直接拦截）→ ⑤ 三维画像比对（仅 <code>≥ 0.80</code> 才原地重试并更新快照）。</li>
          <li><strong>双层快照护栏与判例库</strong>：自愈自动产出 Diff 卡片与新快照，人工点击 <strong>一键回滚</strong> 会将误修选择器写入防误修黑名单；真实业务断言失败则提供 <strong>🚨 一键转缺陷单 (Issue)</strong>。</li>
        </ul>
        <h2>聊天框 0-Token 快捷指令</h2>
        <pre><code>/test                 # 列出当前项目固化回归用例、版本与通过率
/test run             # Headless Chrome 真机全量回归（遇漂移自动五步自愈）
/test run 算薪        # 按关键词单独极速复放指定用例</code></pre>`,
    },
    {
      id: 'runners',
      title: '3. 测试机与网关零耦合管控',
      keywords: 'runner 测试机 wsl2 网关 官方登录 oauth codex claude ota /runner',
      html: `
        <h1>测试机（Runner）接入与网关 / 官方登录零耦合管控指南</h1>
        <blockquote>灵感源自 Eta“聊天即管控、零 Token 瞬时执行”，无需频繁 SSH 登机器改配置。</blockquote>
        <h2>官方 OAuth 登录与自定义网关零耦合</h2>
        <table>
          <thead><tr><th>工作模式</th><th>适用场景</th><th>生效方式</th></tr></thead>
          <tbody>
            <tr><td><strong>自定义网关模式</strong></td><td>团队专用 API 网关（<code>config.toml</code> / <code>settings.json</code>）</td><td>工作台点击「配置网关」或发送 <code>/runner sync &lt;机器名&gt;</code>；自动隔离冲突 <code>auth.json</code> 并过滤上游 503 不可用模型。</td></tr>
            <tr><td><strong>官方 OAuth 模式</strong></td><td>本机 <code>codex login</code> / <code>claude login</code> 官方账号</td><td>点击「切回官方登录」或发送 <code>/runner config &lt;机器名&gt; codex mode=official</code>。</td></tr>
          </tbody>
        </table>
        <h2>/runner 零 Token 指令速查</h2>
        <pre><code>/runner                                   # 查看所有测试机在线状态与认证方式
/runner add                               # 生成 10 分钟有效的单行接入命令
/runner use WSL2                          # 切换默认执行机器（传 cloud 切回云端）
/runner sync all                          # 一键下发主控网关配置到所有在线测试机
/runner config WSL2 codex model=gpt-6-sol # 远程修改默认模型
/runner upgrade all                       # 远程 OTA 热升级 Runner 守护进程</code></pre>`,
    },
    {
      id: 'kb-review',
      title: '4. TMH 代码图谱与全量审查',
      keywords: 'tmh 代码图谱 知识库 14k 代码审查 多租户 共享知识库 协作',
      html: `
        <h1>TMH 代码图谱、14K 全量代码审查与多租户协作指南</h1>
        <h2>TMH 多语言代码图谱（<code>webuddy_core</code>）</h2>
        <p>零外部依赖自动抽取 <strong>业务域 → 核心流程 → 数据表 → 确定性状态机</strong>，每条知识页与证据链均强制绑定真实源码 <code>file:line</code> 与 Git Commit 盖章，并在 SOP 交付合并后自动触发增量刷新。</p>
        <h2>14K 分批全量 AI 代码审查</h2>
        <p>按文件边界智能拆分大型 Diff 逐批深度审查，输出阻断项（Blocker）与建议项，支持人工有效性打分闭环；阻断项未解除自动拦截验收关口。即使执行机处于无外网沙箱，控制面也可安全代提真实 GitHub PR。</p>
        <h2>企业多租户隔离与经验自动沉淀</h2>
        <p>独立团队空间无刷秒切、邀请码准入、成员角色权限管控；每次 SOP 完结产出的 <code>knowledge.md</code> 自动去重入库至租户共享知识中心，跨项目团队成员与 Main Bot 随时可检索复用。</p>`,
    },
    {
      id: 'api',
      title: '5. API、MCP 与快捷指令速查',
      keywords: 'api rest mcp webuddy_autotest_manage webuddy_runners_manage slash command',
      html: `
        <h1>开放 HTTP API、MCP 工具与快捷指令速查手册</h1>
        <h2>Main Bot 内置核心 MCP 工具</h2>
        <table>
          <thead><tr><th>工具名</th><th>能力说明</th></tr></thead>
          <tbody>
            <tr><td><code>webuddy_kb_overview</code> / <code>search</code> / <code>chain</code></td><td>免沙箱秒查代码图谱、接口与源码行号证据链</td></tr>
            <tr><td><code>webuddy_kb_update</code> / <code>shared_search</code></td><td>增量更新过期知识页、检索团队共享知识中心</td></tr>
            <tr><td><code>webuddy_preview_start</code></td><td>一键拉起项目服务并打穿 HTTPS 实时预览通道</td></tr>
            <tr><td><code>webuddy_runners_manage</code></td><td>自然语言管控测试机接入、网关同步、模型修改与升级</td></tr>
            <tr><td><code>webuddy_autotest_manage</code></td><td>自然语言生成并真机固化回归用例、执行 0-Token 回归与自愈</td></tr>
          </tbody>
        </table>
        <h2>自动化测试核心 REST 接口</h2>
        <pre><code>GET  /api/webuddy/autotest/overview?projectId=...
POST /api/webuddy/autotest/cases/generate
POST /api/webuddy/autotest/cases/:id/run
POST /api/webuddy/autotest/cases/:id/inject-drift
POST /api/webuddy/autotest/cases/:id/rollback
POST /api/webuddy/autotest/runs/:id/create-issue</code></pre>`,
    },
  ];

  let activeId = (location.hash || '').replace(/^#/, '') || docsPages[0].id;
  if (!docsPages.some(p => p.id === activeId)) activeId = docsPages[0].id;

  function renderDocs(filterText) {
    const q = String(filterText || '').trim().toLowerCase();
    const list = q
      ? docsPages.filter(p => (p.title + ' ' + p.keywords + ' ' + p.html).toLowerCase().includes(q))
      : docsPages;

    docsNav.innerHTML = list.length
      ? list
          .map(
            p =>
              `<button class="docs-nav-btn ${p.id === activeId ? 'active' : ''}" data-doc="${p.id}">${p.title}</button>`
          )
          .join('')
      : `<div style="font-size:12.5px;color:var(--muted);padding:8px;">未匹配到相关文档</div>`;

    const cur = docsPages.find(p => p.id === activeId) || docsPages[0];
    docsContent.innerHTML = cur.html;

    docsNav.querySelectorAll('[data-doc]').forEach(btn => {
      btn.addEventListener('click', () => {
        activeId = btn.dataset.doc;
        history.replaceState(null, '', '#' + activeId);
        renderDocs(docsSearch.value);
        window.scrollTo({ top: 0, behavior: 'smooth' });
      });
    });
  }

  docsSearch.addEventListener('input', e => renderDocs(e.target.value));
  renderDocs('');
})();
