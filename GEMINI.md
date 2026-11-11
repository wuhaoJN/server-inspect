# Gemini CLI 项目约定

完整 AI 编码规范见根目录 [AGENTS.md](AGENTS.md)，开始工作前请先完整阅读；开发细则见 [DEVELOPMENT.md](DEVELOPMENT.md)。

关键约束：

- 巡检命令只读，不修改目标系统，不使用 sudo。
- 密码/主密钥不得进入日志、响应、报告或提交内容。
- 未获用户明确同意，不部署、不重启服务、不改防火墙。
- 前端只维护 `internal/web/static/`，修改后必须重新构建 `go:embed` 二进制。
- 修改主机/端口必须重新输入登录密码。
- 提交前运行 `gofmt`、`go vet ./...`、`go test ./...`；`staticcheck` 未安装时用 `go run honnef.co/go/tools/cmd/staticcheck@latest ./...`。
- 当前目录为 Git 仓库（`main`，远程 `origin` 已配置），修改前用 `git status` 确认工作区；不要擅自 force push。
