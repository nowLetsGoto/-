// chat_quota.go · 软限流判定纯函数：阈值归一 + 是否放行/告警。
// 职责：把「已归一阈值 + 调用方数好的窗口内次数」折算成一个决定，供编排层执行。
// 输入：contract.AIQuotaConfig（可能来自仓储，也可能是零值）与窗口内已出站次数。
// 输出：QuotaDecision（Allow / Warn / 中文 Reason）；不返回 error，不产生副作用。
// 边界：不查库、不取时钟（窗口起止由调用方算好）、不写统计表、不记日志、不构造 HTTP 错误；
// 本文件出现 time.Now 或任何 SQL/仓储调用即越界。
package rdc

import (
	"fmt"

	"dandelion/server/internal/contract"
)

// QuotaDecision 是限流判定结果；Allow=false 时编排层必须"上游零请求 + 仍落一条记录"。
type QuotaDecision struct {
	Allow  bool   // false = 拒绝（action=reject 且已达上限）
	Warn   bool   // true = 已达上限但放行（action=warn），由编排层记告警日志
	Reason string // 中文可上屏，含实际次数与阈值；不含 Key
}

// normalizeQuota 补齐零值阈值。存在的理由不是"再写一份默认值"，而是把
// 「仓储无行 / 历史脏行」统一成可用值，且默认值的唯一来源仍是 contract.AIQuotaDefaults：
// 这里只做转调，不复制任何数字。零值配置（未配置过阈值的公司）走它即可直接参与判定。
func normalizeQuota(cfg contract.AIQuotaConfig) contract.AIQuotaConfig {
	return contract.NormalizeAIQuota(cfg)
}

// decideQuota 是限流的唯一判定：未启用恒放行；未达上限恒放行；达到上限按动作分流。
//
// 为什么"未启用恒放行"要单独判、而不是把 Enabled 折算成 MaxCalls=∞：
// 默认关闭是 P1 上线不改存量行为的物理保证（PRD §0.2）。用显式分支表达，
// 审查时一眼能看出"关掉就真的什么都不做"，不需要在数值上推演。
//
// 为什么边界是 usedInWindow >= MaxCalls（而不是 >）：MaxCalls 的语义是"窗口内允许的出站次数"，
// 已用 1000 次且上限 1000 时，第 1001 次必须被拦下，否则实际放行数会永远多一次。
func decideQuota(cfg contract.AIQuotaConfig, usedInWindow int) QuotaDecision {
	if !cfg.Enabled {
		return QuotaDecision{Allow: true}
	}
	if usedInWindow < cfg.MaxCalls {
		return QuotaDecision{Allow: true}
	}
	// Reason 只含次数与阈值：这是管理员自己配的数字，可上屏且无敏感信息。
	reason := fmt.Sprintf("本公司在最近 %d 分钟内的模型调用已达 %d 次（阈值 %d 次）",
		cfg.WindowMinutes, usedInWindow, cfg.MaxCalls)
	if cfg.Action == contract.AIQuotaActionReject {
		return QuotaDecision{Allow: false, Reason: reason}
	}
	// warn 是默认动作：超限只告警、照常调用。默认 warn 而非 reject，
	// 是为了让"配了阈值"这件事在灰度期不会变成生产事故。
	return QuotaDecision{Allow: true, Warn: true, Reason: reason}
}
