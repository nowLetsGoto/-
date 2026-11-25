/**
 * 人工审核中心接口的前端唯一调用处。
 * 职责：待审任务列表（分页 + reqKey）、审核详情、审核通过、驳回、提交确认。
 * 输入：契约生成的 ReviewTask / ReviewTaskList / ReviewDetail / DevSubtask 类型与请求体。
 * 输出：同样的契约类型与统一分页形状 PagedList<ReviewTask>。
 * 边界：只从 ./schema 引类型、只经 ./client 发请求；路径是后端契约的唯一镜像，
 *       不做权限判断（由路由守卫与后端负责），不在页面里另拼 URL。
 */
import type { components } from './schema';
import { get, postJSON } from './client';
import type { PageQuery, PagedList } from './query';
import { pageQuery } from './query';

type Schemas = components['schemas'];

export type ReviewTask = Schemas['ReviewTask'];
export type ReviewDetail = Schemas['ReviewDetail'];
export type ReviewRecord = Schemas['ReviewRecord'];
export type ReviewRejectInput = Schemas['ReviewRejectInput'];
export type ReviewCommitInput = Schemas['ReviewCommitInput'];
export type DevSubtask = Schemas['DevSubtask'];

/** 契约路径的前端唯一镜像，与 contract 的审核路由常量对齐。 */
const REVIEW_TASKS_PATH = '/api/v1/rdc/reviews/tasks';

export type ReviewTaskPage = PagedList<ReviewTask>;

/** 列表查询条件：分页由 pageQuery() 处理，reqKey 为空时不写入查询串。 */
export interface ReviewTaskListQuery extends PageQuery {
  reqKey?: string;
}

function reviewTaskQuery(query: ReviewTaskListQuery): string {
  const base = pageQuery(query);
  const params = new URLSearchParams(base.startsWith('?') ? base.slice(1) : base);
  if (query.reqKey) {
    params.set('reqKey', query.reqKey);
  }
  const text = params.toString();
  return text ? `?${text}` : '';
}

/** 待审任务列表：数据范围由登录身份决定，前端不传公司字段。 */
export function listReviewTasks(query: ReviewTaskListQuery = {}): Promise<ReviewTaskPage> {
  return get<ReviewTaskPage>(`${REVIEW_TASKS_PATH}${reviewTaskQuery(query)}`);
}

/** 审核详情：任务、两子任务与审核历史。 */
export function getReviewTask(id: string): Promise<ReviewDetail> {
  return get<ReviewDetail>(`${REVIEW_TASKS_PATH}/${encodeURIComponent(id)}`);
}

function subtaskAction(taskId: string, subtaskId: string, action: string): string {
  return `${REVIEW_TASKS_PATH}/${encodeURIComponent(taskId)}/subtasks/${encodeURIComponent(subtaskId)}/${action}`;
}

/** 审核通过（reviewing → waiting_commit）。 */
export function approveSubtask(taskId: string, subtaskId: string): Promise<DevSubtask> {
  return postJSON<DevSubtask>(subtaskAction(taskId, subtaskId, 'approve'), {});
}

/** 审核驳回（reviewing → rejected），原因必填。 */
export function rejectSubtask(
  taskId: string,
  subtaskId: string,
  reason: string,
): Promise<DevSubtask> {
  const body: ReviewRejectInput = { reason };
  return postJSON<DevSubtask>(subtaskAction(taskId, subtaskId, 'reject'), body);
}

/** 人工提交确认（waiting_commit → committed），commit SHA 必填。 */
export function commitSubtask(
  taskId: string,
  subtaskId: string,
  commitSha: string,
): Promise<DevSubtask> {
  const body: ReviewCommitInput = { commitSha };
  return postJSON<DevSubtask>(subtaskAction(taskId, subtaskId, 'commit'), body);
}
