/**
 * 账号管理接口（仅超级管理员可用）。
 * 职责：列表 / 新建 / 编辑 / 重置密码 / 启用禁用。
 * 输入：契约生成的 User 相关类型；输出：同样的契约类型。
 * 边界：不做缓存（账号变动频繁，且列表页本就分页）；非超管调用会拿到 403。
 */
import type { components } from './schema';
import { get, postJSON, putJSON } from './client';
import type { PageQuery, PagedList } from './query';
import { pageQuery } from './query';

type Schemas = components['schemas'];

export type User = Schemas['User'];
export type UserInput = Schemas['UserInput'];
export type UserUpdate = Schemas['UserUpdate'];

const USERS_PATH = '/api/v1/admin/users';

/** 列表：keyword 同时匹配登录名与绑定人员姓名。 */
export function listUsers(query: PageQuery & { keyword?: string } = {}): Promise<PagedList<User>> {
  const base = pageQuery(query);
  const keyword = query.keyword?.trim();
  const separator = base ? '&' : '?';
  const suffix = keyword ? `${separator}keyword=${encodeURIComponent(keyword)}` : '';
  return get<PagedList<User>>(`${USERS_PATH}${base}${suffix}`);
}

export function createUser(input: UserInput): Promise<User> {
  return postJSON<User>(USERS_PATH, input);
}

export function updateUser(id: string, input: UserUpdate): Promise<User> {
  return putJSON<User>(`${USERS_PATH}/${id}`, input);
}

/** 重置密码：返回一次性临时密码，只在这一个响应里出现。 */
export function resetUserPassword(id: string): Promise<{ password: string }> {
  return postJSON<{ password: string }>(`${USERS_PATH}/${id}/reset-password`, {});
}

export function setUserStatus(id: string, status: 'active' | 'disabled'): Promise<User> {
  return postJSON<User>(`${USERS_PATH}/${id}/status`, { status });
}
