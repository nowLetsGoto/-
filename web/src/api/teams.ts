/**
 * 团队接口的前端唯一调用处。
 * 职责：列出团队、按 id 取单个、新增、编辑（编辑为全量覆盖，含人员/项目多选）。
 * 输入：契约生成的 Team / TeamInput 类型；输出：同样的契约类型。
 * 边界：不做本地缓存（由 hooks/useDirectory 统一负责）、不拼非契约路径。
 */
import type { components } from './schema';
import { del, get, postJSON, putJSON } from './client';
import type { PageQuery, PagedList } from './query';
import { pageQuery } from './query';

type Schemas = components['schemas'];

export type Team = Schemas['Team'];
export type TeamInput = Schemas['TeamInput'];

const TEAMS_PATH = '/api/v1/teams';

export type TeamPage = PagedList<Team>;

/** 传 { pageSize: 0 } 取全量（下拉选项用）；不传则按服务端默认 10 条分页。 */
export function listTeams(query: PageQuery = {}): Promise<TeamPage> {
  return get<TeamPage>(`${TEAMS_PATH}${pageQuery(query)}`);
}

export function getTeam(id: string): Promise<Team> {
  return get<Team>(`${TEAMS_PATH}/${id}`);
}

export function createTeam(input: TeamInput): Promise<Team> {
  return postJSON<Team>(TEAMS_PATH, input);
}

export function updateTeam(id: string, input: TeamInput): Promise<Team> {
  return putJSON<Team>(`${TEAMS_PATH}/${id}`, input);
}

/**
 * 删除团队。只对**没有任何引用**的团队有效：
 * 还有成员、还关联项目、还挂着管理员账号时，服务端返回 400 并说明还剩哪些关系。
 */
export function deleteTeam(id: string): Promise<void> {
  return del<void>(`${TEAMS_PATH}/${id}`);
}
