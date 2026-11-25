/**
 * 全局环境维度的唯一读写处。
 * 职责：从 URL 查询参数读当前环境、改写环境；兼容旧链接的 environment 参数。
 * 输入：react-router 的 searchParams、可选服务端目标地址；输出：当前环境与切换函数。
 * 边界：不做请求、不存 localStorage——URL 是唯一真相，刷新/转发/跨板块切换都不丢。
 */
import { useCallback } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import type { Environment } from '../api/builds';
import { ENVIRONMENTS } from '../utils/format';

/** 默认落在测试环境：改坏东西的成本最低。 */
const DEFAULT_ENVIRONMENT: Environment = 'test';

function isEnvironment(value: string | null): value is Environment {
  return ENVIRONMENTS.some((item) => item.value === value);
}

export function useEnvironment<T extends Environment | 'all' = Environment>(
  defaultEnvironment: T = DEFAULT_ENVIRONMENT as T,
): [Environment | T, (next: Environment | T, destination?: string) => void] {
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  // 旧版把环境写成 ?environment=；这里一并认，避免已转发的链接失效。
  const raw = params.get('env') ?? params.get('environment');

  // 公开下载页切环境时可同时切到对应的包；一次导航保证 URL 的包与环境不出现中间态。
  const setEnvironment = useCallback(
    (next: Environment | T, destination?: string): void => {
      // 只解析服务端返回的地址；切到另一包时使用目标凭证，不能继承来源包的 shareToken。
      const target = destination ? new URL(destination, window.location.href) : null;
      const updated = new URLSearchParams(target ? target.search : params);
      updated.delete('environment');
      updated.set('env', next);
      updated.delete('page');
      if (target) {
        navigate({ pathname: target.pathname, search: updated.toString() }, { replace: true });
      } else {
        setParams(updated, { replace: true });
      }
    },
    [params, navigate, setParams],
  );

  return [isEnvironment(raw) ? raw : defaultEnvironment, setEnvironment];
}
