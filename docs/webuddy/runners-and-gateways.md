# 测试机（Runner）接入与网关 / 官方登录零耦合管控指南

> 灵感源自 Eta 的“聊天即管控、零 Token 瞬时执行”体验：无需频繁 SSH 登录多台 Linux / macOS / Windows WSL2 测试机改配置文件，直接在 Webuddy 工作台或聊天框完成测试机接入、网关下发、官方登录切换与远程 OTA 升级。

---

## 1. 一行命令接入新测试机（Linux / macOS / WSL2）

在工作台聊天框直接发送（**0 Token 消耗**）：

```text
/runner add
```

系统会签发一个 10 分钟有效的一次性安全绑定码并生成单行安装命令。在目标机器终端粘贴执行，即可自动下载 `agentflow-runner` 并注册为后台守护服务。

---

## 2. 官方登录与自定义网关零耦合设计

Webuddy 绝不强耦合任何私有网关，全面支持两种工作模式并可一键无损切换：

| 模式 | 适用场景 | 生效方式 |
| :--- | :--- | :--- |
| **自定义网关模式** | 使用团队专用 API 代理/中转网关（`config.toml` / `settings.json`） | 在「平台管理 → 执行机」点击 **「配置网关」**，或聊天框发 `/runner sync <机器名>` 一键下发；运行器自动隔离同目录残留 `auth.json`，避免官方登录态劫持自定义网关。 |
| **官方 OAuth 登录模式** | 使用本机 `codex login` / `claude login` 官方订阅账号 | 点击 **「切回官方登录」**，或发送 `/runner config <机器名> codex mode=official`，自动清除自定义网关段并恢复本机 OAuth 认证。 |

### 智能模型探活与死模型自动过滤
当自定义网关返回 `/v1/models` 列表时，控制面会自动探测各模型真实可访问性，自动屏蔽上游未配置凭据（返回 `503 no auth available`）的不可用模型，确保工作台下拉列表里只出现立即可用的模型。

---

## 3. `/runner` 零 Token 快捷指令速查

在任意工作台对话输入框中直接发送以下指令，控制面毫秒级执行，不消耗大模型 Token：

```text
/runner                                        # 查看所有在线/离线测试机、默认机器与已启用 CLI
/runner add                                    # 生成接入新测试机的单行安装命令
/runner use WSL2                               # 将默认执行机切换为 WSL2（传 cloud 切回云端）
/runner sync all                               # 一键同步主控网关配置到所有在线自有测试机
/runner sync WSL2 claude                       # 仅同步 Claude Code 配置到指定机器
/runner config WSL2 codex                      # 查看目标机器当前 Codex 网关与可用模型
/runner config WSL2 codex model=gpt-6-sol      # 远程修改目标机器默认模型
/runner config WSL2 codex mode=official        # 远程切回官方 OAuth 登录模式
/runner upgrade all                            # 远程 OTA 热升级所有在线测试机 Runner
```

此外，Main Bot 也内置了 `webuddy_runners_manage` MCP 工具，你可以直接用自然语言说“帮我把 WSL2 测试机的默认模型改成 gpt-6-sol 并同步网关”。
