package store

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStoreServerAndInspection(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	srv := &Server{
		Name: "测试服务器", Host: "10.0.0.1", Port: 22, Username: "root",
		PasswordEnc: "cipher", Group: "测试", NodeRole: "Web", Tags: "a,b", Note: "备注",
	}
	if err := st.CreateServer(srv); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	if srv.ID == 0 {
		t.Fatal("expected server id")
	}

	got, err := st.GetServer(srv.ID)
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if got.Name != srv.Name || got.PasswordEnc != "cipher" {
		t.Fatalf("unexpected server: %#v", got)
	}
	if got.NodeRole != "Web" {
		t.Fatalf("node role = %q", got.NodeRole)
	}

	if err := st.SetHostKey(srv.ID, "SHA256:abc"); err != nil {
		t.Fatalf("SetHostKey: %v", err)
	}

	srv.Name = "改名"
	if err := st.UpdateServer(srv); err != nil {
		t.Fatalf("UpdateServer: %v", err)
	}

	started := time.Now().UTC().Add(-2 * time.Second)
	rec := &Inspection{
		ServerID: srv.ID, ServerName: srv.Name, Host: srv.Host, Status: "warning", Score: 88,
		MetricsJSON: `{"load1":1.2}`, ReportJSON: `{"score":88}`,
		StartedAt: started, FinishedAt: time.Now().UTC(), DurationMS: 2000,
	}
	if err := st.CreateInspection(rec); err != nil {
		t.Fatalf("CreateInspection: %v", err)
	}

	views, err := st.ListServerViews()
	if err != nil {
		t.Fatalf("ListServerViews: %v", err)
	}
	if len(views) != 1 || views[0].LastInspectionID != rec.ID || views[0].LastScore != 88 {
		t.Fatalf("unexpected views: %#v", views)
	}
	if !views[0].Configured {
		t.Fatalf("server with username/password should be configured: %#v", views[0])
	}

	full, err := st.GetInspection(rec.ID)
	if err != nil {
		t.Fatalf("GetInspection: %v", err)
	}
	if full.ReportJSON != `{"score":88}` || full.Status != "warning" {
		t.Fatalf("unexpected inspection: %#v", full)
	}

	items, err := st.ListInspections(InspectionFilter{ServerID: srv.ID, Limit: 10})
	if err != nil || len(items) != 1 {
		t.Fatalf("ListInspections: %v %#v", err, items)
	}
	total, err := st.CountInspections(InspectionFilter{ServerID: srv.ID})
	if err != nil || total != 1 {
		t.Fatalf("CountInspections: %v %d", err, total)
	}
	if n, err := st.CountInspectionsSince(started.Add(-time.Minute)); err != nil || n != 1 {
		t.Fatalf("CountInspectionsSince: %v %d", err, n)
	}
	if avg, err := st.AvgScoreSince(started.Add(-time.Minute)); err != nil || avg != 88 {
		t.Fatalf("AvgScoreSince: %v %v", err, avg)
	}
	counts, err := st.StatusCountsSince(started.Add(-time.Minute))
	if err != nil || counts["warning"] != 1 {
		t.Fatalf("StatusCountsSince: %v %#v", err, counts)
	}
	recent, err := st.RecentInspections(5)
	if err != nil || len(recent) != 1 {
		t.Fatalf("RecentInspections: %v %#v", err, recent)
	}

	if err := st.DeleteInspection(rec.ID); err != nil {
		t.Fatalf("DeleteInspection: %v", err)
	}
	if err := st.DeleteServer(srv.ID); err != nil {
		t.Fatalf("DeleteServer: %v", err)
	}
	if _, err := st.GetServer(srv.ID); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListServerViewsSortsByIP(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	servers := []*Server{
		{Name: "web-150-10-a", Host: "192.168.150.10", Port: 22, Username: "root", PasswordEnc: "x"},
		{Name: "web-150-2", Host: "192.168.150.2", Port: 22, Username: "root", PasswordEnc: "x"},
		{Name: "core-10", Host: "10.0.0.5", Port: 22, Username: "root", PasswordEnc: "x"},
		{Name: "hostname-node", Host: "db.internal", Port: 22, Username: "root", PasswordEnc: "x"},
		{Name: "web-150-10-b", Host: "192.168.150.10", Port: 2222, Username: "root", PasswordEnc: "x"},
		{Name: "v6", Host: "2001:db8::1", Port: 22, Username: "root", PasswordEnc: "x"},
	}
	if err := st.CreateServers(servers); err != nil {
		t.Fatalf("CreateServers: %v", err)
	}

	views, err := st.ListServerViews()
	if err != nil {
		t.Fatalf("ListServerViews: %v", err)
	}
	got := make([]string, 0, len(views))
	for _, v := range views {
		item := v.Host
		if v.Port != 22 {
			item += ":" + strconv.Itoa(v.Port)
		}
		got = append(got, item)
	}
	want := []string{
		"10.0.0.5",
		"192.168.150.2",
		"192.168.150.10",
		"192.168.150.10:2222",
		"2001:db8::1",
		"db.internal",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("sort order = %#v, want %#v", got, want)
	}
}

func TestStoreBulkCreatePendingServers(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	servers := []*Server{
		{Name: "10.0.0.1", Host: "10.0.0.1", Port: 22, Group: "扫描发现"},
		{Name: "10.0.0.2", Host: "10.0.0.2", Port: 2222, Group: "扫描发现"},
	}
	if err := st.CreateServers(servers); err != nil {
		t.Fatalf("CreateServers: %v", err)
	}
	for _, sv := range servers {
		if sv.ID == 0 {
			t.Fatal("bulk create should set ids")
		}
	}

	views, err := st.ListServerViews()
	if err != nil || len(views) != 2 {
		t.Fatalf("ListServerViews: %v %#v", err, views)
	}
	for _, v := range views {
		if v.Configured {
			t.Fatalf("bulk-created server should be pending: %#v", v)
		}
	}

	servers[0].Username = "root"
	servers[0].PasswordEnc = "cipher"
	if err := st.UpdateServer(servers[0]); err != nil {
		t.Fatalf("UpdateServer: %v", err)
	}
	views, err = st.ListServerViews()
	if err != nil {
		t.Fatalf("ListServerViews: %v", err)
	}
	configured := 0
	for _, v := range views {
		if v.Configured {
			configured++
		}
	}
	if configured != 1 {
		t.Fatalf("configured count = %d, views = %#v", configured, views)
	}
}

func TestCountInspectionsSinceSameSecond(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	base := time.Now().UTC().Truncate(time.Second)
	records := []*Inspection{
		{ServerID: 1, ServerName: "after", Host: "10.0.0.1", Status: "ok", Score: 90,
			StartedAt: base.Add(500 * time.Millisecond), FinishedAt: base.Add(time.Second)},
		{ServerID: 2, ServerName: "before", Host: "10.0.0.2", Status: "ok", Score: 80,
			StartedAt: base.Add(-500 * time.Millisecond), FinishedAt: base},
	}
	for _, rec := range records {
		if err := st.CreateInspection(rec); err != nil {
			t.Fatalf("CreateInspection: %v", err)
		}
	}

	n, err := st.CountInspectionsSince(base)
	if err != nil {
		t.Fatalf("CountInspectionsSince: %v", err)
	}
	if n != 1 {
		t.Fatalf("count = %d, want 1 (same-second boundary)", n)
	}
}

func TestOpenMigratesNodeRole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	oldSchema := `CREATE TABLE servers (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		name        TEXT    NOT NULL,
		host        TEXT    NOT NULL,
		port        INTEGER NOT NULL DEFAULT 22,
		username    TEXT    NOT NULL,
		password    TEXT    NOT NULL DEFAULT '',
		host_key    TEXT    NOT NULL DEFAULT '',
		group_name  TEXT    NOT NULL DEFAULT '',
		tags        TEXT    NOT NULL DEFAULT '',
		note        TEXT    NOT NULL DEFAULT '',
		created_at  TEXT    NOT NULL,
		updated_at  TEXT    NOT NULL
	)`
	if _, err := db.Exec(oldSchema); err != nil {
		t.Fatalf("create old schema: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO servers (name,host,port,username,password,created_at,updated_at)
		VALUES ('legacy','10.1.1.1',22,'root','cipher',?,?)`, now, now); err != nil {
		t.Fatalf("insert legacy: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open migrated db: %v", err)
	}
	defer st.Close()

	sv, err := st.GetServer(1)
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if sv.Name != "legacy" || sv.NodeRole != "" {
		t.Fatalf("migration lost data or role not empty: %#v", sv)
	}
}

func TestStoreBatches(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	b := &Batch{
		Trigger: "manual", Name: "手动批量",
		StartedAt: now.Add(-10 * time.Second), FinishedAt: now,
		ServerCount: 2, OKCount: 1, WarningCount: 1, AvgScore: 80,
		Skipped: 1, ElapsedMS: 10000, SummaryJSON: `{"counts":{"ok":1,"warning":1}}`,
	}
	if err := st.CreateBatch(b, []int64{10, 11, 11}); err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}
	if b.ID == 0 {
		t.Fatal("batch id not set")
	}
	got, err := st.GetBatch(b.ID)
	if err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	if got.SummaryJSON != b.SummaryJSON || got.ServerCount != 2 || got.Skipped != 1 {
		t.Fatalf("unexpected batch: %#v", got)
	}
	list, err := st.ListBatches(10, 0)
	if err != nil || len(list) != 1 || list[0].ID != b.ID {
		t.Fatalf("ListBatches: %v %#v", err, list)
	}
	if n, err := st.CountBatches(); err != nil || n != 1 {
		t.Fatalf("CountBatches: %v %d", err, n)
	}
	ids, err := st.BatchInspectionIDs(b.ID)
	if err != nil || len(ids) != 2 || ids[0] != 10 || ids[1] != 11 {
		t.Fatalf("BatchInspectionIDs: %v %#v", err, ids)
	}
}

func TestStoreSchedules(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	next := now.Add(time.Hour)
	sch := &Schedule{
		Name: "每日巡检", Enabled: true, ScopeType: "custom", ScopeValue: "",
		FreqType: "daily", AtHour: 2, AtMinute: 0, NextRunAt: &next,
	}
	if err := st.CreateSchedule(sch); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	if sch.ID == 0 {
		t.Fatal("schedule id not set")
	}
	if err := st.SetScheduleServers(sch.ID, []int64{3, 1, 3}); err != nil {
		t.Fatalf("SetScheduleServers: %v", err)
	}
	ids, err := st.GetScheduleServerIDs(sch.ID)
	if err != nil || len(ids) != 2 || ids[0] != 1 || ids[1] != 3 {
		t.Fatalf("GetScheduleServerIDs: %v %#v", err, ids)
	}

	// 到期查询
	sch.NextRunAt = &past
	if err := st.UpdateSchedule(sch); err != nil {
		t.Fatalf("UpdateSchedule: %v", err)
	}
	due, err := st.ListDueSchedules(now)
	if err != nil || len(due) != 1 || due[0].ID != sch.ID {
		t.Fatalf("ListDueSchedules: %v %#v", err, due)
	}

	if err := st.UpdateScheduleRun(sch.ID, now, next, 99, "ok", ""); err != nil {
		t.Fatalf("UpdateScheduleRun: %v", err)
	}
	got, err := st.GetSchedule(sch.ID)
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if got.LastBatchID != 99 || got.LastStatus != "ok" || got.NextRunAt == nil || !got.NextRunAt.After(now) {
		t.Fatalf("unexpected schedule: %#v", got)
	}
	due, err = st.ListDueSchedules(now)
	if err != nil || len(due) != 0 {
		t.Fatalf("expected no due schedules: %v %#v", err, due)
	}

	if err := st.DeleteSchedule(sch.ID); err != nil {
		t.Fatalf("DeleteSchedule: %v", err)
	}
	if _, err := st.GetSchedule(sch.ID); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStoreAdmin(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	created, err := st.EnsureAdmin("admin", "secret-pass")
	if err != nil || !created {
		t.Fatalf("EnsureAdmin: %v %v", err, created)
	}
	created, err = st.EnsureAdmin("admin", "another")
	if err != nil || created {
		t.Fatalf("second EnsureAdmin should not recreate: %v %v", err, created)
	}
	username, hash, err := st.AdminCredentials()
	if err != nil || username != "admin" || hash == "" {
		t.Fatalf("AdminCredentials: %v %q %q", err, username, hash)
	}
	if err := st.SetAdminPassword("admin", "new-pass"); err != nil {
		t.Fatalf("SetAdminPassword: %v", err)
	}
}
