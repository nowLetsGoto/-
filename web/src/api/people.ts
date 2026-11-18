/**
 * 人员接口的前端唯一调用处。
 * 职责：列出人员、按 id 取单个、新增、编辑（编辑为全量覆盖，含团队/项目多选）。
 * 输入：契约生成的 Person / PersonInput 类型；输出：同样的契约类型。
 * 边界：不做本地缓存（由 hooks/useDirectory 统一负责）、不拼非契约路径。
 */
import type { components } from './schema';
import { del, get, postJSON, putJSON } from './client';
import type { PageQuery, PagedList } from './query';
import { pageQuery } from './query';

type Schemas = components['schemas'];

export type Person = Schemas['Person'];
export type PersonInput = Schemas['PersonInput'];

const PEOPLE_PATH = '/api/v1/people';

export type PersonPage = PagedList<Person>;

/** 传 { pageSize: 0 } 取全量（下拉选项用）；不传则按服务端默认 10 条分页。 */
export function listPeople(query: PageQuery = {}): Promise<PersonPage> {
  return get<PersonPage>(`${PEOPLE_PATH}${pageQuery(query)}`);
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

/**
 * 删除人员。只对**没有任何引用**的人有效：
 * 仍挂在团队/项目里、或仍被账号绑定时，服务端返回 400 并说明还需解除哪些关系。
 */
export function deletePerson(id: string): Promise<void> {
  return del<void>(`${PEOPLE_PATH}/${id}`);
}
