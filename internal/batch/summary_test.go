package batch

import (
	"encoding/json"
	"testing"
	"time"

	"serverinspect/internal/inspect"
	"serverinspect/internal/store"
)

func TestBuildSummary(t *testing.T) {
	now := time.Now().UTC()
	reportCritical := inspect.Report{
		Warnings: []string{"内存 · 物理内存：已用 96.0%", "磁盘存储 · 挂载点 /：已用 91.0%"},
		Sections: []inspect.Section{
			{Title: "内存", Items: []inspect.Item{{Status: inspect.StatusCritical}}},
			{Title: "磁盘存储", Items: []inspect.Item{{Status: inspect.StatusCritical}, {Status: inspect.StatusOK}}},
		},
	}
	reportWarning := inspect.Report{
		Warnings: []string{"磁盘存储 · 挂载点 /data：已用 85.0%"},
		Sections: []inspect.Section{
			{Title: "磁盘存储", Items: []inspect.Item{{Status: inspect.StatusWarning}}},
		},
	}
	criticalJSON, _ := json.Marshal(reportCritical)
	warningJSON, _ := json.Marshal(reportWarning)
	records := []*store.Inspection{
		{ID: 1, ServerID: 1, ServerName: "db-01", Host: "10.0.0.1", Status: "critical", Score: 45, ReportJSON: string(criticalJSON), StartedAt: now},
		{ID: 2, ServerID: 2, ServerName: "web-01", Host: "10.0.0.2", Status: "warning", Score: 72, ReportJSON: string(warningJSON), StartedAt: now},
		{ID: 3, ServerID: 3, ServerName: "web-02", Host: "10.0.0.3", Status: "ok", Score: 100, ReportJSON: "{}", StartedAt: now},
	}
	servers := map[int64]*store.Server{
		1: {ID: 1, Name: "db-01", Host: "10.0.0.1", NodeRole: "DB", Group: "生产"},
		2: {ID: 2, Name: "web-01", Host: "10.0.0.2", NodeRole: "Web", Group: "生产"},
		3: {ID: 3, Name: "web-02", Host: "10.0.0.3", NodeRole: "Web", Group: "生产"},
	}
	failures := []Failure{{ServerID: 4, Name: "cache-01", Host: "10.0.0.4", Error: "连接超时"}}

	summary := Build(records, servers, failures, 2, 4321)
	if summary.Counts["ok"] != 1 || summary.Counts["warning"] != 1 || summary.Counts["critical"] != 1 || summary.Counts["error"] != 1 {
		t.Fatalf("counts = %#v", summary.Counts)
	}
	if summary.IssueCount != 3 || len(summary.Priority) != 3 {
		t.Fatalf("issue count = %d, priority = %#v", summary.IssueCount, summary.Priority)
	}
	if summary.Priority[0].Name != "cache-01" || summary.Priority[1].Name != "db-01" || summary.Priority[2].Name != "web-01" {
		t.Fatalf("priority order = %#v", summary.Priority)
	}
	if summary.AvgScore < 72.3 || summary.AvgScore > 72.4 || !summary.HasScore {
		t.Fatalf("avg = %v hasScore=%v", summary.AvgScore, summary.HasScore)
	}
	if summary.Skipped != 2 || summary.ElapsedMS != 4321 {
		t.Fatalf("meta = %#v", summary)
	}
	if len(summary.Common) != 2 || summary.Common[0].Title != "磁盘存储" || summary.Common[0].Servers != 2 {
		t.Fatalf("common = %#v", summary.Common)
	}
	if summary.Priority[1].NodeRole != "DB" || summary.Priority[1].Group != "生产" {
		t.Fatalf("server metadata = %#v", summary.Priority[1])
	}
}
