// chat_quota_test.go · 软限流判定的纯函数测试（无 DB、无时钟、无 HTTP）。
// 职责：钉死三种分流（未启用 / 未达上限 / 达上限的 warn 与 reject）、窗口边界与文案口径。
// 输入：就地构造的阈值配置与"窗口内已出站次数"整数。
// 输出：全部断言通过；证明判定可脱离数据库与真实时间单测。
// 边界：不连数据库、不读环境变量、不发起任何网络调用。
package rdc

import (
	"strings"
	"testing"

	"dandelion/server/internal/contract"
)

// quotaConfig 造一份启用状态明确的阈值，避免每个用例重复写四个字段。
func quotaConfig(action string, maxCalls int) contract.AIQuotaConfig {
	return contract.AIQuotaConfig{Enabled: true, MaxCalls: maxCalls, WindowMinutes: 60, Action: action}
}

func TestDecideQuotaDisabledAlwaysAllows(t *testing.T) {
	// 默认关闭是 P1 不改存量行为的物理保证：即使窗口内次数远超阈值也必须放行且不告警。
	cfg := quotaConfig(contract.AIQuotaActionReject, 10)
	cfg.Enabled = false
	decision := decideQuota(cfg, 9999)
	if !decision.Allow || decision.Warn || decision.Reason != "" {
		t.Fatalf("未启用必须静默放行，实际 %+v", decision)
	}
}

func TestDecideQuotaBelowThresholdAllows(t *testing.T) {
	cfg := quotaConfig(contract.AIQuotaActionReject, 10)
	// 边界用 MaxCalls-1：这是"最后一次仍应放行"的位置，与下面的 == MaxCalls 成对。
	if decision := decideQuota(cfg, cfg.MaxCalls-1); !decision.Allow || decision.Warn {
		t.Fatalf("未达上限必须放行且不告警，实际 %+v", decision)
	}
}

func TestDecideQuotaWarnAllowsWithReason(t *testing.T) {
	cfg := quotaConfig(contract.AIQuotaActionWarn, 10)
	decision := decideQuota(cfg, cfg.MaxCalls)
	if !decision.Allow || !decision.Warn {
		t.Fatalf("warn 动作必须放行并告警，实际 %+v", decision)
	}
	// 文案要能让管理员自查：必须同时出现实际次数与阈值两个数字。
	if !strings.Contains(decision.Reason, "10") {
		t.Fatalf("文案必须含实际次数与阈值: %s", decision.Reason)
	}
}

func TestDecideQuotaRejectBlocksAtBoundary(t *testing.T) {
	cfg := quotaConfig(contract.AIQuotaActionReject, 10)
	// used == MaxCalls 必须被拦：MaxCalls 的语义是"允许的出站次数"，
	// 若边界写成 > ，实际放行会永远多一次。
	decision := decideQuota(cfg, cfg.MaxCalls)
	if decision.Allow || decision.Warn {
		t.Fatalf("达到上限且 reject 必须拒绝，实际 %+v", decision)
	}
	if decision.Reason == "" {
		t.Fatal("拒绝必须带可上屏的中文原因")
	}
}

func TestDecideQuotaRejectAlsoBlocksAboveBoundary(t *testing.T) {
	cfg := quotaConfig(contract.AIQuotaActionReject, 10)
	if decision := decideQuota(cfg, 500); decision.Allow {
		t.Fatalf("远超上限同样必须拒绝，实际 %+v", decision)
	}
}

func TestNormalizeQuotaMatchesContractDefaults(t *testing.T) {
	// 默认值只有一处定义（contract.AIQuotaDefaults）：这里断言转调而非复制数字。
	normalized := normalizeQuota(contract.AIQuotaConfig{})
	defaults := contract.AIQuotaDefaults()
	if normalized.MaxCalls != defaults.MaxCalls ||
		normalized.WindowMinutes != defaults.WindowMinutes ||
		normalized.Action != defaults.Action ||
		normalized.Enabled != defaults.Enabled {
		t.Fatalf("零值配置必须补齐为契约默认值，实际 %+v", normalized)
	}
	if normalized.Enabled {
		t.Fatal("默认必须关闭：升级后不得突然开始拒绝调用")
	}
}

func TestNormalizeQuotaKeepsExplicitValues(t *testing.T) {
	cfg := contract.AIQuotaConfig{Enabled: true, MaxCalls: 5, WindowMinutes: 30, Action: contract.AIQuotaActionReject}
	normalized := normalizeQuota(cfg)
	// 只比较四项配置：ExtraConfig 是 json.RawMessage（不可比较），且不在本函数职责内。
	if normalized.Enabled != cfg.Enabled || normalized.MaxCalls != cfg.MaxCalls ||
		normalized.WindowMinutes != cfg.WindowMinutes || normalized.Action != cfg.Action {
		t.Fatalf("显式合法值不得被改写，实际 %+v", normalized)
	}
}

func TestNormalizeQuotaFallsBackOnIllegalAction(t *testing.T) {
	// 读路径宽容：历史脏数据的非法动作回落 warn，绝不让统计页因配置问题 500。
	cfg := contract.AIQuotaConfig{Enabled: true, MaxCalls: 5, WindowMinutes: 30, Action: "block"}
	if normalized := normalizeQuota(cfg); normalized.Action != contract.AIQuotaActionWarn {
		t.Fatalf("非法动作必须回落 warn，实际 %s", normalized.Action)
	}
}
