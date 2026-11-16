/**
 * 全站骨架：左侧板块导航 + 右上顶栏 + 内容区。
 * 职责：只负责布局与区域划分，把页面内容原样放进内容区。
 * 输入：children（页面内容）；输出：两段式骨架 JSX。
 * 边界：不取数、不判断权限、不写任何板块文案；
 *       导航项来自 navigation.ts 的配置，加板块不需要改这里。
 */
import type { ReactNode } from 'react';
import { Sidebar } from './Sidebar';
import { Topbar } from './Topbar';

export function Layout({ children }: { children: ReactNode }): JSX.Element {
  return (
    <div className="app-shell">
      <Sidebar />
      <div className="app-main">
        <Topbar />
        <main className="app-content">{children}</main>
      </div>
    </div>
  );
}
