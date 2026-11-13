/**
 * 全站三段式布局：品牌区 + 主导航区 + 内容区。
 * 职责：统一顶栏与内容容器；主导航由一个配置数组渲染。
 * 输入：children（页面内容）；输出：页面骨架 JSX。
 * 边界：第一版主导航只有「应用包」一个真实入口；没有占位菜单、隐藏入口、板块开关或空页面；
 *       顶栏右侧「+ 上传包」是全局动作而非导航项，不属于导航数组。
 */
import type { ReactNode } from 'react';
import { Link, NavLink } from 'react-router-dom';

interface NavItem {
  label: string;
  path: string;
}

/** 未来新增板块只需往这个数组加一项并新增路由，三段式结构与三个现有页面都不用改。 */
const NAV_ITEMS: ReadonlyArray<NavItem> = [{ label: '应用包', path: '/' }];

export function Layout({ children }: { children: ReactNode }): JSX.Element {
  return (
    <div className="app-shell">
      <header className="app-header">
        <div className="brand-area">
          <Link className="brand" to="/">
            Dandelion
          </Link>
        </div>
        <nav className="main-nav" aria-label="主导航">
          {NAV_ITEMS.map((item) => (
            <NavLink
              key={item.path}
              to={item.path}
              end
              className={({ isActive }) => (isActive ? 'nav-link nav-link-active' : 'nav-link')}
            >
              {item.label}
            </NavLink>
          ))}
        </nav>
        <div className="header-action">
          <Link className="button button-ghost" to="/upload">
            + 上传包
          </Link>
        </div>
      </header>
      <main className="app-content">{children}</main>
    </div>
  );
}
