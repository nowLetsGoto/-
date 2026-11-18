/**
 * 权限点守卫。
 * 职责：当前登录人缺少某个权限点时，渲染「无权访问」说明页，而不是让页面自己去请求再吃 403。
 * 输入：需要的权限点、可选的说明文案、children；输出：children 或 403 空态。
 * 边界：**只是体验层**——真正的边界在服务端 `RequirePermission`（一律 403）。
 *       所以这里说「无权访问」而不是「不存在」，与 AdminGate 的措辞保持一致。
 *       与 `can()` 的分工：`can()` 决定按钮要不要渲染，本组件决定整页能不能进。
 */
import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { useMe } from '../hooks/useMe';
import { can } from '../utils/permission';
import { Empty, Loading } from './States';

export function PermissionGate({
  permission,
  description = '你的角色没有该功能的访问权限。如需权限，请联系管理员分配角色。',
  children,
}: {
  permission: string;
  description?: string;
  children: ReactNode;
}): JSX.Element {
  const { me, loading } = useMe();

  if (loading) {
    return <Loading text="正在校验权限…" />;
  }
  if (!can(me, permission)) {
    return (
      <Empty
        title="无权访问该功能"
        description={description}
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
