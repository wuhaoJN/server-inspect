// Package batch 将多次巡检结果聚合为可持久化的批量分析摘要。
package batch

import (
	"encoding/json"
	"sort"
	"strings"

	"serverinspect/internal/inspect"
	"serverinspect/internal/store"
)

// Failure 表示没有生成巡检记录的请求失败（例如 HTTP 错误）。
type Failure struct {
	ServerID int64  `json:"serverId"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	NodeRole string `json:"nodeRole"`
	Group    string `json:"group"`
	Error    string `json:"error"`
}

// Item 是批量结果中的单台服务器摘要。
type Item struct {
	ID           int64    `json:"id"`
	ServerID     int64    `json:"serverId"`
	InspectionID int64    `json:"inspectionId"`
	Name         string   `json:"name"`
	Host         string   `json:"host"`
	NodeRole     string   `json:"nodeRole"`
	Group        string   `json:"group"`
	Status       string   `json:"status"`
	Score        int      `json:"score"`
	Error        string   `json:"error"`
	Warnings     []string `json:"warnings"`
}

// Common 是某一类异常在批量结果中的分布。
type Common struct {
	Title   string `json:"title"`
	Servers int    `json:"servers"`
	Items   int    `json:"items"`
}

// Summary 是批量巡检分析结果，字段与前端结果弹窗保持一致。
type Summary struct {
	Items              []Item         `json:"items"`
	Counts             map[string]int `json:"counts"`
	AvgScore           float64        `json:"avgScore"`
	HasScore           bool           `json:"hasScore"`
	Priority           []Item         `json:"priority"`
	IssueCount         int            `json:"issueCount"`
	Common             []Common       `json:"common"`
	Skipped            int            `json:"skipped"`
	MaintenanceSkipped int            `json:"maintenanceSkipped"`
	ElapsedMS          int64          `json:"elapsedMs"`
}

// Build 根据巡检记录和失败项生成批量摘要。
func Build(records []*store.Inspection, servers map[int64]*store.Server, failures []Failure, skipped int, elapsedMS int64) Summary {
	items := make([]Item, 0, len(records)+len(failures))
	sectionMap := map[string]*Common{}
	counts := map[string]int{"ok": 0, "warning": 0, "critical": 0, "error": 0}

	for _, rec := range records {
		status := rec.Status
		if status != "ok" && status != "warning" && status != "critical" && status != "error" {
			status = "error"
		}
		name, host, role, group := rec.ServerName, rec.Host, "", ""
		if srv := servers[rec.ServerID]; srv != nil {
			name, host, role, group = srv.Name, srv.Host, srv.NodeRole, srv.Group
		}
		item := Item{
			ID:           rec.ID,
			ServerID:     rec.ServerID,
			InspectionID: rec.ID,
			Name:         name,
			Host:         host,
			NodeRole:     role,
			Group:        group,
			Status:       status,
			Score:        rec.Score,
			Error:        rec.Error,
			Warnings:     []string{},
		}

		var report inspect.Report
		if rec.ReportJSON != "" {
			if err := json.Unmarshal([]byte(rec.ReportJSON), &report); err == nil {
				if report.Warnings != nil {
					item.Warnings = report.Warnings
				}
				for _, sec := range report.Sections {
					bad := 0
					for _, it := range sec.Items {
						if it.Status == inspect.StatusWarning || it.Status == inspect.StatusCritical {
							bad++
						}
					}
					if bad == 0 {
						continue
					}
					entry := sectionMap[sec.Title]
					if entry == nil {
						entry = &Common{Title: sec.Title}
						sectionMap[sec.Title] = entry
					}
					entry.Servers++
					entry.Items += bad
				}
			}
		}
		counts[status]++
		items = append(items, item)
	}

	for _, failure := range failures {
		item := Item{
			ID:           failure.ServerID,
			ServerID:     failure.ServerID,
			InspectionID: 0,
			Name:         failure.Name,
			Host:         failure.Host,
			NodeRole:     failure.NodeRole,
			Group:        failure.Group,
			Status:       "error",
			Error:        failure.Error,
			Warnings:     []string{},
		}
		counts["error"]++
		items = append(items, item)
	}

	scored := 0
	scoreSum := 0
	for _, item := range items {
		if item.Status != "error" {
			scored++
			scoreSum += item.Score
		}
	}
	avg := 0.0
	if scored > 0 {
		avg = float64(scoreSum) / float64(scored)
	}

	order := map[string]int{"error": 0, "critical": 1, "warning": 2, "ok": 3}
	priority := make([]Item, 0, len(items))
	for _, item := range items {
		if item.Status != "ok" {
			priority = append(priority, item)
		}
	}
	sort.SliceStable(priority, func(i, j int) bool {
		if order[priority[i].Status] != order[priority[j].Status] {
			return order[priority[i].Status] < order[priority[j].Status]
		}
		if priority[i].Score != priority[j].Score {
			return priority[i].Score < priority[j].Score
		}
		return strings.ToLower(priority[i].Name) < strings.ToLower(priority[j].Name)
	})
	issueCount := len(priority)
	if len(priority) > 12 {
		priority = priority[:12]
	}

	common := make([]Common, 0, len(sectionMap))
	for _, entry := range sectionMap {
		common = append(common, *entry)
	}
	sort.Slice(common, func(i, j int) bool {
		if common[i].Servers != common[j].Servers {
			return common[i].Servers > common[j].Servers
		}
		return common[i].Items > common[j].Items
	})
	if len(common) > 5 {
		common = common[:5]
	}

	return Summary{
		Items:      items,
		Counts:     counts,
		AvgScore:   avg,
		HasScore:   scored > 0,
		Priority:   priority,
		IssueCount: issueCount,
		Common:     common,
		Skipped:    skipped,
		ElapsedMS:  elapsedMS,
	}
}
