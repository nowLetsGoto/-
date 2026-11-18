/**
 * 超管专区守卫（过渡封装，等价于 LevelGate(minLevel="super_admin")）。
 * 职责：非超级管理员直接渲染 403 说明页，而不是让页面自己报错或白屏。
 * 输入：children；输出：children 或 403 空态。
 * 边界：**只是体验层**——真正的边界在服务端 `RequireSuperAdmin`（一律 403）。
 *
 * 为什么保留这个名字：仓里已有若干路由在用它，语义也明确（"超管专区"）。
 * 新页面请直接用 LevelGate —— 它能表达 platform_admin / team_admin 这些更细的门槛，
 * 而本组件只能表达"是不是超管"这个二维判断。
 */
import type { ReactNode } from 'react';
import { LevelGate } from './LevelGate';

export function AdminGate({ children }: { children: ReactNode }): JSX.Element {
  return (
    <LevelGate
      minLevel="super_admin"
      description="该功能仅平台超级管理员可以进入。如需权限，请联系平台超级管理员。"
    >
      {children}
    </LevelGate>
  );
}
