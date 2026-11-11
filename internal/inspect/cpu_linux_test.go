//go:build linux

package inspect

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// TestCPUUsageShellCommand 直接执行真实的 CPU 采样命令，避免 shell 字段解析回归。
func TestCPUUsageShellCommand(t *testing.T) {
	command := ""
	for _, def := range commandList {
		if def.key == "cpuusage" {
			command = def.cmd
			break
		}
	}
	if command == "" {
		t.Fatal("cpuusage command not found")
	}

	out, err := exec.Command("sh", "-c", command).CombinedOutput()
	if err != nil {
		t.Fatalf("cpuusage command failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		t.Fatal("cpuusage command returned empty output")
	}
	value, err := strconv.Atoi(text)
	if err != nil || value < 0 || value > 100 {
		t.Fatalf("invalid cpu usage output: %q", text)
	}
}
