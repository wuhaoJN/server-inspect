package store

import "time"

// Server 表示一台被纳管的目标服务器。PasswordEnc 为 AES-GCM 密文，绝不通过 API 返回。
type Server struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Host        string    `json:"host"`
	Port        int       `json:"port"`
	Username    string    `json:"username"`
	PasswordEnc string    `json:"-"`
	HostKey     string    `json:"hostKey,omitempty"`
	Group       string    `json:"group"`
	NodeRole    string    `json:"nodeRole"`
	Tags        string    `json:"tags"`
	Note        string    `json:"note"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// ServerView 是服务器列表项，附带最近一次巡检结果摘要。
type ServerView struct {
	Server
	Configured       bool       `json:"configured"`
	LastInspectionID int64      `json:"lastInspectionId"`
	LastStatus       string     `json:"lastStatus"`
	LastScore        int        `json:"lastScore"`
	LastAt           *time.Time `json:"lastAt"`
}

// Inspection 是一次巡检记录，ReportJSON 为完整巡检报告的 JSON。
type Inspection struct {
	ID          int64     `json:"id"`
	ServerID    int64     `json:"serverId"`
	ServerName  string    `json:"serverName"`
	Host        string    `json:"host"`
	Status      string    `json:"status"`
	Score       int       `json:"score"`
	Error       string    `json:"error"`
	MetricsJSON string    `json:"-"`
	ReportJSON  string    `json:"-"`
	StartedAt   time.Time `json:"startedAt"`
	FinishedAt  time.Time `json:"finishedAt"`
	DurationMS  int64     `json:"durationMs"`
}

// InspectionSummary 是巡检记录列表项，不包含大字段报告内容。
type InspectionSummary struct {
	ID         int64     `json:"id"`
	ServerID   int64     `json:"serverId"`
	ServerName string    `json:"serverName"`
	Host       string    `json:"host"`
	Status     string    `json:"status"`
	Score      int       `json:"score"`
	Error      string    `json:"error"`
	StartedAt  time.Time `json:"startedAt"`
	DurationMS int64     `json:"durationMs"`
}

// InspectionFilter 用于巡检记录分页查询。
type InspectionFilter struct {
	ServerID int64
	Status   string
	Limit    int
	Offset   int
}

// DashboardData 是首页概览数据。
type DashboardData struct {
	ServerCount      int                 `json:"serverCount"`
	IssueCount       int                 `json:"issueCount"`
	TodayCount       int                 `json:"todayCount"`
	AvgScore         float64             `json:"avgScore"`
	StatusCounts     map[string]int      `json:"statusCounts"`
	Servers          []ServerView        `json:"servers"`
	Recent           []InspectionSummary `json:"recent"`
	UninspectedCount int                 `json:"uninspectedCount"`
}
