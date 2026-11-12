package scheduler

import (
	"path/filepath"
	"testing"
	"time"

	"serverinspect/internal/store"
)

func TestNextRun(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	base := time.Date(2026, 11, 11, 10, 30, 0, 0, loc)

	interval := &store.Schedule{FreqType: "interval", IntervalMinutes: 90}
	if got := NextRun(interval, base); !got.Equal(base.Add(90 * time.Minute)) {
		t.Fatalf("interval next = %v", got)
	}

	daily := &store.Schedule{FreqType: "daily", AtHour: 2, AtMinute: 0}
	if got := NextRun(daily, base); !got.Equal(time.Date(2026, 11, 12, 2, 0, 0, 0, loc)) {
		t.Fatalf("daily next = %v", got)
	}
	beforeDaily := time.Date(2026, 11, 11, 1, 0, 0, 0, loc)
	if got := NextRun(daily, beforeDaily); !got.Equal(time.Date(2026, 11, 11, 2, 0, 0, 0, loc)) {
		t.Fatalf("daily same-day next = %v", got)
	}

	weekly := &store.Schedule{FreqType: "weekly", Weekday: int(time.Friday), AtHour: 3, AtMinute: 15}
	// 2026-11-11 是周三；下一次周五 03:15
	if got := NextRun(weekly, base); !got.Equal(time.Date(2026, 11, 13, 3, 15, 0, 0, loc)) {
		t.Fatalf("weekly next = %v", got)
	}
}

func TestResolveTargetsSkipsMaintenanceAndUnconfigured(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	until := now.Add(time.Hour)
	servers := []*store.Server{
		{Name: "normal", Host: "10.0.0.1", Port: 22, Username: "root", PasswordEnc: "x", Group: "prod"},
		{Name: "maint", Host: "10.0.0.2", Port: 22, Username: "root", PasswordEnc: "x", Group: "prod"},
		{Name: "pending", Host: "10.0.0.3", Port: 22, Group: "prod"},
		{Name: "dev", Host: "10.0.0.4", Port: 22, Username: "root", PasswordEnc: "x", Group: "dev"},
	}
	for _, sv := range servers {
		if err := st.CreateServer(sv); err != nil {
			t.Fatalf("CreateServer: %v", err)
		}
	}
	if err := st.SetServerMaintenance(servers[1].ID, &until, "维保"); err != nil {
		t.Fatalf("SetServerMaintenance: %v", err)
	}

	s := New(st, nil, nil, time.Hour)

	stats, err := s.Resolve(&store.Schedule{ScopeType: "all"})
	if err != nil {
		t.Fatalf("Resolve all: %v", err)
	}
	if len(stats.Servers) != 2 || stats.SkippedMaintenance != 1 || stats.SkippedUnconfigured != 1 {
		t.Fatalf("all stats = %#v", stats)
	}

	stats, err = s.Resolve(&store.Schedule{ScopeType: "group", ScopeValue: "prod"})
	if err != nil {
		t.Fatalf("Resolve group: %v", err)
	}
	if len(stats.Servers) != 1 || stats.Servers[0].Name != "normal" || stats.SkippedMaintenance != 1 || stats.SkippedUnconfigured != 1 {
		t.Fatalf("group stats = %#v", stats)
	}

	sch := &store.Schedule{Name: "自选", ScopeType: "custom", FreqType: "daily", Enabled: true}
	if err := st.CreateSchedule(sch); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	if err := st.SetScheduleServers(sch.ID, []int64{servers[0].ID, servers[1].ID}); err != nil {
		t.Fatalf("SetScheduleServers: %v", err)
	}
	stats, err = s.Resolve(sch)
	if err != nil {
		t.Fatalf("Resolve custom: %v", err)
	}
	if len(stats.Servers) != 1 || stats.Servers[0].Name != "normal" || stats.SkippedMaintenance != 1 {
		t.Fatalf("custom stats = %#v", stats)
	}
}
