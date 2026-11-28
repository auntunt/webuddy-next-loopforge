# Webuddy 2.0 开放接口、MCP 工具与快捷指令速查手册

---

## 1. 聊天框零 Token 快捷指令（Instant Slash Commands）

直接在工作台聊天输入框发送，服务端直接执行并即时返回 Markdown 报告，**消耗 0 Token**：

| 指令 | 说明 |
| :--- | :--- |
| `/runner` | 查看所有测试机在线状态、默认执行机与 CLI 认证方式 |
| `/runner add` | 生成 10 分钟有效的测试机单行安装与绑定命令 |
| `/runner use <名称\|cloud>` | 切换默认执行机或切回平台云端执行池 |
| `/runner sync [名称\|all] [codex\|claude]` | 同步主控网关配置并隔离冲突 OAuth 文件 |
| `/runner config <名称> [codex\|claude] [model=...] [mode=official]` | 远程查询或修改测试机 CLI 配置 / 切回官方登录 |
| `/runner upgrade [名称\|all]` | 远程触发在线测试机 Runner 热升级 |
| `/test`（或 `/autotest`） | 查看当前项目固化自动化用例、版本与通过率 |
| `/test run [all\|关键词]` | 真机 Headless Chrome 极速回归，定位失效自动触发五步自愈 |

---

## 2. Main Bot 内置 MCP 工具清单

| 工具名 | 能力说明 |
| :--- | :--- |
| `webuddy_kb_overview` | 免沙箱秒查项目代码图谱总览（业务域、流程、数据表与知识页） |
| `webuddy_kb_search` | 秒搜代码图谱、外部平台接入、接口、数据表与知识页 |
| `webuddy_kb_chain` | 追溯“业务域 → 流程 → 数据表 → 源码行号”完整证据链 |
| `webuddy_kb_update` | 批量增量分析并更新过期知识库页面（强制源码行号溯源） |
| `webuddy_kb_shared_search` | 检索当前租户跨项目团队共享知识中心 |
| `webuddy_ensure_project_env` | 一键自动配置并核验项目沙箱、知识库与在线执行机 |
| `webuddy_preview_start` | 一键启动项目 Web 服务并打穿 `/app-preview/<projectId>/` HTTPS 预览 |
| `webuddy_list_sops` | 查询当前项目与租户可见的已发布 SOP 流水线 |
| `webuddy_runners_manage` | 自然语言管控测试机接入、网关同步、模型切换与 OTA 升级 |
| `webuddy_autotest_manage` | 自然语言生成并真机固化回归用例、执行 0-Token 回归与自愈 |

---

## 3. 核心 HTTP REST API 速查

### 自动化测试与五步自愈（AutoTest）
- `GET  /api/webuddy/autotest/overview?projectId=...` — 获取飞轮指标、用例集、近期运行记录与判例库
- `POST /api/webuddy/autotest/cases/generate` — 自然语言探索真机 DOM 并固化确定性用例（`Rev 1`）
- `POST /api/webuddy/autotest/cases/:id/run` — 0-Token 确定性回归（失败自动触发五步诊断与 `≥ 0.80` 自愈）
- `POST /api/webuddy/autotest/suites/run` — 一键批量执行项目全量回归
- `POST /api/webuddy/autotest/cases/:id/inject-drift` — 注入 UI 改版定位漂移，用于自愈演练
- `POST /api/webuddy/autotest/cases/:id/rollback` — 一键回滚至指定快照版本并写入防误修判例
- `POST /api/webuddy/autotest/runs/:id/create-issue` — 将异常待裁定运行记录一键升级为项目缺陷单
- `GET  /api/webuddy/autotest/precedents` — 查询项目上下文判例库

### 测试机与 CLI 配置（Runner Workbench）
- `GET  /api/runners/:id/cli-config?provider=codex` — 远程读取测试机 CLI 配置与可用模型列表
- `PUT  /api/runners/:id/cli-config` — 远程更新网关、默认模型、同步主控或切回官方 OAuth 登录
- `POST /api/runners/bind-codes` — 签发一次性测试机安全绑定码

### 代码图谱与实时预览（KB & Preview）
- `GET  /api/webuddy/kb/overview` / `POST /api/webuddy/kb/scan` / `POST /api/webuddy/kb/chain`
- `GET  /api/webuddy/kb/shared` / `POST /api/webuddy/kb/shared`
- `POST /api/webuddy/preview/start` / `POST /api/webuddy/preview/stop`
