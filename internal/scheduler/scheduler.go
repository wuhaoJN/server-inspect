// Package scheduler 负责定时巡检计划的到点执行。
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"serverinspect/internal/runner"
	"serverinspect/internal/store"
)

// ErrAlreadyRunning 表示同一计划正在执行。
var ErrAlreadyRunning = errors.New("该计划正在执行中")

// ErrNoTargets 表示计划范围内没有可巡检的服务器。
var ErrNoTargets = errors.New("计划范围内没有可巡检的服务器（未配置登录或在维护中）")

// Scheduler 轮询到期计划并通过 runner 执行批量巡检。
type Scheduler struct {
	store  *store.Store
	runner *runner.Runner
	log    *slog.Logger
	tick   time.Duration

	mu      sync.Mutex
	running map[int64]bool
}

// New 创建调度器。tick 默认 30 秒。
func New(st *store.Store, run *runner.Runner, log *slog.Logger, tick time.Duration) *Scheduler {
	if tick <= 0 {
		tick = 30 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{store: st, runner: run, log: log, tick: tick, running: map[int64]bool{}}
}

// Start 启动后台调度循环。
func (s *Scheduler) Start(ctx context.Context) {
	go func() {
		s.runDue(ctx)
		ticker := time.NewTicker(s.tick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runDue(ctx)
			}
		}
	}()
}

func (s *Scheduler) runDue(ctx context.Context) {
	due, err := s.store.ListDueSchedules(time.Now())
	if err != nil {
		s.log.Error("查询到期定时计划失败", "error", err)
		return
	}
	for i := range due {
		if ctx.Err() != nil {
			return
		}
		sch := &due[i]
		if _, err := s.execute(ctx, sch, false); err != nil {
			if !errors.Is(err, ErrAlreadyRunning) {
				s.log.Error("执行定时计划失败", "schedule", sch.Name, "error", err)
			}
		}
	}
}

// RunNow 立即执行计划（手动触发）。
func (s *Scheduler) RunNow(ctx context.Context, scheduleID int64) (*store.Batch, error) {
	sch, err := s.store.GetSchedule(scheduleID)
	if err != nil {
		return nil, err
	}
	return s.execute(ctx, sch, true)
}

func (s *Scheduler) execute(ctx context.Context, sch *store.Schedule, manual bool) (*store.Batch, error) {
	s.mu.Lock()
	if s.running[sch.ID] {
		s.mu.Unlock()
		return nil, ErrAlreadyRunning
	}
	s.running[sch.ID] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, sch.ID)
		s.mu.Unlock()
	}()

	now := time.Now()
	next := nextRunForExecution(sch, now, manual)
	// 更新执行状态与下次执行时间；定时执行会推进，手动执行保留仍在未来的原计划。
	if err := s.store.UpdateScheduleRun(sch.ID, now, next, 0, "running", ""); err != nil {
		return nil, err
	}

	stats, err := s.Resolve(sch)
	if err != nil {
		_ = s.store.UpdateScheduleRun(sch.ID, now, next, 0, "error", err.Error())
		return nil, err
	}
	if len(stats.Servers) == 0 {
		_ = s.store.UpdateScheduleRun(sch.ID, now, next, 0, "skipped", ErrNoTargets.Error())
		return nil, ErrNoTargets
	}

	batchRec, err := s.runner.RunBatch(ctx, stats.Servers, runner.BatchOptions{
		Trigger:            "scheduled",
		ScheduleID:         sch.ID,
		Name:               sch.Name,
		Skipped:            stats.SkippedUnconfigured + stats.SkippedMaintenance,
		MaintenanceSkipped: stats.SkippedMaintenance,
	})
	if err != nil {
		_ = s.store.UpdateScheduleRun(sch.ID, now, next, 0, "error", err.Error())
		return nil, err
	}
	status := batchStatus(batchRec)
	_ = s.store.UpdateScheduleRun(sch.ID, now, next, batchRec.ID, status, "")
	s.log.Info("定时计划执行完成", "schedule", sch.Name, "batch", batchRec.ID, "servers", len(stats.Servers),
		"skippedMaintenance", stats.SkippedMaintenance, "skippedUnconfigured", stats.SkippedUnconfigured,
		"status", status, "manual", manual)
	return batchRec, nil
}

// TargetStats 是计划范围解析结果，包含可执行目标与跳过数量。
type TargetStats struct {
	Servers             []*store.Server
	SkippedUnconfigured int
	SkippedMaintenance  int
}

// Resolve 解析计划范围：排除未配置登录与维护中的服务器，并统计跳过数量。
func (s *Scheduler) Resolve(sch *store.Schedule) (TargetStats, error) {
	all, err := s.store.ListServers()
	if err != nil {
		return TargetStats{}, err
	}
	now := time.Now()

	var wanted map[int64]bool
	if sch.ScopeType == "custom" {
		ids, err := s.store.GetScheduleServerIDs(sch.ID)
		if err != nil {
			return TargetStats{}, err
		}
		wanted = make(map[int64]bool, len(ids))
		for _, id := range ids {
			wanted[id] = true
		}
	}

	stats := TargetStats{Servers: make([]*store.Server, 0, len(all))}
	for i := range all {
		srv := &all[i]
		switch sch.ScopeType {
		case "group":
			if srv.Group != sch.ScopeValue {
				continue
			}
		case "custom":
			if !wanted[srv.ID] {
				continue
			}
		}
		if srv.Username == "" || srv.PasswordEnc == "" {
			stats.SkippedUnconfigured++
			continue
		}
		if srv.InMaintenance(now) {
			stats.SkippedMaintenance++
			continue
		}
		stats.Servers = append(stats.Servers, srv)
	}
	return stats, nil
}

// Targets 解析计划范围，只返回当前可巡检的服务器。
func (s *Scheduler) Targets(sch *store.Schedule) ([]*store.Server, error) {
	stats, err := s.Resolve(sch)
	if err != nil {
		return nil, err
	}
	return stats.Servers, nil
}

// nextRunForExecution 计算一次执行后的下次执行时间。
// 手动“立即执行”时，如果原计划时间仍在未来，则保留原计划，避免把当天的定时任务跳过。
func nextRunForExecution(sch *store.Schedule, now time.Time, manual bool) time.Time {
	if manual && sch.NextRunAt != nil && sch.NextRunAt.After(now) {
		return *sch.NextRunAt
	}
	return NextRun(sch, now)
}

// NextRun 根据频率计算下一次执行时间（使用服务器本地时区）。
func NextRun(sch *store.Schedule, from time.Time) time.Time {
	loc := from.Location()
	switch sch.FreqType {
	case "interval":
		minutes := sch.IntervalMinutes
		if minutes <= 0 {
			minutes = 60
		}
		return from.Add(time.Duration(minutes) * time.Minute)
	case "weekly":
		targetDay := time.Weekday(sch.Weekday)
		for i := 0; i <= 7; i++ {
			day := from.AddDate(0, 0, i)
			candidate := time.Date(day.Year(), day.Month(), day.Day(), sch.AtHour, sch.AtMinute, 0, 0, loc)
			if candidate.Weekday() == targetDay && candidate.After(from) {
				return candidate
			}
		}
		return time.Date(from.Year(), from.Month(), from.Day(), sch.AtHour, sch.AtMinute, 0, 0, loc).AddDate(0, 0, 7)
	default: // daily
		candidate := time.Date(from.Year(), from.Month(), from.Day(), sch.AtHour, sch.AtMinute, 0, 0, loc)
		if !candidate.After(from) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		return candidate
	}
}

func batchStatus(b *store.Batch) string {
	switch {
	case b.ErrorCount > 0:
		return "error"
	case b.CriticalCount > 0:
		return "critical"
	case b.WarningCount > 0:
		return "warning"
	default:
		return "ok"
	}
}

func (s *Scheduler) String() string {
	return fmt.Sprintf("scheduler(tick=%s)", s.tick)
}
