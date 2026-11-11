# GitHub Copilot 项目约定

完整规范见根目录 `AGENTS.md` 与 `DEVELOPMENT.md`。生成代码时请遵守：

- Go 代码必须通过 `gofmt`、`go vet`、`go test`；`staticcheck` 未安装时用 `go run honnef.co/go/tools/cmd/staticcheck@latest ./...`。
- 巡检命令必须只读；禁止写文件、改配置、启停服务、安装软件、sudo。
- 服务器密码使用 `secret.Cipher` 加密，禁止明文/日志/响应泄露。
- 新增 API 必须鉴权、校验输入、校验 Origin（写操作），并使用 `{ok,data}` / `{ok,error}`。
- 前端动态内容必须经过 `esc()`；不要引入构建工具；只修改 `internal/web/static/`。
- 修改主机/端口时必须要求重新输入密码。
- 修改前端后必须重新构建二进制（`go:embed`）。
- 不自动部署或修改生产服务器；生产操作需用户明确同意。
