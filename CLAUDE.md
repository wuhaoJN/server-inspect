# Claude Code 项目约定

本项目的完整 AI 编码规范见根目录 **[AGENTS.md](AGENTS.md)**，开始工作前必须先阅读。
详细开发规范见 [DEVELOPMENT.md](DEVELOPMENT.md)，部署见 [README.md](README.md)，计划见 [ROADMAP.md](ROADMAP.md)。

## 核心红线（完整版见 AGENTS.md）

- 对目标服务器的巡检命令必须**只读**，不安装 Agent、不改配置、不提权。
- 不输出、不记录、不提交真实密码/主密钥/Cookie。
- 不在未获用户明确同意时部署、升级、重启服务或修改防火墙。
- 修改主机/端口后必须要求重新输入密码。
- 前端只改 `internal/web/static/`，修改后需重新构建二进制。
- 完成后至少运行：`gofmt -l -w .`、`go vet ./...`、`go test ./...`；`staticcheck` 未安装时用 `go run honnef.co/go/tools/cmd/staticcheck@latest ./...`。
- 当前目录为 Git 仓库（`main`，远程 `origin` 已配置），修改前用 `git status` 确认工作区，重要改动先提交；不要擅自 force push。
- 生产变更前先向用户确认；完成后报告版本、验证结果与剩余风险。
