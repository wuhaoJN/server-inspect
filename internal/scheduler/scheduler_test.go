package scheduler

import (
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
