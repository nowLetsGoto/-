/**
 * 职责：订阅媒体查询，让响应式布局与交互使用相同的断点。
 * 输入：媒体查询字符串；输出：当前是否匹配。
 * 边界：只读取浏览器视口，不持有业务状态；卸载或查询变化时取消订阅。
 */
import { useCallback, useMemo, useSyncExternalStore } from 'react';

export function useMediaQuery(query: string): boolean {
  const media = useMemo(() => window.matchMedia(query), [query]);
  const subscribe = useCallback(
    (notify: () => void) => {
      media.addEventListener('change', notify);
      return () => media.removeEventListener('change', notify);
    },
    [media],
  );
  const snapshot = useCallback(() => media.matches, [media]);
  return useSyncExternalStore(subscribe, snapshot, () => false);
}
