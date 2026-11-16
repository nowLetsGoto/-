/**
 * 一次取回四个环境的包列表，供概览与应用页做聚合。
 * 职责：并发调用 listBuilds，返回每个环境的 items / total，并统一 loading 与 error。
 * 输入：无（环境取值来自契约枚举）；输出：EnvGroup[]、三态与 reload 回调。
 * 边界：不做聚合计算（留给使用方），不发其他请求；这里只负责"取全四个环境"。
 */
import { useEffect, useState } from 'react';
import type { Build, Environment } from '../api/builds';
import { listBuilds } from '../api/builds';
import { errorMessage } from '../api/client';
import { ENVIRONMENTS } from '../utils/format';

export interface EnvGroup {
  environment: Environment;
  items: Build[];
  total: number;
}

interface AllBuilds {
  loading: boolean;
  error: string;
  groups: EnvGroup[];
  reload: () => void;
}

const EMPTY: Omit<AllBuilds, 'reload'> = { loading: true, error: '', groups: [] };

export function useAllBuilds(): AllBuilds {
  const [state, setState] = useState<Omit<AllBuilds, 'reload'>>(EMPTY);
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setState(EMPTY);
    Promise.all(
      ENVIRONMENTS.map(async (item) => {
        const result = await listBuilds({ environment: item.value, platform: 'all' });
        return { environment: item.value, items: result.items, total: result.total };
      }),
    )
      .then((groups) => {
        if (!cancelled) {
          setState({ loading: false, error: '', groups });
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setState({ loading: false, error: errorMessage(err), groups: [] });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [reloadKey]);

  return { ...state, reload: () => setReloadKey((value) => value + 1) };
}
