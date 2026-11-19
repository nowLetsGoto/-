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
import { NAV_SECTIONS, canSeeNavItem, canSeeNavSection, navHref } from './navigation';
import type { NavItem } from './navigation';
import { levelRank } from '../utils/accountLevel';

export function Sidebar(): JSX.Element {
  // 模式来自签名令牌签发时写入的会话，仅用于菜单投影；服务端另有强制边界。
  const mode = readSession()?.mode ?? 'solo';
  // 只读 URL / localStorage：侧栏不为拼链接去拉项目列表（平台级页面因此零额外请求）。
  const projectKey = useProjectKey();
  // /me 有模块级缓存，整页只取一次；它的 isSuperAdmin 决定超管专属菜单是否渲染。
  const { me } = useMe();
  const isAdmin = me?.isSuperAdmin === true;
  // 层级序数由 /me 的 accountLevel 折算；me 还没回来时是 0，所有层级菜单都不显示。
  // 这比"先画出来再吃 403"体验好——不会闪一下再消失。
  const level = levelRank(me?.accountLevel);
  const permissions = me?.permissions ?? [];

  // 单人模式：个人工作台，只有概览 / 应用包 / 应用；超管额外保留账号管理与平台集成。
  // 团队模式：按身份层级 + 角色权限完整投影。
  //
  // 审计日志不在这里：它属于"看别人做了什么"的管理能力，服务端要求团队模式
  // （solo 分区的审计不带公司过滤，放进单人视图会变成跨公司泄漏）。
  const soloAllowed = new Set(['/overview', '/packages', '/apps', '/directory/projects']);
  if (mode === 'solo' && isAdmin) {
    soloAllowed.add('/admin/accounts');
    soloAllowed.add('/settings');
  }
  const canSee = (item: NavItem): boolean => {
    if (mode === 'solo') {
      return soloAllowed.has(item.path);
    }
    // 团队模式统一走 canSeeNavItem：minLevel → requireSuperAdmin → permission。
    return canSeeNavItem(item, level, permissions, isAdmin);
  };

  const sections = NAV_SECTIONS.map((section) => ({
    ...section,
    items: section.items.filter(canSee),
  })).filter((section) => canSeeNavSection(section, level, isAdmin, section.items.length));

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
        {sections.map((section) => {
          // 角标跟着"是不是管理区"走：超管区与公司设置都算管理动作，给个视觉分隔。
          const isAdminSection = Boolean(section.minLevel);
          const badge = section.minLevel === 'platform_admin' ? 'Adm' : 'Admin';
          return (
            <div className="nav-section" key={section.title}>
              <p className={isAdminSection ? 'nav-group nav-group-admin' : 'nav-group'}>
                {section.title}
                {isAdminSection ? <span className="admin-badge">{badge}</span> : null}
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
          );
        })}
      </nav>

      <AccountMenu />
    </aside>
  );
}
