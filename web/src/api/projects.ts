/**
 * 项目接口的前端唯一调用处。
 * 职责：列出项目、查看详情、新建、编辑（含项目库地址）。
 * 输入：契约生成的 Project / ProjectInput / ProjectDetail 类型。
 * 输出：同样的契约类型。
 * 边界：不做本地缓存（由 hooks/useProjects 负责）、不拼非契约路径。
 */
import type { components } from './schema';
import { get, postJSON, putJSON } from './client';

type Schemas = components['schemas'];

export type Project = Schemas['Project'];
export type ProjectInput = Schemas['ProjectInput'];
export type ProjectDetail = Schemas['ProjectDetail'];

/** 契约路径的前端唯一镜像。 */
const PROJECTS_PATH = '/api/v1/projects';

export function listProjects(): Promise<{ items: Project[] }> {
  return get<{ items: Project[] }>(PROJECTS_PATH);
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
