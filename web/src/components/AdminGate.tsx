/**
 * 超管专区守卫。
 * 职责：非超级管理员直接渲染 403 说明页，而不是让页面自己报错或白屏。
 * 输入：children；输出：children 或 403 空态。
 * 边界：**只是体验层**——真正的边界在服务端 `RequireSuperAdmin`（一律 403）。
 *       前端隐藏菜单与路由拦截都挡不住构造请求，所以这里用"无权访问"而不是"不存在"。
 */
import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { useMe } from '../hooks/useMe';
import { Empty, Loading } from './States';

export function AdminGate({ children }: { children: ReactNode }): JSX.Element {
  const { me, loading } = useMe();

  if (loading) {
    return <Loading text="正在校验权限…" />;
  }
  if (!me?.isSuperAdmin) {
    return (
      <Empty
        title="无权访问该功能"
        description="超级管理员专区只有超级管理员可以进入。如需权限，请联系平台管理员。"
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
