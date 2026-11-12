package store

import (
	"fmt"
	"time"
)

// SetServerMaintenance 将服务器标记为维护中。
// until 为零值表示持续维护，直到手动结束。
func (s *Store) SetServerMaintenance(id int64, until *time.Time, note string) error {
	var untilText string
	if until != nil && !until.IsZero() {
		untilText = until.UTC().Format(timeLayout)
	}
	res, err := s.db.Exec(
		`UPDATE servers SET maintenance = 1, maintenance_until = ?, maintenance_note = ?, updated_at = ?
		 WHERE id = ?`,
		untilText, note, nowString(), id)
	if err != nil {
		return fmt.Errorf("标记维护状态失败: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearServerMaintenance 结束维护状态。
func (s *Store) ClearServerMaintenance(id int64) error {
	res, err := s.db.Exec(
		`UPDATE servers SET maintenance = 0, maintenance_until = '', maintenance_note = '', updated_at = ?
		 WHERE id = ?`,
		nowString(), id)
	if err != nil {
		return fmt.Errorf("结束维护状态失败: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}
