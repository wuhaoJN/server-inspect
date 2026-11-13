package web_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"serverinspect/internal/store"
)

func TestServerMaintenanceAPI(t *testing.T) {
	env := newTestEnv(t)
	client := newClient(t)
	base := env.http.URL

	if status, resp := call(t, client, http.MethodPost, base+"/api/login",
		map[string]string{"username": "admin", "password": "Admin@12345"}, ""); status != http.StatusOK || !resp.OK {
		t.Fatalf("login: status=%d %s", status, resp.Error)
	}

	srv := &store.Server{
		Name: "maint-api", Host: "10.9.9.9", Port: 22,
		Username: "root", PasswordEnc: "cipher", Group: "维保",
	}
	if err := env.st.CreateServer(srv); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	path := base + "/api/servers/" + strconv.FormatInt(srv.ID, 10)

	until := time.Now().UTC().Add(3 * time.Hour).Truncate(time.Second)
	status, resp := call(t, client, http.MethodPost, path+"/maintenance", map[string]any{
		"enabled": true,
		"until":   until.Format(time.RFC3339),
		"note":    "硬件更换",
	}, base)
	if status != http.StatusOK || !resp.OK {
		t.Fatalf("mark maintenance: status=%d %s", status, resp.Error)
	}
	var updated struct {
		ID               int64  `json:"id"`
		Maintenance      bool   `json:"maintenance"`
		MaintenanceUntil string `json:"maintenanceUntil"`
		MaintenanceNote  string `json:"maintenanceNote"`
	}
	if err := json.Unmarshal(resp.Data, &updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !updated.Maintenance || updated.MaintenanceUntil == "" || updated.MaintenanceNote != "硬件更换" {
		t.Fatalf("unexpected server: %#v", updated)
	}

	// 列表与详情都应返回维护状态。
	_, resp = call(t, client, http.MethodGet, base+"/api/servers", nil, "")
	var views []struct {
		ID          int64 `json:"id"`
		Maintenance bool  `json:"maintenance"`
	}
	if err := json.Unmarshal(resp.Data, &views); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, v := range views {
		if v.ID == srv.ID {
			found = v.Maintenance
		}
	}
	if !found {
		t.Fatalf("maintenance flag missing from list: %#v", views)
	}

	status, resp = call(t, client, http.MethodGet, path, nil, "")
	if status != http.StatusOK || !resp.OK {
		t.Fatalf("detail: status=%d %s", status, resp.Error)
	}
	var detail struct {
		Maintenance      bool   `json:"maintenance"`
		MaintenanceUntil string `json:"maintenanceUntil"`
	}
	if err := json.Unmarshal(resp.Data, &detail); err != nil || !detail.Maintenance || detail.MaintenanceUntil == "" {
		t.Fatalf("detail: %v %#v", err, detail)
	}

	// 仪表盘应单独统计维护中数量。
	_, resp = call(t, client, http.MethodGet, base+"/api/dashboard", nil, "")
	var dash struct {
		MaintenanceCount int `json:"maintenanceCount"`
		IssueCount       int `json:"issueCount"`
	}
	if err := json.Unmarshal(resp.Data, &dash); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}
	if dash.MaintenanceCount != 1 || dash.IssueCount != 0 {
		t.Fatalf("dashboard = %#v", dash)
	}

	// 过去的结束时间应被拒绝。
	if status, _ := call(t, client, http.MethodPost, path+"/maintenance", map[string]any{
		"enabled": true,
		"until":   time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
	}, base); status != http.StatusBadRequest {
		t.Fatalf("past until status = %d, want 400", status)
	}

	// 结束维护。
	status, resp = call(t, client, http.MethodPost, path+"/maintenance", map[string]any{"enabled": false}, base)
	if status != http.StatusOK || !resp.OK {
		t.Fatalf("clear maintenance: status=%d %s", status, resp.Error)
	}
	if err := json.Unmarshal(resp.Data, &updated); err != nil || updated.Maintenance {
		t.Fatalf("clear decode: %v %#v", err, updated)
	}

	if status, _ := call(t, client, http.MethodPost, base+"/api/servers/999999/maintenance",
		map[string]any{"enabled": true}, base); status != http.StatusNotFound {
		t.Fatalf("missing server status = %d, want 404", status)
	}
}

func TestBatchSummarySkippedTotal(t *testing.T) {
	env := newTestEnv(t)
	client := newClient(t)
	base := env.http.URL

	if status, resp := call(t, client, http.MethodPost, base+"/api/login",
		map[string]string{"username": "admin", "password": "Admin@12345"}, ""); status != http.StatusOK || !resp.OK {
		t.Fatalf("login: status=%d %s", status, resp.Error)
	}

	now := time.Now().UTC()
	rec := &store.Inspection{
		ServerID: 1, ServerName: "skip-total", Host: "10.0.0.1",
		Status: "ok", Score: 100, MetricsJSON: "{}", ReportJSON: "{}",
		StartedAt: now, FinishedAt: now,
	}
	if err := env.st.CreateInspection(rec); err != nil {
		t.Fatalf("CreateInspection: %v", err)
	}

	status, resp := call(t, client, http.MethodPost, base+"/api/batches", map[string]any{
		"inspectionIds":      []int64{rec.ID},
		"skipped":            1,
		"maintenanceSkipped": 2,
		"elapsedMs":          1000,
	}, base)
	if status != http.StatusOK || !resp.OK {
		t.Fatalf("create batch: status=%d %s", status, resp.Error)
	}
	var got struct {
		Batch struct {
			Skipped int `json:"skipped"`
		} `json:"batch"`
		Summary struct {
			Skipped            int `json:"skipped"`
			MaintenanceSkipped int `json:"maintenanceSkipped"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(resp.Data, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Batch.Skipped != 3 || got.Summary.Skipped != 3 || got.Summary.MaintenanceSkipped != 2 {
		t.Fatalf("unexpected skipped stats: %#v", got)
	}
}
