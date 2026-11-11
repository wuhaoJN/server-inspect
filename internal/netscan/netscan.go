// Package netscan 提供面向巡检中心的轻量网络扫描：
// 仅对指定 IPv4 网段做 TCP 连接探测，并读取 SSH 服务标识，不尝试登录、不发送任何探测载荷。
package netscan

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultTimeout 是单个 TCP 连接的超时时间。
	DefaultTimeout = 600 * time.Millisecond
	// DefaultConcurrency 是并发探测的地址数量。
	DefaultConcurrency = 128
	// DefaultMaxHosts 是单次扫描允许的最大地址数（/20）。
	DefaultMaxHosts = 4096
	maxPorts        = 16
)

// Options 是扫描参数。
type Options struct {
	Timeout      time.Duration
	Concurrency  int
	MaxHosts     int
	ResolveNames bool
	NameTimeout  time.Duration
}

func (o Options) withDefaults() Options {
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.Concurrency <= 0 {
		o.Concurrency = DefaultConcurrency
	}
	if o.Concurrency > 512 {
		o.Concurrency = 512
	}
	if o.MaxHosts <= 0 {
		o.MaxHosts = DefaultMaxHosts
	}
	if o.NameTimeout <= 0 {
		o.NameTimeout = 400 * time.Millisecond
	}
	return o
}

// Host 是一台发现的主机。
type Host struct {
	IP        string `json:"ip"`
	Hostname  string `json:"hostname,omitempty"`
	Ports     []int  `json:"ports"`
	SSHBanner string `json:"sshBanner,omitempty"`
	SSH       bool   `json:"ssh"`

	ipValue uint32
}

// Result 是一次扫描的结果。
type Result struct {
	CIDR       string `json:"cidr"`
	Addresses  int    `json:"addresses"`
	Scanned    int    `json:"scanned"`
	DurationMS int64  `json:"durationMs"`
	Hosts      []Host `json:"hosts"`
}

// Scan 扫描指定 CIDR 网段。ports 为空时默认扫描 22。
func Scan(ctx context.Context, cidr string, ports []int, opts Options) (*Result, error) {
	opts = opts.withDefaults()
	start := time.Now()

	normalized, addresses, err := parseTargets(cidr, opts.MaxHosts)
	if err != nil {
		return nil, err
	}
	ports, err = normalizePorts(ports)
	if err != nil {
		return nil, err
	}

	hosts, err := scanAddresses(ctx, addresses, ports, opts)
	if err != nil {
		return nil, err
	}
	if opts.ResolveNames && len(hosts) > 0 {
		resolveHostnames(ctx, hosts, opts.NameTimeout)
	}

	sort.Slice(hosts, func(i, j int) bool { return hosts[i].ipValue < hosts[j].ipValue })
	return &Result{
		CIDR:       normalized,
		Addresses:  len(addresses),
		Scanned:    len(addresses),
		DurationMS: time.Since(start).Milliseconds(),
		Hosts:      hosts,
	}, nil
}

// parseTargets 解析 CIDR 并展开为 IPv4 地址列表。
func parseTargets(input string, maxHosts int) (string, []uint32, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return "", nil, errors.New("请输入要扫描的网段，例如 192.168.1.0/24")
	}
	if !strings.Contains(raw, "/") {
		if ip := net.ParseIP(raw); ip != nil && ip.To4() != nil {
			raw += "/32"
		} else {
			return "", nil, fmt.Errorf("无效的网段：%s", input)
		}
	}
	_, ipnet, err := net.ParseCIDR(raw)
	if err != nil {
		return "", nil, fmt.Errorf("无效的网段：%s（示例：192.168.1.0/24）", input)
	}
	if ipnet.IP.To4() == nil {
		return "", nil, errors.New("目前仅支持 IPv4 网段")
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 {
		return "", nil, errors.New("目前仅支持 IPv4 网段")
	}
	total := uint64(1) << uint(bits-ones)
	if total > uint64(maxHosts) {
		return "", nil, fmt.Errorf("网段包含 %d 个地址，超过单次扫描上限 %d，请缩小范围（例如使用 /24）", total, maxHosts)
	}

	base := uint64(binary.BigEndian.Uint32(ipnet.IP.To4()))
	startAddr := base
	endAddr := base + total - 1
	// /30 及更大网段排除网络地址与广播地址；/31、/32 全部可用。
	if total > 2 {
		startAddr++
		endAddr--
	}

	addresses := make([]uint32, 0, endAddr-startAddr+1)
	for v := startAddr; v <= endAddr; v++ {
		addresses = append(addresses, uint32(v))
	}
	return ipnet.String(), addresses, nil
}

func normalizePorts(ports []int) ([]int, error) {
	if len(ports) == 0 {
		ports = []int{22}
	}
	seen := map[int]bool{}
	out := make([]int, 0, len(ports))
	for _, port := range ports {
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("端口 %d 无效，必须在 1-65535 之间", port)
		}
		if seen[port] {
			continue
		}
		seen[port] = true
		out = append(out, port)
	}
	if len(out) > maxPorts {
		return nil, fmt.Errorf("单次最多扫描 %d 个端口", maxPorts)
	}
	sort.Ints(out)
	return out, nil
}

func scanAddresses(ctx context.Context, addresses []uint32, ports []int, opts Options) ([]Host, error) {
	jobs := make(chan uint32)
	results := make(chan Host, 64)

	workerCount := opts.Concurrency
	if workerCount > len(addresses) {
		workerCount = len(addresses)
	}
	if workerCount < 1 {
		workerCount = 1
	}

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range jobs {
				if host, ok := probeHost(ctx, ip, ports, opts.Timeout); ok {
					select {
					case results <- host:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	go func() {
		defer close(jobs)
		for _, ip := range addresses {
			select {
			case jobs <- ip:
			case <-ctx.Done():
				return
			}
		}
	}()

	hosts := make([]Host, 0, 8)
	for host := range results {
		hosts = append(hosts, host)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return hosts, nil
}

func probeHost(ctx context.Context, ip uint32, ports []int, timeout time.Duration) (Host, bool) {
	ipAddr := uint32ToIP(ip)
	ipText := ipAddr.String()

	openPorts := make([]int, 0, len(ports))
	banner := ""
	for _, port := range ports {
		if ctx.Err() != nil {
			return Host{}, false
		}
		dialer := net.Dialer{Timeout: timeout}
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ipText, strconv.Itoa(port)))
		if err != nil {
			continue
		}
		openPorts = append(openPorts, port)
		if banner == "" {
			banner = readSSHBanner(conn)
		}
		_ = conn.Close()
	}
	if len(openPorts) == 0 {
		return Host{}, false
	}

	ssh := strings.HasPrefix(banner, "SSH-")
	if !ssh {
		for _, port := range openPorts {
			if port == 22 || port == 2222 {
				ssh = true
				break
			}
		}
	}
	return Host{
		IP:        ipText,
		Ports:     openPorts,
		SSHBanner: banner,
		SSH:       ssh,
		ipValue:   ip,
	}, true
}

// readSSHBanner 读取 SSH 服务端主动发送的版本标识（不发送任何数据）。
func readSSHBanner(conn net.Conn) string {
	_ = conn.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
	reader := bufio.NewReaderSize(conn, 256)
	line, err := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if err != nil && line == "" {
		return ""
	}
	if !strings.HasPrefix(line, "SSH-") {
		return ""
	}
	if len(line) > 200 {
		line = line[:200]
	}
	return line
}

func resolveHostnames(ctx context.Context, hosts []Host, timeout time.Duration) {
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sem := make(chan struct{}, 16)
	var wg sync.WaitGroup
	for i := range hosts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-lookupCtx.Done():
				return
			}
			defer func() { <-sem }()

			nameCtx, nameCancel := context.WithTimeout(lookupCtx, timeout)
			defer nameCancel()
			names, err := net.DefaultResolver.LookupAddr(nameCtx, hosts[i].IP)
			if err != nil || len(names) == 0 {
				return
			}
			name := strings.TrimSuffix(strings.TrimSpace(names[0]), ".")
			if len(name) > 200 {
				name = name[:200]
			}
			hosts[i].Hostname = name
		}(i)
	}
	wg.Wait()
}

func uint32ToIP(v uint32) net.IP {
	return net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}
