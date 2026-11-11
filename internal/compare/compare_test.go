package compare

import (
	"testing"
	"time"

	"serverinspect/internal/inspect"
	"serverinspect/internal/store"
)

func TestBuildComparison(t *testing.T) {
	now := time.Now().UTC()
	current := &store.Inspection{
		ID: 2, ServerID: 1, Status: "warning", Score: 80, StartedAt: now,
	}
	previous := &store.Inspection{
		ID: 1, ServerID: 1, Status: "critical", Score: 60, StartedAt: now.Add(-time.Hour),
	}
	currentReport := inspect.Report{
		Metrics: inspect.Metrics{CPUPercent: 30, MemPercent: 95, DiskPercent: 70},
		Sections: []inspect.Section{
			{Title: "内存", Items: []inspect.Item{{Label: "物理内存", Value: "95%", Status: inspect.StatusCritical}}},
			{Title: "磁盘存储", Items: []inspect.Item{{Label: "挂载点 /", Value: "70%", Status: inspect.StatusWarning}}},
			{Title: "安全与合规", Items: []inspect.Item{{Label: "SSH", Value: "ok", Status: inspect.StatusOK}}},
		},
	}
	previousReport := inspect.Report{
		Metrics: inspect.Metrics{CPUPercent: 20, MemPercent: 50, DiskPercent: 92},
		Sections: []inspect.Section{
			{Title: "内存", Items: []inspect.Item{{Label: "物理内存", Value: "50%", Status: inspect.StatusOK}}},
			{Title: "磁盘存储", Items: []inspect.Item{{Label: "挂载点 /", Value: "92%", Status: inspect.StatusCritical}}},
			{Title: "安全与合规", Items: []inspect.Item{{Label: "SSH", Value: "weak", Status: inspect.StatusWarning}}},
		},
	}

	c := Build(current, currentReport, previous, previousReport)
	if !c.Available || c.ScoreDelta != 20 || c.PreviousID != 1 {
		t.Fatalf("unexpected comparison: %#v", c)
	}
	if len(c.NewIssues) != 1 || c.NewIssues[0].Label != "物理内存" {
		t.Fatalf("new issues = %#v", c.NewIssues)
	}
	if len(c.ResolvedIssues) != 1 || c.ResolvedIssues[0].Label != "SSH" {
		t.Fatalf("resolved issues = %#v", c.ResolvedIssues)
	}
	if len(c.Changed) != 3 {
		t.Fatalf("changed = %#v", c.Changed)
	}
}
