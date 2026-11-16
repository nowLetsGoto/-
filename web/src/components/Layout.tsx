/**
 * 全站三段式布局：品牌区 + 主导航区 + 内容区。
 * 职责：统一顶栏与内容容器；主导航由一个配置数组渲染；右上角在已登录时显示账号与退出。
 * 输入：children（页面内容）；输出：页面骨架 JSX。
 * 边界：主导航只有「应用包」一个真实入口，没有占位菜单、隐藏入口、板块开关或空页面；
 *       「+ 上传包」是全局动作而非导航项；账号区只在本地存在会话时渲染，
 *       所以公开的下载页对未登录访客不会出现任何管理入口。
 */
import type { ReactNode } from 'react';
import { Link, NavLink, useNavigate } from 'react-router-dom';
import { clearSession, readSession } from '../api/session';

interface NavItem {
  label: string;
  path: string;
}

/** 未来新增板块只需往这个数组加一项并新增路由，三段式结构与三个现有页面都不用改。 */
const NAV_ITEMS: ReadonlyArray<NavItem> = [{ label: '应用包', path: '/' }];

export function Layout({ children }: { children: ReactNode }): JSX.Element {
  const navigate = useNavigate();
  const session = readSession();

  /** 退出即清空本地会话并回登录页；令牌是无状态的，服务端没有需要调用的登出接口。 */
  const handleLogout = (): void => {
    clearSession();
    navigate('/login', { replace: true });
  };

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
        {session ? (
          <div className="header-account">
            <span className="header-user" title="当前登录账号">
              {session.username}
            </span>
            <button className="button button-ghost button-small" type="button" onClick={handleLogout}>
              退出
            </button>
          </div>
        ) : null}
      </header>
      <main className="app-content">{children}</main>
    </div>
  );
}
