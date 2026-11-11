# 开发规范与维护指南

本文档面向后续参与本项目的开发者，约定代码结构、编码风格、安全检查、测试与发布流程。

> 相关文档：[README.md](README.md)（部署与使用）、[ROADMAP.md](ROADMAP.md)（下一步计划）

---

## 1. 项目定位与设计原则

- 服务器巡检中心，Go 单二进制 + 内嵌前端 + SQLite。
- 目标服务器通过 SSH 账号密码登录，巡检命令**全部只读**，不安装 Agent、不修改配置。
- 部署目标：Ubuntu / Debian / CentOS / Rocky / Alma / openEuler 等 systemd Linux。
- 单管理员模式，数据本地存储，面向内网运维场景。

新增功能必须遵守：

1. **不侵入目标系统**：不写文件、不改配置、不安装软件、不提权、不重启服务。
2. **最小权限**：服务以 `inspect` 用户运行；巡检账号只需读取权限。
3. **敏感信息不落日志**：密码、私钥、Cookie、主密钥不得出现在日志、错误消息或报告中。
4. **默认安全**：新增写接口必须有鉴权、同源校验与输入校验。
5. **可回滚**：数据库结构变更、发布升级必须保留原有数据。

---

## 2. 目录结构与依赖方向

```text
.
├── main.go                     # 配置、启动、优雅退出
├── internal/
│   ├── console/                # 终端编码适配（Windows UTF-8）
│   ├── inspect/                # 巡检引擎、命令、解析器、报告、评分
│   ├── netscan/                # IPv4 网段扫描
│   ├── secret/                 # AES-GCM、bcrypt、随机口令/令牌
│   ├── sshx/                   # SSH 连接、主机指纹、命令执行
│   ├── store/                  # SQLite 持久化
│   └── web/                    # HTTP 路由、鉴权、API、内嵌前端
│       └── static/             # index.html / app.css / app.js
├── deploy/                     # install.sh、systemd 单元
├── dist/                       # release.sh 产出的 Linux 发布包
├── build.ps1 / build.sh        # 本地构建
├── release.sh                  # Linux 发布包构建
└── README.md / DEVELOPMENT.md / ROADMAP.md
```

依赖方向（禁止反向依赖）：

```text
main → web → (store, sshx, inspect, netscan, secret)
main → (store, secret, web, inspect)
inspect → 标准库 + Runner 接口（不依赖 sshx/web）
sshx   → golang.org/x/crypto/ssh
store  → modernc.org/sqlite、secret（仅 bcrypt 哈希）
web    → 所有内部业务包
```

- 新包必须有包注释。
- 禁止 `internal/web` 被底层包导入；避免循环依赖。
- 与 OS 相关的代码使用构建标签（参考 `console_windows.go` / `console_other.go`）。

---

## 3. Go 编码规范

- 必须通过 `gofmt`，提交前运行 `go vet ./...` 和 `staticcheck ./...`。
- 错误使用 `%w` 包装；用户可见错误用中文，底层错误保留上下文。
- 所有外部调用、循环、批量任务必须支持 `context.Context` 或显式超时。
- 资源必须关闭：SSH 会话、数据库、文件、HTTP 响应体。
- 日志使用 `log/slog`，字段化输出；禁止打印密码、Cookie、密钥。
- 避免全局可变状态；配置通过结构体传递。`inspect.commandList` 属于只读常量表。
- 并发必须可控：
  - 外部命令执行使用超时；
  - 扫描/批量任务使用固定 worker 数；
  - 共享缓冲区参考 `sshx.safeBuffer`。
- 解析函数必须是纯函数，方便 table-driven 测试。
- 导出的类型/函数必须写注释；不导出但业务含义复杂的也要注释。
- 不要用 `panic` 处理业务错误；`main`/初始化阶段的不可恢复错误可以 `os.Exit(1)`。

---

## 4. 数据与存储规范

### 4.1 SQLite

- 使用 `modernc.org/sqlite`，保持 `CGO_ENABLED=0` 可编译。
- 当前 `SetMaxOpenConns(1)`，避免 WAL 写锁竞争。若修改并发模型，必须重新评估 `busy_timeout` 与事务。
- 数据目录必须为本地磁盘（ext4/xfs）；不要放在 NFS/CIFS 上。
- 批量写入使用事务（参考 `store.CreateServers`）。

### 4.2 时间

- 所有时间以 **UTC** 存储，格式 `time.RFC3339Nano`。
- 范围查询使用 `queryTime()`（固定 9 位小数），避免同秒边界被字符串比较错误排除。
- 前端按浏览器本地时区展示。

### 4.3 表结构变更

- 当前使用 `CREATE TABLE IF NOT EXISTS` 初始化。
- 后续新增字段/表时，必须增加显式迁移逻辑（按版本号顺序执行，幂等），禁止直接改已有列语义。
- 升级前建议备份 `inspect.db` 与 `master.key`。

### 4.4 敏感数据

- 服务器密码：AES-256-GCM 加密，`PasswordEnc` 绝不出现在 JSON/API/日志中。
- 主密钥：`master.key`，权限 `0600`；与数据库一起备份，丢失后密码无法恢复。
- 管理员密码：bcrypt 哈希。
- 不要在 `SELECT *` 后直接把结构体返回给前端；使用明确的 JSON 标签。

### 4.5 排序

- 服务器列表按 IP 数值排序（`netip.Addr.Compare`），不得用字符串排序。
- 同 IP 按端口、名称、ID 排序；非 IP 主机名排在 IP 之后。

---

## 5. 巡检引擎规范

### 5.1 命令

- 所有命令只能读取状态，例如 `cat /proc/*`、`df`、`ps`、`ss`、`systemctl status/is-active`。
- **禁止**：写文件、改配置、启停服务、安装软件、删除文件、修改用户/权限、`sudo`、上传脚本。
- 需要解析数字/英文输出的命令使用 `LC_ALL=C`。
- 必须提供降级方案：`ss` ↔ `netstat`、`ip` ↔ `ifconfig`、`nproc` ↔ `/proc/cpuinfo`。
- 命令失败（非连接错误）不应中断整次巡检，应标记为跳过/信息。
- 命令超时默认 15 秒；连续 3 次超时才中止。

### 5.2 解析

- 解析器不依赖中文输出。
- 对空输出、权限不足、命令不存在要返回“跳过”，不要 panic。
- 新增解析逻辑必须附真实输出样例测试（Ubuntu/CentOS 优先）。

### 5.3 状态与评分

- 状态：`ok` / `info` / `warning` / `critical` / `skipped`。
- 阈值集中在 `inspect.Options`，不要在解析器里硬编码。
- 评分公式：`100 - 25×critical - 6×warning`，警告扣分最多 40，最低 0。
- 新增检查项时必须确认是否参与评分，并在 README 巡检项表中说明。

### 5.4 新增检查项 Checklist

1. 在 `commandList` 增加稳定 key 与只读命令。
2. 在 `parse.go` 增加纯解析函数 + 单测样例。
3. 在对应 `buildXxxSection` 生成 Item，设置状态/详情。
4. 如涉及指标，更新 `Metrics` 与前端指标卡片。
5. 在 `inspect_test.go` 的假 Runner 中补充输出。
6. 用真实 Ubuntu/CentOS 目标做一次冒烟。
7. 更新 README 巡检项说明。

### 5.5 已踩过的坑

- `/proc/stat` 字段数比预期多，`read` 必须预留 `guest/guest_nice` 及额外字段吸收变量，否则 shell 算术报错。
- `grep -c` 无匹配时退出码为 1，属于正常命令失败，不能当作 SSH 断连。
- CPU 使用率需要间隔 1 秒采样两次；0% 是合法结果。

---

## 6. 网络扫描规范

- 仅支持 IPv4；单次最多 4096 个地址（/20）、16 个端口。
- 仅做 TCP connect + 读取 SSH banner，**不尝试登录、不发送探测载荷**。
- 扫描由巡检中心服务器发起，必须有明确授权提示。
- 默认端口 22；自定义端口通过多端口参数传入。
- SSH 判定：端口 22/2222 开放，或读取到 `SSH-` 开头的 banner。
- 反向 DNS 为 best-effort（总等待约 5 秒），失败时使用 IP。
- 全局只允许一个扫描任务（`scanSem`）。
- 扫描结果加入列表时必须 `host:port` 去重，并保持“待配置登录”状态。
- 前端必须区分：可添加 / 已在列表 / 未检测到 SSH，禁止误选。

---

## 7. 安全规范

### 7.1 凭据

- 服务器密码必须使用 `secret.Cipher` 加密；禁止明文落库。
- 修改主机地址或端口时，必须要求重新输入密码，防止旧凭据被静默发往新目标。
- 测试连接同样遵守该规则。
- API 永不返回密码字段；日志不记录认证失败的具体密码。

### 7.2 会话与登录

- 管理员密码 bcrypt；新密码至少 8 个字符、最多 72 字节。
- 会话 Cookie：HMAC-SHA256 签名、HttpOnly、SameSite=Lax、默认 12 小时。
- 管理员用户名不能为空，不能包含 `|`、换行，长度 ≤ 64（会话 payload 使用 `|` 分隔）。
- 生产环境必须使用 HTTPS 并开启 `-cookie-secure`。

### 7.3 CSRF 与输入

- 所有写请求校验 `Origin` 与 `Host` 同源。
- `readJSON` 限制 1 MiB；所有字段做长度、范围、格式校验。
- 前端所有动态内容必须经过 `esc()`，禁止直接插入未转义文本。

### 7.4 SSH 主机指纹

- 首次连接记录 SHA256 指纹（TOFU），后续严格校验。
- 主机密钥不一致时默认拒绝；只有用户显式“重新信任”才更新。
- `-insecure-host-key` 仅允许测试环境使用。

### 7.5 安全测试

- 新增接口必须包含未登录 401、跨站 Origin 403、非法参数 400 的测试。
- 新增凭据相关逻辑必须检查日志/响应中不出现敏感字段。
- 定期运行 `govulncheck ./...` 与依赖升级。

---

## 8. Web / API 规范

### 8.1 路由

- 使用 Go 1.22+ `ServeMux` 模式（`GET /api/servers/{id}`）。
- 除 `/api/login`、`/api/session`、`/healthz` 外，所有 API 必须包裹 `s.authed()`。
- 新增写路由必须经过 `securityHeaders` 的同源校验。

### 8.2 响应格式

```json
{"ok": true, "data": {...}}
{"ok": false, "error": "中文可读错误"}
```

- 401：未登录/会话过期
- 400：参数错误、业务校验失败
- 404：资源不存在
- 409：扫描任务冲突
- 500：服务端错误
- 504：长任务超时

### 8.3 新增接口 Checklist

1. 路由 + `authed`（如需要）。
2. 输入结构体、`normalize/validate`。
3. 业务错误使用 `writeFail`，不要泄露内部堆栈。
4. 数据库操作走 store 层。
5. 添加 httptest 测试（含鉴权/CSRF）。
6. 更新 README API 表。
7. 评估审计日志/限流需求。

---

## 9. 前端规范

- 无构建步骤，原生 JS；通过 `go:embed` 打包。
- 页面使用 hash 路由：`#/dashboard`、`#/servers`、`#/inspections/{id}`。
- 所有请求统一走 `api()`，自动处理 401 与 JSON 错误。
- 所有服务端字符串必须 `esc()` 后再插入 HTML/属性。
- 禁止内联 `<script>`；CSP 仅允许 `'self'` 脚本与 `'unsafe-inline'` 样式。
- 交互必须处理：加载中、禁用重复提交、空状态、错误状态。
- 使用现有 CSS 变量、`.card`、`.btn`、`.badge`、`.modal`、`.toast` 组件风格。
- 密码输入统一使用 `.password-wrap` + “显示/隐藏”按钮模式。
- 涉及表单新增字段时，必须同时更新 `collect()`、后端结构体、校验与测试。

### 前端改动验证清单

- 桌面宽度（≥1280）与窄屏（~1000）无横向溢出。
- 无控制台报错。
- 新增/编辑/删除/批量操作主流程可用。
- 修改密码、登录过期、空数据、接口失败路径正常。
- 升级后强制刷新，静态资源无缓存问题。

---

## 10. 测试规范

### 10.1 必跑命令

```bash
gofmt -l -w .
go vet ./...
go test ./...
# staticcheck 未安装时：
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
```

### 10.2 测试类型

- 解析器单测：`internal/inspect/*_test.go`，使用真实输出样例。
- 巡检端到端：内存 SSH 服务端（`integration_test.go`），验证认证、指纹、命令、报告。
- 扫描单测：仅使用 `127.0.0.1` 本地监听端口，禁止扫描真实网段。
- Web 测试：`httptest` + CookieJar，覆盖登录、CSRF、CRUD、扫描、批量导入、密码修改。
- Linux-only 测试：`cpu_linux_test.go` 直接执行 shell 命令，必须在 Linux CI 运行。
- 并发：建议在 Linux 环境运行 `go test -race ./...`（需要 gcc）。

### 10.3 前端验证

当前以浏览器手工验证为主，后续建议引入 Playwright：

- 登录/退出/会话过期
- 添加服务器/测试连接/巡检/查看报告
- 网络扫描/多选导入/配置登录
- 密码显示切换
- 排序、筛选、批量巡检

---

## 11. 构建与发布

### 11.1 版本

- 版本号在 `main.version`，构建时通过 `-X main.version=` 注入。
- 功能修复/新增后递增版本（示例：1.0.4 → 1.0.5）。

### 11.2 构建

```bash
./release.sh                 # Linux amd64 + arm64 → dist/
VERSION=1.0.5 ./release.sh   # 指定版本
```

发布包包含：

- `server-inspect-linux-amd64`
- `server-inspect-linux-arm64`
- `install.sh`
- `server-inspect.service`
- `README.md`
- `DEVELOPMENT.md`
- `ROADMAP.md`
- `AGENTS.md`
- `SHA256SUMS`

### 11.3 部署

```bash
scp dist/server-inspect-linux-amd64 dist/install.sh root@host:/tmp/
ssh root@host 'bash /tmp/install.sh --port 8080'
```

升级只替换二进制即可保留数据；重跑 `install.sh` 会重写 systemd 单元。

### 11.4 发布前检查

- [ ] `go test ./...` 通过
- [ ] `go vet` / `staticcheck` 通过
- [ ] `install.sh` 通过 `bash -n`
- [ ] `SHA256SUMS` 校验通过
- [ ] Linux 二进制为静态 ELF（`ELFCLASS64`、`static=true`、`CGO_ENABLED=0`）
- [ ] 升级后 `/healthz` 返回 ok
- [ ] 数据目录与管理员密码未被改动

---

## 12. 变更与评审

### 本地版本控制

- 当前仓库为本地 `main` 分支，未配置远程仓库（受网络限制）。
- 修改前先 `git status` 确认工作区；重要改动完成后本地提交。
- 提交信息建议使用下面的前缀；发布时打标签，例如 `git tag -a v1.0.5 -m "release v1.0.5"`。
- **不要提交**：`dist/`、`server-inspect.exe`、`data/`、日志、真实凭据。它们已在 `.gitignore` 中排除。
- 网络允许后如需推送 GitHub：`git remote add origin <url>`，先推送 `main`，再 `git push --tags`。
- `.gitattributes` 已强制 shell/systemd/源码文件使用 LF，避免 Windows 下 checkout 后 Linux 脚本无法执行。

### 提交信息

建议使用：

```text
feat: 增加网络扫描
fix: 修复 CPU 使用率采集
docs: 补充开发规范
refactor: 拆分解析器
test: 增加时间边界测试
chore: 升级依赖
```

### 评审 Checklist

- [ ] 是否有只读/安全边界被突破？
- [ ] 是否新增写接口但遗漏鉴权/CSRF/校验？
- [ ] 是否记录敏感信息？
- [ ] 是否引入无界 goroutine、连接、扫描范围？
- [ ] 数据库变更是否兼容旧数据？
- [ ] 是否补充单元/集成/前端验证？
- [ ] 文档/README/API 是否同步？
- [ ] 版本号是否更新？

### 明确禁止

- 在目标服务器执行写操作或提权。
- 明文存储/返回/记录密码。
- 使用 `SELECT *` 直接返回敏感字段。
- 无超时、无上限的并发或网络调用。
- 前端直接拼接未转义的服务端数据。

---

## 13. 常见故障与排查

| 现象 | 排查 |
| --- | --- |
| CPU 使用率「无法采集」 | 检查 `/proc/stat` 字段读取、命令是否报错；Linux 环境跑 `cpu_linux_test.go` |
| 服务启动失败 203/EXEC | 检查二进制执行位、SELinux、`journalctl -u server-inspect` |
| 浏览器无法访问 | 服务监听地址、UFW/firewalld、云安全组 |
| 主机指纹不一致 | 服务器重装/换 IP；编辑服务器重新信任 |
| 修改主机后测试连接报错 | 需要重新输入密码（安全设计） |
| 升级后界面仍旧 | 强刷；静态资源已配置 no-cache |
| 数据目录异常 | 不要放 NFS；确认属主 `inspect`；备份 `master.key` |
| 忘记管理员密码 | `runuser -u inspect -- ... -reset-admin-pass` |
| Windows 控制台中文乱码 | 程序已自动设置 UTF-8；旧终端可 `chcp 65001` |

---

## 14. 文档索引

- [AGENTS.md](AGENTS.md)：AI 编码代理入口（切换模型/新会话必读）
- [README.md](README.md)：功能、部署、使用、API
- [ROADMAP.md](ROADMAP.md)：后续开发优先级
- [deploy/install.sh](deploy/install.sh)：Linux 一键安装
- [deploy/server-inspect.service](deploy/server-inspect.service)：systemd 单元参考
- [release.sh](release.sh)：发布包构建

### AI 工具兼容入口

- `CLAUDE.md`（Claude Code）
- `GEMINI.md`（Gemini CLI）
- `.github/copilot-instructions.md`（GitHub Copilot）
- `.cursor/rules/project.mdc`（Cursor）
- `.windsurfrules`（Windsurf）

以上文件只保留关键红线，完整规范始终以 `AGENTS.md` 为准；修改规范时同步更新入口文件，避免内容漂移。
