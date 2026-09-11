// Package repository outbox 事件读取与状态回写。
package repository

import (
	"context"
	"database/sql"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// OutboxRow outbox_event 行。
type OutboxRow struct {
	ID          int64
	AggregateID int64
	EventType   string // RULE_UPSERT / RULE_DELETE
	EventKey    string
	Payload     string
}

// Store outbox 持久化。
// U0 单实例：直接按 id 轮询发布（at-least-once，Flink/下游按 rule version 幂等去重）；
// rule_bcast 重复无害，U2 起如需多实例再引入行级认领（ADR）。
type Store struct{ db *sql.DB }

func NewStore(dsn string) (*Store, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Pending 读取待发布事件（按 id 升序，保证规则版本顺序）。
func (s *Store) Pending(ctx context.Context, limit int) ([]*OutboxRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, aggregate_id, event_type, event_key, payload FROM outbox_event
		 WHERE status = 'PENDING' AND next_retry_at <= NOW(3)
		 ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*OutboxRow
	for rows.Next() {
		r := &OutboxRow{}
		if err := rows.Scan(&r.ID, &r.AggregateID, &r.EventType, &r.EventKey, &r.Payload); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkPublished 发布成功标记（幂等）。
func (s *Store) MarkPublished(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE outbox_event SET status='PUBLISHED', published_at=NOW(3) WHERE id=?`, id)
	return err
}

// MarkFailed 发布失败退避重试；超过 MAX_RETRY 置 FAILED 由人工处理（可观测、可重放）。
func (s *Store) MarkFailed(ctx context.Context, id int64, reason string, next time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE outbox_event SET status='PENDING', retry_count=retry_count+1,
		 next_retry_at=?, last_error=? WHERE id=?`, next, reason, id)
	return err
}

// MarkDead 超过重试上限。
func (s *Store) MarkDead(ctx context.Context, id int64, reason string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE outbox_event SET status='FAILED', last_error=? WHERE id=?`, reason, id)
	return err
}

// CleanupBefore 批量清理已发布且早于保留期的 outbox 事件（数据生命周期，docs/04 §6）。
// 仅删 PUBLISHED（PENDING/FAILED 保留供排查与重放）；单批 LIMIT 控制锁窗口。
func (s *Store) CleanupBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM outbox_event WHERE status='PUBLISHED' AND published_at < ? LIMIT ?`,
		before, limit)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
