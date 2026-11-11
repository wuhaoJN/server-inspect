// 服务器巡检系统：基于 Go + SSH 的非侵入式 Linux 服务器巡检平台。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"serverinspect/internal/console"
	"serverinspect/internal/inspect"
	"serverinspect/internal/secret"
	"serverinspect/internal/store"
	"serverinspect/internal/web"
)

// version 可在构建时通过 -ldflags "-X main.version=x.y.z" 注入。
var version = "1.0.0"

func main() {
	console.EnableUTF8()
	var (
		addr           = flag.String("addr", envOr("INSPECT_ADDR", ":8080"), "HTTP 监听地址")
		dataDir        = flag.String("data", envOr("INSPECT_DATA", "./data"), "数据目录（数据库与主密钥）")
		adminUser      = flag.String("admin-user", envOr("INSPECT_ADMIN_USER", "admin"), "管理员用户名")
		adminPass      = flag.String("admin-pass", os.Getenv("INSPECT_ADMIN_PASS"), "管理员初始密码（留空则自动生成并打印一次）")
		resetAdminPass = flag.String("reset-admin-pass", "", "重置管理员密码后退出（不启动 Web 服务）")
		skipHostKey    = flag.Bool("insecure-host-key", false, "跳过 SSH 主机密钥指纹校验（不推荐）")
		cookieSecure   = flag.Bool("cookie-secure", false, "会话 Cookie 仅通过 HTTPS 发送（启用 HTTPS 时建议开启）")
		dialTimeout    = flag.Duration("dial-timeout", 10*time.Second, "SSH 连接超时")
		inspectTimeout = flag.Duration("inspect-timeout", 3*time.Minute, "单次巡检总超时")
		showVersion    = flag.Bool("version", false, "打印版本号后退出")
	)
	flag.Parse()
	if *showVersion {
		fmt.Println("server-inspect " + version)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// 会话 Cookie 的 payload 以 | 分隔用户名与过期时间，用户名中不能包含分隔符。
	adminName := strings.TrimSpace(*adminUser)
	if adminName == "" || len(adminName) > 64 || strings.ContainsAny(adminName, "|\r\n") {
		logger.Error("管理员用户名无效：不能为空、不能包含 | 或换行，长度不能超过 64")
		os.Exit(1)
	}
	*adminUser = adminName

	absData, err := filepath.Abs(*dataDir)
	if err != nil {
		logger.Error("解析数据目录失败", "error", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(absData, 0o700); err != nil {
		logger.Error("创建数据目录失败", "dir", absData, "error", err)
		os.Exit(1)
	}

	masterKey, err := secret.LoadOrCreateKey(filepath.Join(absData, "master.key"))
	if err != nil {
		logger.Error("初始化主密钥失败", "error", err)
		os.Exit(1)
	}
	cipher, err := secret.NewCipher(masterKey)
	if err != nil {
		logger.Error("初始化加密器失败", "error", err)
		os.Exit(1)
	}

	st, err := store.Open(filepath.Join(absData, "inspect.db"))
	if err != nil {
		logger.Error("初始化数据库失败", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	if *resetAdminPass != "" {
		if err := st.SetAdminPassword(*adminUser, *resetAdminPass); err != nil {
			logger.Error("重置管理员密码失败", "error", err)
			os.Exit(1)
		}
		logger.Info("管理员密码已重置", "user", *adminUser)
		fmt.Println("管理员密码已重置。请启动服务：systemctl start server-inspect")
		return
	}

	generatedPassword := ""
	if *adminPass == "" {
		generatedPassword, err = secret.RandomPassword(16)
		if err != nil {
			logger.Error("生成初始密码失败", "error", err)
			os.Exit(1)
		}
	}
	effectivePassword := *adminPass
	if effectivePassword == "" {
		effectivePassword = generatedPassword
	}
	created, err := st.EnsureAdmin(*adminUser, effectivePassword)
	if err != nil {
		logger.Error("初始化管理员账号失败", "error", err)
		os.Exit(1)
	}

	cfg := web.Config{
		Version:          version,
		SessionSecret:    secret.DeriveKey(masterKey, "session-token"),
		SessionTTL:       12 * time.Hour,
		CookieSecure:     *cookieSecure,
		DialTimeout:      *dialTimeout,
		InspectTimeout:   *inspectTimeout,
		SkipHostKeyCheck: *skipHostKey,
		InspectOptions:   inspect.DefaultOptions(),
	}
	handler := web.New(st, cipher, cfg, logger).Handler()

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      *inspectTimeout + 30*time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	printBanner(logger, *addr, absData, created, *adminUser, generatedPassword)
	if *skipHostKey {
		logger.Warn("已跳过 SSH 主机密钥校验，存在中间人攻击风险，仅建议在受控测试环境使用")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("HTTP 服务已启动", "addr", *addr, "version", version)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		logger.Error("HTTP 服务异常退出", "error", err)
		os.Exit(1)
	case <-ctx.Done():
		logger.Info("收到退出信号，正在关闭服务…")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("关闭 HTTP 服务失败", "error", err)
	}
	logger.Info("服务已退出")
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func printBanner(logger *slog.Logger, addr, dataDir string, created bool, user, generatedPassword string) {
	display := addr
	if strings.HasPrefix(display, ":") {
		display = "http://127.0.0.1" + display
	}
	logger.Info("服务器巡检系统", "version", version, "url", display, "dataDir", dataDir)
	if created {
		fmt.Println("")
		fmt.Println("================================================================")
		fmt.Println("  服务器巡检系统初始化完成")
		fmt.Println("  访问地址  : " + display)
		fmt.Println("  管理员账号: " + user)
		if generatedPassword != "" {
			fmt.Println("  初始密码  : " + generatedPassword)
			fmt.Println("  ⚠ 初始密码仅显示一次，请立即登录并妥善保存。")
			fmt.Println("  ⚠ 忘记密码时可使用 -reset-admin-pass 参数重置。")
		} else {
			fmt.Println("  初始密码  : 使用 -admin-pass / INSPECT_ADMIN_PASS 指定")
		}
		fmt.Println("================================================================")
		fmt.Println("")
	}
}
