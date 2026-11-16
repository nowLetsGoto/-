/**
 * 当前登录人（/api/v1/me）的唯一读取入口。
 * 职责：取回「我是谁 + 我关联了哪些项目」，供列表页决定默认项目。
 * 输入：无；输出：me 与 loading。
 * 边界：只在会话内缓存一次；不刷新（人员关系变更后重新登录或刷新页面即可生效）。
 */
import { useEffect, useState } from 'react';
import type { Me } from '../api/me';
import { getMe } from '../api/me';

let cache: Me | null = null;
let inflight: Promise<Me> | null = null;

function loadMe(): Promise<Me> {
  if (cache) {
    return Promise.resolve(cache);
  }
  if (!inflight) {
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
}

export function useMe(): { me: Me | null; loading: boolean } {
  const [state, setState] = useState<{ me: Me | null; loading: boolean }>({ me: cache, loading: cache === null });

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
