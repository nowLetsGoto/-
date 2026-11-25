// chat_record.go · 调用记录归一：把一次调用的全部事实折成一行 contract.AIModelCallRecord。
// 职责：单点判定 success / error_category / used_upstream / tokens_reported，并把 token 折成指针。
// 输入：租户与场景、当次选中的模型（本地拒绝时为零值）、需求快照、出站账本、调用结果或错误。
// 输出：可直接交给 callRepo.Insert 的记录行；不返回 error（记录是旁路，永远不该阻断调用）。
// 边界：不写 SQL、不碰 Key / 密文、不发请求、不取时钟（created_at 由数据库默认值给）、不判断该不该拦。
package rdc

import (
	"errors"

	"dandelion/server/internal/contract"
)

// callFacts 是一次调用的全部原始事实。用结构体而非长参数列表，是因为这七项总是
// 一起出现、一起被消费，且将来加维度（如分组、重试次数）只改这里一处。
type callFacts struct {
	TenantID string
	Scene    string
	// Record 是当次选中的模型；本地拒绝（限流 / 无匹配）时为零值，落库时 model_id 为 NULL。
	Record contract.AIModelRecord
	Needs  ChatNeeds
	Call   upstreamCall
	Result ChatResult
	Err    error
}

// buildCallRecord 是「一次调用算不算压力、token 是不是真的」的唯一判定点。
//
// 为什么 used_upstream 直接取 call.usedUpstream 而不是在这里重新推导：那个字段只在
// transport() 里发起出站并拿到返回后赋值一次，是全项目「出站已发生」的唯一来源。
// 若在此处补一个 `err == nil || ...` 之类的第二条规则，两个来源一旦分叉，
// 限流窗口就会开始数错——这正是设计要避免的"语义副本"。
//
// 为什么 tokens_reported=false 时 token 写 NULL 而不是 0：上游没报 usage 时我们
// **不知道**用了多少，0 是"确实没用量"这一真实数据的合法取值。把不知道当零，
// 会让容量规划与用量账单系统性偏低，而且无法再从数据里区分两者。
func buildCallRecord(facts callFacts) contract.AIModelCallRecord {
	record := contract.AIModelCallRecord{
		TenantID:              facts.TenantID,
		Scene:                 facts.Scene,
		Success:               facts.Err == nil,
		LatencyMS:             facts.Call.latencyMS,
		UsedUpstream:          facts.Call.usedUpstream,
		UsedDefault:           facts.Record.IsDefault,
		UsedFallback:          facts.Result.UsedFallback,
		RequestedMinContext:   facts.Needs.MinContextTokens,
		RequestedEffort:       facts.Needs.Effort,
		RequestedCapabilities: facts.Needs.Capabilities,
	}
	// 失败时没有目标模型就保持空串：写库靠 NULLIF('')::uuid 落 NULL，绝不伪造 UUID。
	if facts.Record.ID != "" {
		record.ModelID = facts.Record.ID
		record.ModelName = facts.Record.Name
		record.ModelIdentifier = facts.Record.ModelID
		record.Protocol = facts.Record.Protocol
	}
	if facts.Err != nil {
		record.ErrorCategory = string(errorCategory(facts.Err))
	}
	// 上游状态码只在出站后才有意义；本地失败保持 0，与"上游返回 200"区分得开。
	record.StatusCode = facts.Call.statusCode
	record.TokensReported = facts.Result.TokensReported
	if facts.Result.TokensReported {
		prompt, completion := int64(facts.Result.PromptTokens), int64(facts.Result.CompletionTokens)
		record.PromptTokens, record.CompletionTokens = &prompt, &completion
	}
	return record
}

// errorCategory 从错误里取出可落库的分类字符串；非 ChatError 归 response_error。
// 为什么兜底不是空串：记录里"失败但没有类别"会让失败分布统计出现一个无名字的桶，
// 那比归到"响应异常"更不可解释。ChatError 是内部唯一错误形态，兜底只是防御性。
func errorCategory(err error) contract.AIChatCategory {
	var chatErr *ChatError
	if errors.As(err, &chatErr) {
		return chatErr.Category
	}
	return contract.AIChatBadResponse
}
