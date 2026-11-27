/**
 * 职责：按项目、环境与平台加载应用包，并在编辑、删除后刷新当前列表。
 * 输入：可信页面筛选值与编辑表单；输出：列表三态、刷新及写入方法。
 * 边界：请求统一走 api/builds，不管理筛选 URL、不持有抽屉或确认框状态。
 */
import { useCallback, useEffect, useRef, useState } from 'react';
import type { Build, ListQuery, UpdateBuildRequest } from '../api/builds';
import { deleteBuild, listBuilds, updateBuild } from '../api/builds';
import { errorMessage } from '../api/client';

export function usePackageBuilds({ project, environment, platform }: ListQuery) {
  const [state, setState] = useState({ items: [] as Build[], loading: true, error: '' });
  const [revision, setRevision] = useState(0);

  // silent 用 ref 而不是 state：它只是「本次刷新怎么渲染」的标记，
  // 不该成为 effect 的依赖。放依赖数组里的话，silent 从 false 变 true
  // 会与 revision 变化叠加，让同一次轮询发出两次请求。
  const silentRef = useRef(false);

  const reload = useCallback(() => {
    silentRef.current = false;
    setRevision((value) => value + 1);
  }, []);

  // reloadSilently 供后台轮询使用：保留当前列表，不闪骨架屏。
  const reloadSilently = useCallback(() => {
    silentRef.current = true;
    setRevision((value) => value + 1);
  }, []);

  useEffect(() => {
    if (!project) return;
    let cancelled = false;
    const silent = silentRef.current;

    // 静默刷新不清空 items、不转 loading：进度轮询每 5 秒触发一次，
    // 每次都闪骨架屏的话，用户根本看不清进度条在动（实测就是这样）。
    // 非静默时才回到「从零加载」，用于切换筛选或用户主动刷新。
    if (silent) {
      setState((current) => ({ ...current, error: '' }));
    } else {
      setState({ items: [], loading: true, error: '' });
    }

    listBuilds({ project, environment, platform })
      .then((data) => {
        if (!cancelled) setState({ items: data.items, loading: false, error: '' });
      })
      .catch((err: unknown) => {
        // 静默刷新失败时保留旧列表：轮询失败不该把已经显示的内容抹掉。
        if (!cancelled) {
          setState((current) => ({
            items: silent ? current.items : [],
            loading: false,
            error: errorMessage(err),
          }));
        }
      });
    return () => {
      cancelled = true;
    };
  }, [project, environment, platform, revision]);

  const save = useCallback(
    async (id: string, input: UpdateBuildRequest): Promise<void> => {
      await updateBuild(id, input);
      reload();
    },
    [reload],
  );

  const remove = useCallback(async (target: { id: string }): Promise<void> => {
    await deleteBuild(target.id);
  }, []);

  return { ...state, reload, reloadSilently, save, remove };
}
