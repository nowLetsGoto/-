/**
 * 登录会话的本地存储（全仓唯一读写处）。
 * 职责：读写 / 清除 localStorage 中的令牌与用户名。
 * 输入：登录成功后由登录页写入；输出：client.ts 取令牌、Layout 取用户名、路由守卫判断是否已登录。
 * 边界：不发请求、不判断令牌是否过期（有效性以服务端 401 为准）、不存密码。
 */

const STORAGE_KEY = 'dandelion.session';

export type SessionMode = 'solo' | 'team';

export interface Session {
  token: string;
  username: string;
  /** 登录时选定、令牌也带着的视图模式；只用于菜单投影，权限仍由服务端判定。 */
  mode: SessionMode;
}

/** readSession 读取会话；localStorage 被禁用、结构损坏或字段缺失一律当作未登录。 */
export function readSession(): Session | null {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) {
      return null;
    }
    const parsed = JSON.parse(raw) as Partial<Session>;
    if (typeof parsed.token === 'string' && parsed.token !== '' && typeof parsed.username === 'string') {
      // 老会话没有 mode：按"单人模式"这个更保守的默认处理，不会突然放开菜单。
      const mode = parsed.mode === 'team' ? 'team' : 'solo';
      return { token: parsed.token, username: parsed.username, mode };
    }
    return null;
  } catch {
    return null;
  }
}

/** saveSession 覆盖写入当前会话；只在登录成功后调用。 */
export function saveSession(session: Session): void {
  window.localStorage.setItem(STORAGE_KEY, JSON.stringify(session));
}

/** clearSession 清除会话：退出登录与收到 401 时都走这里。 */
export function clearSession(): void {
  window.localStorage.removeItem(STORAGE_KEY);
}
