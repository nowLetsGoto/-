/**
 * 项目接口的前端唯一调用处。
 * 职责：列出项目、查看详情、新建、编辑（含项目库地址）。
 * 输入：契约生成的 Project / ProjectInput / ProjectDetail 类型。
 * 输出：同样的契约类型。
 * 边界：不做本地缓存（由 hooks/useProjects 负责）、不拼非契约路径。
 */
import type { components } from './schema';
import { get, postJSON, putJSON } from './client';
import type { PageQuery, PagedList } from './query';
import { pageQuery } from './query';

type Schemas = components['schemas'];

export type Project = Schemas['Project'];
export type ProjectInput = Schemas['ProjectInput'];
export type ProjectDetail = Schemas['ProjectDetail'];

/** 契约路径的前端唯一镜像。 */
const PROJECTS_PATH = '/api/v1/projects';

export type ProjectPage = PagedList<Project>;

/** 传 { pageSize: 0 } 取全量（下拉与守卫用）；不传则按服务端默认 10 条分页。 */
export function listProjects(query: PageQuery = {}): Promise<ProjectPage> {
  return get<ProjectPage>(`${PROJECTS_PATH}${pageQuery(query)}`);
}

export function getProject(key: string): Promise<ProjectDetail> {
  return get<ProjectDetail>(`${PROJECTS_PATH}/${key}`);
}

export function createProject(input: ProjectInput): Promise<Project> {
  return postJSON<Project>(PROJECTS_PATH, input);
}

export function updateProject(key: string, input: ProjectInput): Promise<Project> {
  return putJSON<Project>(`${PROJECTS_PATH}/${key}`, input);
}
