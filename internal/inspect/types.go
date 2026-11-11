// Package inspect 实现非侵入式（只读）Linux 服务器巡检。
package inspect

import (
	"context"
	"time"
)

// Status 是单项巡检结果状态。
type Status string

const (
	StatusOK       Status = "ok"
	StatusInfo     Status = "info"
	StatusWarning  Status = "warning"
	StatusCritical Status = "critical"
	StatusSkipped  Status = "skipped"
)

// Item 是一条巡检指标。
type Item struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Detail string `json:"detail,omitempty"`
	Status Status `json:"status"`
}

// Section 是巡检报告中的一个分类。
type Section struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Items []Item `json:"items"`
}

// Summary 汇总各类状态数量。
type Summary struct {
	OK       int `json:"ok"`
	Info     int `json:"info"`
	Warning  int `json:"warning"`
	Critical int `json:"critical"`
	Skipped  int `json:"skipped"`
}

// Metrics 是用于趋势展示的关键指标。
type Metrics struct {
	Load1        float64 `json:"load1"`
	Load5        float64 `json:"load5"`
	Load15       float64 `json:"load15"`
	CPUCores     int     `json:"cpuCores"`
	CPUPercent   float64 `json:"cpuPercent"`
	MemPercent   float64 `json:"memPercent"`
	SwapPercent  float64 `json:"swapPercent"`
	DiskPercent  float64 `json:"diskPercent"`
	InodePercent float64 `json:"inodePercent"`
	ProcessCount int     `json:"processCount"`
	ListenPorts  int     `json:"listenPorts"`
	Established  int     `json:"established"`
}

// Report 是一次巡检的完整结果。
type Report struct {
	ServerName    string    `json:"serverName"`
	Host          string    `json:"host"`
	DetectedOS    string    `json:"detectedOs"`
	OSFamily      string    `json:"osFamily"`
	Kernel        string    `json:"kernel"`
	Arch          string    `json:"arch"`
	UptimeSeconds int64     `json:"uptimeSeconds"`
	StartedAt     time.Time `json:"startedAt"`
	FinishedAt    time.Time `json:"finishedAt"`
	DurationMS    int64     `json:"durationMs"`
	Overall       Status    `json:"overall"`
	Score         int       `json:"score"`
	Summary       Summary   `json:"summary"`
	Metrics       Metrics   `json:"metrics"`
	Sections      []Section `json:"sections"`
	Warnings      []string  `json:"warnings"`
}

// Runner 抽象远程命令执行器，便于单元测试注入假实现。
type Runner interface {
	Run(ctx context.Context, command string) (stdout string, stderr string, err error)
}

// Options 是巡检阈值与超时配置。
type Options struct {
	CommandTimeout  time.Duration
	LoadWarnFactor  float64
	LoadCritFactor  float64
	CPUWarn         float64
	CPUCritical     float64
	MemWarn         float64
	MemCritical     float64
	SwapWarn        float64
	SwapCritical    float64
	DiskWarn        float64
	DiskCritical    float64
	InodeWarn       float64
	InodeCritical   float64
	FailedLoginWarn int
	FailedLoginCrit int
}

// DefaultOptions 返回默认阈值：磁盘 80/90，内存 85/95，CPU 85/95，负载为核数的 1/2 倍。
func DefaultOptions() Options {
	return Options{
		CommandTimeout:  15 * time.Second,
		LoadWarnFactor:  1.0,
		LoadCritFactor:  2.0,
		CPUWarn:         85,
		CPUCritical:     95,
		MemWarn:         85,
		MemCritical:     95,
		SwapWarn:        80,
		SwapCritical:    95,
		DiskWarn:        80,
		DiskCritical:    90,
		InodeWarn:       80,
		InodeCritical:   90,
		FailedLoginWarn: 20,
		FailedLoginCrit: 100,
	}
}

func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.CommandTimeout <= 0 {
		o.CommandTimeout = d.CommandTimeout
	}
	if o.LoadWarnFactor <= 0 {
		o.LoadWarnFactor = d.LoadWarnFactor
	}
	if o.LoadCritFactor <= 0 {
		o.LoadCritFactor = d.LoadCritFactor
	}
	if o.CPUWarn <= 0 {
		o.CPUWarn = d.CPUWarn
	}
	if o.CPUCritical <= 0 {
		o.CPUCritical = d.CPUCritical
	}
	if o.MemWarn <= 0 {
		o.MemWarn = d.MemWarn
	}
	if o.MemCritical <= 0 {
		o.MemCritical = d.MemCritical
	}
	if o.SwapWarn <= 0 {
		o.SwapWarn = d.SwapWarn
	}
	if o.SwapCritical <= 0 {
		o.SwapCritical = d.SwapCritical
	}
	if o.DiskWarn <= 0 {
		o.DiskWarn = d.DiskWarn
	}
	if o.DiskCritical <= 0 {
		o.DiskCritical = d.DiskCritical
	}
	if o.InodeWarn <= 0 {
		o.InodeWarn = d.InodeWarn
	}
	if o.InodeCritical <= 0 {
		o.InodeCritical = d.InodeCritical
	}
	if o.FailedLoginWarn <= 0 {
		o.FailedLoginWarn = d.FailedLoginWarn
	}
	if o.FailedLoginCrit <= 0 {
		o.FailedLoginCrit = d.FailedLoginCrit
	}
	return o
}
