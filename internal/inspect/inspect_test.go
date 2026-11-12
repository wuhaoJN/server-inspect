package inspect

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	outputs map[string]string
}

func (f fakeRunner) Run(_ context.Context, command string) (string, string, error) {
	for _, def := range commandList {
		if def.cmd == command {
			if out, ok := f.outputs[def.key]; ok {
				return out, "", nil
			}
			return "", "", nil
		}
	}
	return "", "", nil
}

func TestParseOSRelease(t *testing.T) {
	raw := `NAME="Ubuntu"
VERSION="22.04.3 LTS (Jammy Jellyfish)"
ID=ubuntu
ID_LIKE=debian
PRETTY_NAME="Ubuntu 22.04.3 LTS"
`
	info := parseOSRelease(raw)
	if info["ID"] != "ubuntu" {
		t.Fatalf("ID = %q", info["ID"])
	}
	if pretty := osPretty(info, raw); pretty != "Ubuntu 22.04.3 LTS" {
		t.Fatalf("pretty = %q", pretty)
	}
	if family := osFamily(info, "Ubuntu 22.04.3 LTS"); family != "debian" {
		t.Fatalf("family = %q", family)
	}
}

func TestParseOSReleaseFallback(t *testing.T) {
	raw := "CentOS Linux release 7.9.2009 (Core)\n"
	info := parseOSRelease(raw)
	pretty := osPretty(info, raw)
	if pretty != "CentOS Linux release 7.9.2009 (Core)" {
		t.Fatalf("pretty = %q", pretty)
	}
	if family := osFamily(info, pretty); family != "rhel" {
		t.Fatalf("family = %q", family)
	}
}

func TestOSFamilyDetection(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		wantPretty string
		wantFamily string
	}{
		{
			name:       "centos7 redhat-release",
			raw:        "CentOS Linux release 7.9.2009 (Core)\n",
			wantPretty: "CentOS Linux release 7.9.2009 (Core)",
			wantFamily: "rhel",
		},
		{
			name:       "rocky",
			raw:        "NAME=\"Rocky Linux\"\nVERSION=\"9.4 (Blue Onyx)\"\nID=\"rocky\"\nID_LIKE=\"rhel centos fedora\"\nPRETTY_NAME=\"Rocky Linux 9.4 (Blue Onyx)\"\n",
			wantPretty: "Rocky Linux 9.4 (Blue Onyx)",
			wantFamily: "rhel",
		},
		{
			name:       "openeuler",
			raw:        "NAME=\"openEuler\"\nVERSION=\"22.03 (LTS-SP3)\"\nID=\"openEuler\"\nPRETTY_NAME=\"openEuler 22.03 (LTS-SP3)\"\n",
			wantPretty: "openEuler 22.03 (LTS-SP3)",
			wantFamily: "rhel",
		},
		{
			name:       "debian",
			raw:        "PRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\nNAME=\"Debian GNU/Linux\"\nVERSION_ID=\"12\"\nID=debian\n",
			wantPretty: "Debian GNU/Linux 12 (bookworm)",
			wantFamily: "debian",
		},
		{
			name:       "kylin desktop",
			raw:        "NAME=\"Kylin\"\nVERSION=\"V10\"\nID=kylin\nID_LIKE=debian\nPRETTY_NAME=\"Kylin V10\"\n",
			wantPretty: "Kylin V10",
			wantFamily: "debian",
		},
		{
			name:       "suse",
			raw:        "NAME=\"SLES\"\nVERSION=\"15-SP4\"\nID=\"sles\"\nID_LIKE=\"suse\"\nPRETTY_NAME=\"SLES 15-SP4\"\n",
			wantPretty: "SLES 15-SP4",
			wantFamily: "suse",
		},
		{
			name:       "alpine",
			raw:        "NAME=\"Alpine Linux\"\nID=alpine\nVERSION_ID=3.19.1\nPRETTY_NAME=\"Alpine Linux v3.19\"\n",
			wantPretty: "Alpine Linux v3.19",
			wantFamily: "alpine",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := parseOSRelease(tc.raw)
			pretty := osPretty(info, tc.raw)
			if pretty != tc.wantPretty {
				t.Fatalf("pretty = %q, want %q", pretty, tc.wantPretty)
			}
			if family := osFamily(info, pretty); family != tc.wantFamily {
				t.Fatalf("family = %q, want %q", family, tc.wantFamily)
			}
		})
	}
}

func TestParseMemInfoAndLoad(t *testing.T) {
	mem := parseMemInfo("MemTotal: 16384000 kB\nMemFree: 2000000 kB\nMemAvailable: 8192000 kB\nSwapTotal: 2097152 kB\nSwapFree: 1048576 kB\n")
	if mem["MemTotal"] != 16384000 || mem["SwapFree"] != 1048576 {
		t.Fatalf("unexpected meminfo: %#v", mem)
	}
	l1, l5, l15, running, total, ok := parseLoadAvg("0.50 0.45 0.40 2/350 9876\n")
	if !ok || l1 != 0.5 || l5 != 0.45 || l15 != 0.4 || running != 2 || total != 350 {
		t.Fatalf("unexpected load: %v %v %v %d %d %v", l1, l5, l15, running, total, ok)
	}
}

func TestParseDF(t *testing.T) {
	blocks := `Filesystem     1024-blocks     Used Available Capacity Mounted on
/dev/sda1         41943040 31457280  10485760      75% /
/dev/sdb1        104857600 94371840  10485760      90% /data
`
	inodes := `Filesystem      Inodes  IUsed  IFree IUse% Mounted on
/dev/sda1      2621440 524288 2097152   20% /
/dev/sdb1      6553600 6291456  262144   96% /data
`
	disks := mergeDisks(parseDFBlocks(blocks), parseDFInodes(inodes))
	if len(disks) != 2 {
		t.Fatalf("disks = %d", len(disks))
	}
	// 按使用率倒序，/data 应排第一
	if disks[0].Mount != "/data" || disks[0].UsePct != 90 {
		t.Fatalf("unexpected first disk: %#v", disks[0])
	}
	if !disks[0].HasInodes || disks[0].InodePct != 96 {
		t.Fatalf("inode not merged: %#v", disks[0])
	}
}

func TestParseListeners(t *testing.T) {
	ss := `Netid State  Recv-Q Send-Q Local Address:Port Peer Address:Port Process
udp   UNCONN 0      0            0.0.0.0:68         0.0.0.0:*
tcp   LISTEN 0      128          0.0.0.0:22         0.0.0.0:*
tcp   LISTEN 0      511             [::]:80            [::]:*
`
	got := parseListeners(ss)
	if len(got) != 3 {
		t.Fatalf("listeners = %d: %#v", len(got), got)
	}
	ports := map[string]bool{}
	for _, l := range got {
		ports[l.Proto+"/"+l.Port] = true
	}
	for _, want := range []string{"udp/68", "tcp/22", "tcp/80"} {
		if !ports[want] {
			t.Fatalf("missing %s in %#v", want, got)
		}
	}
}

func TestParseProcessesAndSSHD(t *testing.T) {
	procs := parseProcesses("  PID USER %CPU %MEM   RSS COMMAND\n 1234 root 45.2  3.1 512000 nginx: worker\n")
	if len(procs) != 1 || procs[0].PID != 1234 || procs[0].RSSKB != 512000 || procs[0].Command != "nginx: worker" {
		t.Fatalf("unexpected procs: %#v", procs)
	}
	cfg := parseSSHDConfig("# comment\nPort 2222\nPermitRootLogin yes\nPasswordAuthentication no\n")
	if cfg["port"] != "2222" || cfg["permitrootlogin"] != "yes" || cfg["passwordauthentication"] != "no" {
		t.Fatalf("unexpected sshd config: %#v", cfg)
	}
}

func TestRunBuildsReport(t *testing.T) {
	fake := fakeRunner{outputs: map[string]string{
		"osrelease": `NAME="Ubuntu"
VERSION="22.04.3 LTS (Jammy Jellyfish)"
ID=ubuntu
ID_LIKE=debian
PRETTY_NAME="Ubuntu 22.04.3 LTS"
`,
		"uname":       "Linux 5.15.0-91-generic x86_64\n",
		"hostname":    "web-01\n",
		"uptime":      "123456.78 1000.00\n",
		"date":        "2026-11-10 10:00:00 CST +0800\n",
		"timezone":    "Asia/Shanghai\n",
		"lscpu":       "Architecture: x86_64\nModel name: Intel(R) Xeon(R) CPU E5-2680 v4 @ 2.40GHz\nCPU(s): 8\nHypervisor vendor: VMware\n",
		"nproc":       "8\n",
		"cpuusage":    "12\n",
		"loadavg":     "0.50 0.45 0.40 2/350 9876\n",
		"meminfo":     "MemTotal: 16384000 kB\nMemFree: 2000000 kB\nMemAvailable: 8192000 kB\nBuffers: 500000 kB\nCached: 4000000 kB\nSwapTotal: 2097152 kB\nSwapFree: 1048576 kB\n",
		"disk":        "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/sda1 41943040 31457280 10485760 75% /\n/dev/sdb1 104857600 89128960 15728640 85% /data\n",
		"inode":       "Filesystem Inodes IUsed IFree IUse% Mounted on\n/dev/sda1 2621440 524288 2097152 20% /\n/dev/sdb1 6553600 5373952 1179648 82% /data\n",
		"topcpu":      "PID USER %CPU %MEM RSS COMMAND\n1234 root 45.2 3.1 512000 nginx\n",
		"topmem":      "PID USER %CPU %MEM RSS COMMAND\n4321 mysql 1.2 12.5 2048000 mysqld\n",
		"proccount":   "350\n",
		"zombie":      "0\n",
		"listen":      "Netid State Recv-Q Send-Q Local Address:Port Peer Address:Port\ntcp LISTEN 0 128 0.0.0.0:22 0.0.0.0:*\ntcp LISTEN 0 511 0.0.0.0:443 0.0.0.0:*\n",
		"estab":       "7\n",
		"interfaces":  "1: lo    inet 127.0.0.1/8 scope host lo\\       valid_lft forever\n2: eth0    inet 10.0.0.5/24 brd 10.0.0.255 scope global eth0\\       valid_lft forever\n",
		"init":        "systemd\n",
		"failedunits": "",
		"keyservices": "sshd=active\nnginx=active\n",
		"last":        "root pts/0 10.0.0.1 Tue Nov 10 09:00 still logged in\n",
		"failedlogin": "/var/log/auth.log 3\n",
		"sshdconfig":  "Port 22\nPermitRootLogin yes\nPasswordAuthentication yes\nPubkeyAuthentication yes\n",
		"firewall":    "Status: active\n",
		"selinux":     "Enforcing\n",
		"reboot":      "no\n",
		"updates":     "4\n",
		"dmesg":       "",
	}}
	report, err := Run(context.Background(), fake, Options{CommandTimeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if report.DetectedOS != "Ubuntu 22.04.3 LTS" {
		t.Fatalf("DetectedOS = %q", report.DetectedOS)
	}
	if report.Kernel != "5.15.0-91-generic" {
		t.Fatalf("Kernel = %q", report.Kernel)
	}
	if report.Metrics.CPUCores != 8 || report.Metrics.CPUPercent != 12 {
		t.Fatalf("cpu metrics = %#v", report.Metrics)
	}
	if report.Metrics.ListenPorts != 2 || report.Metrics.Established != 7 {
		t.Fatalf("network metrics = %#v", report.Metrics)
	}
	if report.Metrics.DiskPercent != 85 {
		t.Fatalf("DiskPercent = %v", report.Metrics.DiskPercent)
	}
	if report.Overall != StatusWarning {
		t.Fatalf("Overall = %q, warnings = %#v", report.Overall, report.Warnings)
	}
	if report.Score >= 100 || report.Score <= 0 {
		t.Fatalf("Score = %d", report.Score)
	}
	if len(report.Warnings) == 0 || !strings.Contains(strings.Join(report.Warnings, "|"), "挂载点 /data") {
		t.Fatalf("warnings = %#v", report.Warnings)
	}
	if report.Summary.Warning == 0 {
		t.Fatalf("summary = %#v", report.Summary)
	}
	// 应包含全部 9 个分类
	if len(report.Sections) != 9 {
		t.Fatalf("sections = %d", len(report.Sections))
	}
}

func TestRunFailsWhenNoOutput(t *testing.T) {
	_, err := Run(context.Background(), fakeRunner{outputs: map[string]string{}}, Options{CommandTimeout: time.Second})
	if err == nil {
		t.Fatal("expected error when no command output available")
	}
}

func TestCPUUsageZero(t *testing.T) {
	fake := fakeRunner{outputs: map[string]string{
		"osrelease": "NAME=\"Ubuntu\"\nID=ubuntu\nPRETTY_NAME=\"Ubuntu 22.04 LTS\"\n",
		"uname":     "Linux 5.15.0 x86_64\n",
		"uptime":    "100.00 100.00\n",
		"cpuusage":  "0\n",
		"nproc":     "4\n",
		"loadavg":   "0.00 0.00 0.00 1/100 1234\n",
		"meminfo":   "MemTotal: 1000000 kB\nMemAvailable: 500000 kB\n",
	}}
	rep, err := Run(context.Background(), fake, Options{CommandTimeout: time.Second})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var found *Item
	for i := range rep.Sections {
		if rep.Sections[i].Key != "cpu" {
			continue
		}
		for j := range rep.Sections[i].Items {
			if rep.Sections[i].Items[j].Label == "CPU 使用率" {
				found = &rep.Sections[i].Items[j]
			}
		}
	}
	if found == nil {
		t.Fatal("CPU usage item missing")
	}
	if found.Status == StatusSkipped {
		t.Fatalf("0%% usage should be collected, got %#v", *found)
	}
	if found.Value != "0.0%" {
		t.Fatalf("value = %q", found.Value)
	}
}

type fakeCommandFailure struct{}

func (fakeCommandFailure) Error() string       { return "unexpected EOF" }
func (fakeCommandFailure) CommandFailed() bool { return true }

func TestIsConnectionErrorIgnoresCommandFailures(t *testing.T) {
	if isConnectionError(fakeCommandFailure{}) {
		t.Fatal("command failure should not be treated as connection error")
	}
	if !isConnectionError(errors.New("ssh: connection lost")) {
		t.Fatal("real connection error should be detected")
	}
}

func TestThresholdStatus(t *testing.T) {
	if got := thresholdStatus(50, 80, 90); got != StatusOK {
		t.Fatalf("got %q", got)
	}
	if got := thresholdStatus(85, 80, 90); got != StatusWarning {
		t.Fatalf("got %q", got)
	}
	if got := thresholdStatus(95, 80, 90); got != StatusCritical {
		t.Fatalf("got %q", got)
	}
}

func TestParseZombieProcesses(t *testing.T) {
	raw := " 1234  1200 Z    defunct\n 5678  1200 Z+   worker process\n 999   1    Ss   bash\nbad line\n"
	got := parseZombieProcesses(raw)
	if len(got) != 2 {
		t.Fatalf("zombies = %#v", got)
	}
	if got[0].PID != 1234 || got[0].PPID != 1200 || got[0].State != "Z" || got[0].Command != "defunct" {
		t.Fatalf("first = %#v", got[0])
	}
	if got[1].Command != "worker process" || got[1].PID != 5678 {
		t.Fatalf("second = %#v", got[1])
	}
}

func TestBuildProcessSectionZombieDetails(t *testing.T) {
	c := &collector{results: map[string]cmdResult{
		"proccount":    {stdout: "120\n"},
		"zombie":       {stdout: "2\n"},
		"zombiedetail": {stdout: " 1234  1200 Z    defunct\n 5678  1200 Z+   worker process\n"},
	}}
	sec := buildProcessSection(c, &Report{})
	items := map[string]Item{}
	for _, it := range sec.Items {
		items[it.Label] = it
	}
	main, ok := items["僵尸进程"]
	if !ok || main.Value != "2 个" || main.Status != StatusWarning {
		t.Fatalf("main zombie item = %#v", main)
	}
	z1, ok := items["僵尸进程 #1"]
	if !ok || z1.Value != "PID 1234" || !strings.Contains(z1.Detail, "父进程 1200") || !strings.Contains(z1.Detail, "defunct") {
		t.Fatalf("zombie #1 = %#v", z1)
	}
	z2, ok := items["僵尸进程 #2"]
	if !ok || z2.Value != "PID 5678" || !strings.Contains(z2.Detail, "worker process") {
		t.Fatalf("zombie #2 = %#v", z2)
	}
}
