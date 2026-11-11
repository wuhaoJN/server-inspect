// Package sshx 封装只读 SSH 命令执行，用于非侵入式巡检。
package sshx

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// DialOptions 是建立 SSH 连接所需的参数。
type DialOptions struct {
	Host     string
	Port     int
	Username string
	Password string
	Timeout  time.Duration

	// KnownHostKey 为首次连接时记录的 SHA256 指纹；非空时会进行严格校验。
	KnownHostKey string
	// TrustHostKey 为 true 时忽略指纹不一致（用于用户主动重新信任）。
	TrustHostKey bool
}

// DialResult 包含连接对象与本次握手观察到的主机密钥指纹。
type DialResult struct {
	Client         *Client
	Fingerprint    string
	HostKeyChanged bool
}

// Client 是指向目标服务器的 SSH 连接。
type Client struct {
	client *ssh.Client
	addr   string
}

// 兼容旧版系统（CentOS 6/7 等），在默认算法基础上追加 sha1/cbc 系列算法。
var hostKeyAlgorithms = []string{
	"ssh-ed25519",
	"ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521",
	"rsa-sha2-512", "rsa-sha2-256", "ssh-rsa", "ssh-dss",
}

var keyExchanges = []string{
	"curve25519-sha256", "curve25519-sha256@libssh.org",
	"ecdh-sha2-nistp256", "ecdh-sha2-nistp384", "ecdh-sha2-nistp521",
	"diffie-hellman-group14-sha256", "diffie-hellman-group14-sha1", "diffie-hellman-group1-sha1",
	"diffie-hellman-group-exchange-sha256",
}

var ciphers = []string{
	"chacha20-poly1305@openssh.com",
	"aes128-gcm@openssh.com", "aes256-gcm@openssh.com",
	"aes128-ctr", "aes192-ctr", "aes256-ctr",
	"aes128-cbc", "aes192-cbc", "aes256-cbc", "3des-cbc",
}

// Dial 建立 SSH 连接，默认 10 秒超时。
func Dial(opts DialOptions) (*DialResult, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	if opts.Port == 0 {
		opts.Port = 22
	}

	var observed string
	var changed bool
	hostKeyCallback := func(_ string, _ net.Addr, key ssh.PublicKey) error {
		observed = ssh.FingerprintSHA256(key)
		if opts.KnownHostKey != "" && !opts.TrustHostKey && opts.KnownHostKey != observed {
			changed = true
			return fmt.Errorf("主机密钥指纹不一致：已保存 %s，当前为 %s", opts.KnownHostKey, observed)
		}
		return nil
	}

	cfg := &ssh.ClientConfig{
		User: opts.Username,
		Auth: []ssh.AuthMethod{
			ssh.Password(opts.Password),
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = opts.Password
				}
				return answers, nil
			}),
		},
		HostKeyCallback:   hostKeyCallback,
		Timeout:           opts.Timeout,
		HostKeyAlgorithms: hostKeyAlgorithms,
		Config: ssh.Config{
			KeyExchanges: keyExchanges,
			Ciphers:      ciphers,
		},
	}

	addr := net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port))
	conn, err := net.DialTimeout("tcp", addr, opts.Timeout)
	if err != nil {
		return nil, fmt.Errorf("无法连接 %s: %w", addr, err)
	}
	cc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		if changed {
			return nil, err
		}
		return nil, fmt.Errorf("SSH 认证失败: %w", err)
	}
	return &DialResult{
		Client:      &Client{client: ssh.NewClient(cc, chans, reqs), addr: addr},
		Fingerprint: observed,
	}, nil
}

// Addr 返回连接地址。
func (c *Client) Addr() string { return c.addr }

// Close 关闭连接。
func (c *Client) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}

// safeBuffer 让超时返回与后台写入可以安全并发。
type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// Run 执行一条远程命令（只读原则由调用方保证），返回 stdout、stderr。
func (c *Client) Run(ctx context.Context, command string) (string, string, error) {
	sess, err := c.client.NewSession()
	if err != nil {
		return "", "", fmt.Errorf("创建 SSH 会话失败: %w", err)
	}
	defer sess.Close()

	var stdout, stderr safeBuffer
	sess.Stdout = &stdout
	sess.Stderr = &stderr

	done := make(chan error, 1)
	go func() {
		done <- sess.Run(command)
	}()

	select {
	case err := <-done:
		if err != nil {
			var exitErr *ssh.ExitError
			if ok := asExitError(err, &exitErr); ok {
				return stdout.String(), stderr.String(), &CommandError{Code: exitErr.ExitStatus(), Stderr: strings.TrimSpace(stderr.String())}
			}
			return stdout.String(), stderr.String(), err
		}
		return stdout.String(), stderr.String(), nil
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		_ = sess.Close()
		<-done
		return stdout.String(), stderr.String(), ctx.Err()
	}
}

func asExitError(err error, target **ssh.ExitError) bool {
	if e, ok := err.(*ssh.ExitError); ok {
		*target = e
		return true
	}
	return false
}

// CommandError 表示命令执行完成但退出码非 0。
type CommandError struct {
	Code   int
	Stderr string
}

func (e *CommandError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("命令退出码 %d: %s", e.Code, e.Stderr)
	}
	return fmt.Sprintf("命令退出码 %d", e.Code)
}

// CommandFailed 标记这是远程命令退出码非 0，而不是 SSH 连接错误。
func (e *CommandError) CommandFailed() bool { return true }

// Probe 在测试连接时执行一条无害命令，确认账号具备命令执行权限。
func (c *Client) Probe(ctx context.Context) (string, error) {
	out, stderr, err := c.Run(ctx, `uname -srm 2>/dev/null; (cat /etc/os-release 2>/dev/null | head -n 2) || true`)
	if err != nil {
		if _, ok := err.(*CommandError); !ok {
			return "", err
		}
	}
	result := strings.TrimSpace(out)
	if result == "" {
		result = strings.TrimSpace(stderr)
	}
	return result, nil
}
