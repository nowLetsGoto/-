/**
 * 登录接口的前端唯一调用处。
 * 职责：把用户名 / 密码提交给 /api/v1/auth/login，返回契约生成的 LoginResult。
 * 输入：LoginRequest（恰好 username、password 两个字段）；输出：LoginResult（token / username / expiresAt）。
 * 边界：不写 localStorage（会话归 api/session.ts）、不做表单校验与跳转（页面负责）。
 */
import type { components } from './schema';
import { postJSON } from './client';

type Schemas = components['schemas'];

export type LoginRequest = Schemas['LoginRequest'];
export type LoginResult = Schemas['LoginResult'];

/** 契约路径的前端唯一镜像；页面不得自行拼 /api/v1 路径。 */
const LOGIN_PATH = '/api/v1/auth/login';

export function login(input: LoginRequest): Promise<LoginResult> {
  return postJSON<LoginResult>(LOGIN_PATH, input);
}
