/**
 * 左侧板块导航。
 * 职责：渲染品牌区、按配置渲染的一级/二级菜单，以及底部账号与退出。
 * 输入：react-router 的当前路径；输出：导航骨架。
 * 边界：不发请求、不做权限判断（能渲染就说明已登录，未登录进不来 Layout）；
 *       项目选择放在各页面的头部筛选里，侧栏不再重复一个全局切换器。
 */
import { NavLink, useNavigate } from 'react-router-dom';
import { clearSession, readSession } from '../api/session';
import { useProjectKey } from '../hooks/useProjects';
import { Icon } from './icons';
import { NAV_SECTIONS, navHref } from './navigation';

export function Sidebar(): JSX.Element {
  const navigate = useNavigate();
  const session = readSession();
  // 只读 URL / localStorage：侧栏不为拼链接去拉项目列表（平台级页面因此零额外请求）。
  const projectKey = useProjectKey();

  /** 退出即清空本地会话并回登录页；令牌是无状态的，服务端没有需要调用的登出接口。 */
  const handleLogout = (): void => {
    clearSession();
    navigate('/login', { replace: true });
  };

  return (
    <aside className="sidebar">
      <div className="sidebar-brand">
        <span className="brand-mark" aria-hidden="true">
          D
        </span>
        <div>
          <div className="brand-name">Dandelion</div>
          <div className="brand-sub">研发交付平台</div>
        </div>
      </div>

      <nav className="sidebar-nav" aria-label="主导航">
        {NAV_SECTIONS.map((section) => (
          <div className="nav-section" key={section.title}>
            <p className="nav-group">{section.title}</p>
            {section.items.map((item) => (
              <NavLink
                key={item.path}
                to={navHref(item, projectKey)}
                className={({ isActive }) => (isActive ? 'nav-item nav-item-active' : 'nav-item')}
              >
                <Icon name={item.icon} />
                {item.label}
              </NavLink>
            ))}
          </div>
        ))}
      </nav>

      {session ? (
        <div className="sidebar-foot">
          <span className="avatar" aria-hidden="true">
            {session.username.slice(0, 1).toUpperCase()}
          </span>
          <span className="account-name" title="当前登录账号">
            {session.username}
          </span>
          <button className="button button-quiet button-small" type="button" onClick={handleLogout}>
            退出
          </button>
        </div>
      ) : null}
    </aside>
  );
}
