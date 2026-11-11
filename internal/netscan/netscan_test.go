package netscan

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func startTestServer(t *testing.T, banner string) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				if banner != "" {
					_, _ = c.Write([]byte(banner))
				}
				time.Sleep(150 * time.Millisecond)
			}(conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func testOptions() Options {
	return Options{Timeout: 400 * time.Millisecond, Concurrency: 4, MaxHosts: 4096}
}

func TestParseTargets(t *testing.T) {
	cases := []struct {
		input string
		count int
		cidr  string
	}{
		{"192.168.1.0/30", 2, "192.168.1.0/30"},
		{"192.168.1.0/31", 2, "192.168.1.0/31"},
		{"192.168.1.10/32", 1, "192.168.1.10/32"},
		{"192.168.1.10", 1, "192.168.1.10/32"},
	}
	for _, tc := range cases {
		cidr, addrs, err := parseTargets(tc.input, 4096)
		if err != nil {
			t.Fatalf("%s: %v", tc.input, err)
		}
		if cidr != tc.cidr {
			t.Fatalf("%s: cidr = %q", tc.input, cidr)
		}
		if len(addrs) != tc.count {
			t.Fatalf("%s: count = %d, want %d", tc.input, len(addrs), tc.count)
		}
	}
}

func TestParseTargetsErrors(t *testing.T) {
	if _, _, err := parseTargets("", 4096); err == nil {
		t.Fatal("empty cidr should fail")
	}
	if _, _, err := parseTargets("not-a-cidr", 4096); err == nil {
		t.Fatal("invalid cidr should fail")
	}
	if _, _, err := parseTargets("2001:db8::/64", 4096); err == nil {
		t.Fatal("ipv6 should fail")
	}
	if _, _, err := parseTargets("10.0.0.0/8", 4096); err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("expected max host error, got %v", err)
	}
}

func TestNormalizePorts(t *testing.T) {
	ports, err := normalizePorts(nil)
	if err != nil || len(ports) != 1 || ports[0] != 22 {
		t.Fatalf("default ports = %#v, %v", ports, err)
	}
	ports, err = normalizePorts([]int{2222, 22, 2222})
	if err != nil || len(ports) != 2 || ports[0] != 22 || ports[1] != 2222 {
		t.Fatalf("ports = %#v, %v", ports, err)
	}
	if _, err := normalizePorts([]int{0}); err == nil {
		t.Fatal("invalid port should fail")
	}
	if _, err := normalizePorts([]int{70000}); err == nil {
		t.Fatal("invalid port should fail")
	}
}

func TestScanSSHHost(t *testing.T) {
	port := startTestServer(t, "SSH-2.0-OpenSSH_8.9p1 Ubuntu-3\r\n")
	res, err := Scan(context.Background(), "127.0.0.1/32", []int{port}, testOptions())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Hosts) != 1 {
		t.Fatalf("hosts = %#v", res.Hosts)
	}
	host := res.Hosts[0]
	if host.IP != "127.0.0.1" || len(host.Ports) != 1 || host.Ports[0] != port {
		t.Fatalf("unexpected host: %#v", host)
	}
	if !host.SSH || !strings.Contains(host.SSHBanner, "OpenSSH_8.9") {
		t.Fatalf("ssh detection failed: %#v", host)
	}
	if res.Scanned != 1 || res.Addresses != 1 {
		t.Fatalf("unexpected result meta: %#v", res)
	}
}

func TestScanNonSSHHost(t *testing.T) {
	port := startTestServer(t, "")
	res, err := Scan(context.Background(), "127.0.0.1/32", []int{port}, testOptions())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Hosts) != 1 {
		t.Fatalf("hosts = %#v", res.Hosts)
	}
	if res.Hosts[0].SSH {
		t.Fatalf("non-SSH port should not be flagged: %#v", res.Hosts[0])
	}
}

func TestScanNoHosts(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	res, err := Scan(context.Background(), "127.0.0.1/32", []int{port}, testOptions())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Hosts) != 0 {
		t.Fatalf("expected no hosts, got %#v", res.Hosts)
	}
}

func TestScanCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Scan(ctx, "192.0.2.0/30", []int{22}, testOptions())
	if err == nil {
		t.Fatal("expected context error")
	}
}
