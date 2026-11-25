// chat_select.go · 选模纯函数：需求归一 + 硬筛 + 稳定排序，全程无 IO。
// 职责：把「调用方声明的能力要求」与「公司已启用模型清单」做确定性匹配，选出唯一模型。
// 输入：ChatRequest 的三个可选条件（原始值）、同租户启用模型全量（仓储已保证租户与 enabled）。
// 输出：命中的 AIModelRecord，或 capability_mismatch 分类错误；不返回任何 IO 错误。
// 边界：不查库、不取时间、不写日志、不认识 http/sql/context；本文件出现 context 参数即越界。
package rdc

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"dandelion/server/internal/contract"
)

// ChatNeeds 是归一后的需求快照，也是"要不要走选模"的唯一开关载体。
type ChatNeeds struct {
	Capabilities     []string
	MinContextTokens int
	Effort           string
	EffortRank       int
}

// Declared 表示调用方是否声明了任何能力条件。
//
// 为什么判定是"三者全零"而不是"任一非零"：P1 的三个字段是纯增量，老调用方一个都不传，
// 那种情况下必须逐字节等同 P0 行为（同一 SQL、同一 ORDER BY）。若改成"任一非零才特殊处理"，
// 语义上等价但会让"传了空 slice"与"没传"产生歧义；用全零判定把歧义消掉，
// 且它是 chatOnce 之前唯一的分叉点，改一处就能影响全链路，便于审查。
func (n ChatNeeds) Declared() bool {
	return len(n.Capabilities) > 0 || n.MinContextTokens > 0 || n.Effort != ""
}

// normalizeNeeds 只做"裁剪 / 去重 / 合法性校验"，不做任何筛选。
// 非法入参返回 parameter_error：那是调用方 bug，不该落统计、更不该出站浪费一次上游调用。
func normalizeNeeds(in ChatRequest) (ChatNeeds, error) {
	needs := ChatNeeds{
		Capabilities:     normalizeCapabilities(in.Capabilities),
		MinContextTokens: in.MinContextTokens,
		Effort:           strings.TrimSpace(in.ReasoningEffort),
	}
	if needs.MinContextTokens < 0 {
		return ChatNeeds{}, newChatError(contract.AIChatBadParameter, 0)
	}
	for _, capability := range needs.Capabilities {
		if !slices.Contains(contract.AICapabilities, capability) {
			return ChatNeeds{}, newChatError(contract.AIChatBadParameter, 0)
		}
	}
	if needs.Effort != "" {
		needs.EffortRank = contract.AIReasoningRank(needs.Effort)
		if needs.EffortRank < 0 {
			return ChatNeeds{}, newChatError(contract.AIChatBadParameter, 0)
		}
	}
	return needs, nil
}

// normalizeCapabilities 去空白、去空串、去重并保持首次出现顺序：能力是集合语义，
// 顺序无意义但保持稳定能让错误文案与日志可复现。
func normalizeCapabilities(raw []string) []string {
	normalized := make([]string, 0, len(raw))
	for _, item := range raw {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" || slices.Contains(normalized, trimmed) {
			continue
		}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

// selectModel 是选模的唯一实现：硬筛 → 稳定排序 → 取首，绝不静默降级。
//
// 为什么是"硬筛 + 稳定排序"而不是打分：打分天然是"尽量满足"，会返回一个不满足要求的模型；
// 请求视觉能力却拿到纯文本模型，错误会推迟到业务层才暴露，是最难查的一类 bug。
// 硬筛让"不满足即报错"，把问题钉在调度层，也把可解释性做到最好（排序键只有两个，与 P0 同序）。
//
// 筛选顺序语义上不可交换：enabled（调用方已保证）→ context ≥ → effort ≥ → capabilities AND。
// 先筛便宜的标量比较、后筛集合运算，也让无候选时的诊断信息更准确。
func selectModel(candidates []contract.AIModelRecord, needs ChatNeeds) (contract.AIModelRecord, error) {
	matched := make([]contract.AIModelRecord, 0, len(candidates))
	for _, model := range candidates {
		if !model.Enabled {
			continue // 仓储已过滤，这里再判一次：让本函数可脱离 DB 单测
		}
		if needs.MinContextTokens > 0 && model.ContextTokens < needs.MinContextTokens {
			// 上下文是容量上限：声明"至少要 16K"时 32K 的模型完全能承载，不能因为"不精确相等"被排除。
			// 反过来取"最接近"是错的——容量不足的模型会直接截断请求，比报错更难定位。
			continue
		}
		if needs.EffortRank > 0 && contract.AIReasoningRank(contract.AIModelOfferedEffort(model.AIModel)) < needs.EffortRank {
			// 强度同样用 ≥：声明 low 的请求用 high 模型能跑（只是多花钱），用 none 模型则能力不足。
			// 若改成精确相等，管理员把某模型档位调高后低档请求会突然失配，属隐式耦合。
			continue
		}
		if !hasAllCapabilities(model.Capabilities, needs.Capabilities) {
			// 能力标签是布尔事实（有没有视觉），不存在"更多"，所以必须精确包含（AND）。
			continue
		}
		matched = append(matched, model)
	}
	if len(matched) == 0 {
		return contract.AIModelRecord{}, newCapabilityMismatch(candidates, needs)
	}
	// 排序键与 P0 SQL 逐字同序（is_default DESC, created_at ASC），两处引用同一注释锚点：
	// 仓储 ai_model_repository.go 的 aiEnabledOrder。字符串 RFC3339 可直接比较。
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].IsDefault != matched[j].IsDefault {
			return matched[i].IsDefault
		}
		return matched[i].CreatedAt < matched[j].CreatedAt
	})
	return matched[0], nil
}

// hasAllCapabilities 判断模型是否具备请求的全部能力标签（AND 语义，请求为空即恒真）。
func hasAllCapabilities(offered, requested []string) bool {
	for _, item := range requested {
		if !slices.Contains(offered, item) {
			return false
		}
	}
	return true
}

// newCapabilityMismatch 组装"配了但不够用"的错误，文案只列调用方**声明了的**维度，
// 并附上公司实际最大值 —— 用户据此就能知道该补什么配置，不必来问客服。
func newCapabilityMismatch(candidates []contract.AIModelRecord, needs ChatNeeds) error {
	details := make([]string, 0, 3)
	if needs.MinContextTokens > 0 {
		details = append(details, fmt.Sprintf("已声明上下文 ≥ %d（公司最高 %d）",
			needs.MinContextTokens, maxContextTokens(candidates)))
	}
	if needs.EffortRank > 0 {
		details = append(details, fmt.Sprintf("已声明思考强度 ≥ %s（公司最高 %s）",
			needs.Effort, maxOfferedEffort(candidates)))
	}
	if len(needs.Capabilities) > 0 {
		details = append(details, fmt.Sprintf("已声明能力 %s（公司现有 %s）",
			strings.Join(needs.Capabilities, "、"), strings.Join(unionCapabilities(candidates), "、")))
	}
	message := contract.AIChatMessage(contract.AIChatCapabilityMismatch)
	if len(details) > 0 {
		message += "：" + strings.Join(details, "；")
	}
	// Cause 只存分类事实（不含公司数据与请求值），与 ChatError.Error() 的可读文案解耦。
	return &ChatError{
		Category: contract.AIChatCapabilityMismatch,
		Message:  message,
		Cause:    errors.New(string(contract.AIChatCapabilityMismatch)),
	}
}

// maxContextTokens 返回候选里的最大窗口；空集返回 0（"一个都没配"本身就是可解释的答案）。
func maxContextTokens(candidates []contract.AIModelRecord) int {
	maximal := 0
	for _, model := range candidates {
		if model.ContextTokens > maximal {
			maximal = model.ContextTokens
		}
	}
	return maximal
}

// maxOfferedEffort 返回候选里可提供的最高档位；空集返回 none。
func maxOfferedEffort(candidates []contract.AIModelRecord) string {
	best := contract.AIReasoningNone
	for _, model := range candidates {
		offered := contract.AIModelOfferedEffort(model.AIModel)
		if contract.AIReasoningRank(offered) > contract.AIReasoningRank(best) {
			best = offered
		}
	}
	return best
}

// unionCapabilities 汇总候选具备的能力标签（按 contract.AICapabilities 顺序，便于稳定展示）。
func unionCapabilities(candidates []contract.AIModelRecord) []string {
	union := make([]string, 0, len(contract.AICapabilities))
	for _, capability := range contract.AICapabilities {
		for _, model := range candidates {
			if slices.Contains(model.Capabilities, capability) {
				union = append(union, capability)
				break
			}
		}
	}
	return union
}
