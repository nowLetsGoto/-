// ai_model_usage_test.go · P1 统计与阈值契约的纯函数单测。
// 职责：锁死默认统计区间、分组维度校验、阈值规范化与校验、保留期解析。
// 输入：contract 包的 DTO 与纯函数。
// 输出：任一行为漂移即 t.Fatal。
// 边界：不访问 DB / 网络 / 环境变量；不测 SQL 聚合（在仓储层测试里）。
package contract

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNormalizeAIModelCallQuery(t *testing.T) {
	now := time.Date(2026, 3, 10, 15, 42, 0, 0, time.UTC)
	// 缺省：end 取当前整点，start 取 7 天前 —— 半开区间让连续多天求和不会重复计入边界。
	got, err := NormalizeAIModelCallQuery(AIModelCallQuery{}, now)
	if err != nil {
		t.Fatalf("缺省查询应通过: %v", err)
	}
	if !got.End.Equal(now.Truncate(time.Hour)) || !got.Start.Equal(got.End.AddDate(0, 0, -AIUsageDefaultRangeDays)) {
		t.Fatalf("缺省区间错误: %+v", got)
	}
	if _, err := NormalizeAIModelCallQuery(AIModelCallQuery{
		Start: now, End: now.Add(-time.Hour),
	}, now); err == nil {
		t.Fatal("start >= end 必须被拒")
	}
	if _, err := NormalizeAIModelCallQuery(AIModelCallQuery{GroupBy: []string{"tenant"}}, now); err == nil {
		t.Fatal("白名单外分组维度必须被拒")
	}
	multi, err := NormalizeAIModelCallQuery(AIModelCallQuery{GroupBy: []string{AIUsageGroupModel, AIUsageGroupDay}}, now)
	if err != nil {
		t.Fatalf("合法组合分组应通过: %v", err)
	}
	if !multi.HasGroup(AIUsageGroupModel) || multi.HasGroup(AIUsageGroupScene) {
		t.Fatalf("HasGroup 判定错误: %+v", multi.GroupBy)
	}
}

func TestNormalizeAIQuotaFillsDefaults(t *testing.T) {
	// 历史脏行 / 从未配置：零值与非法 action 一律回落默认，读路径绝不报错。
	got := NormalizeAIQuota(AIQuotaConfig{})
	defaults := AIQuotaDefaults()
	if got.MaxCalls != defaults.MaxCalls || got.WindowMinutes != defaults.WindowMinutes || got.Action != defaults.Action {
		t.Fatalf("零值应补默认: %+v", got)
	}
	dirty := NormalizeAIQuota(AIQuotaConfig{Enabled: true, MaxCalls: 50, WindowMinutes: 30, Action: "block"})
	if dirty.MaxCalls != 50 || dirty.WindowMinutes != 30 || dirty.Action != AIQuotaActionWarn {
		t.Fatalf("非法 action 应回落 warn 且保留合法字段: %+v", dirty)
	}
	if !dirty.Enabled {
		t.Fatal("enabled 是显式布尔，规范化不得篡改")
	}
}

func TestValidateAIQuotaInput(t *testing.T) {
	if _, err := ValidateAIQuotaInput(AIQuotaInput{MaxCalls: 1000, WindowMinutes: 60, Action: AIQuotaActionWarn}); err != nil {
		t.Fatalf("合法阈值应通过: %v", err)
	}
	for _, invalid := range []AIQuotaInput{
		{MaxCalls: 0, WindowMinutes: 60, Action: AIQuotaActionWarn},
		{MaxCalls: 1000, WindowMinutes: 0, Action: AIQuotaActionWarn},
		{MaxCalls: 1000, WindowMinutes: 60, Action: "block"},
	} {
		if _, err := ValidateAIQuotaInput(invalid); err == nil {
			t.Fatalf("非法阈值 %+v 必须被拒", invalid)
		}
	}
}

func TestAIRetentionDays(t *testing.T) {
	// 缺失 / 非法 → 默认 90；合法区间 [7,365]。
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{"", AIRetentionDaysDefault},
		{"{}", AIRetentionDaysDefault},
		{"null", AIRetentionDaysDefault},
		{`{"retentionDays":30}`, 30},
		{`{"retentionDays":6}`, AIRetentionDaysDefault},
		{`{"retentionDays":366}`, AIRetentionDaysDefault},
		{`{"retentionDays":7}`, 7},
		{`{"retentionDays":365}`, 365},
		{`{"retentionDays":30,"burstMultiplier":1.5}`, 30}, // 未知键原样忽略，不影响已知键解析
	} {
		if got := AIRetentionDays(json.RawMessage(tc.raw)); got != tc.want {
			t.Fatalf("AIRetentionDays(%s)=%d，期望 %d", tc.raw, got, tc.want)
		}
	}
}

func TestAIModelCallStatsTokensArePointers(t *testing.T) {
	// 指针不是风格偏好："未上报"必须能与"零"区分，否则容量规划系统性偏低。
	stats := AIModelCallStats{}
	if stats.PromptTokens != nil || stats.AvgSuccessLatencyMS != nil {
		t.Fatal("零值 stats 的指针字段应为 nil，表示没有可用数据")
	}
	zero := int64(0)
	stats.PromptTokens = &zero
	if stats.PromptTokens == nil || *stats.PromptTokens != 0 {
		t.Fatal("显式 0 必须能被表达")
	}
}
