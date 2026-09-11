// Package source 定义主动拉取型行情源接口（docs/09 §5.1 Branch 3：东财 → MiniQMT →
// 商用源只换 adapter 不换链路）。与 HTTP 被动接入（/ingest/ticks，bench/tick-source 使用）
// 并存：源实现把标准化 Tick 交给 application.IngestService，由它统一校验并发布 tick_raw。
package source

import (
	"context"

	"github.com/hqpush/gate/services/hq-gateway/internal/domain"
)

// Ingest 用例接口，与 application.IngestService 对齐。
type Ingest interface {
	Ingest(ctx context.Context, ticks []*domain.Tick) (int, error)
}

// Source 行情源：ingest 在构造时注入，Run 阻塞直到 ctx 取消（返回 nil）；
// 源内部自行负责节流与失败退避。
type Source interface {
	Name() string
	Run(ctx context.Context) error
}
