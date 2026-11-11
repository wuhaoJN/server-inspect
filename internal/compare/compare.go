// Package compare 计算相邻两次巡检报告的差异。
package compare

import (
	"sort"
	"time"

	"serverinspect/internal/inspect"
	"serverinspect/internal/store"
)

// Issue 是新增或已消除的告警项。
type Issue struct {
	Section string `json:"section"`
	Label   string `json:"label"`
	Status  string `json:"status"`
	Value   string `json:"value"`
}

// ChangedItem 是状态发生变化的检查项。
type ChangedItem struct {
	Section        string `json:"section"`
	Label          string `json:"label"`
	PreviousStatus string `json:"previousStatus"`
	CurrentStatus  string `json:"currentStatus"`
	PreviousValue  string `json:"previousValue"`
	CurrentValue   string `json:"currentValue"`
}

// Comparison 是两次巡检的对比结果。
type Comparison struct {
	Available       bool            `json:"available"`
	CurrentID       int64           `json:"currentId"`
	PreviousID      int64           `json:"previousId"`
	CurrentAt       time.Time       `json:"currentAt"`
	PreviousAt      time.Time       `json:"previousAt"`
	CurrentStatus   string          `json:"currentStatus"`
	PreviousStatus  string          `json:"previousStatus"`
	CurrentScore    int             `json:"currentScore"`
	PreviousScore   int             `json:"previousScore"`
	ScoreDelta      int             `json:"scoreDelta"`
	CurrentMetrics  inspect.Metrics `json:"currentMetrics"`
	PreviousMetrics inspect.Metrics `json:"previousMetrics"`
	NewIssues       []Issue         `json:"newIssues"`
	ResolvedIssues  []Issue         `json:"resolvedIssues"`
	Changed         []ChangedItem   `json:"changed"`
}

type itemKey struct {
	section string
	label   string
}

func itemMap(report inspect.Report) map[itemKey]inspect.Item {
	out := make(map[itemKey]inspect.Item)
	for _, sec := range report.Sections {
		for _, item := range sec.Items {
			out[itemKey{section: sec.Title, label: item.Label}] = item
		}
	}
	return out
}

func isBad(status inspect.Status) bool {
	return status == inspect.StatusWarning || status == inspect.StatusCritical
}

// Build 计算当前报告相对上一次报告的差异。
func Build(current *store.Inspection, currentReport inspect.Report, previous *store.Inspection, previousReport inspect.Report) *Comparison {
	c := &Comparison{
		Available:      previous != nil,
		CurrentID:      current.ID,
		CurrentAt:      current.StartedAt,
		CurrentStatus:  current.Status,
		CurrentScore:   current.Score,
		CurrentMetrics: currentReport.Metrics,
		NewIssues:      []Issue{},
		ResolvedIssues: []Issue{},
		Changed:        []ChangedItem{},
	}
	if previous == nil {
		return c
	}
	c.PreviousID = previous.ID
	c.PreviousAt = previous.StartedAt
	c.PreviousStatus = previous.Status
	c.PreviousScore = previous.Score
	c.ScoreDelta = current.Score - previous.Score
	c.PreviousMetrics = previousReport.Metrics

	cur := itemMap(currentReport)
	prev := itemMap(previousReport)

	for key, item := range cur {
		old, exists := prev[key]
		if isBad(item.Status) && (!exists || !isBad(old.Status)) {
			c.NewIssues = append(c.NewIssues, Issue{Section: key.section, Label: key.label, Status: string(item.Status), Value: item.Value})
		}
		if exists && old.Status != item.Status {
			c.Changed = append(c.Changed, ChangedItem{
				Section: key.section, Label: key.label,
				PreviousStatus: string(old.Status), CurrentStatus: string(item.Status),
				PreviousValue: old.Value, CurrentValue: item.Value,
			})
		}
	}
	for key, item := range prev {
		now, exists := cur[key]
		if isBad(item.Status) && (!exists || !isBad(now.Status)) {
			c.ResolvedIssues = append(c.ResolvedIssues, Issue{Section: key.section, Label: key.label, Status: string(item.Status), Value: item.Value})
		}
	}

	sort.Slice(c.NewIssues, func(i, j int) bool {
		return c.NewIssues[i].Section+c.NewIssues[i].Label < c.NewIssues[j].Section+c.NewIssues[j].Label
	})
	sort.Slice(c.ResolvedIssues, func(i, j int) bool {
		return c.ResolvedIssues[i].Section+c.ResolvedIssues[i].Label < c.ResolvedIssues[j].Section+c.ResolvedIssues[j].Label
	})
	sort.Slice(c.Changed, func(i, j int) bool {
		return c.Changed[i].Section+c.Changed[i].Label < c.Changed[j].Section+c.Changed[j].Label
	})
	if len(c.Changed) > 20 {
		c.Changed = c.Changed[:20]
	}
	return c
}
