// Package repository 基础设施适配器：ClickHouse 批量写、Doris 归档。
package repository

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// TickRow ClickHouse tick 明细行。
type TickRow struct {
	Market, Symbol                                       string
	TSMS                                                 int64
	LastPrice, Open, High, Low, PreClose, Volume, Amount float64
}

// ClickHouseStore tick/K 线明细写入。
// 幂等：event_id（market:symbol:ts_ms）+ ingest_version 版本列，
// ReplacingMergeTree 去重，查询不依赖 FINAL（docs/07 §8）。
type ClickHouseStore struct {
	conn clickhouse.Conn
}

func NewClickHouseStore(ctx context.Context, dsn string) (*ClickHouseStore, error) {
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, err
	}
	if err := conn.Ping(ctx); err != nil {
		return nil, fmt.Errorf("clickhouse ping: %w", err)
	}
	return &ClickHouseStore{conn: conn}, nil
}

func (s *ClickHouseStore) Close() error { return s.conn.Close() }

// KlineRow ClickHouse K线行（1m 基期；5m 深历史由 Baostock 回填直接写同一表）。
type KlineRow struct {
	Market, Symbol         string
	PeriodMin              uint16
	BeginTS                int64
	Open, High, Low, Close float64
	Volume, Amount         float64
}

// InsertKlines 批量写 kline_local（U0 单节点直写 local 表）；event_id（market:symbol:period:begin_ts）幂等，
// ReplacingMergeTree 以 ingest_version 去重（回填与实时流重叠时后到者覆盖）。
func (s *ClickHouseStore) InsertKlines(ctx context.Context, rows []KlineRow, version uint64) error {
	if len(rows) == 0 {
		return nil
	}
	stmt, err := s.conn.PrepareBatch(ctx,
		`INSERT INTO kline_local
		 (event_id, ingest_version, market, symbol, period_min, begin_ts,
		  open, high, low, close, volume, amount)`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		eventID := fmt.Sprintf("%s:%s:%d:%d", r.Market, r.Symbol, r.PeriodMin, r.BeginTS)
		if err := stmt.Append(
			eventID, version, r.Market, r.Symbol, r.PeriodMin, r.BeginTS,
			r.Open, r.High, r.Low, r.Close, r.Volume, r.Amount,
		); err != nil {
			return err
		}
	}
	return stmt.Send()
}

// InsertTicks 批量写 tick_local（U0 单节点直写 local 表）；version 用当前微秒时间，
// 重放后新版本覆盖旧版本。
func (s *ClickHouseStore) InsertTicks(ctx context.Context, rows []TickRow, version uint64) error {
	if len(rows) == 0 {
		return nil
	}
	stmt, err := s.conn.PrepareBatch(ctx,
		`INSERT INTO tick_local
		 (event_id, ingest_version, market, symbol, ts_ms,
		  last_price, open, high, low, pre_close, volume, amount)`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		eventID := fmt.Sprintf("%s:%s:%d", r.Market, r.Symbol, r.TSMS)
		if err := stmt.Append(
			eventID, version, r.Market, r.Symbol, r.TSMS,
			r.LastPrice, r.Open, r.High, r.Low, r.PreClose, r.Volume, r.Amount,
		); err != nil {
			return err
		}
	}
	return stmt.Send()
}
