# AGENTS.md — AI 编码代理工作规范

> **本文件是所有 AI 编码代理（OpenCode / Codex / Claude Code / Gemini CLI / Cursor / Copilot 等）的唯一入口规范。**
> 最后更新：2026-11-13 · 当前版本：以 `main.go` 中 `version` 为准（本文档编写时为 1.0.15）
> 详细规范：[DEVELOPMENT.md](DEVELOPMENT.md) · 部署使用：[README.md](README.md) · 后续计划：[ROADMAP.md](ROADMAP.md)

---

## 0. 接手项目时先做这些

1. 依次阅读：`AGENTS.md` → `DEVELOPMENT.md` → `README.md` → `ROADMAP.md`。
2. 查看当前版本：`main.go` 中的 `version`、`dist/` 二进制执行 `-version`、线上 `/api/session`。
3. 查看是否有未完成的改动。**当前目录为 Git 仓库（`main` 分支），远程 `origin` 已配置为 GitHub（SSH Deploy Key）**：用 `git status` / `git diff` / `git log` 判断遗留修改；重要改动前先本地提交；未经用户同意不要 `force push` 或改写历史。
4. **不要猜测线上服务器地址、账号、密码或部署状态**；需要部署、重启服务、修改防火墙时，必须先获得用户明确指令。
5. 不要在任何文件、日志、提交信息或回复中写入真实密码、私钥、Cookie、主密钥。
6. 不确定的业务规则，先读 `DEVELOPMENT.md` 与现有实现，再向用户确认，不要自行扩大范围。

---

## 1. 项目定位与不可违反的红线

**定位**：Go 单二进制 + 内嵌前端的 Linux 服务器巡检中心。通过 SSH 账号密码执行**只读**巡检，Web 管理服务器、扫描、查看历史与报告。

### 绝对红线

- ❌ 不对被巡检目标执行写操作：不安装软件、不改配置、不启停服务、不删除文件、不修改用户/权限、不使用 `sudo` 提权。
- ❌ 不在目标服务器上传脚本或 Agent。
- ❌ 不明文存储/返回/记录服务器密码；密码只能经 `secret.Cipher` 加密后落库。
- ❌ 不把服务器密码、管理员密码、主密钥写到日志、报告、API 响应或 Git。
- ❌ 不在未获用户明确同意时，对真实服务器执行部署、升级、重启、改防火墙、删除数据等操作。
- ❌ 不绕过鉴权、CSRF、SSH 主机指纹校验。
- ❌ 不手动修改 `dist/` 里的二进制；必须通过 `release.sh` 重新构建。
- ❌ 不把前端源码复制到别处维护；前端只有 `internal/web/static/` 一份，通过 `go:embed` 打包。

---

## 2. 架构地图

```text
main.go                     启动、参数、数据目录、HTTP Server、调度器、优雅退出
internal/secret              AES-256-GCM、bcrypt、随机口令/令牌
internal/store              SQLite：servers / inspections / batches / schedules / settings
internal/sshx               SSH 连接、TOFU 指纹、命令执行
internal/inspect            巡检引擎：命令表、解析器、报告、评分
internal/runner             单台/批量巡检执行，结果持久化
internal/scheduler          定时计划循环、NextRun、目标解析
internal/batch              批量结果聚合分析（与前端摘要结构一致）
internal/compare            相邻两次报告对比
internal/netscan            IPv4 网段扫描：TCP 探测 + SSH banner
internal/web                HTTP 路由、鉴权、API、内嵌前端
internal/web/static/        index.html / app.css / app.js
deploy/install.sh           Linux 一键安装（systemd）
deploy/server-inspect.service
release.sh                  Linux amd64/arm64 发布包
build.ps1 / build.sh        本地构建
```

依赖方向：`main → web → (store, sshx, inspect, netscan, secret)`；`inspect` 不依赖 `sshx/web`；禁止反向依赖。

---

## 3. 环境与常用命令

要求：Go **1.26+**（验证版本 1.27.3）。Linux 目标机运行二进制不需要 Go。

```bash
# 格式化 + 静态检查 + 测试（提交前必跑）
gofmt -l -w .
go vet ./...
go test ./...
# 未安装 staticcheck 时：
go run honnef.co/go/tools/cmd/staticcheck@latest ./...

# 本地开发
go run . -addr 127.0.0.1:8080 -data ./data

# Linux 发布包（输出 dist/）
./release.sh
VERSION=1.0.15 ./release.sh

# 安装脚本语法检查
bash -n deploy/install.sh
```

Windows 开发机如果 `go` 不在 PATH，可临时使用现有工具链路径，或安装 Go 1.26+ 后继续；构建 Windows 开发版用 `.\build.ps1`。

---

## 4. 编码约定（必须遵守）

### Go

- 必须 `gofmt`；错误用 `%w` 包装；用户可见错误用中文。
- 外部调用、循环、批量任务必须有超时或 `context`。
- 资源必须关闭（SSH session、DB、HTTP body）。
- 日志用 `slog`，结构化字段；禁止记录敏感信息。
- 并发必须有上限；参考 `sshx.safeBuffer` 与 `netscan` worker 模式。
- 解析函数保持纯函数，便于 table-driven 测试。
- 新增导出符号写注释；新增包写包注释。
- 不引入不必要依赖；引入新依赖需说明用途并更新 `go.mod/go.sum`。

### 前端

- 原生 JS，无构建步骤；只改 `internal/web/static/`。
- 所有服务端数据插入 HTML 前必须 `esc()`；禁止直接拼未转义值。
- 请求统一走 `api()`，保持 `{ok,data}` / `{ok,error}` 约定。
- 交互必须处理：加载中、按钮禁用、空状态、错误提示。
- 复用现有 CSS 变量与组件（`.card`、`.btn`、`.badge`、`.modal`、`.toast`）。
- 密码输入使用 `.password-wrap` + “显示/隐藏”模式。
- 修改前端后必须重新构建二进制，否则 `go:embed` 不会更新。

### SQL / 数据

- SQLite 单连接、WAL；批量写入用事务。
- 时间统一 UTC `RFC3339Nano`；范围查询用 `store.queryTime()` 固定 9 位小数。
- 服务器列表按 IP 数值排序（`netip.Addr.Compare`），禁止字符串排序。
- 结构变更必须考虑旧数据兼容；禁止直接破坏已有列语义。

---

## 5. 常见任务操作手册

### 5.1 新增一个巡检项

1. `internal/inspect/engine.go` 的 `commandList` 增加**只读**命令与稳定 key。
2. `internal/inspect/parse.go` 增加纯解析函数。
3. 对应 `buildXxxSection` 增加 `Item`，设置 `ok/info/warning/critical/skipped`。
4. 涉及指标时更新 `Metrics` 与前端 `viewInspection` 指标卡片。
5. 在 `inspect_test.go` 的假 Runner 中补样例；必要时补真实 Ubuntu/CentOS 冒烟。
6. 更新 `README.md` 巡检项表。
7. 运行全部测试。

### 5.2 新增 API

1. `internal/web/server.go` 注册路由；除登录/会话/healthz 外必须 `s.authed(...)`。
2. `internal/web/handlers.go` 实现输入校验、业务逻辑、`writeOK/writeFail`。
3. 数据访问放 `store` 层；不要手写重复 SQL。
4. 添加 `internal/web/web_test.go` 覆盖 401、CSRF 403、参数 400、正常响应。
5. 更新 README API 表。
6. 检查日志与响应中不出现敏感字段。

### 5.3 前端交互

1. 只改 `app.js` / `app.css`。
2. 复用 `api()`、`esc()`、`openModal()`、`toast()`、`statusBadge()`。
3. 新增弹窗注意 `type="button"`、关闭逻辑、Escape、重复提交禁用。
4. 验证 1280 与 1000 宽度、空数据、接口失败路径。
5. 重新构建并在浏览器实际点一遍。

### 5.4 网络扫描

- 仅 IPv4；最多 /20（4096 地址）、16 端口；全局同时只允许 1 个扫描。
- 只做 TCP connect + 读取 SSH banner；禁止登录尝试。
- 结果按 `IP:端口` 去重；导入后为“待配置登录”。
- 修改扫描逻辑必须补 `internal/netscan` 单测，只使用 `127.0.0.1` 本地端口，禁止扫描真实网段。

### 5.5 部署与升级

- 默认开发/验证在本地；**生产操作必须先获得用户明确同意**。
- Linux 升级：上传新二进制 → 停止服务 → `install -m 0755` 覆盖 → 启动服务 → 验证 `/healthz` 与版本。
- 保留 `/var/lib/server-inspect`（数据库 + `master.key`），不要删除。
- 通过 scp/pscp 上传的文件可能没有执行位；`install.sh` 会自动 `chmod +x`，手工替换时也要 `chmod +x`。
- 修改 systemd 单元或防火墙属于生产变更，先确认。

---

## 6. 安全清单（改代码前/后都过一遍）

- [ ] 新接口是否鉴权？是否校验 Origin/CSRF？
- [ ] 是否把密码/凭据写进日志、错误、JSON、报告？
- [ ] 是否把未转义数据插入前端 HTML？
- [ ] 是否新增了无上限的 goroutine、连接、扫描范围或请求体？
- [ ] 是否修改了主机/端口却沿用旧密码？（必须要求重新输入）
- [ ] 是否破坏 SSH 指纹校验或允许默认跳过？
- [ ] 是否在目标上执行了非只读命令？
- [ ] 是否在未授权情况下操作了生产服务器？

---

## 7. 测试与完成定义（DoD）

一次改动至少完成：

```bash
gofmt -l -w .
go vet ./...
staticcheck ./...
go test ./...
bash -n deploy/install.sh   # 改安装脚本时
```

并在需要时：

- 前端在浏览器实际验证主流程，控制台无报错。
- 涉及巡检项时，用真实 Ubuntu/CentOS 目标冒烟。
- 涉及部署时，验证线上版本、`/healthz`、数据未丢失。
- 更新 README / DEVELOPMENT / ROADMAP / API 文档。
- 递增版本号（`VERSION=x.y.z ./release.sh`）。
- 发布包 `sha256sum -c` 通过。

**未跑测试、未验证前端、未说明影响的改动，不算完成。**

---

## 8. 已知坑（务必避免重复踩）

1. **`/proc/stat` 字段比预期多**：`read` 必须预留 `guest/guest_nice` 和额外吸收变量，否则 shell 算术报错，CPU 显示“无法采集”。
2. **命令退出码非 0 不等于 SSH 断连**：`grep -c` 无匹配会返回 1，不能中止巡检；`isConnectionError` 已区分 `CommandError`。
3. **修改主机/端口必须重新输入密码**：后端已强制，前端也做了提示，不要移除。
4. **IP 排序不能用字符串**：必须用 `netip.Addr.Compare`。
5. **时间范围查询不能用会被省略小数的格式**：使用固定 9 位小数的 `queryTime()`。
6. **scp/pscp 上传文件没有执行位**：安装脚本已处理；手工部署要 `chmod +x`。
7. **升级后浏览器缓存旧前端**：静态资源已设 `no-cache`；仍异常时强刷。
8. **SQLite 不要放 NFS**；备份必须同时包含 `inspect.db` 与 `master.key`。
9. **不要假设目标 shell 是 bash**：巡检命令使用 POSIX sh 语法；非 POSIX 账号会失败。
10. **端口扫描/HTTP 请求必须有时限**；不要写无界循环。

---

## 9. 当前状态与下一步

- 当前功能：服务器 CRUD、节点职责、列表「更多」菜单、批量巡检结果分析、单台/批量巡检、网络扫描、IP 排序、待配置登录、报告历史、密码可见切换、管理员改密、Linux systemd 部署；定时任务（间隔/每天/每周）、批量记录持久化、服务器详情页、7/30/90 天趋势、报告对比、服务器维护状态（批量/定时自动跳过）、僵尸进程明细、虚拟化内核日志降噪、非 root 防火墙状态兜底检测。
- 当前线上版本：1.0.15（以 `main.go`/`dist` 二进制 `-version` 为准）。
- 待办优先级见 `ROADMAP.md`：
  - P0：会话安全（改密失效旧会话/登录限流）、告警通知、批量配置登录、数据保留备份。
  - P1：RBAC/审计、SSH 私钥/跳板机、服务端批量任务、扫描增强、可观测性、Docker/HTTPS。
- 不要在未确认需求时直接做 P1/P2 功能；优先修 Bug 和 P0。

---

## 10. 切换模型/新会话交接清单

新模型接手后按此顺序执行：

1. 阅读 `AGENTS.md`、`DEVELOPMENT.md`、`README.md`、`ROADMAP.md`。
2. 运行 `go test ./...`，确认基线；检查 `gofmt`/`go vet`。
3. 查看 `main.go` 版本与 `dist/` 发布包，确认线上版本（如果用户提供访问方式）。
4. 用 `git status` / `git log` 检查是否有未提交改动；远程 `origin` 已配置，但未经用户同意不要强制推送或重写历史。
5. 向用户确认本次任务范围，不要自行部署或改生产配置。
6. 按第 5、6、7 节执行；完成后给出变更摘要、验证结果、剩余风险。
7. 若修改了版本/部署，更新 README/ROADMAP/DEVELOPMENT 中受影响的说明。

> 规范文档是“活文档”：发现新的坑或约定，请同步更新本文件、`DEVELOPMENT.md` 和兼容入口文件，避免下一个模型重复踩坑。
