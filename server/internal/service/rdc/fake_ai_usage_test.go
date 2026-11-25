// fake_ai_usage_test.go · 统计与限流端口的测试替身（内存实现，无数据库依赖）。
// 职责：为 ModelChatService / ModelStatsService 的单测提供可控的窗口计数、记录写入与阈值读取。
// 输入：测试用例直接设置字段（usedCount / insertErr / quota 等）。
// 输出：所有方法行为确定、可断言；不产生任何 IO。
// 边界：只实现接口方法，不模拟 SQL 语义（聚合口径由仓储层单测与真实库覆盖）。
package rdc

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"dandelion/server/internal/contract"
)

// fakeCallRepo 记录每次 Insert 的入参与窗口计数返回值，供断言"是否落记录/落了几条"。
type fakeCallRepo struct {
	inserted  []contract.AIModelCallRecord
	insertErr error
	usedCount int
	countErr  error
	stats     contract.AIModelCallStats
	scope     contract.Scope
	since     time.Time
}

func (f *fakeCallRepo) Insert(_ context.Context, record contract.AIModelCallRecord) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	f.inserted = append(f.inserted, record)
	return nil
}

func (f *fakeCallRepo) CountInWindow(_ context.Context, scope contract.Scope, since time.Time) (int, error) {
	f.scope, f.since = scope, since
	return f.usedCount, f.countErr
}

func (f *fakeCallRepo) Aggregate(
	_ context.Context, _ contract.AIModelCallQuery, _ contract.Scope,
) (contract.AIModelCallStats, error) {
	return f.stats, f.countErr
}

// lastInserted 返回最近一次写入记录；没有记录时返回零值（测试断言"不应写入"时用 len 判断）。
func (f *fakeCallRepo) lastInserted() contract.AIModelCallRecord {
	if len(f.inserted) == 0 {
		return contract.AIModelCallRecord{}
	}
	return f.inserted[len(f.inserted)-1]
}

// fakeQuotaRepo 是阈值端口的替身：findErr 可用来模拟"无配置行"与"读库失败"两种路径。
type fakeQuotaRepo struct {
	cfg      contract.AIQuotaConfig
	findErr  error
	upserted *contract.AIQuotaInput
	extra    []byte
	writeErr error
}

func (f *fakeQuotaRepo) Find(context.Context, contract.Scope) (contract.AIQuotaConfig, error) {
	if f.findErr != nil {
		return contract.AIQuotaConfig{}, f.findErr
	}
	return f.cfg, nil
}

func (f *fakeQuotaRepo) Upsert(
	_ context.Context, _ contract.Scope, input contract.AIQuotaInput, extra json.RawMessage,
) (contract.AIQuotaConfig, error) {
	if f.writeErr != nil {
		return contract.AIQuotaConfig{}, f.writeErr
	}
	f.upserted, f.extra = &input, extra
	cfg := f.cfg
	cfg.Enabled, cfg.MaxCalls, cfg.WindowMinutes, cfg.Action = input.Enabled, input.MaxCalls, input.WindowMinutes, input.Action
	cfg.ExtraConfig = extra
	return cfg, nil
}

// noQuota 返回"从未配置过阈值"的仓储：Find 返回 sql.ErrNoRows，等价于生产里的缺行。
func noQuota() *fakeQuotaRepo {
	return &fakeQuotaRepo{findErr: sql.ErrNoRows}
}
