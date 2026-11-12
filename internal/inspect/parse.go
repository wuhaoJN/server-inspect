package inspect

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ---------- 系统信息 ----------

func parseOSRelease(raw string) map[string]string {
	info := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" {
			info[key] = value
		}
	}
	return info
}

func osPretty(info map[string]string, raw string) string {
	if v := strings.TrimSpace(info["PRETTY_NAME"]); v != "" {
		return v
	}
	name := strings.TrimSpace(info["NAME"])
	version := strings.TrimSpace(info["VERSION"])
	if name == "" {
		name = strings.TrimSpace(info["ID"])
	}
	if name != "" {
		if version != "" {
			return name + " " + version
		}
		return name
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.Contains(line, "=") {
			continue
		}
		return line
	}
	return ""
}

func osFamily(info map[string]string, pretty string) string {
	haystack := strings.ToLower(strings.Join([]string{
		info["ID"], info["ID_LIKE"], info["NAME"], pretty,
	}, " "))
	switch {
	case strings.Contains(haystack, "ubuntu"), strings.Contains(haystack, "debian"),
		strings.Contains(haystack, "linuxmint"), strings.Contains(haystack, "kali"),
		strings.Contains(haystack, "raspbian"), strings.Contains(haystack, "uos"):
		return "debian"
	case strings.Contains(haystack, "centos"), strings.Contains(haystack, "rhel"),
		strings.Contains(haystack, "red hat"), strings.Contains(haystack, "fedora"),
		strings.Contains(haystack, "rocky"), strings.Contains(haystack, "alma"),
		strings.Contains(haystack, "openeuler"), strings.Contains(haystack, "anolis"),
		strings.Contains(haystack, "opencloudos"), strings.Contains(haystack, "kylin"):
		return "rhel"
	case strings.Contains(haystack, "suse"), strings.Contains(haystack, "sles"):
		return "suse"
	case strings.Contains(haystack, "alpine"):
		return "alpine"
	case strings.Contains(haystack, "arch"), strings.Contains(haystack, "manjaro"):
		return "arch"
	default:
		return ""
	}
}

func familyLabel(family string) string {
	switch family {
	case "debian":
		return "Debian/Ubuntu 系"
	case "rhel":
		return "RHEL/CentOS 系"
	case "suse":
		return "SUSE 系"
	case "alpine":
		return "Alpine Linux"
	case "arch":
		return "Arch 系"
	default:
		return "其他 Linux"
	}
}

func parseUname(raw string) (sys, release, machine string) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) > 0 {
		sys = fields[0]
	}
	if len(fields) > 1 {
		release = fields[1]
	}
	if len(fields) > 2 {
		machine = fields[2]
	}
	return
}

func parseUptime(raw string) int64 {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return 0
	}
	v := atof(fields[0])
	return int64(v)
}

// ---------- CPU / 内存 ----------

func parseLoadAvg(raw string) (l1, l5, l15 float64, running, total int, ok bool) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) < 3 {
		return 0, 0, 0, 0, 0, false
	}
	l1, l5, l15 = atof(fields[0]), atof(fields[1]), atof(fields[2])
	if len(fields) >= 4 {
		if idx := strings.Index(fields[3], "/"); idx > 0 {
			running = atoi(fields[3][:idx])
			total = atoi(fields[3][idx+1:])
		}
	}
	return l1, l5, l15, running, total, true
}

func parseMemInfo(raw string) map[string]float64 {
	info := map[string]float64{}
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		info[strings.TrimSpace(key)] = atof(fields[0])
	}
	return info
}

// ---------- 磁盘 ----------

type diskUsage struct {
	FS        string
	Mount     string
	SizeKB    float64
	UsedKB    float64
	AvailKB   float64
	UsePct    float64
	InodePct  float64
	HasInodes bool
}

func parseDFBlocks(raw string) []diskUsage {
	var out []diskUsage
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		usePct := trimPercent(fields[len(fields)-2])
		if !strings.Contains(fields[len(fields)-2], "%") {
			continue
		}
		out = append(out, diskUsage{
			FS:      fields[0],
			SizeKB:  atof(fields[1]),
			UsedKB:  atof(fields[2]),
			AvailKB: atof(fields[3]),
			UsePct:  usePct,
			Mount:   strings.Join(fields[5:], " "),
		})
	}
	return out
}

func parseDFInodes(raw string) []diskUsage {
	var out []diskUsage
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		pctField := fields[len(fields)-2]
		if !strings.Contains(pctField, "%") {
			continue
		}
		out = append(out, diskUsage{
			FS:        fields[0],
			InodePct:  trimPercent(pctField),
			HasInodes: true,
			Mount:     strings.Join(fields[5:], " "),
		})
	}
	return out
}

var pseudoFS = []string{
	"tmpfs", "devtmpfs", "udev", "shm", "none", "squashfs", "sysfs", "proc",
	"cgroup", "cgroup2", "ramfs", "devpts", "mqueue", "hugetlbfs", "debugfs",
	"tracefs", "securityfs", "pstore", "configfs", "fusectl", "binfmt_misc",
	"autofs", "efivarfs", "bpf",
}

func isPseudoFS(name string) bool {
	base := strings.ToLower(name)
	for _, p := range pseudoFS {
		if base == p || strings.HasPrefix(base, p+"/") {
			return true
		}
	}
	return strings.HasPrefix(base, "/dev/loop") || strings.HasPrefix(base, "fuse.")
}

func mergeDisks(blocks, inodes []diskUsage) []diskUsage {
	byMount := make(map[string]diskUsage, len(blocks))
	for _, d := range blocks {
		if isPseudoFS(d.FS) || d.SizeKB <= 0 {
			continue
		}
		byMount[d.Mount] = d
	}
	for _, ino := range inodes {
		if d, ok := byMount[ino.Mount]; ok {
			d.InodePct = ino.InodePct
			d.HasInodes = ino.HasInodes
			byMount[ino.Mount] = d
		}
	}
	out := make([]diskUsage, 0, len(byMount))
	for _, d := range byMount {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UsePct == out[j].UsePct {
			return out[i].Mount < out[j].Mount
		}
		return out[i].UsePct > out[j].UsePct
	})
	return out
}

// ---------- 进程 / 端口 ----------

type procInfo struct {
	PID     int
	User    string
	CPU     float64
	Mem     float64
	RSSKB   float64
	Command string
}

func parseProcesses(raw string) []procInfo {
	var out []procInfo
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		out = append(out, procInfo{
			PID:     pid,
			User:    fields[1],
			CPU:     atof(fields[2]),
			Mem:     atof(fields[3]),
			RSSKB:   atof(fields[4]),
			Command: strings.Join(fields[5:], " "),
		})
	}
	return out
}

type zombieInfo struct {
	PID     int
	PPID    int
	State   string
	Command string
}

// parseZombieProcesses 解析 ps -eo pid,ppid,stat,comm 输出的僵尸进程行。
// 命令名可能包含空格，因此第 4 个字段之后的内容整体作为命令名。
func parseZombieProcesses(raw string) []zombieInfo {
	var out []zombieInfo
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		if !strings.HasPrefix(fields[2], "Z") {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		command := strings.Join(fields[3:], " ")
		if command == "" {
			command = "未知"
		}
		out = append(out, zombieInfo{PID: pid, PPID: ppid, State: fields[2], Command: command})
	}
	return out
}

type listenerInfo struct {
	Proto string
	Addr  string
	Port  string
}

func parseListeners(raw string) []listenerInfo {
	lines := strings.Split(raw, "\n")
	ssStyle := false
	for _, line := range lines {
		if strings.Contains(line, "Netid") {
			ssStyle = true
			break
		}
		if strings.TrimSpace(line) != "" {
			break
		}
	}
	seen := map[string]listenerInfo{}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		proto := strings.ToLower(fields[0])
		if !strings.HasPrefix(proto, "tcp") && !strings.HasPrefix(proto, "udp") {
			continue
		}
		addrIdx := 3
		if ssStyle {
			addrIdx = 4
		}
		if len(fields) <= addrIdx {
			continue
		}
		addr := fields[addrIdx]
		port := extractPort(addr)
		if port == "" || port == "*" {
			continue
		}
		key := proto + "/" + port
		if _, ok := seen[key]; !ok {
			seen[key] = listenerInfo{Proto: proto, Addr: addr, Port: port}
		}
	}
	out := make([]listenerInfo, 0, len(seen))
	for _, l := range seen {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool {
		pi, pj := atoi(out[i].Port), atoi(out[j].Port)
		if pi == pj {
			return out[i].Proto < out[j].Proto
		}
		return pi < pj
	})
	return out
}

func extractPort(addr string) string {
	idx := strings.LastIndex(addr, ":")
	if idx < 0 || idx == len(addr)-1 {
		return ""
	}
	return strings.Trim(addr[idx+1:], "[]")
}

// ---------- 网络接口 ----------

type ifaceInfo struct {
	Name string
	Addr string
}

func parseInterfaces(raw string) []ifaceInfo {
	var out []ifaceInfo
	current := ""
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(line, ": flags=") || strings.Contains(line, "Link encap") {
			name := strings.SplitN(trimmed, ":", 2)[0]
			if fields := strings.Fields(name); len(fields) > 0 {
				current = fields[0]
			}
		}
		idx := strings.Index(line, " inet ")
		if idx < 0 {
			continue
		}
		rest := strings.Fields(line[idx+len(" inet "):])
		if len(rest) == 0 {
			continue
		}
		name := current
		if name == "" {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				name = strings.TrimPrefix(strings.TrimSuffix(fields[1], ":"), "@")
			}
		}
		if name == "" || strings.Contains(rest[0], ":") {
			continue
		}
		key := name + "|" + rest[0]
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ifaceInfo{Name: name, Addr: rest[0]})
		if len(out) >= 10 {
			break
		}
	}
	return out
}

// ---------- 服务 / 安全 ----------

func parseSystemdFailed(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "●"))
		if line == "" || strings.HasPrefix(line, "0 loaded") || strings.Contains(line, "loaded units listed") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.Contains(fields[0], ".") {
			continue
		}
		if len(line) > 160 {
			line = line[:160]
		}
		out = append(out, line)
		if len(out) >= 10 {
			break
		}
	}
	return out
}

func parseServices(raw string) []struct{ Name, State string } {
	var out []struct{ Name, State string }
	for _, line := range strings.Split(raw, "\n") {
		name, state, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || name == "" || state == "" {
			continue
		}
		out = append(out, struct{ Name, State string }{name, state})
	}
	return out
}

func parseSSHDConfig(raw string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		out[strings.ToLower(fields[0])] = strings.Join(fields[1:], " ")
	}
	return out
}

func parseFailedLogins(raw string) int {
	total := 0
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		total += atoi(fields[len(fields)-1])
	}
	return total
}

func parseLastLogins(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "wtmp begins") || strings.HasPrefix(line, "btmp begins") {
			continue
		}
		if len(line) > 140 {
			line = line[:140]
		}
		out = append(out, line)
		if len(out) >= 5 {
			break
		}
	}
	return out
}

var errPattern = regexp.MustCompile(`(?i)error|fail|oom|panic|segfault`)

func parseDMesg(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !errPattern.MatchString(line) {
			continue
		}
		if len(line) > 160 {
			line = line[:160]
		}
		out = append(out, line)
		if len(out) >= 5 {
			break
		}
	}
	return out
}
