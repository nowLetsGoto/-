/**
 * 团队模式守卫（体验层）。
 * 职责：研发中心只在团队模式提供服务；非 team 会话直接渲染诚实空态，不让页面发出注定 403 的请求。
 * 输入：children；输出：children 或「仅团队模式可用」空态。
 * 边界：**只是体验层**——真正的边界在服务端 RequireTeamMode（一律 403）；
 *       模式取自签名令牌对应的本地会话快照，前端不能用来提权；不判断权限（由 PermissionGate 负责）。
 * 与 PermissionGate 的分工与顺序：TeamModeGate 在外先挡模式，PermissionGate 在内再挡权限。
 */
import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { readSession } from '../api/session';
import { Empty } from './States';

export function TeamModeGate({ children }: { children: ReactNode }): JSX.Element {
  const mode = readSession()?.mode ?? 'solo';
  if (mode !== 'team') {
    return (
      <Empty
        title="仅团队模式可用"
        description="研发中心只在团队模式下提供服务。请切换为团队模式后重新进入。"
        action={
          <Link className="button button-ghost" to="/">
            返回首页
          </Link>
        }
      />
    );
  }
  return <>{children}</>;
}
