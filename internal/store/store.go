// Package store 封装 SQLite 持久化层。
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"serverinspect/internal/secret"

	_ "modernc.org/sqlite"
)

// ErrNotFound 表示记录不存在。
var ErrNotFound = errors.New("记录不存在")

const timeLayout = time.RFC3339Nano

// queryTimeLayout 使用固定 9 位小数，避免 RFC3339Nano 省略小数导致字符串比较时
// 同一秒内的记录被错误排除。
const queryTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func queryTime(t time.Time) string {
	return t.UTC().Format(queryTimeLayout)
}

// Store 是数据库访问入口。
type Store struct {
	db *sql.DB
}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS servers (
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
	)`,
	`CREATE TABLE IF NOT EXISTS inspections (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		server_id    INTEGER NOT NULL DEFAULT 0,
		server_name  TEXT    NOT NULL,
		host         TEXT    NOT NULL,
		status       TEXT    NOT NULL,
		score        INTEGER NOT NULL DEFAULT 0,
		error        TEXT    NOT NULL DEFAULT '',
		metrics_json TEXT    NOT NULL DEFAULT '{}',
		report_json  TEXT    NOT NULL DEFAULT '',
		started_at   TEXT    NOT NULL,
		finished_at  TEXT    NOT NULL,
		duration_ms  INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS idx_inspections_server ON inspections(server_id, id DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_inspections_started ON inspections(started_at DESC)`,
	`CREATE TABLE IF NOT EXISTS settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,
}

// Open 打开（必要时创建）SQLite 数据库并完成建表。
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// 单连接可避免 SQLite 写锁竞争，巡检系统并发量很小。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("初始化数据库失败(%s): %w", pragma, err)
		}
	}
	for _, stmt := range schema {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("初始化数据表失败: %w", err)
		}
	}
	return &Store{db: db}, nil
}

// Close 关闭数据库。
func (s *Store) Close() error { return s.db.Close() }

func nowString() string { return time.Now().UTC().Format(timeLayout) }

func parseTime(v string) time.Time {
	t, err := time.Parse(timeLayout, v)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ---------- 服务器 ----------

const serverColumns = `id, name, host, port, username, password, host_key, group_name, tags, note, created_at, updated_at`

func scanServer(scanner interface{ Scan(...any) error }) (*Server, error) {
	var (
		sv               Server
		created, updated string
	)
	err := scanner.Scan(&sv.ID, &sv.Name, &sv.Host, &sv.Port, &sv.Username, &sv.PasswordEnc,
		&sv.HostKey, &sv.Group, &sv.Tags, &sv.Note, &created, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	sv.CreatedAt = parseTime(created)
	sv.UpdatedAt = parseTime(updated)
	return &sv, nil
}

// CreateServer 新增服务器，回填 ID 与时间戳。
func (s *Store) CreateServer(sv *Server) error {
	if sv.Port == 0 {
		sv.Port = 22
	}
	now := time.Now().UTC()
	res, err := s.db.Exec(
		`INSERT INTO servers (name, host, port, username, password, host_key, group_name, tags, note, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sv.Name, sv.Host, sv.Port, sv.Username, sv.PasswordEnc, sv.HostKey,
		sv.Group, sv.Tags, sv.Note, now.Format(timeLayout), now.Format(timeLayout),
	)
	if err != nil {
		return fmt.Errorf("保存服务器失败: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	sv.ID = id
	sv.CreatedAt = now
	sv.UpdatedAt = now
	return nil
}

// CreateServers 批量新增服务器（用于网络扫描结果导入），在同一事务中写入。
func (s *Store) CreateServers(servers []*Server) error {
	if len(servers) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开启数据库事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(
		`INSERT INTO servers (name, host, port, username, password, host_key, group_name, tags, note, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("准备批量写入失败: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UTC()
	timestamp := now.Format(timeLayout)
	for _, sv := range servers {
		if sv.Port == 0 {
			sv.Port = 22
		}
		res, err := stmt.Exec(sv.Name, sv.Host, sv.Port, sv.Username, sv.PasswordEnc, sv.HostKey,
			sv.Group, sv.Tags, sv.Note, timestamp, timestamp)
		if err != nil {
			return fmt.Errorf("批量保存服务器失败: %w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		sv.ID = id
		sv.CreatedAt = now
		sv.UpdatedAt = now
	}
	return tx.Commit()
}

// UpdateServer 全量更新服务器信息。
func (s *Store) UpdateServer(sv *Server) error {
	if sv.Port == 0 {
		sv.Port = 22
	}
	now := time.Now().UTC()
	res, err := s.db.Exec(
		`UPDATE servers SET name = ?, host = ?, port = ?, username = ?, password = ?, host_key = ?,
		 group_name = ?, tags = ?, note = ?, updated_at = ? WHERE id = ?`,
		sv.Name, sv.Host, sv.Port, sv.Username, sv.PasswordEnc, sv.HostKey,
		sv.Group, sv.Tags, sv.Note, now.Format(timeLayout), sv.ID,
	)
	if err != nil {
		return fmt.Errorf("更新服务器失败: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	sv.UpdatedAt = now
	return nil
}

// DeleteServer 删除服务器（历史巡检记录保留）。
func (s *Store) DeleteServer(id int64) error {
	res, err := s.db.Exec(`DELETE FROM servers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// GetServer 按 ID 查询服务器。
func (s *Store) GetServer(id int64) (*Server, error) {
	row := s.db.QueryRow(`SELECT `+serverColumns+` FROM servers WHERE id = ?`, id)
	return scanServer(row)
}

// ListServers 查询全部服务器。
func (s *Store) ListServers() ([]Server, error) {
	rows, err := s.db.Query(`SELECT ` + serverColumns + ` FROM servers ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Server, 0, 8)
	for rows.Next() {
		sv, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sv)
	}
	return out, rows.Err()
}

// SetHostKey 保存（或更新）服务器主机密钥指纹。
func (s *Store) SetHostKey(id int64, fingerprint string) error {
	_, err := s.db.Exec(`UPDATE servers SET host_key = ?, updated_at = ? WHERE id = ?`,
		fingerprint, nowString(), id)
	return err
}

// ListServerViews 查询服务器列表并关联最近一次巡检结果。
func (s *Store) ListServerViews() ([]ServerView, error) {
	const q = `
		SELECT s.id, s.name, s.host, s.port, s.username, s.host_key, s.group_name, s.tags, s.note,
		       s.created_at, s.updated_at,
		       CASE WHEN s.username <> '' AND s.password <> '' THEN 1 ELSE 0 END,
		       i.id, i.status, i.score, i.started_at
		FROM servers s
		LEFT JOIN inspections i ON i.id = (
			SELECT id FROM inspections WHERE server_id = s.id ORDER BY id DESC LIMIT 1
		)
		ORDER BY s.name COLLATE NOCASE, s.id`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ServerView, 0, 16)
	for rows.Next() {
		var (
			v                  ServerView
			created, updated   string
			configured         int
			lastID, lastScore  sql.NullInt64
			lastStatus, lastAt sql.NullString
		)
		if err := rows.Scan(&v.ID, &v.Name, &v.Host, &v.Port, &v.Username, &v.HostKey, &v.Group, &v.Tags, &v.Note,
			&created, &updated, &configured, &lastID, &lastStatus, &lastScore, &lastAt); err != nil {
			return nil, err
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
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortServerViewsByIP(out)
	return out, nil
}

// sortServerViewsByIP 按 IP 数值排序（IPv4 优先于 IPv6），同 IP 再按端口、名称排序；
// 非 IP 主机名排在 IP 之后。
func sortServerViewsByIP(views []ServerView) {
	sort.SliceStable(views, func(i, j int) bool {
		a, b := views[i], views[j]
		aAddr, aOK := parseHostAddr(a.Host)
		bAddr, bOK := parseHostAddr(b.Host)

		if aOK && bOK {
			if cmp := aAddr.Compare(bAddr); cmp != 0 {
				return cmp < 0
			}
			if a.Port != b.Port {
				return a.Port < b.Port
			}
			if !strings.EqualFold(a.Name, b.Name) {
				return strings.ToLower(a.Name) < strings.ToLower(b.Name)
			}
			return a.ID < b.ID
		}
		if aOK != bOK {
			return aOK
		}

		an, bn := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if an != bn {
			return an < bn
		}
		ah, bh := strings.ToLower(a.Host), strings.ToLower(b.Host)
		if ah != bh {
			return ah < bh
		}
		if a.Port != b.Port {
			return a.Port < b.Port
		}
		return a.ID < b.ID
	})
}

func parseHostAddr(host string) (netip.Addr, bool) {
	host = strings.TrimSpace(host)
	host = strings.Trim(host, "[]")
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// CountServers 统计服务器数量。
func (s *Store) CountServers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM servers`).Scan(&n)
	return n, err
}

// ---------- 巡检记录 ----------

// CreateInspection 写入一条巡检记录并回填 ID。
func (s *Store) CreateInspection(rec *Inspection) error {
	res, err := s.db.Exec(
		`INSERT INTO inspections
		 (server_id, server_name, host, status, score, error, metrics_json, report_json, started_at, finished_at, duration_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ServerID, rec.ServerName, rec.Host, rec.Status, rec.Score, rec.Error,
		rec.MetricsJSON, rec.ReportJSON,
		rec.StartedAt.UTC().Format(timeLayout), rec.FinishedAt.UTC().Format(timeLayout), rec.DurationMS,
	)
	if err != nil {
		return fmt.Errorf("保存巡检记录失败: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	rec.ID = id
	return nil
}

// GetInspection 查询完整巡检记录。
func (s *Store) GetInspection(id int64) (*Inspection, error) {
	var (
		rec               Inspection
		started, finished string
	)
	err := s.db.QueryRow(
		`SELECT id, server_id, server_name, host, status, score, error, metrics_json, report_json,
		        started_at, finished_at, duration_ms
		 FROM inspections WHERE id = ?`, id,
	).Scan(&rec.ID, &rec.ServerID, &rec.ServerName, &rec.Host, &rec.Status, &rec.Score, &rec.Error,
		&rec.MetricsJSON, &rec.ReportJSON, &started, &finished, &rec.DurationMS)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	rec.StartedAt = parseTime(started)
	rec.FinishedAt = parseTime(finished)
	return &rec, nil
}

// DeleteInspection 删除巡检记录。
func (s *Store) DeleteInspection(id int64) error {
	res, err := s.db.Exec(`DELETE FROM inspections WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (f InspectionFilter) where() (string, []any) {
	var clauses []string
	var args []any
	if f.ServerID > 0 {
		clauses = append(clauses, "server_id = ?")
		args = append(args, f.ServerID)
	}
	if f.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, f.Status)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// ListInspections 分页查询巡检记录摘要。
func (s *Store) ListInspections(f InspectionFilter) ([]InspectionSummary, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 20
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	where, args := f.where()
	args = append(args, f.Limit, f.Offset)
	rows, err := s.db.Query(
		`SELECT id, server_id, server_name, host, status, score, error, started_at, duration_ms
		 FROM inspections`+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]InspectionSummary, 0, f.Limit)
	for rows.Next() {
		var (
			rec     InspectionSummary
			started string
		)
		if err := rows.Scan(&rec.ID, &rec.ServerID, &rec.ServerName, &rec.Host, &rec.Status,
			&rec.Score, &rec.Error, &started, &rec.DurationMS); err != nil {
			return nil, err
		}
		rec.StartedAt = parseTime(started)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// CountInspections 统计满足条件的巡检记录数量。
func (s *Store) CountInspections(f InspectionFilter) (int, error) {
	where, args := f.where()
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM inspections`+where, args...).Scan(&n)
	return n, err
}

// RecentInspections 查询最近若干条巡检记录。
func (s *Store) RecentInspections(limit int) ([]InspectionSummary, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(
		`SELECT id, server_id, server_name, host, status, score, error, started_at, duration_ms
		 FROM inspections ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]InspectionSummary, 0, limit)
	for rows.Next() {
		var (
			rec     InspectionSummary
			started string
		)
		if err := rows.Scan(&rec.ID, &rec.ServerID, &rec.ServerName, &rec.Host, &rec.Status,
			&rec.Score, &rec.Error, &started, &rec.DurationMS); err != nil {
			return nil, err
		}
		rec.StartedAt = parseTime(started)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// CountInspectionsSince 统计指定时间之后的巡检次数。
func (s *Store) CountInspectionsSince(since time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM inspections WHERE started_at >= ?`,
		queryTime(since)).Scan(&n)
	return n, err
}

// AvgScoreSince 统计指定时间之后的平均健康分（不含失败记录）。
func (s *Store) AvgScoreSince(since time.Time) (float64, error) {
	var avg sql.NullFloat64
	err := s.db.QueryRow(`SELECT AVG(score) FROM inspections WHERE started_at >= ? AND status <> 'error'`,
		queryTime(since)).Scan(&avg)
	if err != nil {
		return 0, err
	}
	if !avg.Valid {
		return 0, nil
	}
	return avg.Float64, nil
}

// StatusCountsSince 统计指定时间之后各状态数量。
func (s *Store) StatusCountsSince(since time.Time) (map[string]int, error) {
	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM inspections WHERE started_at >= ? GROUP BY status`,
		queryTime(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var (
			status string
			n      int
		)
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[status] = n
	}
	return out, rows.Err()
}

// ---------- 设置 ----------

// GetSetting 读取配置项。
func (s *Store) GetSetting(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// SetSetting 写入配置项。
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// EnsureAdmin 首次启动时创建管理员账号。created 表示本次是否新建。
func (s *Store) EnsureAdmin(username, password string) (created bool, err error) {
	if _, ok, err := s.GetSetting("admin_password_hash"); err != nil {
		return false, err
	} else if ok {
		return false, nil
	}
	hash, err := secret.HashPassword(password)
	if err != nil {
		return false, err
	}
	if err := s.SetSetting("admin_username", username); err != nil {
		return false, err
	}
	if err := s.SetSetting("admin_password_hash", hash); err != nil {
		return false, err
	}
	return true, nil
}

// AdminCredentials 返回管理员用户名与口令哈希。
func (s *Store) AdminCredentials() (username, hash string, err error) {
	var ok bool
	hash, ok, err = s.GetSetting("admin_password_hash")
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", ErrNotFound
	}
	username, _, err = s.GetSetting("admin_username")
	if err != nil {
		return "", "", err
	}
	return username, hash, nil
}

// SetAdminPassword 重置管理员用户名与口令（用于 -reset-admin-pass）。
func (s *Store) SetAdminPassword(username, password string) error {
	hash, err := secret.HashPassword(password)
	if err != nil {
		return err
	}
	if err := s.SetSetting("admin_username", username); err != nil {
		return err
	}
	return s.SetSetting("admin_password_hash", hash)
}
