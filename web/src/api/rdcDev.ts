/**
 * 研发任务编排层接口的前端唯一调用处。
 * 职责：列出任务（分页 + reqKey + 整体状态）、详情、新建、编辑、删除、推进与跳过。
 * 输入：契约生成的 DevTask / DevTaskDetail / DevTaskInput / DevTaskUpdate 类型与查询条件。
 * 输出：同样的契约类型与统一分页形状 PagedList<DevTask>。
 * 边界：只从 ./schema 引类型、只经 ./client 发请求；路径是后端契约的唯一镜像，
 *       不在这里拼业务参数以外的 URL，也不做权限判断与本地状态机校验。
 */
import type { components } from './schema';
import { get, postJSON, putJSON, del } from './client';
import type { PageQuery, PagedList } from './query';
import { pageQuery } from './query';

type Schemas = components['schemas'];

export type DevSubtaskStatus = Schemas['DevSubtaskStatus'];
export type DevSubtaskKind = Schemas['DevSubtaskKind'];
export type DevSubtask = Schemas['DevSubtask'];
export type DevTask = Schemas['DevTask'];
export type DevTaskDetail = Schemas['DevTaskDetail'];
export type DevTaskInput = Schemas['DevTaskInput'];
export type DevTaskUpdate = Schemas['DevTaskUpdate'];
export type DevSubtaskNoteUpdate = Schemas['DevSubtaskNoteUpdate'];

/** 契约路径的前端唯一镜像，与 contract 的路由常量对齐。 */
const DEV_TASKS_PATH = '/api/v1/rdc/dev/tasks';

export type DevTaskPage = PagedList<DevTask>;

/** 列表查询条件：分页由 pageQuery() 处理，reqKey / status 为空时不写入查询串。 */
export interface DevTaskListQuery extends PageQuery {
  reqKey?: string;
  status?: DevSubtaskStatus;
}

/** 复用 pageQuery() 拼 page/pageSize，再追加需求编号与整体状态。 */
function devTaskQuery(query: DevTaskListQuery): string {
  const base = pageQuery(query);
  const params = new URLSearchParams(base.startsWith('?') ? base.slice(1) : base);
  if (query.reqKey) {
    params.set('reqKey', query.reqKey);
  }
  if (query.status) {
    params.set('status', query.status);
  }
  const text = params.toString();
  return text ? `?${text}` : '';
}

/** 任务列表：数据范围由登录身份决定，前端不传任何公司字段。 */
export function listDevTasks(query: DevTaskListQuery = {}): Promise<DevTaskPage> {
  return get<DevTaskPage>(`${DEV_TASKS_PATH}${devTaskQuery(query)}`);
}

/** 任务详情：含两条子任务与需求投影。 */
export function getDevTask(id: string): Promise<DevTaskDetail> {
  return get<DevTaskDetail>(`${DEV_TASKS_PATH}/${encodeURIComponent(id)}`);
}

/** 新建任务：标题与来源需求由服务端复校，子任务由服务端成对生成。 */
export function createDevTask(input: DevTaskInput): Promise<DevTaskDetail> {
  return postJSON<DevTaskDetail>(DEV_TASKS_PATH, input);
}

/** 编辑任务资料：请求体类型刻意没有 status，任意层级塞状态都会被后端拒绝。 */
export function updateDevTask(id: string, input: DevTaskUpdate): Promise<DevTaskDetail> {
  return putJSON<DevTaskDetail>(`${DEV_TASKS_PATH}/${encodeURIComponent(id)}`, input);
}

/** 删除任务：任务与两条子任务一并删除，不可恢复。 */
export function deleteDevTask(id: string): Promise<null> {
  return del<null>(`${DEV_TASKS_PATH}/${encodeURIComponent(id)}`);
}

/** 手动推进子任务到下一状态（纯平台编排，不触发研发动作）。 */
export function advanceDevSubtask(taskId: string, subtaskId: string): Promise<DevSubtask> {
  return postJSON<DevSubtask>(
    `${DEV_TASKS_PATH}/${encodeURIComponent(taskId)}/subtasks/${encodeURIComponent(subtaskId)}/transition`,
    {},
  );
}

/** 标记子任务为 SKIPPED：永久不可恢复。 */
export function skipDevSubtask(taskId: string, subtaskId: string): Promise<DevSubtask> {
  return postJSON<DevSubtask>(
    `${DEV_TASKS_PATH}/${encodeURIComponent(taskId)}/subtasks/${encodeURIComponent(subtaskId)}/skip`,
    {},
  );
}

/** 驳回后重新研发（rejected → ai_completed）：研发中心触发，回到链路等待再次审核。 */
export function resubmitDevSubtask(taskId: string, subtaskId: string): Promise<DevSubtask> {
  return postJSON<DevSubtask>(
    `${DEV_TASKS_PATH}/${encodeURIComponent(taskId)}/subtasks/${encodeURIComponent(subtaskId)}/resubmit`,
    {},
  );
}
