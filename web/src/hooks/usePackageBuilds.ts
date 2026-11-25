/**
 * 职责：按项目、环境与平台加载应用包，并在编辑、删除后刷新当前列表。
 * 输入：可信页面筛选值与编辑表单；输出：列表三态、刷新及写入方法。
 * 边界：请求统一走 api/builds，不管理筛选 URL、不持有抽屉或确认框状态。
 */
import { useCallback, useEffect, useState } from 'react';
import type { Build, ListQuery, UpdateBuildRequest } from '../api/builds';
import { deleteBuild, listBuilds, updateBuild } from '../api/builds';
import { errorMessage } from '../api/client';

export function usePackageBuilds({ project, environment, platform }: ListQuery) {
  const [state, setState] = useState({ items: [] as Build[], loading: true, error: '' });
  const [revision, setRevision] = useState(0);
  const reload = useCallback(() => setRevision((value) => value + 1), []);

  useEffect(() => {
    if (!project) return;
    let cancelled = false;
    setState({ items: [], loading: true, error: '' });
    listBuilds({ project, environment, platform })
      .then((data) => {
        if (!cancelled) setState({ items: data.items, loading: false, error: '' });
      })
      .catch((err: unknown) => {
        if (!cancelled) setState({ items: [], loading: false, error: errorMessage(err) });
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

  return { ...state, reload, save, remove };
}
