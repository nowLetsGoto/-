// Package rdc · 研发任务编排层的业务编排。
// 职责：字段校验、需求归属校验、状态机唯一调用、整体状态聚合、审计数据组装。
// 输入：handler 已严格解码的 DTO、调用者身份、scope、任务 / 子任务 id。
// 输出：contract 的 DevTaskDetail / DevTaskList / DevSubtask；校验失败返回 ValidationError，
//
//	越界 / 不存在返回 NotFoundError，陈旧状态更新返回 400。
//
// 边界：不碰 *http.Request / ResponseWriter、不写 SQL、不判权限、不写审计；
//
//	只有 Advance / Skip 调用 contract 的状态机函数——它们是 status 的唯一写入口。
package rdc

import (
	"context"
	"strings"
	"unicode/utf8"

	"dandelion/server/internal/contract"
)

// DevRepository 是研发任务业务层依赖的仓储契约；由 repository/rdc.DevRepository 实现。
// 用接口而非具体类型是为了纯逻辑测试能替换仓储，不必连数据库。
type DevRepository interface {
	CreateWithSubtasks(ctx context.Context, in contract.DevTaskInput, creatorUserID, tenantID string) (contract.DevTaskDetail, error)
	List(ctx context.Context, query contract.DevTaskQuery, scope contract.Scope) (contract.DevTaskList, error)
	FindByID(ctx context.Context, id string, scope contract.Scope) (contract.DevTaskDetail, error)
	FindSubtask(ctx context.Context, taskID, subtaskID string, scope contract.Scope) (contract.DevSubtask, error)
	UpdateTask(ctx context.Context, id string, in contract.DevTaskUpdate, scope contract.Scope) (contract.DevTaskDetail, error)
	UpdateSubtaskNote(ctx context.Context, taskID, subtaskID, note string, scope contract.Scope) (bool, error)
	UpdateStatusIf(ctx context.Context, taskID, subtaskID, from, to string, scope contract.Scope) (bool, error)
	Delete(ctx context.Context, id string, scope contract.Scope) error
}

// RequirementValidator 校验绑定需求是否存在且属当前 scope；由需求仓储实现。
type RequirementValidator interface {
	ExistsVisibleByID(ctx context.Context, id string, scope contract.Scope) bool
}

// DevService 是研发任务的业务入口。
type DevService struct {
	repo         DevRepository
	requirements RequirementValidator
}

// NewDevService 构造研发任务业务层；仓储与需求校验器全部由装配层注入。
func NewDevService(repo DevRepository, requirements RequirementValidator) *DevService {
	return &DevService{repo: repo, requirements: requirements}
}

// Create 校验标题与来源需求归属后建任务，同事务拆出两条 pending 子任务。
func (s *DevService) Create(
	ctx context.Context, caller contract.Identity, scope contract.Scope, in contract.DevTaskInput,
) (contract.DevTaskDetail, error) {
	title, requirementID, err := normalizeDevFields(in.Title, in.RequirementID)
	if err != nil {
		return contract.DevTaskDetail{}, err
	}
	if !s.requirementVisible(ctx, requirementID, scope) {
		return contract.DevTaskDetail{}, &contract.ValidationError{Message: "来源需求无效，请重新选择"}
	}
	detail, err := s.repo.CreateWithSubtasks(ctx, contract.DevTaskInput{
		Title: title, RequirementID: requirementID,
	}, caller.UserID, scope.TenantID)
	if err != nil {
		return contract.DevTaskDetail{}, err
	}
	return withAggregate(detail), nil
}

// List 校验整体状态筛选值后查询分页列表，并逐行填充聚合状态。
func (s *DevService) List(
	ctx context.Context, query contract.DevTaskQuery, scope contract.Scope,
) (contract.DevTaskList, error) {
	if query.Status != "" && !validOverallStatus(query.Status) {
		return contract.DevTaskList{}, &contract.ValidationError{Message: "整体状态筛选不合法"}
	}
	result, err := s.repo.List(ctx, query, scope)
	if err != nil {
		return contract.DevTaskList{}, err
	}
	for index := range result.Items {
		result.Items[index].OverallStatus = overallStatus(result.Items[index].FrontendStatus, result.Items[index].BackendStatus)
	}
	return result, nil
}

// Get 按 id 查详情；非法 UUID 与越界同为 404，不泄漏存在性。
func (s *DevService) Get(ctx context.Context, id string, scope contract.Scope) (contract.DevTaskDetail, error) {
	if !contract.IsValidUUID(id) {
		return contract.DevTaskDetail{}, contract.NotFound(contract.ResourceDevTask)
	}
	detail, err := s.repo.FindByID(ctx, id, scope)
	if err != nil {
		return contract.DevTaskDetail{}, err
	}
	return withAggregate(detail), nil
}

// Update 编辑任务名 / 换绑需求 / 子任务备注。
// 本方法从不接触状态：DTO 无 status 字段，仓储 SET 列表也无 status。
func (s *DevService) Update(
	ctx context.Context, id string, in contract.DevTaskUpdate, scope contract.Scope,
) (contract.DevTaskDetail, error) {
	if !contract.IsValidUUID(id) {
		return contract.DevTaskDetail{}, contract.NotFound(contract.ResourceDevTask)
	}
	title, requirementID, err := normalizeDevFields(in.Title, in.RequirementID)
	if err != nil {
		return contract.DevTaskDetail{}, err
	}
	notes, err := normalizeDevNotes(in.Subtasks)
	if err != nil {
		return contract.DevTaskDetail{}, err
	}
	if !s.requirementVisible(ctx, requirementID, scope) {
		return contract.DevTaskDetail{}, &contract.ValidationError{Message: "来源需求无效，请重新选择"}
	}
	detail, err := s.repo.UpdateTask(ctx, id, contract.DevTaskUpdate{
		Title: title, RequirementID: requirementID, Subtasks: notes,
	}, scope)
	if err != nil {
		return contract.DevTaskDetail{}, err
	}
	return withAggregate(detail), nil
}

// Delete 删除任务；子任务由数据库外键级联删除，越界 / 不存在一律 404。
func (s *DevService) Delete(ctx context.Context, id string, scope contract.Scope) error {
	if !contract.IsValidUUID(id) {
		return contract.NotFound(contract.ResourceDevTask)
	}
	return s.repo.Delete(ctx, id, scope)
}

// Advance 把子任务合法前进一步。状态写入的唯一入口之一：
// from 与 to 必须在同一次 findSubtask 快照上计算，再以 WHERE status=from 条件更新；
// 返回值中的 from 就是实际用于条件更新的值，供审计 detail 使用，不能在 handler 另做预读。
func (s *DevService) Advance(
	ctx context.Context, taskID, subtaskID string, scope contract.Scope,
) (contract.DevSubtask, string, error) {
	subtask, err := s.findSubtask(ctx, taskID, subtaskID, scope)
	if err != nil {
		return contract.DevSubtask{}, "", err
	}
	from := subtask.Status
	to, ok := contract.DevNextStatus(from)
	if !ok {
		return contract.DevSubtask{}, "", &contract.ValidationError{Message: "只能推进到下一状态"}
	}
	updated, err := s.writeStatus(ctx, taskID, subtaskID, from, to, scope, "状态已变化，请刷新后重试")
	if err != nil {
		return contract.DevSubtask{}, "", err
	}
	return updated, from, nil
}

// Skip 把未完成子任务标为 SKIPPED。状态写入的另一唯一入口：
// 与 Advance 一样在单次快照上确定 from/to，返回实际写入条件里的 from 供审计留痕。
func (s *DevService) Skip(
	ctx context.Context, taskID, subtaskID string, scope contract.Scope,
) (contract.DevSubtask, string, error) {
	subtask, err := s.findSubtask(ctx, taskID, subtaskID, scope)
	if err != nil {
		return contract.DevSubtask{}, "", err
	}
	from := subtask.Status
	if !contract.CanSkipDevStatus(from) {
		return contract.DevSubtask{}, "", &contract.ValidationError{Message: "终态子任务不可再标跳过"}
	}
	updated, err := s.writeStatus(ctx, taskID, subtaskID, from, contract.DevStatusSkipped, scope,
		"状态已变化，请刷新后重试")
	if err != nil {
		return contract.DevSubtask{}, "", err
	}
	return updated, from, nil
}

// ApplyReviewAction 执行一次人工审核动作（approve/reject/commit/resubmit）。
// 与 Advance/Skip 完全同构：先过 contract.ReviewActionTarget 校验，
// 在单次 findSubtask 快照上确定 from/to，再复用 writeStatus 条件更新；
// 返回的子任务与 from 供审核记录与审计使用，保证 from/to 同源、并发下无跳级。
func (s *DevService) ApplyReviewAction(
	ctx context.Context, action, taskID, subtaskID string, scope contract.Scope,
) (contract.DevSubtask, string, error) {
	if !contract.IsValidReviewAction(action) {
		return contract.DevSubtask{}, "", &contract.ValidationError{Message: "未知的审核动作"}
	}
	subtask, err := s.findSubtask(ctx, taskID, subtaskID, scope)
	if err != nil {
		return contract.DevSubtask{}, "", err
	}
	from := subtask.Status
	to, ok := contract.ReviewActionTarget(action, from)
	if !ok {
		return contract.DevSubtask{}, "", &contract.ValidationError{Message: "当前状态不允许该审核操作"}
	}
	updated, err := s.writeStatus(ctx, taskID, subtaskID, from, to, scope, "状态已变化，请刷新后重试")
	if err != nil {
		return contract.DevSubtask{}, "", err
	}
	return updated, from, nil
}

// writeStatus 执行一次条件状态更新并回读最新行，供 Advance / Skip / ApplyReviewAction 共用。
func (s *DevService) writeStatus(
	ctx context.Context, taskID, subtaskID, from, to string, scope contract.Scope, staleMessage string,
) (contract.DevSubtask, error) {
	ok, err := s.repo.UpdateStatusIf(ctx, taskID, subtaskID, from, to, scope)
	if err != nil {
		return contract.DevSubtask{}, err
	}
	if !ok {
		return contract.DevSubtask{}, &contract.ValidationError{Message: staleMessage}
	}
	updated, err := s.repo.FindSubtask(ctx, taskID, subtaskID, scope)
	if err != nil {
		return contract.DevSubtask{}, err
	}
	// 回读期间其它请求可能已继续推进。响应与审计都应描述"本次条件更新写入的 to"，
	// 否则 handler 用 from + updated.Status 会拼出状态机中不存在的跳级边。
	updated.Status = to
	return updated, nil
}

// findSubtask 校验两个 UUID 后读取子任务；非法 id 一律 404。
func (s *DevService) findSubtask(
	ctx context.Context, taskID, subtaskID string, scope contract.Scope,
) (contract.DevSubtask, error) {
	if !contract.IsValidUUID(taskID) || !contract.IsValidUUID(subtaskID) {
		return contract.DevSubtask{}, contract.NotFound(contract.ResourceDevSubtask)
	}
	return s.repo.FindSubtask(ctx, taskID, subtaskID, scope)
}

// requirementVisible 在校验器缺失或 id 不可见时统一失败关闭。
func (s *DevService) requirementVisible(ctx context.Context, id string, scope contract.Scope) bool {
	return s.requirements != nil && s.requirements.ExistsVisibleByID(ctx, id, scope)
}

// normalizeDevFields 校验必填标题与 UUID 需求 id，返回可直接落库的值。
func normalizeDevFields(title, requirementID string) (string, string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", "", &contract.ValidationError{Message: "任务名称不能为空"}
	}
	if utf8.RuneCountInString(title) > contract.MaxDevTitleLen {
		return "", "", &contract.ValidationError{Message: "任务名称不能超过 100 字"}
	}
	requirementID = strings.TrimSpace(requirementID)
	if !contract.IsValidUUID(requirementID) {
		return "", "", &contract.ValidationError{Message: "来源需求无效，请重新选择"}
	}
	return title, requirementID, nil
}

// normalizeDevNotes 复校子任务备注 id 与长度；空白备注保留为空串，不改状态。
func normalizeDevNotes(notes []contract.DevSubtaskNoteUpdate) ([]contract.DevSubtaskNoteUpdate, error) {
	result := make([]contract.DevSubtaskNoteUpdate, 0, len(notes))
	for _, note := range notes {
		if !contract.IsValidUUID(note.ID) {
			return nil, &contract.ValidationError{Message: "子任务标识无效"}
		}
		if utf8.RuneCountInString(note.Note) > contract.MaxDevNoteLen {
			return nil, &contract.ValidationError{Message: "子任务备注不能超过 500 字"}
		}
		result = append(result, contract.DevSubtaskNoteUpdate{
			ID: note.ID, Note: strings.TrimSpace(note.Note),
		})
	}
	return result, nil
}

// validOverallStatus 只允许八值整体态；skipped 是子任务终态，不是任务整体态。
func validOverallStatus(status string) bool {
	for _, item := range contract.DevSubtaskStatuses[:8] {
		if item == status {
			return true
		}
	}
	return false
}

// overallStatus 是列表行的聚合口径，展示唯一来源是 contract.AggregateDevStatus。
func overallStatus(frontend, backend string) string {
	return contract.AggregateDevStatus([]string{frontend, backend})
}

// withAggregate 给详情补上聚合状态，并保证 Subtasks 非 nil。
func withAggregate(detail contract.DevTaskDetail) contract.DevTaskDetail {
	if detail.Subtasks == nil {
		detail.Subtasks = make([]contract.DevSubtask, 0, 2)
	}
	statuses := make([]string, 0, len(detail.Subtasks))
	for _, subtask := range detail.Subtasks {
		statuses = append(statuses, subtask.Status)
	}
	detail.OverallStatus = contract.AggregateDevStatus(statuses)
	return detail
}
