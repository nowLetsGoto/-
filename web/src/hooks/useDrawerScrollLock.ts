/**
 * 职责：把抽屉的打开、关闭动画及卸载生命周期连接到页面滚动锁。
 * 输入：open；输出：唯一根类名与 afterOpenChange 回调。
 * 边界：关闭动画结束后才解锁；卸载兜底清理，重叠抽屉各自持有锁，不提前放开背景。
 */
import { useCallback, useId, useLayoutEffect, useRef } from 'react';
import { acquireDrawerScrollLock } from './drawerScrollLock';

export function useDrawerScrollLock(open: boolean) {
  const id = useId();
  const rootClassName = `drawer-scroll-${id.replace(/[^a-zA-Z0-9_-]/g, '')}`;
  const unlock = useRef<(() => void) | undefined>();
  const release = useCallback(() => {
    unlock.current?.();
    unlock.current = undefined;
  }, []);

  useLayoutEffect(() => {
    if (open && !unlock.current) unlock.current = acquireDrawerScrollLock(rootClassName);
  }, [open, rootClassName]);
  useLayoutEffect(() => release, [release]);

  const afterOpenChange = useCallback(
    (visible: boolean) => {
      if (!visible && !open) release();
    },
    [open, release],
  );
  return { rootClassName, afterOpenChange };
}
