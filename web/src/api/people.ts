/**
 * 人员接口的前端唯一调用处。
 * 职责：列出人员、按 id 取单个、新增、编辑（编辑为全量覆盖，含团队/项目多选）。
 * 输入：契约生成的 Person / PersonInput 类型；输出：同样的契约类型。
 * 边界：不做本地缓存（由 hooks/useDirectory 统一负责）、不拼非契约路径。
 */
import type { components } from './schema';
import { get, postJSON, putJSON } from './client';

type Schemas = components['schemas'];

export type Person = Schemas['Person'];
export type PersonInput = Schemas['PersonInput'];

const PEOPLE_PATH = '/api/v1/people';

export function listPeople(): Promise<{ items: Person[] }> {
  return get<{ items: Person[] }>(PEOPLE_PATH);
}

export function getPerson(id: string): Promise<Person> {
  return get<Person>(`${PEOPLE_PATH}/${id}`);
}

export function createPerson(input: PersonInput): Promise<Person> {
  return postJSON<Person>(PEOPLE_PATH, input);
}

export function updatePerson(id: string, input: PersonInput): Promise<Person> {
  return putJSON<Person>(`${PEOPLE_PATH}/${id}`, input);
}
