package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Batch 是一次批量巡检的持久化记录。
type Batch struct {
	ID            int64     `json:"id"`
	Trigger       string    `json:"trigger"`
	ScheduleID    int64     `json:"scheduleId"`
	Name          string    `json:"name"`
	StartedAt     time.Time `json:"startedAt"`
	FinishedAt    time.Time `json:"finishedAt"`
	ServerCount   int       `json:"serverCount"`
	OKCount       int       `json:"okCount"`
	WarningCount  int       `json:"warningCount"`
	CriticalCount int       `json:"criticalCount"`
	ErrorCount    int       `json:"errorCount"`
	IssueCount    int       `json:"issueCount"`
	AvgScore      float64   `json:"avgScore"`
	Skipped       int       `json:"skipped"`
	ElapsedMS     int64     `json:"elapsedMs"`
	SummaryJSON   string    `json:"-"`
	CreatedAt     time.Time `json:"createdAt"`
}

// CreateBatch 写入批量巡检记录及关联的巡检记录。
func (s *Store) CreateBatch(b *Batch, inspectionIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开启批量记录事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	if b.SummaryJSON == "" {
		b.SummaryJSON = "{}"
	}
	res, err := tx.Exec(
		`INSERT INTO batches
		 (trigger_type, schedule_id, name, started_at, finished_at, server_count, ok_count,
		  warning_count, critical_count, error_count, issue_count, avg_score, skipped_count, elapsed_ms, summary_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.Trigger, b.ScheduleID, b.Name,
		b.StartedAt.UTC().Format(timeLayout), b.FinishedAt.UTC().Format(timeLayout),
		b.ServerCount, b.OKCount, b.WarningCount, b.CriticalCount, b.ErrorCount, b.IssueCount,
		b.AvgScore, b.Skipped, b.ElapsedMS, b.SummaryJSON, now.Format(timeLayout),
	)
	if err != nil {
		return fmt.Errorf("保存批量巡检记录失败: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	b.ID = id
	b.CreatedAt = now

	seen := map[int64]bool{}
	for _, inspectionID := range inspectionIDs {
		if inspectionID <= 0 || seen[inspectionID] {
			continue
		}
		seen[inspectionID] = true
		if _, err := tx.Exec(`INSERT OR IGNORE INTO batch_inspections (batch_id, inspection_id) VALUES (?, ?)`,
			id, inspectionID); err != nil {
			return fmt.Errorf("保存批量巡检关联失败: %w", err)
		}
	}
	return tx.Commit()
}

// ListBatches 分页查询批量巡检记录（不包含汇总 JSON）。
func (s *Store) ListBatches(limit, offset int) ([]Batch, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(
		`SELECT id, trigger_type, schedule_id, name, started_at, finished_at, server_count,
		        ok_count, warning_count, critical_count, error_count, issue_count, avg_score,
		        skipped_count, elapsed_ms, created_at
		 FROM batches ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Batch, 0, limit)
	for rows.Next() {
		var (
			b                 Batch
			started, finished string
			created           string
		)
		if err := rows.Scan(&b.ID, &b.Trigger, &b.ScheduleID, &b.Name, &started, &finished,
			&b.ServerCount, &b.OKCount, &b.WarningCount, &b.CriticalCount, &b.ErrorCount,
			&b.IssueCount, &b.AvgScore, &b.Skipped, &b.ElapsedMS, &created); err != nil {
			return nil, err
		}
		b.StartedAt = parseTime(started)
		b.FinishedAt = parseTime(finished)
		b.CreatedAt = parseTime(created)
		out = append(out, b)
	}
	return out, rows.Err()
}

// CountBatches 统计批量巡检记录数量。
func (s *Store) CountBatches() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM batches`).Scan(&n)
	return n, err
}

// GetBatch 查询批量巡检记录（包含汇总 JSON）。
func (s *Store) GetBatch(id int64) (*Batch, error) {
	var (
		b                 Batch
		started, finished string
		created           string
	)
	err := s.db.QueryRow(
		`SELECT id, trigger_type, schedule_id, name, started_at, finished_at, server_count,
		        ok_count, warning_count, critical_count, error_count, issue_count, avg_score,
		        skipped_count, elapsed_ms, summary_json, created_at
		 FROM batches WHERE id = ?`, id,
	).Scan(&b.ID, &b.Trigger, &b.ScheduleID, &b.Name, &started, &finished, &b.ServerCount,
		&b.OKCount, &b.WarningCount, &b.CriticalCount, &b.ErrorCount, &b.IssueCount,
		&b.AvgScore, &b.Skipped, &b.ElapsedMS, &b.SummaryJSON, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	b.StartedAt = parseTime(started)
	b.FinishedAt = parseTime(finished)
	b.CreatedAt = parseTime(created)
	return &b, nil
}

// BatchInspectionIDs 返回批次关联的巡检记录 ID。
func (s *Store) BatchInspectionIDs(batchID int64) ([]int64, error) {
	rows, err := s.db.Query(`SELECT inspection_id FROM batch_inspections WHERE batch_id = ? ORDER BY inspection_id`, batchID)
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
