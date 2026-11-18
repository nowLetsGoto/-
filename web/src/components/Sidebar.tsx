/**
 * 左侧板块导航。
 * 职责：渲染品牌区、按配置渲染的一级/二级菜单，以及底部账号区（见 AccountMenu）。
 * 输入：react-router 的当前路径；输出：导航骨架。
 * 边界：不发请求、不做权限判断（能渲染就说明已登录，未登录进不来 Layout）；
 *       项目选择放在各页面的头部筛选里，侧栏不再重复一个全局切换器；
 *       账号卡、改密与退出都收在 AccountMenu 内，本组件只决定它出现的位置。
 */
import { NavLink } from 'react-router-dom';
import { readSession } from '../api/session';
import { useMe } from '../hooks/useMe';
import { useProjectKey } from '../hooks/useProjects';
import { AccountMenu } from './AccountMenu';
import { Icon } from './icons';
import { NAV_SECTIONS, navHref } from './navigation';
import type { NavItem } from './navigation';

export function Sidebar(): JSX.Element {
  // 模式来自签名令牌签发时写入的会话，仅用于菜单投影；服务端另有强制边界。
  const mode = readSession()?.mode ?? 'solo';
  // 只读 URL / localStorage：侧栏不为拼链接去拉项目列表（平台级页面因此零额外请求）。
  const projectKey = useProjectKey();
  // /me 有模块级缓存，整页只取一次；它的 isSuperAdmin 决定超管专属菜单是否渲染。
  const { me } = useMe();
  const isAdmin = me?.isSuperAdmin === true;

  // 单人模式：只有概览 / 应用包 / 应用；管理员额外保留审计日志与平台集成。
  // 团队模式：沿用完整的角色权限投影。
  const soloAllowed = new Set(['/overview', '/packages', '/apps', '/directory/projects']);
  if (mode === 'solo' && isAdmin) {
    soloAllowed.add('/admin/accounts');
    soloAllowed.add('/admin/audit-logs');
    soloAllowed.add('/settings');
  }
  const canSee = (item: NavItem): boolean => {
    // 菜单项级的超管门槛先判：角色管理没有权限点（已从权限目录移除），只能靠这一条。
    if (item.requireSuperAdmin && !isAdmin) {
      return false;
    }
    if (mode === 'solo') {
      return soloAllowed.has(item.path);
    }
    return !item.permission || me?.permissions.includes(item.permission) === true;
  };

  const sections = NAV_SECTIONS.map((section) => ({
    ...section,
    items: section.items.filter(canSee),
  }))
    .filter((section) => (!section.requireSuperAdmin || isAdmin) && section.items.length > 0)
    .map((section) =>
      mode === 'solo' && isAdmin && section.requireSuperAdmin
        ? { ...section, title: '管理员工具' }
        : section,
    );

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
        {sections.map((section) => (
          <div className="nav-section" key={section.title}>
            <p className={section.requireSuperAdmin ? 'nav-group nav-group-admin' : 'nav-group'}>
              {section.title}
              {section.requireSuperAdmin ? <span className="admin-badge">Admin</span> : null}
            </p>
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

      <AccountMenu />
    </aside>
  );
}
