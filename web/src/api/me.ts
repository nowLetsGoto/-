/**
 * 当前登录人接口。
 * 职责：取回「我是谁 + 我关联了哪些项目」，以及修改自己的密码。
 * 输入：无 / 旧密码 + 新密码；输出：契约 Me（没有对应人员时 personId / projectKeys 为空）。
 * Me 同时携带服务端推导的租户身份：tenantId / tenantName / accountLevel / teamIds，
 * 前端只读它们做展示投影；数据范围以服务端为准，这里不做任何权限判定。
 * 边界：不做缓存（只在首屏与切换项目时用一次）、不携带任何权限判断。
 */
import type { components } from './schema';
import { get, putJSON } from './client';

type Schemas = components['schemas'];

export type Me = Schemas['Me'];
export type PasswordChange = Schemas['PasswordChange'];

const ME_PATH = '/api/v1/me';

export function getMe(): Promise<Me> {
  return get<Me>(ME_PATH);
}

/**
 * 修改自己的密码。成功后服务端会让旧令牌立即失效，
 * 所以调用方必须紧接着清会话并回登录页，不能继续用当前令牌。
 */
export function changeOwnPassword(input: PasswordChange): Promise<null> {
  return putJSON<null>(`${ME_PATH}/password`, input);
}
