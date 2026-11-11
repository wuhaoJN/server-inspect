package web_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"serverinspect/internal/store"
)

func TestBatchAndScheduleAPI(t *testing.T) {
	env := newTestEnv(t)
	client := newClient(t)
	base := env.http.URL

	if status, resp := call(t, client, http.MethodPost, base+"/api/login",
		map[string]string{"username": "admin", "password": "Admin@12345"}, ""); status != http.StatusOK || !resp.OK {
		t.Fatalf("login: status=%d %s", status, resp.Error)
	}

	now := time.Now().UTC()
	rec := &store.Inspection{
		ServerID: 1, ServerName: "api-test", Host: "10.0.0.1",
		Status: "ok", Score: 100, MetricsJSON: "{}", ReportJSON: "{}",
		StartedAt: now, FinishedAt: now,
	}
	if err := env.st.CreateInspection(rec); err != nil {
		t.Fatalf("CreateInspection: %v", err)
	}

	status, resp := call(t, client, http.MethodPost, base+"/api/batches", map[string]any{
		"inspectionIds": []int64{rec.ID},
		"skipped":       1,
		"elapsedMs":     1500,
	}, base)
	if status != http.StatusOK || !resp.OK {
		t.Fatalf("create batch: status=%d %s", status, resp.Error)
	}
	var batchResp struct {
		Batch struct {
			ID          int64 `json:"id"`
			ServerCount int   `json:"serverCount"`
		} `json:"batch"`
		Summary struct {
			Counts struct {
				OK int `json:"ok"`
			} `json:"counts"`
			Skipped int `json:"skipped"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(resp.Data, &batchResp); err != nil {
		t.Fatalf("decode batch: %v", err)
	}
	if batchResp.Batch.ID == 0 || batchResp.Batch.ServerCount != 1 || batchResp.Summary.Counts.OK != 1 {
		t.Fatalf("unexpected batch: %#v", batchResp)
	}

	_, resp = call(t, client, http.MethodGet, base+"/api/batches", nil, "")
	var list struct {
		Total int `json:"total"`
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(resp.Data, &list); err != nil || list.Total != 1 || len(list.Items) != 1 {
		t.Fatalf("list batches: %v %#v", err, list)
	}
	if status, resp := call(t, client, http.MethodGet, base+"/api/batches/"+strconv.FormatInt(batchResp.Batch.ID, 10), nil, ""); status != http.StatusOK || !resp.OK {
		t.Fatalf("batch detail: status=%d %s", status, resp.Error)
	}

	status, resp = call(t, client, http.MethodPost, base+"/api/schedules", map[string]any{
		"name": "每日巡检", "enabled": true, "scopeType": "all", "scopeValue": "",
		"freqType": "daily", "intervalMinutes": 60, "atHour": 2, "atMinute": 0,
		"weekday": 0, "serverIds": []int64{},
	}, base)
	if status != http.StatusOK || !resp.OK {
		t.Fatalf("create schedule: status=%d %s", status, resp.Error)
	}
	var created struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		NextRunAt   string `json:"nextRunAt"`
		TargetCount int    `json:"targetCount"`
	}
	if err := json.Unmarshal(resp.Data, &created); err != nil || created.ID == 0 || created.Name != "每日巡检" || created.NextRunAt == "" {
		t.Fatalf("decode schedule: %v %#v", err, created)
	}

	_, resp = call(t, client, http.MethodGet, base+"/api/schedules", nil, "")
	var schedules []struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(resp.Data, &schedules); err != nil || len(schedules) != 1 || schedules[0].ID != created.ID {
		t.Fatalf("list schedules: %v %#v", err, schedules)
	}

	status, resp = call(t, client, http.MethodPut, base+"/api/schedules/"+strconv.FormatInt(created.ID, 10), map[string]any{
		"name": "每日巡检-停用", "enabled": false, "scopeType": "all", "scopeValue": "",
		"freqType": "daily", "intervalMinutes": 60, "atHour": 3, "atMinute": 30,
		"weekday": 0, "serverIds": []int64{},
	}, base)
	if status != http.StatusOK || !resp.OK {
		t.Fatalf("update schedule: status=%d %s", status, resp.Error)
	}
	if status, resp := call(t, client, http.MethodDelete, base+"/api/schedules/"+strconv.FormatInt(created.ID, 10), nil, base); status != http.StatusOK || !resp.OK {
		t.Fatalf("delete schedule: status=%d %s", status, resp.Error)
	}
	if status, _ := call(t, client, http.MethodGet, base+"/api/schedules", nil, ""); status != http.StatusOK {
		t.Fatalf("list after delete: %d", status)
	}
}
