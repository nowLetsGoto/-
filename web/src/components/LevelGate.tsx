/**
 * 管理身份层级守卫。
 * 职责：当前登录人的 accountLevel 低于要求时，渲染「无权访问」说明页，而不是让页面自己去请求再吃 403。
 * 输入：minLevel（最低层级）、可选的说明文案、children；输出：children 或 403 空态。
 * 边界：**只是体验层**——真正的边界在服务端 `RequireMinLevel` + scope 校验（一律 403）。
 *       所以这里说「无权访问」而不是「不存在」，与 PermissionGate 的措辞保持一致。
 *
 * 与 PermissionGate 的分工：
 *   - PermissionGate 判「业务能力」（能不能传包、能不能看项目），来源是角色权限点；
 *   - LevelGate 判「管理身份」（能不能管人、能不能改公司配置），来源是 account_level。
 * 两者不可互相替代：管理权限点已从角色可勾选目录移除，前端拿权限点判管理层级会永远为假。
 */
import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { useMe } from '../hooks/useMe';
import { LEVEL_LABELS, atLeastLevel } from '../utils/accountLevel';
import type { AccountLevel } from '../utils/accountLevel';
import { Empty, Loading } from './States';

export function LevelGate({
  minLevel,
  description,
  children,
}: {
  minLevel: AccountLevel;
  description?: string;
  children: ReactNode;
}): JSX.Element {
  const { me, loading } = useMe();

  if (loading) {
    return <Loading text="正在校验权限…" />;
  }
  if (!atLeastLevel(me, minLevel)) {
    return (
      <Empty
        title="无权访问该功能"
        description={
          description ??
          `该功能需要${LEVEL_LABELS[minLevel]}及以上身份。当前身份：${
            me ? (LEVEL_LABELS[me.accountLevel as AccountLevel] ?? '普通成员') : '未知'
          }。如需权限，请联系平台管理员。`
        }
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
