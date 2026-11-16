/**
 * 当前登录人（/api/v1/me）的唯一读取入口。
 * 职责：取回「我是谁 + 我关联了哪些项目」，供列表页决定默认项目。
 * 输入：无；输出：me 与 loading。
 * 边界：只在会话内缓存一次；不刷新（人员关系变更后重新登录或刷新页面即可生效）。
 */
import { useEffect, useState } from 'react';
import { readSession } from '../api/session';
import type { Me } from '../api/me';
import { getMe } from '../api/me';

/** 令牌变了就丢弃缓存，避免换账号后沿用旧菜单。 */
function currentToken(): string | null {
  return readSession()?.token ?? null;
}

// 缓存按令牌隔离：同一浏览器换账号时不会沿用旧账号的菜单权限。
let cacheToken: string | null = null;
let cache: Me | null = null;
let inflight: Promise<Me> | null = null;

function loadMe(): Promise<Me> {
  const token = currentToken();
  if (cache && cacheToken === token) {
    return Promise.resolve(cache);
  }
  if (!inflight || cacheToken !== token) {
    cacheToken = token;
    inflight = getMe()
      .then((me) => {
        cache = me;
        return me;
      })
      .finally(() => {
        inflight = null;
      });
  }
  return inflight;
}

/** 退出登录时调用，避免换账号后沿用上一个人的默认项目。 */
export function clearMe(): void {
  cache = null;
  cacheToken = null;
  inflight = null;
}

export function useMe(): { me: Me | null; loading: boolean } {
  const [state, setState] = useState<{ me: Me | null; loading: boolean }>({
    me: cacheToken === currentToken() ? cache : null,
    loading: cacheToken !== currentToken(),
  });

  useEffect(() => {
    if (cache) {
      return;
    }
    let cancelled = false;
    loadMe()
      .then((me) => {
        if (!cancelled) {
          setState({ me, loading: false });
        }
      })
      .catch(() => {
        // 取不到当前人员不算致命：默认项目会退化到「第一个项目」。
        if (!cancelled) {
          setState({ me: null, loading: false });
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return state;
}
