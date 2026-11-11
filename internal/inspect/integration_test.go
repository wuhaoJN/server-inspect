package inspect_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"serverinspect/internal/inspect"
	"serverinspect/internal/sshx"
)

// fakeSSHServer 启动一个仅用于测试的 SSH 服务端，按命令返回固定巡检数据。
func fakeSSHServer(t *testing.T) (port int, cleanup func()) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}

	cfg := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if meta.User() == "tester" && string(password) == "secret" {
				return nil, nil
			}
			return nil, fmt.Errorf("密码错误")
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				serverConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				defer serverConn.Close()
				go ssh.DiscardRequests(reqs)
				for newChan := range chans {
					if newChan.ChannelType() != "session" {
						_ = newChan.Reject(ssh.UnknownChannelType, "only session")
						continue
					}
					channel, requests, err := newChan.Accept()
					if err != nil {
						continue
					}
					go func() {
						defer channel.Close()
						for req := range requests {
							if req.Type != "exec" {
								continue
							}
							var payload struct{ Command string }
							if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
								return
							}
							if req.WantReply {
								_ = req.Reply(true, nil)
							}
							_, _ = channel.Write([]byte(fakeOutput(payload.Command)))
							_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
							return
						}
					}()
				}
			}()
		}
	}()

	cleanup = func() {
		_ = ln.Close()
		wg.Wait()
	}
	port = ln.Addr().(*net.TCPAddr).Port
	return port, cleanup
}

func fakeOutput(command string) string {
	switch {
	case strings.Contains(command, "/etc/os-release"):
		return "NAME=\"Ubuntu\"\nVERSION=\"22.04.4 LTS\"\nID=ubuntu\nID_LIKE=debian\nPRETTY_NAME=\"Ubuntu 22.04.4 LTS\"\n"
	case strings.Contains(command, "uname -srm"):
		return "Linux 6.8.0-45-generic x86_64\n"
	case strings.Contains(command, "hostname"):
		return "it-web-01\n"
	case strings.Contains(command, "/proc/uptime"):
		return "500000.00 2000000.00\n"
	case strings.Contains(command, "/proc/loadavg"):
		return "0.10 0.20 0.30 1/200 4242\n"
	case strings.Contains(command, "/proc/meminfo"):
		return "MemTotal: 8000000 kB\nMemFree: 1000000 kB\nMemAvailable: 6000000 kB\nBuffers: 100000 kB\nCached: 900000 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n"
	case strings.Contains(command, "df -P -i"):
		return "Filesystem Inodes IUsed IFree IUse% Mounted on\n/dev/vda1 5242880 524288 4718592 10% /\n"
	case strings.Contains(command, "df -P -k"):
		return "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/vda1 41943040 20971520 20971520 50% /\n"
	case strings.Contains(command, "ps -eo pid,user,pcpu"):
		return "PID USER %CPU %MEM RSS COMMAND\n 1000 www-data 5.0 2.0 102400 nginx\n"
	case strings.Contains(command, "ps -e --no-headers"):
		return "210\n"
	case strings.Contains(command, "grep -c '^Z'") && strings.Contains(command, "stat"):
		return "0\n"
	case strings.Contains(command, "ss -tuln"):
		return "Netid State Recv-Q Send-Q Local Address:Port Peer Address:Port\ntcp LISTEN 0 511 0.0.0.0:80 0.0.0.0:*\ntcp LISTEN 0 128 0.0.0.0:22 0.0.0.0:*\n"
	case strings.Contains(command, "ss -tn state established"):
		return "5\n"
	case strings.Contains(command, "ip -o -4 addr"):
		return "1: lo    inet 127.0.0.1/8 scope host lo\\       valid_lft forever\n2: eth0    inet 172.16.8.21/24 brd 172.16.8.255 scope global eth0\\       valid_lft forever\n"
	case strings.Contains(command, "ps -p 1 -o comm="):
		return "systemd\n"
	case strings.Contains(command, "systemctl --failed"):
		return ""
	case strings.Contains(command, "systemctl is-active"):
		return "ssh=active\nnginx=active\n"
	case strings.Contains(command, "sshd_config"):
		return "Port 22\nPermitRootLogin no\nPasswordAuthentication no\nPubkeyAuthentication yes\n"
	case strings.Contains(command, "ufw status"):
		return "Status: active\n"
	case strings.Contains(command, "getenforce"):
		return "Disabled\n"
	case strings.Contains(command, "reboot-required"):
		return "no\n"
	case strings.Contains(command, "apt list"):
		return "0\n"
	case strings.Contains(command, "dmesg"):
		return ""
	case strings.Contains(command, "last -n"):
		return "root pts/0 172.16.8.1 Tue Nov 10 09:00 still logged in\n"
	case strings.Contains(command, "/var/log/auth.log"):
		return "/var/log/auth.log 2\n"
	case strings.Contains(command, "/proc/stat"):
		return "8\n"
	case strings.Contains(command, "/etc/timezone"):
		return "Asia/Shanghai\n"
	case strings.Contains(command, "date '+%Y-%m-%d"):
		return "2026-11-10 10:00:00 CST +0800\n"
	case strings.Contains(command, "lscpu"):
		return "Model name: Intel(R) Core(TM) i7-9700 CPU @ 3.00GHz\nCPU(s): 4\n"
	case strings.Contains(command, "nproc"):
		return "4\n"
	default:
		return ""
	}
}

func TestSSHInspectionEndToEnd(t *testing.T) {
	port, cleanup := fakeSSHServer(t)
	defer cleanup()

	res, err := sshx.Dial(sshx.DialOptions{
		Host: "127.0.0.1", Port: port, Username: "tester", Password: "secret",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer res.Client.Close()
	if res.Fingerprint == "" || !strings.HasPrefix(res.Fingerprint, "SHA256:") {
		t.Fatalf("fingerprint = %q", res.Fingerprint)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := res.Client.Probe(ctx)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !strings.Contains(out, "Ubuntu") {
		t.Fatalf("probe output = %q", out)
	}

	report, err := inspect.Run(ctx, res.Client, inspect.DefaultOptions())
	if err != nil {
		t.Fatalf("inspect.Run: %v", err)
	}
	if report.DetectedOS != "Ubuntu 22.04.4 LTS" {
		t.Fatalf("DetectedOS = %q", report.DetectedOS)
	}
	if report.Metrics.ListenPorts != 2 {
		t.Fatalf("ListenPorts = %d", report.Metrics.ListenPorts)
	}
	if report.Overall != inspect.StatusOK {
		t.Fatalf("Overall = %q, warnings = %#v", report.Overall, report.Warnings)
	}
	if len(report.Sections) != 9 {
		t.Fatalf("sections = %d", len(report.Sections))
	}
}

func TestSSHAuthFailure(t *testing.T) {
	port, cleanup := fakeSSHServer(t)
	defer cleanup()

	_, err := sshx.Dial(sshx.DialOptions{
		Host: "127.0.0.1", Port: port, Username: "tester", Password: "wrong",
		Timeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatal("expected authentication failure")
	}
}

func TestSSHHostKeyMismatch(t *testing.T) {
	port, cleanup := fakeSSHServer(t)
	defer cleanup()

	_, err := sshx.Dial(sshx.DialOptions{
		Host: "127.0.0.1", Port: port, Username: "tester", Password: "secret",
		Timeout: 5 * time.Second, KnownHostKey: "SHA256:not-the-real-fingerprint",
	})
	if err == nil || !strings.Contains(err.Error(), "指纹") {
		t.Fatalf("expected host key mismatch, got %v", err)
	}
}
