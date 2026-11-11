package inspect

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// humanKB 将 KB 数值转换为可读容量。
func humanKB(kb float64) string {
	if kb <= 0 {
		return "0 B"
	}
	if kb < 1024 {
		return fmt.Sprintf("%.0f KB", kb)
	}
	units := []string{"MB", "GB", "TB", "PB"}
	v := kb / 1024
	for _, unit := range units {
		if v < 1024 || unit == "PB" {
			return fmt.Sprintf("%.1f %s", v, unit)
		}
		v /= 1024
	}
	return fmt.Sprintf("%.1f PB", v)
}

// humanDuration 将秒转换为「X 天 X 小时 X 分」。
func humanDuration(seconds int64) string {
	if seconds <= 0 {
		return "未知"
	}
	days := seconds / 86400
	hours := (seconds % 86400) / 3600
	mins := (seconds % 3600) / 60
	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%d 天", days))
	}
	if hours > 0 || days > 0 {
		parts = append(parts, fmt.Sprintf("%d 小时", hours))
	}
	parts = append(parts, fmt.Sprintf("%d 分", mins))
	return strings.Join(parts, " ")
}

// pctText 格式化百分比，保留一位小数。
func pctText(v float64) string {
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', 1, 64) + "%"
}

// thresholdStatus 根据阈值给出状态。
func thresholdStatus(v, warn, critical float64) Status {
	switch {
	case v >= critical:
		return StatusCritical
	case v >= warn:
		return StatusWarning
	default:
		return StatusOK
	}
}

// worseStatus 返回两个状态中更严重的一个。
func worseStatus(a, b Status) Status {
	rank := func(s Status) int {
		switch s {
		case StatusCritical:
			return 4
		case StatusWarning:
			return 3
		case StatusInfo:
			return 2
		case StatusOK:
			return 1
		default:
			return 0
		}
	}
	if rank(a) >= rank(b) {
		return a
	}
	return b
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func atof(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func trimPercent(s string) float64 {
	return atof(strings.TrimSuffix(strings.TrimSpace(s), "%"))
}
