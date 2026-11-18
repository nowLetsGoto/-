/**
 * 登录会话的本地存储（全仓唯一读写处）。
 * 职责：读写 / 清除 localStorage 中的令牌与用户名；
 *       并在会话切换（登录 / 登出 / 401）时清掉**与身份绑定**的本地快照。
 * 输入：登录成功后由登录页写入；输出：client.ts 取令牌、Layout 取用户名、路由守卫判断是否已登录。
 * 边界：不发请求、不判断令牌是否过期（有效性以服务端 401 为准）、不存密码。
 */

const STORAGE_KEY = 'dandelion.session';

/**
 * 与登录身份绑定的本地键：换账号时必须清掉，否则会带着上一个账号的状态去请求。
 *
 * dandelion.project 是「最近访问的项目 key」。它按浏览器存，不按账号存——
 * 甲账号看过的项目 key 会被乙账号继承。而乙账号可能根本没有那个项目
 * （最典型：超级管理员/新建公司的平台管理员，压根没有可见项目），
 * 于是首屏拿着失效 key 请求 /builds，后端只能回「项目不存在」。
 *
 * 这里集中列出而不是让各模块自己清：漏清一个就会复现同一类问题。
 */
const IDENTITY_SCOPED_KEYS = ['dandelion.project'];

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
    if (
      typeof parsed.token === 'string' &&
      parsed.token !== '' &&
      typeof parsed.username === 'string'
    ) {
      // 老会话没有 mode：按"单人模式"这个更保守的默认处理，不会突然放开菜单。
      const mode = parsed.mode === 'team' ? 'team' : 'solo';
      return { token: parsed.token, username: parsed.username, mode };
    }
    return null;
  } catch {
    return null;
  }
}

/**
 * saveSession 覆盖写入当前会话；只在登录成功后调用。
 *
 * 若写入的是**另一个身份**（换了用户名或模式），先清掉上一个身份的快照：
 * 同一个浏览器登录不同账号，页面级缓存与会话无关，留着就是数据串味。
 * 同一身份重登（刷新令牌）不清理，避免把用户"最近访问的项目"无谓地重置。
 */
export function saveSession(session: Session): void {
  const previous = readSession();
  const switched =
    previous !== null && (previous.username !== session.username || previous.mode !== session.mode);
  if (switched) {
    clearIdentityScopedState();
  }
  window.localStorage.setItem(STORAGE_KEY, JSON.stringify(session));
}

/** clearSession 清除会话：退出登录与收到 401 时都走这里。 */
export function clearSession(): void {
  window.localStorage.removeItem(STORAGE_KEY);
  // 登出同样要清：否则下一次登录会继承这次的"最近项目"。
  clearIdentityScopedState();
}

/** 清掉与登录身份绑定的本地快照；localStorage 被禁用时静默跳过。 */
export function clearIdentityScopedState(): void {
  try {
    for (const key of IDENTITY_SCOPED_KEYS) {
      window.localStorage.removeItem(key);
    }
  } catch {
    // 禁用 localStorage 的环境下本来也读不到旧值，无需处理。
  }
}
