# Webuddy 2.0 快速部署与首次上手指南

> 适用对象：个人超级开发者、研发团队负责人、DevOps / 平台工程师  
> 预计耗时：3 分钟完成自托管部署并跑通首个项目闭环

---

## 1. 环境准备

准备一台 Linux（Ubuntu 22.04/24.04、Debian 12、Rocky/AlmaLinux）或 macOS 机器：

- **基础配置**：2 vCPU / 4 GB 内存 / 20 GB 磁盘（若启用本机 Docker 沙箱与 Headless Chrome 自动化测试，推荐 4 vCPU / 8 GB 内存）
- **必备工具**：`git`（`./start.sh` 会自动检测并按需引导安装 Docker、Docker Compose、Python 3 与 curl）

---

## 2. 三分钟一键部署

```bash
git clone https://github.com/auntunt/webuddy-next-loopforge.git
cd webuddy-next-loopforge
./start.sh
```

`./start.sh` 会自动完成以下动作：

1. **自动测速选源**：并行探测官方 `ghcr.io` 与南京大学镜像源 `ghcr.nju.edu.cn`，校验 SHA-256 摘要后高速拉取多架构镜像（`linux/amd64` + `linux/arm64`）；
2. **生成安全配置**：首次运行自动创建 `.env`，生成随机数据库密码、会话密钥与持久化数据目录 `${WEBUDDY_DATA_DIR}`；
3. **初始化与无损迁移**：启动 `mysql` 与 `platform`（默认监听 `0.0.0.0:8090`），自动完成数据库 Schema 增量迁移；
4. **输出访问地址**：打印公网、局域网与本机可访问的工作台 URL。

### 常用运维命令

```bash
./start.sh status     # 查看容器状态与健康检查
./start.sh logs       # 实时跟踪平台运行日志
./start.sh pull       # 拉取最新镜像并执行无损升级
./start.sh restart    # 平滑重启平台服务
./start.sh stop       # 停止服务（保留数据库与持久化卷）
./start.sh dev        # 切换至本地源码构建模式
```

国内源码构建加速示例：

```bash
GOPROXY=https://goproxy.cn,direct NPM_REGISTRY=https://registry.npmmirror.com ./start.sh dev
```

---

## 3. 首次登录与四步开箱流程

1. **注册首个管理员账号**：打开工作台首页注册的第一个用户自动成为平台管理员，同时拥有默认团队空间。
2. **接入代码仓库（4 种方式任选）**：
   - **GitHub**：在「设置 → Git 配置」保存 Personal Access Token，建项时填入 `https://github.com/owner/repo`；
   - **私有 GitLab / Gitea HTTP(S)**：支持企业内网域名及 IPv4 端口地址（如 `http://10.0.0.8:8929/group/repo.git`）；
   - **Git SSH**：支持标准 `git@host:group/repo.git` 及非标端口 `ssh://git@host:port/group/repo.git`；
   - **零 Git 压缩包直传**：直接上传 `.zip` / `.tar.gz` 源码包，系统自动解压持久化并构建 TMH 代码图谱。
3. **接入执行算力（可选）**：
   - 平台自带内置沙箱，也可在工作台聊天框直接发送 `/runner add` 生成一行命令，将本机、测试服务器或 **Windows WSL2** 接入为专属执行机（详见 [测试机与网关管控指南](./runners-and-gateways.md)）。
4. **跑通首个端到端闭环**：
   - 在 **知识库** 查看自动生成的业务域、流程链路与状态机图谱；
   - 在 **工作台** 与 Main Bot 对话澄清需求并一键发起 SOP；
   - 在 **代码审查** 查看 14K 分批全量 AI 审查结论；
   - 在 **自动化测试** 用自然语言生成并真机固化首条 0-Token 回归用例（详见 [AI 自动化测试与五步自愈指南](./autotest-self-healing.md)）。
