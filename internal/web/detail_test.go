package web_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"serverinspect/internal/store"
)

func TestServerDetailTrendsAndCompare(t *testing.T) {
	env := newTestEnv(t)
	client := newClient(t)
	base := env.http.URL

	if status, resp := call(t, client, http.MethodPost, base+"/api/login",
		map[string]string{"username": "admin", "password": "Admin@12345"}, ""); status != http.StatusOK || !resp.OK {
		t.Fatalf("login: status=%d %s", status, resp.Error)
	}

	srv := &store.Server{
		Name: "detail-test", Host: "10.9.0.1", Port: 22, Username: "root",
		PasswordEnc: "cipher", NodeRole: "Web", Group: "测试",
	}
	if err := env.st.CreateServer(srv); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}

	now := time.Now().UTC()
	previousReport := `{"overall":"critical","score":60,"metrics":{"cpuPercent":20,"memPercent":50,"diskPercent":92},"sections":[{"key":"disk","title":"磁盘存储","items":[{"label":"挂载点 /","value":"92%","status":"critical"}]},{"key":"mem","title":"内存","items":[{"label":"物理内存","value":"50%","status":"ok"}]}]}`
	currentReport := `{"overall":"warning","score":80,"metrics":{"cpuPercent":30,"memPercent":95,"diskPercent":70},"sections":[{"key":"disk","title":"磁盘存储","items":[{"label":"挂载点 /","value":"70%","status":"warning"}]},{"key":"mem","title":"内存","items":[{"label":"物理内存","value":"95%","status":"critical"}]}]}`
	prev := &store.Inspection{
		ServerID: srv.ID, ServerName: srv.Name, Host: srv.Host,
		Status: "critical", Score: 60, ReportJSON: previousReport, MetricsJSON: `{}`,
		StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-59 * time.Minute),
	}
	cur := &store.Inspection{
		ServerID: srv.ID, ServerName: srv.Name, Host: srv.Host,
		Status: "warning", Score: 80, ReportJSON: currentReport, MetricsJSON: `{"cpuPercent":30,"memPercent":95,"diskPercent":70}`,
		StartedAt: now, FinishedAt: now.Add(time.Minute),
	}
	for _, rec := range []*store.Inspection{prev, cur} {
		if err := env.st.CreateInspection(rec); err != nil {
			t.Fatalf("CreateInspection: %v", err)
		}
	}

	status, resp := call(t, client, http.MethodGet, base+"/api/servers/"+strconv.FormatInt(srv.ID, 10), nil, "")
	if status != http.StatusOK || !resp.OK {
		t.Fatalf("server detail: status=%d %s", status, resp.Error)
	}
	var view struct {
		Name       string `json:"name"`
		NodeRole   string `json:"nodeRole"`
		LastStatus string `json:"lastStatus"`
		LastScore  int    `json:"lastScore"`
	}
	if err := json.Unmarshal(resp.Data, &view); err != nil || view.Name != "detail-test" || view.NodeRole != "Web" || view.LastStatus != "warning" || view.LastScore != 80 {
		t.Fatalf("detail view: %v %#v", err, view)
	}

	status, resp = call(t, client, http.MethodGet, base+"/api/servers/"+strconv.FormatInt(srv.ID, 10)+"/trends?days=30", nil, "")
	if status != http.StatusOK || !resp.OK {
		t.Fatalf("trends: status=%d %s", status, resp.Error)
	}
	var trends struct {
		Points []struct {
			Score int `json:"score"`
		} `json:"points"`
	}
	if err := json.Unmarshal(resp.Data, &trends); err != nil || len(trends.Points) != 2 {
		t.Fatalf("trends: %v %#v", err, trends)
	}

	status, resp = call(t, client, http.MethodGet, base+"/api/inspections/"+strconv.FormatInt(cur.ID, 10)+"/compare", nil, "")
	if status != http.StatusOK || !resp.OK {
		t.Fatalf("compare: status=%d %s", status, resp.Error)
	}
	var comparison struct {
		Available  bool `json:"available"`
		ScoreDelta int  `json:"scoreDelta"`
		NewIssues  []struct {
			Label string `json:"label"`
		} `json:"newIssues"`
		ResolvedIssues []struct {
			Label string `json:"label"`
		} `json:"resolvedIssues"`
	}
	if err := json.Unmarshal(resp.Data, &comparison); err != nil {
		t.Fatalf("decode compare: %v", err)
	}
	if !comparison.Available || comparison.ScoreDelta != 20 || len(comparison.NewIssues) != 1 || comparison.NewIssues[0].Label != "物理内存" {
		t.Fatalf("comparison: %#v", comparison)
	}
}
