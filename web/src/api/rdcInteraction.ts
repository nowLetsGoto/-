/**
 * AI 人机交互层接口的前端唯一调用处。
 * 职责：Diff 审查（读 / 提交 / 单 hunk 决策）、计划确认（读 / 确认）、
 *       检查点（列表 / 回滚）、分级审批（读 / 决定）共 9 个操作。
 * 输入：契约生成的类型与请求体，路径参数由调用方传入。
 * 输出：同样的契约类型；provider 未就绪时 GET 返回 null（由页面显示真实空态）。
 * 边界：类型只从 ./schema 引、请求只经 ./client；路径是本契约的唯一镜像，
 *       不拼业务 URL、不做权限判断、不在这里伪造任何产物。
 */
import type { components } from './schema';
import { get, postJSON, putJSON } from './client';

type Schemas = components['schemas'];

export type DiffReview = Schemas['DiffReview'];
export type FileDiff = Schemas['FileDiff'];
export type Hunk = Schemas['Hunk'];
export type HunkDecision = Schemas['HunkDecision'];
export type DiffReviewInput = Schemas['DiffReviewInput'];
export type Question = Schemas['Question'];
export type PlanTask = Schemas['PlanTask'];
export type PlanConfirmation = Schemas['PlanConfirmation'];
export type PlanConfirmInput = Schemas['PlanConfirmInput'];
export type FileSnapshot = Schemas['FileSnapshot'];
export type Checkpoint = Schemas['Checkpoint'];
export type RollbackInput = Schemas['RollbackInput'];
export type ApprovalView = Schemas['ApprovalView'];
export type ApprovalDecideInput = Schemas['ApprovalDecideInput'];
export type Decision = Schemas['ReviewDecision'];
export type FileStatus = Schemas['InteractionFileStatus'];
export type RiskLevel = Schemas['RiskLevel'];
export type Intervention = Schemas['Intervention'];
export type CheckpointStatus = Schemas['CheckpointStatus'];

/** 契约路径的前端唯一镜像，与 contract 的 9 个路由常量逐字对应。 */
const BASE = '/api/v1/rdc/interactions';

/** 任务 + 子任务路径段；id 只从真实任务详情取得，不在此校验。 */
function taskSubtask(taskId: string, subtaskId: string): string {
  return `${BASE}/tasks/${encodeURIComponent(taskId)}/subtasks/${encodeURIComponent(subtaskId)}`;
}

/** ① 读取 Diff 审查；执行器未就绪时返回 null，页面显示真实空态。 */
export function getDiffReview(taskId: string, subtaskId: string): Promise<DiffReview | null> {
  return get<DiffReview | null>(`${taskSubtask(taskId, subtaskId)}/diff-review`);
}

/** ② 提交整份 hunk 决策；存在 pending 时后端 400，不写真实文件。 */
export function submitDiffReview(
  taskId: string,
  subtaskId: string,
  input: DiffReviewInput,
): Promise<DiffReview> {
  return postJSON<DiffReview>(`${taskSubtask(taskId, subtaskId)}/diff-review/submit`, input);
}

/** ③ 单 hunk 三态决策；只写决策表，点击不写审核记录。 */
export function setHunkDecision(
  reviewId: string,
  hunkId: string,
  decision: Decision,
): Promise<DiffReview> {
  return putJSON<DiffReview>(
    `${BASE}/diff-reviews/${encodeURIComponent(reviewId)}/hunks/${encodeURIComponent(hunkId)}/decision`,
    { decision },
  );
}

/** ④ 读取生成前计划确认；无真实产物时返回 null。 */
export function getPlanConfirmation(
  taskId: string,
  subtaskId: string,
): Promise<PlanConfirmation | null> {
  return get<PlanConfirmation | null>(`${taskSubtask(taskId, subtaskId)}/plan-confirmation`);
}

/** ⑤ 确认计划门禁；必答题与计划任务未全部完成会被 400 拒绝。 */
export function confirmPlan(planId: string, input: PlanConfirmInput): Promise<PlanConfirmation> {
  return postJSON<PlanConfirmation>(
    `${BASE}/plan-confirmations/${encodeURIComponent(planId)}/confirm`,
    input,
  );
}

/** ⑥ 读取检查点时间线；无产物时为空数组。 */
export function listCheckpoints(taskId: string, subtaskId: string): Promise<Checkpoint[]> {
  return get<Checkpoint[]>(`${taskSubtask(taskId, subtaskId)}/checkpoints`);
}

/** ⑦ 回滚检查点；哈希不匹配时后端 409 并给出冲突文件，不提供强制覆盖。 */
export function rollbackCheckpoint(
  checkpointId: string,
  input: RollbackInput,
): Promise<Checkpoint> {
  return postJSON<Checkpoint>(
    `${BASE}/checkpoints/${encodeURIComponent(checkpointId)}/rollback`,
    input,
  );
}

/** ⑧ 读取分级审批徽标：整体风险取最大、风险映射介入强度。 */
export function getApproval(taskId: string, subtaskId: string): Promise<ApprovalView | null> {
  return get<ApprovalView | null>(`${taskSubtask(taskId, subtaskId)}/approval`);
}

/** ⑨ 记录审批决定；是业务介入强度证据，不改子任务状态。 */
export function decideApproval(
  taskId: string,
  subtaskId: string,
  input: ApprovalDecideInput,
): Promise<ApprovalView> {
  return postJSON<ApprovalView>(`${taskSubtask(taskId, subtaskId)}/approval/decide`, input);
}
