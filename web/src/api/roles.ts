/**
 * 角色与权限点接口。
 * 职责：读权限点目录（前端据此渲染勾选矩阵）、读角色、新建与编辑角色。
 * 输入：契约生成的 Role / RoleInput / Permission 类型；输出：同样的契约类型。
 * 边界：权限点目录由服务端提供，前端**不硬编码**任何权限 key；
 *       写操作仅超管可用（普通账号会拿到 403）。
 */
import type { components } from './schema';
import { get, postJSON, putJSON } from './client';

type Schemas = components['schemas'];

export type Role = Schemas['Role'];
export type RoleInput = Schemas['RoleInput'];
export type Permission = Schemas['Permission'];

const ROLES_PATH = '/api/v1/roles';
const PERMISSIONS_PATH = '/api/v1/permissions';

/** 超级管理员角色的 key：权限固定为全量，前端据此把勾选矩阵设为只读。 */
export const SUPER_ADMIN_ROLE_KEY = 'super-admin';

export function listPermissions(): Promise<{ items: Permission[] }> {
  return get<{ items: Permission[] }>(PERMISSIONS_PATH);
}

export function listRoles(): Promise<{ items: Role[] }> {
  return get<{ items: Role[] }>(ROLES_PATH);
}

export function getRole(key: string): Promise<Role> {
  return get<Role>(`${ROLES_PATH}/${key}`);
}

export function createRole(input: RoleInput): Promise<Role> {
  return postJSON<Role>(ROLES_PATH, input);
}

export function updateRole(key: string, input: RoleInput): Promise<Role> {
  return putJSON<Role>(`${ROLES_PATH}/${key}`, input);
}
