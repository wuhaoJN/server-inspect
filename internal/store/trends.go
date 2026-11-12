package store

import (
	"database/sql"
	"errors"
	"time"
)

// GetServerView 查询单台服务器及最近一次巡检摘要。
func (s *Store) GetServerView(id int64) (*ServerView, error) {
	const q = `
		SELECT s.id, s.name, s.host, s.port, s.username, s.host_key, s.group_name, s.node_role, s.tags, s.note,
		       s.maintenance, s.maintenance_until, s.maintenance_note,
		       s.created_at, s.updated_at,
		       CASE WHEN s.username <> '' AND s.password <> '' THEN 1 ELSE 0 END,
		       i.id, i.status, i.score, i.started_at
		FROM servers s
		LEFT JOIN inspections i ON i.id = (
			SELECT id FROM inspections WHERE server_id = s.id ORDER BY id DESC LIMIT 1
		)
		WHERE s.id = ?`
	var (
		v                           ServerView
		maintenance                 int
		maintenanceUntil, maintNote string
		created, updated            string
		configured                  int
		lastID, lastScore           sql.NullInt64
		lastStatus, lastAt          sql.NullString
	)
	err := s.db.QueryRow(q, id).Scan(&v.ID, &v.Name, &v.Host, &v.Port, &v.Username, &v.HostKey,
		&v.Group, &v.NodeRole, &v.Tags, &v.Note,
		&maintenance, &maintenanceUntil, &maintNote, &created, &updated, &configured,
		&lastID, &lastStatus, &lastScore, &lastAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	v.Maintenance = maintenance == 1
	v.MaintenanceNote = maintNote
	if maintenanceUntil != "" {
		if t := parseTime(maintenanceUntil); !t.IsZero() {
			v.MaintenanceUntil = &t
		}
	}
	v.Configured = configured == 1
	v.CreatedAt = parseTime(created)
	v.UpdatedAt = parseTime(updated)
	if lastID.Valid {
		v.LastInspectionID = lastID.Int64
		v.LastStatus = lastStatus.String
		v.LastScore = int(lastScore.Int64)
		if lastAt.Valid {
			t := parseTime(lastAt.String)
			v.LastAt = &t
		}
	}
	return &v, nil
}

// TrendPoint 是趋势图所需的单次巡检指标。
type TrendPoint struct {
	ID          int64     `json:"id"`
	Status      string    `json:"status"`
	Score       int       `json:"score"`
	MetricsJSON string    `json:"-"`
	StartedAt   time.Time `json:"startedAt"`
}

// ListTrendPoints 查询指定时间之后的巡检指标点。
func (s *Store) ListTrendPoints(serverID int64, since time.Time, limit int) ([]TrendPoint, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.db.Query(
		`SELECT id, status, score, metrics_json, started_at
		 FROM inspections
		 WHERE server_id = ? AND started_at >= ? AND status <> 'error' AND report_json <> ''
		 ORDER BY started_at ASC LIMIT ?`,
		serverID, queryTime(since), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TrendPoint, 0, limit)
	for rows.Next() {
		var (
			p       TrendPoint
			started string
		)
		if err := rows.Scan(&p.ID, &p.Status, &p.Score, &p.MetricsJSON, &started); err != nil {
			return nil, err
		}
		p.StartedAt = parseTime(started)
		out = append(out, p)
	}
	return out, rows.Err()
}

// PreviousInspection 查询同一服务器上一次可对比的巡检记录。
func (s *Store) PreviousInspection(serverID, currentID int64) (*Inspection, error) {
	var prevID int64
	err := s.db.QueryRow(
		`SELECT id FROM inspections
		 WHERE server_id = ? AND id < ? AND report_json <> ''
		 ORDER BY id DESC LIMIT 1`, serverID, currentID).Scan(&prevID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return s.GetInspection(prevID)
}
