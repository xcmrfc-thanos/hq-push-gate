// Package repository 基础设施适配器：MySQL alert-inbox、Redis 路由索引。
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/hqpush/gate/packages/secretx"
	"github.com/hqpush/gate/services/notify/internal/application"
	"github.com/hqpush/gate/services/notify/internal/domain"
)

// MySQLStore alert-inbox 持久化。ring 可选：配置后 DeveloperByUser 自动解密 v1: 密文。
type MySQLStore struct {
	db   *sql.DB
	ring *secretx.KeyRing
}

// SetRing 注入主密钥环（app_secret 密文解密；在 secretx.LoadKeys 成功后调用）。
func (s *MySQLStore) SetRing(r *secretx.KeyRing) { s.ring = r }

func NewMySQLStore(dsn string) (*MySQLStore, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)
	return &MySQLStore{db: db}, nil
}

func (s *MySQLStore) Close() error { return s.db.Close() }

func (s *MySQLStore) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Rule alert_rule 行。
type Rule struct {
	ID            int64
	UserID        int64
	Market        string
	Symbol        string
	Status        int
	Version       int64
	ChannelPrefer string // sms|email|""（""=继承 user_vip，docs/10 §3.3）
	RuleType      string // PRICE_ABOVE/...（rule_bcast DELETE 重建需要）
	Condition     string // JSON 条件
	CooldownSec   int64
}

// RuleByID 查询规则（无论启停，只要存在即可投递；规则删除后触发的事件仍投递其归属用户）。
func (s *MySQLStore) RuleByID(ctx context.Context, id int64) (*Rule, error) {
	const q = `SELECT id, user_id, market, symbol, status, version, IFNULL(channel_prefer,'')
	           FROM alert_rule WHERE id = ?`
	r := &Rule{}
	err := s.db.QueryRowContext(ctx, q, id).
		Scan(&r.ID, &r.UserID, &r.Market, &r.Symbol, &r.Status, &r.Version, &r.ChannelPrefer)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

// InsertAlertRecords 幂等批量落库：delivery_id 唯一键 + INSERT IGNORE；
// cursor_id 由 alert_cursor_seq 单调分配（行锁保证全局单调，即补拉游标）。
// 返回实际新插入的条数。
func (s *MySQLStore) InsertAlertRecords(ctx context.Context, records []*domain.AlertRecord) (int64, error) {
	if len(records) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var next int64
	if err := tx.QueryRowContext(ctx,
		`SELECT next_cursor_id FROM alert_cursor_seq WHERE id = 1 FOR UPDATE`).Scan(&next); err != nil {
		return 0, fmt.Errorf("alloc cursor: %w", err)
	}
	inserted := int64(0)
	for _, rec := range records {
		res, err := tx.ExecContext(ctx,
			`INSERT IGNORE INTO alert_record
			 (delivery_id, event_id, rule_id, user_id, market, symbol, title, trigger_at, status, cursor_id)
			 VALUES (?,?,?,?,?,?,?,?,?,?)`,
			rec.DeliveryID, rec.EventID, rec.RuleID, rec.UserID, rec.Market, rec.Symbol,
			rec.Title, rec.TriggerAt, domain.StatusPending, next)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		inserted += n
		if n > 0 {
			rec.CursorID = next
			rec.Status = domain.StatusPending
			next++
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE alert_cursor_seq SET next_cursor_id = ? WHERE id = 1`, next); err != nil {
		return 0, err
	}
	return inserted, tx.Commit()
}

// VipByID 查用户套餐与 IM 绑定（docs/10 §8 user_vip）；无记录返回 nil（免费口径）。
func (s *MySQLStore) VipByID(ctx context.Context, userID int64) (*application.VipRow, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT plan_type, allow_sms, channel_default,
		        IFNULL(dingtalk_webhook,''), IFNULL(feishu_webhook,'')
		 FROM user_vip WHERE user_id = ?`, userID)
	v := &application.VipRow{}
	var allowSMS int
	if err := row.Scan(&v.PlanType, &allowSMS, &v.ChannelDefault, &v.DingtalkWebhook, &v.FeishuWebhook); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	v.AllowSMS = allowSMS == 1
	return v, nil
}

// DB 暴露底层连接（渠道配置 ConfigProvider / 管理端复用同一连接池）。
func (s *MySQLStore) DB() *sql.DB { return s.db }

// DeveloperByUser 查开发者推送配置（api_developer_config，docs/10 §6）；无记录返回 nil。
// app_secret 兼容两种形态：v1: 密文（ring 可用时解密）与旧明文（V4 legacy）。
func (s *MySQLStore) DeveloperByUser(ctx context.Context, userID int64) (*application.DeveloperRow, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT IFNULL(push_mode,''), IFNULL(hook_url,''), IFNULL(app_secret,'')
		 FROM api_developer_config WHERE user_id = ?`, userID)
	d := &application.DeveloperRow{}
	if err := row.Scan(&d.PushMode, &d.HookURL, &d.AppSecret); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if strings.HasPrefix(d.AppSecret, secretx.Prefix+":") {
		if s.ring == nil {
			return nil, fmt.Errorf("api_developer_config app_secret encrypted but master keys not configured")
		}
		plain, err := s.ring.Decrypt(d.AppSecret)
		if err != nil {
			return nil, fmt.Errorf("app_secret decrypt: %w", err)
		}
		d.AppSecret = plain
	}
	return d, nil
}

// EmailOfUser 查用户邮箱（user.email，退订按 email 反查用户的唯一键）。
func (s *MySQLStore) EmailOfUser(ctx context.Context, userID int64) (string, error) {
	var email string
	err := s.db.QueryRowContext(ctx,
		`SELECT IFNULL(email,'') FROM user WHERE id = ?`, userID).Scan(&email)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return email, nil
}

// InsertSendLogs 批量写通知发送记录（notify_send_log，docs/10 §8）。
func (s *MySQLStore) InsertSendLogs(ctx context.Context, logs []*application.SendLogRow) error {
	if len(logs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, l := range logs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO notify_send_log
			 (user_id, rule_id, event_id, channel, provider, recipient, provider_msg_id, status, error)
			 VALUES (?,?,?,?,?,?,?,?,?)`,
			l.UserID, l.RuleID, l.EventID, l.Channel, l.Provider, l.Recipient,
			l.ProviderMsgID, l.Status, l.Error); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UnsubscribeResult 邮件退订结果：用户 ID + 被停用的规则快照。
// 调用方必须据此向 rule_bcast 发 DELETE（否则 Flink 广播状态里的规则仍会触发）。
type UnsubResult struct {
	UserID int64
	Rules  []*Rule
}

// UnsubscribeByEmail 邮件退订：按收件邮箱停用该用户全部告警规则（docs/10 §4.4）。
func (s *MySQLStore) UnsubscribeByEmail(ctx context.Context, email string) (*UnsubResult, error) {
	var uid int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT id FROM user WHERE email = ?`, email).Scan(&uid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &UnsubResult{UserID: 0}, nil
		}
		return nil, err
	}
	out := &UnsubResult{UserID: uid}
	rs, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, market, symbol, status, version,
		        IFNULL(rule_type,''), IFNULL(`+"`condition`"+`,''), IFNULL(cooldown_sec,60)
		 FROM alert_rule WHERE user_id = ? AND status = 1`, uid)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	for rs.Next() {
		r := &Rule{}
		if err := rs.Scan(&r.ID, &r.UserID, &r.Market, &r.Symbol,
			&r.Status, &r.Version, &r.RuleType, &r.Condition, &r.CooldownSec); err != nil {
			return nil, err
		}
		out.Rules = append(out.Rules, r)
	}
	if _, err = s.db.ExecContext(ctx,
		`UPDATE alert_rule SET status = 0 WHERE user_id = ? AND status = 1`, uid); err != nil {
		return nil, err
	}
	return out, nil
}

// RecordDeliveryReceipt 投递回执落库：按收件地址更新最近一条 mail 记录状态（docs/10 §5.2）。
func (s *MySQLStore) RecordDeliveryReceipt(ctx context.Context, email, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE notify_send_log SET status = ? WHERE channel = 'mail' AND recipient = ?`,
		status, email)
	return err
}

// MarkSent 批量推进 PENDING -> SENT（幂等，已 ACK 不回退）。
func (s *MySQLStore) MarkSent(ctx context.Context, deliveryIDs []string) error {
	if len(deliveryIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range deliveryIDs {
		if _, err := tx.ExecContext(ctx,
			`UPDATE alert_record SET status = 'SENT' WHERE delivery_id = ? AND status = 'PENDING'`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Ack 客户端 ACK：PENDING/SENT -> ACKED（幂等）。
func (s *MySQLStore) Ack(ctx context.Context, deliveryID string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE alert_record SET status = 'ACKED' WHERE delivery_id = ? AND status IN ('PENDING','SENT')`,
		deliveryID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// PullPending 补拉：cursor_id > cursor、未 ACK 未过期、按 cursor_id 升序。
func (s *MySQLStore) PullPending(ctx context.Context, userID, cursor int64, limit int, now time.Time, window time.Duration) ([]*domain.AlertRecord, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	const q = `SELECT delivery_id, event_id, rule_id, user_id, market, symbol, title, trigger_at, status, cursor_id
	           FROM alert_record
	           WHERE user_id = ? AND cursor_id > ? AND status IN ('PENDING','SENT') AND trigger_at >= ?
	           ORDER BY cursor_id LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, userID, cursor, now.Add(-window), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.AlertRecord
	for rows.Next() {
		r := &domain.AlertRecord{}
		if err := rows.Scan(&r.DeliveryID, &r.EventID, &r.RuleID, &r.UserID, &r.Market, &r.Symbol,
			&r.Title, &r.TriggerAt, &r.Status, &r.CursorID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ExpireStale 超窗未 ACK 置 EXPIRED（保留窗口 72h）。
func (s *MySQLStore) ExpireStale(ctx context.Context, now time.Time, window time.Duration) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE alert_record SET status = 'EXPIRED' WHERE status IN ('PENDING','SENT') AND trigger_at < ?`,
		now.Add(-window))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// CleanupSendLogsBefore 批量清理早于保留期的发送记录（数据生命周期，docs/04 §6）。
// 单批 LIMIT 控制锁窗口；高频追加表依赖 idx_cleanup（V9）索引。
func (s *MySQLStore) CleanupSendLogsBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM notify_send_log WHERE created_at < ? LIMIT ?`, before, limit)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
