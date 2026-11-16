/**
 * 团队接口的前端唯一调用处。
 * 职责：列出团队、按 id 取单个、新增、编辑（编辑为全量覆盖，含人员/项目多选）。
 * 输入：契约生成的 Team / TeamInput 类型；输出：同样的契约类型。
 * 边界：不做本地缓存（由 hooks/useDirectory 统一负责）、不拼非契约路径。
 */
import type { components } from './schema';
import { get, postJSON, putJSON } from './client';

type Schemas = components['schemas'];

export type Team = Schemas['Team'];
export type TeamInput = Schemas['TeamInput'];

const TEAMS_PATH = '/api/v1/teams';

export function listTeams(): Promise<{ items: Team[] }> {
  return get<{ items: Team[] }>(TEAMS_PATH);
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
