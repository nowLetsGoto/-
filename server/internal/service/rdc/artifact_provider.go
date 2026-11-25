// 职责：定义交互层与 Agent 执行器之间的唯一数据源边界，并提供 Group A 生产用的「未就绪」实现。
// 输入：context、任务 / 子任务 id、scope 与检查点；调用方只依赖本接口，不认识任何执行器细节。
// 输出：diff / 计划 / 快照产物，或明确的 ErrArtifactUnavailable；ApplyRollback 仅 Group B 真实现。
// 边界：本文件不写数据库、不写文件、不发网络请求；unavailableProvider 的所有方法都真实失败，
//
//	绝不返回假 diff / 假计划 / 假快照，也不注册任何 HTTP 产物写入端点（杜绝人工灌入模拟数据）。
package rdc

import (
	"context"

	"dandelion/server/internal/contract"
)

// ErrArtifactUnavailable 是 Group A 生产实现的真实报错：执行器尚未就绪，产物暂不可用。
//
// 用 ValidationError 而非自建 sentinel：handler.WriteError 已统一把它翻译成 400 中文文案；
// 读路径会把它折叠成真实空态 data:null，不会把这个技术错误暴露成可点的假按钮。
var ErrArtifactUnavailable = &contract.ValidationError{
	Message: "Agent 执行器尚未就绪，产物暂不可用",
}

// AgentArtifactProvider 是交互层读取产物的唯一契约。
//
// Group A：main.go 注入 unavailableProvider，所有方法返回 ErrArtifactUnavailable。
// Group B：真实执行器实现本接口，在 main.go 一处替换注入；UI / 后端零返工。
type AgentArtifactProvider interface {
	// LatestDiffReview 取某子任务最近一次 AI 动作的 diff 产物。
	LatestDiffReview(ctx context.Context, taskID, subtaskID string, scope contract.Scope) ([]contract.FileDiff, error)
	// LatestPlan 取生成前的结构化问卷与可确认任务列表。
	LatestPlan(ctx context.Context, taskID, subtaskID string, scope contract.Scope) ([]contract.Question, []contract.PlanTask, error)
	// Checkpoints 取文件快照、哈希与服务端签发的回滚令牌。
	Checkpoints(ctx context.Context, taskID, subtaskID string, scope contract.Scope) ([]contract.FileSnapshot, string, error)
	// ApplyRollback 把 beforeContent 写回真实文件；Group A 直接返回未就绪，绝不碰真实文件。
	ApplyRollback(ctx context.Context, checkpoint contract.Checkpoint, scope contract.Scope) error
}

// unavailableProvider 是 Group A 的生产默认实现：零字段、零副作用、零模拟数据。
type unavailableProvider struct{}

// NewUnavailableProvider 返回生产用「未就绪」执行器替身。
// 导出构造函数仅供装配层与测试使用；返回值是值类型，无状态、可并发复用。
func NewUnavailableProvider() AgentArtifactProvider { return unavailableProvider{} }

// LatestDiffReview 真实失败：没有任何 diff 产物可用。
func (unavailableProvider) LatestDiffReview(
	_ context.Context, _, _ string, _ contract.Scope,
) ([]contract.FileDiff, error) {
	return nil, ErrArtifactUnavailable
}

// LatestPlan 真实失败：没有任何计划产物可用。
func (unavailableProvider) LatestPlan(
	_ context.Context, _, _ string, _ contract.Scope,
) ([]contract.Question, []contract.PlanTask, error) {
	return nil, nil, ErrArtifactUnavailable
}

// Checkpoints 真实失败：没有任何快照或回滚令牌可用。
func (unavailableProvider) Checkpoints(
	_ context.Context, _, _ string, _ contract.Scope,
) ([]contract.FileSnapshot, string, error) {
	return nil, "", ErrArtifactUnavailable
}

// ApplyRollback 真实失败：本版只做证据层，绝不写回真实文件。
func (unavailableProvider) ApplyRollback(
	_ context.Context, _ contract.Checkpoint, _ contract.Scope,
) error {
	return ErrArtifactUnavailable
}
