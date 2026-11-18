/**
 * 项目接口的前端唯一调用处。
 * 职责：列出项目、查看详情、新建、编辑（含项目库地址）。
 * 输入：契约生成的 Project / ProjectInput / ProjectDetail 类型。
 * 输出：同样的契约类型。
 * 边界：不做本地缓存（由 hooks/useProjects 负责）、不拼非契约路径。
 */
import type { components } from './schema';
import { del, get, postJSON, putJSON } from './client';
import type { PageQuery, PagedList } from './query';
import { pageQuery } from './query';

type Schemas = components['schemas'];

export type Project = Schemas['Project'];
export type ProjectInput = Schemas['ProjectInput'];
export type ProjectDetail = Schemas['ProjectDetail'];

/** 契约路径的前端唯一镜像。 */
const PROJECTS_PATH = '/api/v1/projects';

export type ProjectPage = PagedList<Project>;

/** 项目列表只需分页：可见范围由登录模式决定，前端不传任何过滤条件。 */
export type ProjectListQuery = PageQuery;

export function listProjects(query: ProjectListQuery = {}): Promise<ProjectPage> {
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

/**
 * 删除项目。只对**没有任何引用**的项目有效：
 * 下面还有应用包、还有成员、还被团队关联时，服务端返回 400 并说明剩余数量。
 * 包是不可恢复的资产，因此不会被级联删除。
 */
export function deleteProject(key: string): Promise<void> {
  return del<void>(`${PROJECTS_PATH}/${key}`);
}
