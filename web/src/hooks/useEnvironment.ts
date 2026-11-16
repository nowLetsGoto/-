/**
 * 全局环境维度的唯一读写处。
 * 职责：从 URL 查询参数读当前环境、改写环境；兼容旧链接的 environment 参数。
 * 输入：react-router 的 searchParams；输出：当前环境与切换函数。
 * 边界：不做请求、不存 localStorage——URL 是唯一真相，刷新/转发/跨板块切换都不丢。
 */
import { useSearchParams } from 'react-router-dom';
import type { Environment } from '../api/builds';
import { ENVIRONMENTS } from '../utils/format';

/** 默认落在测试环境：改坏东西的成本最低。 */
const DEFAULT_ENVIRONMENT: Environment = 'test';

function isEnvironment(value: string | null): value is Environment {
  return ENVIRONMENTS.some((item) => item.value === value);
}

export function useEnvironment(): [Environment, (next: Environment) => void] {
  const [params, setParams] = useSearchParams();
  // 旧版把环境写成 ?environment=；这里一并认，避免已转发的链接失效。
  const raw = params.get('env') ?? params.get('environment');

  const setEnvironment = (next: Environment): void => {
    const updated = new URLSearchParams(params);
    updated.delete('environment');
    updated.set('env', next);
    setParams(updated, { replace: true });
  };

  return [isEnvironment(raw) ? raw : DEFAULT_ENVIRONMENT, setEnvironment];
}
