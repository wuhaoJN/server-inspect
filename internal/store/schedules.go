package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Schedule 是定时巡检计划。
type Schedule struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	Enabled         bool       `json:"enabled"`
	ScopeType       string     `json:"scopeType"`
	ScopeValue      string     `json:"scopeValue"`
	FreqType        string     `json:"freqType"`
	IntervalMinutes int        `json:"intervalMinutes"`
	AtHour          int        `json:"atHour"`
	AtMinute        int        `json:"atMinute"`
	Weekday         int        `json:"weekday"`
	LastRunAt       *time.Time `json:"lastRunAt"`
	NextRunAt       *time.Time `json:"nextRunAt"`
	LastBatchID     int64      `json:"lastBatchId"`
	LastStatus      string     `json:"lastStatus"`
	LastError       string     `json:"lastError"`
	ServerIDs       []int64    `json:"serverIds,omitempty"`
	TargetCount     int        `json:"targetCount"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

func scanSchedule(scanner interface{ Scan(...any) error }) (*Schedule, error) {
	var (
		sch              Schedule
		enabled          int
		lastRun, nextRun string
		created, updated string
	)
	err := scanner.Scan(
		&sch.ID, &sch.Name, &enabled, &sch.ScopeType, &sch.ScopeValue, &sch.FreqType,
		&sch.IntervalMinutes, &sch.AtHour, &sch.AtMinute, &sch.Weekday,
		&lastRun, &nextRun, &sch.LastBatchID, &sch.LastStatus, &sch.LastError,
		&created, &updated,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	sch.Enabled = enabled == 1
	if lastRun != "" {
		t := parseTime(lastRun)
		sch.LastRunAt = &t
	}
	if nextRun != "" {
		t := parseTime(nextRun)
		sch.NextRunAt = &t
	}
	sch.CreatedAt = parseTime(created)
	sch.UpdatedAt = parseTime(updated)
	return &sch, nil
}

const scheduleColumns = `id, name, enabled, scope_type, scope_value, freq_type,
	interval_minutes, at_hour, at_minute, weekday, last_run_at, next_run_at,
	last_batch_id, last_status, last_error, created_at, updated_at`

// CreateSchedule 新增定时巡检计划。
func (s *Store) CreateSchedule(sch *Schedule) error {
	now := time.Now().UTC()
	res, err := s.db.Exec(
		`INSERT INTO schedules
		 (name, enabled, scope_type, scope_value, freq_type, interval_minutes, at_hour, at_minute,
		  weekday, next_run_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sch.Name, boolInt(sch.Enabled), sch.ScopeType, sch.ScopeValue, sch.FreqType,
		sch.IntervalMinutes, sch.AtHour, sch.AtMinute, sch.Weekday,
		formatOptionalTime(sch.NextRunAt), now.Format(timeLayout), now.Format(timeLayout),
	)
	if err != nil {
		return fmt.Errorf("保存定时计划失败: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	sch.ID = id
	sch.CreatedAt = now
	sch.UpdatedAt = now
	return nil
}

// UpdateSchedule 更新计划配置与下次执行时间。
func (s *Store) UpdateSchedule(sch *Schedule) error {
	now := time.Now().UTC()
	res, err := s.db.Exec(
		`UPDATE schedules SET name = ?, enabled = ?, scope_type = ?, scope_value = ?,
		 freq_type = ?, interval_minutes = ?, at_hour = ?, at_minute = ?, weekday = ?,
		 next_run_at = ?, updated_at = ? WHERE id = ?`,
		sch.Name, boolInt(sch.Enabled), sch.ScopeType, sch.ScopeValue, sch.FreqType,
		sch.IntervalMinutes, sch.AtHour, sch.AtMinute, sch.Weekday,
		formatOptionalTime(sch.NextRunAt), now.Format(timeLayout), sch.ID,
	)
	if err != nil {
		return fmt.Errorf("更新定时计划失败: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	sch.UpdatedAt = now
	return nil
}

// GetSchedule 查询定时计划。
func (s *Store) GetSchedule(id int64) (*Schedule, error) {
	row := s.db.QueryRow(`SELECT `+scheduleColumns+` FROM schedules WHERE id = ?`, id)
	return scanSchedule(row)
}

// ListSchedules 查询全部定时计划。
func (s *Store) ListSchedules() ([]Schedule, error) {
	rows, err := s.db.Query(`SELECT ` + scheduleColumns + ` FROM schedules ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Schedule, 0, 8)
	for rows.Next() {
		sch, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sch)
	}
	return out, rows.Err()
}

// DeleteSchedule 删除定时计划及其服务器关联。
func (s *Store) DeleteSchedule(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM schedule_servers WHERE schedule_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM schedules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// SetScheduleServers 设置自选服务器范围。
func (s *Store) SetScheduleServers(scheduleID int64, serverIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM schedule_servers WHERE schedule_id = ?`, scheduleID); err != nil {
		return err
	}
	seen := map[int64]bool{}
	for _, id := range serverIDs {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		if _, err := tx.Exec(`INSERT INTO schedule_servers (schedule_id, server_id) VALUES (?, ?)`, scheduleID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetScheduleServerIDs 查询自选服务器 ID。
func (s *Store) GetScheduleServerIDs(scheduleID int64) ([]int64, error) {
	rows, err := s.db.Query(`SELECT server_id FROM schedule_servers WHERE schedule_id = ? ORDER BY server_id`, scheduleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ListDueSchedules 查询到期待执行的计划。
func (s *Store) ListDueSchedules(now time.Time) ([]Schedule, error) {
	rows, err := s.db.Query(
		`SELECT `+scheduleColumns+` FROM schedules
		 WHERE enabled = 1 AND next_run_at <> '' AND next_run_at <= ?
		 ORDER BY next_run_at ASC`, queryTime(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Schedule, 0, 4)
	for rows.Next() {
		sch, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sch)
	}
	return out, rows.Err()
}

// UpdateScheduleRun 更新计划执行状态。
func (s *Store) UpdateScheduleRun(id int64, lastRun, nextRun time.Time, batchID int64, status, errText string) error {
	_, err := s.db.Exec(
		`UPDATE schedules SET last_run_at = ?, next_run_at = ?, last_batch_id = ?,
		 last_status = ?, last_error = ?, updated_at = ? WHERE id = ?`,
		lastRun.UTC().Format(timeLayout), nextRun.UTC().Format(timeLayout), batchID,
		status, errText, time.Now().UTC().Format(timeLayout), id,
	)
	return err
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func formatOptionalTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeLayout)
}
