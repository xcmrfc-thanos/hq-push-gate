package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// DorisStore 告警明细归档（alert_event -> Doris alert_event_detail，MySQL 协议写入）。
// Doris 不进入实时告警热路径（docs/09 §1）；U0 默认关闭（DorisDSN 为空）。
type DorisStore struct{ db *sql.DB }

func NewDorisStore(ctx context.Context, dsn string) (*DorisStore, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("doris ping: %w", err)
	}
	return &DorisStore{db: db}, nil
}

func (s *DorisStore) Close() error { return s.db.Close() }

// InsertAlertEvents 批量归档告警明细（幂等：event_id 主键 + REPLACE 语义）。
func (s *DorisStore) InsertAlertEvents(ctx context.Context, rows []AlertEventRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx,
			`REPLACE INTO alert_event_detail
			 (event_id, rule_id, rule_version, market, symbol, title, trigger_at, detail_json)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			r.EventID, r.RuleID, r.Version, r.Market, r.Symbol, r.Title,
			time.UnixMilli(r.TriggerTS).Format(time.DateTime), r.DetailJSON); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AlertEventRow 归档行。
type AlertEventRow struct {
	EventID    string
	RuleID     int64
	Version    int64
	Market     string
	Symbol     string
	Title      string
	DetailJSON string
	TriggerTS  int64
}
