package inspect

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type commandDef struct {
	key string
	cmd string
}

// 所有命令均为只读操作（cat / ps / df / ss / systemctl status 等），不修改目标系统。
var commandList = []commandDef{
	{"osrelease", `cat /etc/os-release 2>/dev/null || cat /etc/system-release 2>/dev/null || cat /etc/redhat-release 2>/dev/null || cat /etc/issue 2>/dev/null`},
	{"uname", `uname -srm 2>/dev/null`},
	{"hostname", `hostname 2>/dev/null`},
	{"uptime", `cat /proc/uptime 2>/dev/null`},
	{"date", `LC_ALL=C date '+%Y-%m-%d %H:%M:%S %Z %z' 2>/dev/null`},
	{"timezone", `cat /etc/timezone 2>/dev/null; readlink /etc/localtime 2>/dev/null`},
	{"lscpu", `LC_ALL=C lscpu 2>/dev/null`},
	{"nproc", `nproc 2>/dev/null || grep -c '^processor' /proc/cpuinfo 2>/dev/null`},
	{"cpuusage", `if [ -r /proc/stat ]; then read -r cpu a b c d e f g h i j rest < /proc/stat; t1=$((a+b+c+d+e+f+g+h)); i1=$((d+e)); sleep 1; read -r cpu a b c d e f g h i j rest < /proc/stat; t2=$((a+b+c+d+e+f+g+h)); i2=$((d+e)); if [ $((t2-t1)) -gt 0 ]; then echo $(( (100*((t2-t1)-(i2-i1))) / (t2-t1) )); fi; fi`},
	{"loadavg", `cat /proc/loadavg 2>/dev/null`},
	{"meminfo", `cat /proc/meminfo 2>/dev/null`},
	{"disk", `LC_ALL=C df -P -k -x tmpfs -x devtmpfs -x squashfs 2>/dev/null || LC_ALL=C df -P -k 2>/dev/null`},
	{"inode", `LC_ALL=C df -P -i -x tmpfs -x devtmpfs -x squashfs 2>/dev/null`},
	{"topcpu", `LC_ALL=C ps -eo pid,user,pcpu,pmem,rss,comm --sort=-pcpu 2>/dev/null | head -n 11`},
	{"topmem", `LC_ALL=C ps -eo pid,user,pcpu,pmem,rss,comm --sort=-pmem 2>/dev/null | head -n 11`},
	{"proccount", `ps -e --no-headers 2>/dev/null | wc -l`},
	{"zombie", `ps -eo stat --no-headers 2>/dev/null | grep -c '^Z'`},
	{"listen", `if command -v ss >/dev/null 2>&1; then ss -tuln 2>/dev/null; else netstat -tuln 2>/dev/null; fi`},
	{"estab", `if command -v ss >/dev/null 2>&1; then ss -tn state established 2>/dev/null | tail -n +2 | wc -l; else netstat -tn 2>/dev/null | grep -c ESTAB; fi`},
	{"interfaces", `ip -o -4 addr show 2>/dev/null || ifconfig -a 2>/dev/null`},
	{"init", `ps -p 1 -o comm= 2>/dev/null`},
	{"failedunits", `LC_ALL=C systemctl --failed --no-legend --no-pager 2>/dev/null | head -n 12`},
	{"keyservices", `for s in ssh sshd crond cron rsyslog firewalld ufw docker nginx mysql mariadb postgresql redis; do state=$(systemctl is-active "$s" 2>/dev/null); if [ -n "$state" ] && [ "$state" != "inactive" ] && [ "$state" != "unknown" ]; then printf '%s=%s\n' "$s" "$state"; fi; done`},
	{"last", `LC_ALL=C last -n 8 -w 2>/dev/null | head -n 8`},
	{"failedlogin", `for f in /var/log/auth.log /var/log/secure; do if [ -r "$f" ]; then printf '%s %s\n' "$f" "$(grep -c 'Failed password' "$f" 2>/dev/null)"; fi; done`},
	{"sshdconfig", `grep -Ei '^[[:space:]]*(Port|PermitRootLogin|PasswordAuthentication|PubkeyAuthentication|PermitEmptyPasswords)[[:space:]]' /etc/ssh/sshd_config 2>/dev/null | head -n 12`},
	{"firewall", `if command -v ufw >/dev/null 2>&1; then ufw status 2>&1 | head -n 5; elif command -v firewall-cmd >/dev/null 2>&1; then firewall-cmd --state 2>&1 | head -n 2; elif command -v iptables >/dev/null 2>&1; then iptables -S 2>&1 | head -n 3; else echo 'no-firewall-tool'; fi`},
	{"selinux", `getenforce 2>/dev/null || cat /sys/fs/selinux/enforce 2>/dev/null`},
	{"reboot", `if [ -f /var/run/reboot-required ]; then echo yes; else echo no; fi`},
	{"updates", `if command -v apt-get >/dev/null 2>&1; then apt list --upgradable 2>/dev/null | grep -c upgradable; elif command -v dnf >/dev/null 2>&1; then dnf -C -q list --upgrades 2>/dev/null | grep -c '^[a-zA-Z]'; elif command -v yum >/dev/null 2>&1; then yum -C -q list updates 2>/dev/null | grep -c '^[a-zA-Z]'; fi`},
	{"dmesg", `dmesg 2>/dev/null | grep -Ei 'error|fail|oom' | tail -n 5`},
}

type cmdResult struct {
	stdout   string
	stderr   string
	err      error
	timedOut bool
}

type collector struct {
	runner   Runner
	timeout  time.Duration
	results  map[string]cmdResult
	firstErr error
}

func (c *collector) get(key string) string { return c.results[key].stdout }

func (c *collector) collect(ctx context.Context) error {
	timeouts := 0
	for _, def := range commandList {
		if err := ctx.Err(); err != nil {
			return err
		}
		cmdCtx, cancel := context.WithTimeout(ctx, c.timeout)
		stdout, stderr, err := c.runner.Run(cmdCtx, def.cmd)
		cancel()

		res := cmdResult{stdout: stdout, stderr: stderr, err: err}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if errors.Is(err, context.DeadlineExceeded) {
				res.timedOut = true
				timeouts++
				if timeouts >= 3 {
					return errors.New("远程命令连续超时，巡检中止（请检查目标服务器负载或网络）")
				}
			} else if isConnectionError(err) {
				return fmt.Errorf("SSH 连接中断: %w", err)
			} else if c.firstErr == nil {
				c.firstErr = err
			}
		}
		c.results[def.key] = res
	}
	return nil
}

func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	// 远程命令退出码非 0 属于单项采集失败，不应中断整次巡检。
	var cmdFailure interface{ CommandFailed() bool }
	if errors.As(err, &cmdFailure) && cmdFailure.CommandFailed() {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"connection lost", "connection reset", "broken pipe", "closed network connection",
		"session closed", "ssh: disconnect", "eof", "timeout waiting for",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// Run 对目标执行一次完整巡检。返回 error 仅在无法建立/维持连接或命令完全不可用时发生。
func Run(ctx context.Context, runner Runner, opts Options) (*Report, error) {
	opts = opts.withDefaults()
	started := time.Now().UTC()

	col := &collector{
		runner:  runner,
		timeout: opts.CommandTimeout,
		results: make(map[string]cmdResult, len(commandList)),
	}
	if err := col.collect(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(col.get("uname")) == "" && strings.TrimSpace(col.get("osrelease")) == "" &&
		strings.TrimSpace(col.get("uptime")) == "" {
		if col.firstErr != nil {
			return nil, fmt.Errorf("无法在目标服务器执行巡检命令: %w", col.firstErr)
		}
		return nil, errors.New("无法获取目标服务器基础信息，请确认账号权限")
	}

	rep := &Report{
		StartedAt: started,
		Sections:  make([]Section, 0, 9),
	}
	rep.Sections = append(rep.Sections,
		buildSystemSection(col, rep),
		buildCPUSection(col, rep, opts),
		buildMemorySection(col, rep, opts),
		buildDiskSection(col, rep, opts),
		buildProcessSection(col, rep),
		buildNetworkSection(col, rep),
		buildServiceSection(col, rep),
		buildSecuritySection(col, rep, opts),
		buildLogSection(col, rep),
	)

	rep.FinishedAt = time.Now().UTC()
	rep.DurationMS = rep.FinishedAt.Sub(rep.StartedAt).Milliseconds()
	finalize(rep)
	return rep, nil
}

func finalize(rep *Report) {
	var sum Summary
	for _, sec := range rep.Sections {
		for _, item := range sec.Items {
			switch item.Status {
			case StatusOK:
				sum.OK++
			case StatusInfo:
				sum.Info++
			case StatusWarning:
				sum.Warning++
			case StatusCritical:
				sum.Critical++
			default:
				sum.Skipped++
			}
			if item.Status == StatusWarning || item.Status == StatusCritical {
				if len(rep.Warnings) < 20 {
					rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s · %s：%s", sec.Title, item.Label, item.Value))
				}
			}
		}
	}
	rep.Summary = sum

	warnPenalty := 6 * sum.Warning
	if warnPenalty > 40 {
		warnPenalty = 40
	}
	penalty := 25*sum.Critical + warnPenalty
	score := 100 - penalty
	if score < 0 {
		score = 0
	}
	rep.Score = score

	switch {
	case sum.Critical > 0:
		rep.Overall = StatusCritical
	case sum.Warning > 0:
		rep.Overall = StatusWarning
	default:
		rep.Overall = StatusOK
	}
}

func buildSystemSection(col *collector, rep *Report) Section {
	sec := Section{Key: "system", Title: "系统信息"}
	rawOS := col.get("osrelease")
	info := parseOSRelease(rawOS)
	pretty := osPretty(info, rawOS)
	family := osFamily(info, pretty)
	rep.DetectedOS = pretty
	rep.OSFamily = family

	sysName, release, machine := parseUname(col.get("uname"))
	rep.Kernel = release
	rep.Arch = machine

	osStatus := StatusOK
	if pretty == "" {
		osStatus = StatusWarning
	}
	sec.Items = append(sec.Items, Item{
		Label:  "操作系统",
		Value:  dash(pretty),
		Detail: familyLabel(family),
		Status: osStatus,
	})
	if release != "" {
		sec.Items = append(sec.Items, Item{
			Label:  "内核版本",
			Value:  release,
			Detail: strings.TrimSpace(sysName + " " + machine),
			Status: StatusOK,
		})
	}
	if hostname := firstLine(col.get("hostname")); hostname != "" {
		sec.Items = append(sec.Items, Item{Label: "主机名", Value: hostname, Status: StatusOK})
	}
	if secs := parseUptime(col.get("uptime")); secs > 0 {
		rep.UptimeSeconds = secs
		sec.Items = append(sec.Items, Item{
			Label:  "运行时长",
			Value:  humanDuration(secs),
			Status: StatusInfo,
		})
	}
	if sysTime := firstLine(col.get("date")); sysTime != "" {
		sec.Items = append(sec.Items, Item{Label: "系统时间", Value: sysTime, Status: StatusInfo})
	}
	if tz := parseTimezone(col.get("timezone")); tz != "" {
		sec.Items = append(sec.Items, Item{Label: "时区", Value: tz, Status: StatusInfo})
	}
	if init := firstLine(col.get("init")); init != "" {
		sec.Items = append(sec.Items, Item{Label: "初始化系统", Value: init, Status: StatusInfo})
	}
	return sec
}

func buildCPUSection(col *collector, rep *Report, opts Options) Section {
	sec := Section{Key: "cpu", Title: "CPU 与负载"}
	cpu := parseLscpu(col.get("lscpu"))
	cores := atoi(col.get("nproc"))
	if cores <= 0 {
		cores = cpu.Cores
	}
	rep.Metrics.CPUCores = cores

	if cpu.Model != "" {
		detail := ""
		if cpu.Virtualization != "" {
			detail = cpu.Virtualization
		}
		sec.Items = append(sec.Items, Item{Label: "处理器型号", Value: cpu.Model, Detail: detail, Status: StatusInfo})
	}
	if cores > 0 {
		sec.Items = append(sec.Items, Item{Label: "逻辑核心数", Value: fmt.Sprintf("%d 核", cores), Status: StatusInfo})
	}

	if raw := strings.TrimSpace(col.get("cpuusage")); raw != "" {
		usage := atof(firstLine(raw))
		rep.Metrics.CPUPercent = usage
		sec.Items = append(sec.Items, Item{
			Label:  "CPU 使用率",
			Value:  pctText(usage),
			Detail: "1 秒采样",
			Status: thresholdStatus(usage, opts.CPUWarn, opts.CPUCritical),
		})
	} else {
		sec.Items = append(sec.Items, Item{Label: "CPU 使用率", Value: "无法采集", Status: StatusSkipped})
	}

	if l1, l5, l15, running, total, ok := parseLoadAvg(col.get("loadavg")); ok {
		rep.Metrics.Load1, rep.Metrics.Load5, rep.Metrics.Load15 = l1, l5, l15
		status := StatusInfo
		if cores > 0 {
			status = thresholdStatus(l1, float64(cores)*opts.LoadWarnFactor, float64(cores)*opts.LoadCritFactor)
		}
		detail := ""
		if total > 0 {
			detail = fmt.Sprintf("运行中进程 %d / 总进程 %d", running, total)
		}
		sec.Items = append(sec.Items, Item{
			Label:  "系统负载 (1/5/15 分钟)",
			Value:  fmt.Sprintf("%.2f / %.2f / %.2f", l1, l5, l15),
			Detail: detail,
			Status: status,
		})
	}
	return sec
}

func buildMemorySection(col *collector, rep *Report, opts Options) Section {
	sec := Section{Key: "memory", Title: "内存"}
	mem := parseMemInfo(col.get("meminfo"))
	total := mem["MemTotal"]
	if total <= 0 {
		sec.Items = append(sec.Items, Item{Label: "物理内存", Value: "无法采集", Status: StatusSkipped})
		return sec
	}
	available := mem["MemAvailable"]
	if available <= 0 {
		available = mem["MemFree"] + mem["Buffers"] + mem["Cached"]
	}
	used := total - available
	if used < 0 {
		used = 0
	}
	usedPct := used / total * 100
	rep.Metrics.MemPercent = usedPct

	sec.Items = append(sec.Items, Item{
		Label:  "物理内存",
		Value:  fmt.Sprintf("已用 %s / 共 %s (%s)", humanKB(used), humanKB(total), pctText(usedPct)),
		Detail: fmt.Sprintf("可用 %s · 缓存/缓冲 %s", humanKB(available), humanKB(mem["Buffers"]+mem["Cached"])),
		Status: thresholdStatus(usedPct, opts.MemWarn, opts.MemCritical),
	})

	swapTotal := mem["SwapTotal"]
	if swapTotal > 0 {
		swapUsed := swapTotal - mem["SwapFree"]
		if swapUsed < 0 {
			swapUsed = 0
		}
		swapPct := swapUsed / swapTotal * 100
		rep.Metrics.SwapPercent = swapPct
		sec.Items = append(sec.Items, Item{
			Label:  "交换分区",
			Value:  fmt.Sprintf("已用 %s / 共 %s (%s)", humanKB(swapUsed), humanKB(swapTotal), pctText(swapPct)),
			Status: thresholdStatus(swapPct, opts.SwapWarn, opts.SwapCritical),
		})
	} else {
		sec.Items = append(sec.Items, Item{Label: "交换分区", Value: "未启用", Status: StatusInfo})
	}
	return sec
}

func buildDiskSection(col *collector, rep *Report, opts Options) Section {
	sec := Section{Key: "disk", Title: "磁盘存储"}
	disks := mergeDisks(parseDFBlocks(col.get("disk")), parseDFInodes(col.get("inode")))
	if len(disks) == 0 {
		sec.Items = append(sec.Items, Item{Label: "磁盘分区", Value: "未发现可用分区", Status: StatusSkipped})
		return sec
	}

	maxDisk, maxInode := 0.0, 0.0
	for i, d := range disks {
		if d.UsePct > maxDisk {
			maxDisk = d.UsePct
		}
		if d.HasInodes && d.InodePct > maxInode {
			maxInode = d.InodePct
		}

		status := thresholdStatus(d.UsePct, opts.DiskWarn, opts.DiskCritical)
		detail := fmt.Sprintf("设备 %s · 可用 %s", d.FS, humanKB(d.AvailKB))
		if d.HasInodes {
			detail += fmt.Sprintf(" · inode 使用 %s", pctText(d.InodePct))
			status = worseStatus(status, thresholdStatus(d.InodePct, opts.InodeWarn, opts.InodeCritical))
		}
		sec.Items = append(sec.Items, Item{
			Label:  "挂载点 " + d.Mount,
			Value:  fmt.Sprintf("已用 %s / %s (%s)", humanKB(d.UsedKB), humanKB(d.SizeKB), pctText(d.UsePct)),
			Detail: detail,
			Status: status,
		})
		if i >= 19 {
			sec.Items = append(sec.Items, Item{Label: "其他分区", Value: "已省略", Status: StatusInfo})
			break
		}
	}
	rep.Metrics.DiskPercent = maxDisk
	rep.Metrics.InodePercent = maxInode
	return sec
}

func buildProcessSection(col *collector, rep *Report) Section {
	sec := Section{Key: "process", Title: "进程"}
	count := atoi(col.get("proccount"))
	if count > 0 {
		rep.Metrics.ProcessCount = count
		sec.Items = append(sec.Items, Item{Label: "进程总数", Value: fmt.Sprintf("%d 个", count), Status: StatusInfo})
	}
	if zombies := atoi(firstLine(col.get("zombie"))); zombies > 0 {
		sec.Items = append(sec.Items, Item{
			Label:  "僵尸进程",
			Value:  fmt.Sprintf("%d 个", zombies),
			Detail: "建议排查父进程回收逻辑",
			Status: StatusWarning,
		})
	}

	topCPU := parseProcesses(col.get("topcpu"))
	for i, p := range topCPU {
		if i >= 5 {
			break
		}
		sec.Items = append(sec.Items, Item{
			Label:  fmt.Sprintf("CPU 占用 #%d %s", i+1, p.Command),
			Value:  pctText(p.CPU),
			Detail: fmt.Sprintf("PID %d · 用户 %s · 内存 %s · RSS %s", p.PID, p.User, pctText(p.Mem), humanKB(p.RSSKB)),
			Status: StatusInfo,
		})
	}
	topMem := parseProcesses(col.get("topmem"))
	for i, p := range topMem {
		if i >= 3 {
			break
		}
		sec.Items = append(sec.Items, Item{
			Label:  fmt.Sprintf("内存占用 #%d %s", i+1, p.Command),
			Value:  pctText(p.Mem),
			Detail: fmt.Sprintf("PID %d · 用户 %s · RSS %s", p.PID, p.User, humanKB(p.RSSKB)),
			Status: StatusInfo,
		})
	}
	return sec
}

func buildNetworkSection(col *collector, rep *Report) Section {
	sec := Section{Key: "network", Title: "网络"}
	ifaces := parseInterfaces(col.get("interfaces"))
	if len(ifaces) > 0 {
		lines := make([]string, 0, len(ifaces))
		for _, it := range ifaces {
			lines = append(lines, fmt.Sprintf("%s  %s", it.Name, it.Addr))
		}
		sec.Items = append(sec.Items, Item{
			Label:  "IPv4 地址",
			Value:  fmt.Sprintf("%d 个", len(ifaces)),
			Detail: strings.Join(lines, "；"),
			Status: StatusInfo,
		})
	}

	listeners := parseListeners(col.get("listen"))
	rep.Metrics.ListenPorts = len(listeners)
	if len(listeners) > 0 {
		ports := make([]string, 0, len(listeners))
		for _, l := range listeners {
			ports = append(ports, l.Proto+"/"+l.Port)
		}
		detail := strings.Join(ports, ", ")
		if len(detail) > 300 {
			detail = detail[:300] + "…"
		}
		sec.Items = append(sec.Items, Item{
			Label:  "监听端口",
			Value:  fmt.Sprintf("%d 个", len(listeners)),
			Detail: detail,
			Status: StatusInfo,
		})
	} else {
		sec.Items = append(sec.Items, Item{
			Label:  "监听端口",
			Value:  "未采集到",
			Detail: "目标服务器可能缺少 ss / netstat 命令",
			Status: StatusSkipped,
		})
	}

	if raw := strings.TrimSpace(col.get("estab")); raw != "" {
		estab := atoi(firstLine(raw))
		rep.Metrics.Established = estab
		sec.Items = append(sec.Items, Item{Label: "已建立 TCP 连接", Value: fmt.Sprintf("%d 个", estab), Status: StatusInfo})
	}
	return sec
}

func buildServiceSection(col *collector, rep *Report) Section {
	sec := Section{Key: "service", Title: "服务与端口"}
	init := firstLine(col.get("init"))
	if init == "" {
		init = "未知"
	}

	failed := parseSystemdFailed(col.get("failedunits"))
	if init != "systemd" {
		sec.Items = append(sec.Items, Item{
			Label:  "初始化系统",
			Value:  init,
			Detail: "非 systemd 环境，已跳过 systemd 服务检查",
			Status: StatusSkipped,
		})
		return sec
	}
	sec.Items = append(sec.Items, Item{Label: "初始化系统", Value: init, Status: StatusInfo})
	if len(failed) == 0 {
		sec.Items = append(sec.Items, Item{Label: "失败的服务单元", Value: "0 个", Status: StatusOK})
	} else {
		sec.Items = append(sec.Items, Item{
			Label:  "失败的服务单元",
			Value:  fmt.Sprintf("%d 个", len(failed)),
			Status: StatusWarning,
		})
		for _, f := range failed {
			sec.Items = append(sec.Items, Item{
				Label:  "失败单元",
				Value:  strings.Fields(f)[0],
				Detail: f,
				Status: StatusWarning,
			})
		}
	}

	services := parseServices(col.get("keyservices"))
	if len(services) == 0 {
		sec.Items = append(sec.Items, Item{
			Label:  "关键服务",
			Value:  "未检测到常见服务",
			Detail: "已检查 ssh/sshd、crond、rsyslog、docker、nginx、数据库等",
			Status: StatusSkipped,
		})
	}
	for _, svc := range services {
		status := StatusOK
		switch svc.State {
		case "failed":
			status = StatusCritical
		case "activating", "deactivating", "reloading":
			status = StatusWarning
		case "active":
			status = StatusOK
		default:
			status = StatusInfo
		}
		sec.Items = append(sec.Items, Item{Label: "服务 " + svc.Name, Value: svc.State, Status: status})
	}
	return sec
}

func buildSecuritySection(col *collector, rep *Report, opts Options) Section {
	sec := Section{Key: "security", Title: "安全与合规"}

	sshd := parseSSHDConfig(col.get("sshdconfig"))
	if len(sshd) == 0 {
		sec.Items = append(sec.Items, Item{
			Label:  "SSH 配置",
			Value:  "无法读取",
			Detail: "需要读取 /etc/ssh/sshd_config 的权限",
			Status: StatusSkipped,
		})
	} else {
		if v, ok := sshd["permitrootlogin"]; ok {
			status := StatusOK
			if strings.EqualFold(v, "yes") {
				status = StatusWarning
			}
			sec.Items = append(sec.Items, Item{Label: "SSH 允许 root 登录", Value: v, Status: status})
		}
		if v, ok := sshd["passwordauthentication"]; ok {
			status := StatusInfo
			if strings.EqualFold(v, "yes") {
				status = StatusInfo
			}
			sec.Items = append(sec.Items, Item{Label: "SSH 密码认证", Value: v, Status: status})
		}
		if v, ok := sshd["permitemptypasswords"]; ok && strings.EqualFold(v, "yes") {
			sec.Items = append(sec.Items, Item{Label: "SSH 允许空密码", Value: v, Status: StatusCritical})
		}
		if v, ok := sshd["pubkeyauthentication"]; ok {
			sec.Items = append(sec.Items, Item{Label: "SSH 公钥认证", Value: v, Status: StatusInfo})
		}
		if v, ok := sshd["port"]; ok {
			sec.Items = append(sec.Items, Item{Label: "SSH 端口", Value: v, Status: StatusInfo})
		}
	}

	sec.Items = append(sec.Items, buildFirewallItem(col))
	sec.Items = append(sec.Items, buildSELinuxItem(col))

	if raw := col.get("failedlogin"); strings.TrimSpace(raw) != "" {
		count := parseFailedLogins(raw)
		status := StatusOK
		switch {
		case count >= opts.FailedLoginCrit:
			status = StatusCritical
		case count >= opts.FailedLoginWarn:
			status = StatusWarning
		}
		sec.Items = append(sec.Items, Item{
			Label:  "SSH 登录失败次数",
			Value:  fmt.Sprintf("%d 次", count),
			Detail: "统计 /var/log/auth.log 与 /var/log/secure 中的 Failed password 记录",
			Status: status,
		})
	} else {
		sec.Items = append(sec.Items, Item{
			Label:  "SSH 登录失败次数",
			Value:  "无法统计",
			Detail: "认证日志不可读，需要 root 权限",
			Status: StatusSkipped,
		})
	}

	if logins := parseLastLogins(col.get("last")); len(logins) > 0 {
		sec.Items = append(sec.Items, Item{
			Label:  "最近登录记录",
			Value:  fmt.Sprintf("%d 条", len(logins)),
			Detail: strings.Join(logins, "\n"),
			Status: StatusInfo,
		})
	}

	if raw := firstLine(col.get("reboot")); raw == "yes" {
		sec.Items = append(sec.Items, Item{Label: "待重启", Value: "是", Detail: "系统存在 /var/run/reboot-required 标记", Status: StatusWarning})
	} else if raw == "no" {
		sec.Items = append(sec.Items, Item{Label: "待重启", Value: "否", Status: StatusOK})
	}

	if raw := strings.TrimSpace(col.get("updates")); raw != "" {
		n := atoi(firstLine(raw))
		status := StatusOK
		if n > 0 {
			status = StatusInfo
		}
		sec.Items = append(sec.Items, Item{
			Label:  "可更新软件包",
			Value:  fmt.Sprintf("%d 个", n),
			Detail: "基于本地软件源缓存统计，未执行实际更新",
			Status: status,
		})
	}
	return sec
}

func buildFirewallItem(col *collector) Item {
	raw := col.get("firewall")
	stderr := col.results["firewall"].stderr
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "status: active"):
		return Item{Label: "防火墙", Value: "已启用 (ufw)", Status: StatusOK}
	case strings.Contains(lower, "status: inactive"):
		return Item{Label: "防火墙", Value: "未启用 (ufw)", Status: StatusWarning}
	case strings.Contains(lower, "not running"):
		return Item{Label: "防火墙", Value: "未启用 (firewalld)", Status: StatusWarning}
	case strings.TrimSpace(lower) == "running":
		return Item{Label: "防火墙", Value: "已启用 (firewalld)", Status: StatusOK}
	case strings.Contains(lower, "no-firewall-tool"):
		return Item{Label: "防火墙", Value: "未检测到防火墙工具", Status: StatusWarning}
	case strings.Contains(lower, "permission denied"), strings.Contains(lower, "must be root"),
		strings.Contains(lower, "operation not permitted"), strings.Contains(lower, "you must be root"):
		return Item{Label: "防火墙", Value: "无权限读取", Detail: "需要使用具有 root 权限的账号", Status: StatusSkipped}
	case strings.Contains(lower, "-a "):
		lines := 0
		for _, l := range strings.Split(raw, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "-A ") {
				lines++
			}
		}
		return Item{Label: "防火墙", Value: fmt.Sprintf("iptables 规则 %d 条", lines), Status: StatusOK}
	case strings.TrimSpace(raw) == "":
		detail := strings.TrimSpace(stderr)
		return Item{Label: "防火墙", Value: "未采集到状态", Detail: detail, Status: StatusSkipped}
	default:
		return Item{Label: "防火墙", Value: firstLine(raw), Status: StatusInfo}
	}
}

func buildSELinuxItem(col *collector) Item {
	raw := strings.TrimSpace(col.get("selinux"))
	switch {
	case strings.EqualFold(raw, "Enforcing"), raw == "1":
		return Item{Label: "SELinux", Value: "Enforcing", Status: StatusOK}
	case strings.EqualFold(raw, "Permissive"), raw == "0":
		return Item{Label: "SELinux", Value: "Permissive", Detail: "仅记录告警，不阻止访问", Status: StatusWarning}
	case strings.EqualFold(raw, "Disabled"):
		return Item{Label: "SELinux", Value: "Disabled", Status: StatusInfo}
	default:
		return Item{Label: "SELinux", Value: "未启用", Status: StatusSkipped}
	}
}

func buildLogSection(col *collector, rep *Report) Section {
	sec := Section{Key: "log", Title: "日志与其他"}
	raw := col.get("dmesg")
	if strings.TrimSpace(raw) == "" {
		detail := ""
		res := col.results["dmesg"]
		if res.err != nil {
			detail = strings.TrimSpace(res.stderr)
			if detail == "" {
				detail = "需要 root 权限读取内核日志"
			}
		}
		sec.Items = append(sec.Items, Item{Label: "内核错误日志", Value: "未发现或无法读取", Detail: detail, Status: StatusSkipped})
	} else {
		lines := parseDMesg(raw)
		sec.Items = append(sec.Items, Item{
			Label:  "内核错误日志",
			Value:  fmt.Sprintf("%d 条", len(lines)),
			Detail: strings.Join(lines, "\n"),
			Status: StatusWarning,
		})
	}
	return sec
}

// ---------- 辅助 ----------

func dash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "未知"
	}
	return strings.TrimSpace(v)
}

func firstLine(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			return line
		}
	}
	return ""
}

func parseTimezone(raw string) string {
	if tz := firstLine(raw); tz != "" {
		if strings.Contains(tz, "zoneinfo/") {
			if _, rest, ok := strings.Cut(tz, "zoneinfo/"); ok {
				return rest
			}
		}
		return tz
	}
	return ""
}

type cpuDetails struct {
	Model          string
	Cores          int
	Virtualization string
}

func parseLscpu(raw string) cpuDetails {
	var d cpuDetails
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "Model name":
			d.Model = value
		case "CPU(s)":
			d.Cores = atoi(value)
		case "Hypervisor vendor":
			if value != "" {
				d.Virtualization = value + " 虚拟化"
			}
		case "Virtualization type":
			if d.Virtualization == "" && value != "" {
				d.Virtualization = value + " 虚拟化"
			}
		}
	}
	return d
}
