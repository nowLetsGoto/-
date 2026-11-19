/**
 * 角色与权限点接口。
 * 职责：读权限点目录（前端据此渲染勾选矩阵）、读角色、新建与编辑角色。
 * 输入：契约生成的 Role / RoleInput / Permission 类型；输出：同样的契约类型。
 * 边界：权限点目录由服务端提供，前端**不硬编码**任何权限 key；
 *       写操作仅超管可用（普通账号会拿到 403）。
 */
import type { components } from './schema';
import { del, get, postJSON, putJSON } from './client';

type Schemas = components['schemas'];

export type Role = Schemas['Role'];
export type RoleInput = Schemas['RoleInput'];
export type Permission = Schemas['Permission'];

const ROLES_PATH = '/api/v1/roles';
const PERMISSIONS_PATH = '/api/v1/permissions';

// 注意：这里**没有** super-admin / platform-admin 的角色常量。
// 超级管理员与平台管理员是账号身份（isSuperAdmin / accountLevel），不是角色，
// 它们不会出现在角色列表里。角色只有 project-owner / developer / tester / guest 及自定义角色。

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

/**
 * 删除本公司自定义角色。平台内置角色、超管角色、以及已被人员/项目使用的角色
 * 都会被服务端拒绝（400 并说明原因），前端只需展示那条 message。
 */
export function deleteRole(key: string): Promise<void> {
  return del<void>(`${ROLES_PATH}/${key}`);
}
