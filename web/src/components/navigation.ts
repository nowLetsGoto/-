/**
 * 侧边栏导航的唯一配置。
 * 职责：声明板块分组、路径与图标；Sidebar 据此渲染导航，Topbar 据此推导面包屑。
 * 输入：无；输出：NAV_SECTIONS、boardLabel()。
 * 边界：新增板块只改这里 + App.tsx 加一条路由，Layout 不动；
 *       不放「敬请期待」占位项——能出现在导航里的板块必须有对应后端。
 */
import type { IconName } from './icons';

export interface NavItem {
  label: string;
  path: string;
  icon: IconName;
  /** 该板块是否受「环境」维度影响；系统设置这类全局配置设为 false。缺省视为 true。 */
  envScoped?: boolean;
}

export interface NavSection {
  title: string;
  items: ReadonlyArray<NavItem>;
}

export const NAV_SECTIONS: ReadonlyArray<NavSection> = [
  { title: '总览', items: [{ label: '概览', path: '/', icon: 'overview' }] },
  { title: '分发', items: [{ label: '应用包', path: '/packages', icon: 'packages' }] },
  {
    title: '管理',
    items: [
      { label: '应用', path: '/apps', icon: 'apps' },
      { label: '系统设置', path: '/settings', icon: 'settings', envScoped: false },
    ],
  },
];

/** 不在导航里的页面也要有面包屑名字，集中登记在这里，避免各页自己写死。 */
const EXTRA_LABELS: Record<string, string> = {
  '/upload': '上传包',
};

/** boardLabel 把当前路径映射成板块名；未命中时回落到「概览」。 */
export function boardLabel(pathname: string): string {
  const navItem = NAV_SECTIONS.flatMap((section) => section.items).find((item) => item.path === pathname);
  return navItem?.label ?? EXTRA_LABELS[pathname] ?? '概览';
}

/** isEnvScoped 判断当前板块是否按环境区分；未登记路径（如上传页）默认区分。 */
export function isEnvScoped(pathname: string): boolean {
  const navItem = NAV_SECTIONS.flatMap((section) => section.items).find((item) => item.path === pathname);
  return navItem ? navItem.envScoped !== false : true;
}
