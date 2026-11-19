/**
 * 侧边栏导航的唯一配置。
 * 职责：声明一级菜单（分组）、二级菜单（具体页面）、图标与板块级开关；
 *       Sidebar 据此渲染，Topbar 据此推导面包屑。
 * 输入：无；输出：NAV_SECTIONS 与若干查询函数。
 * 边界：新增页面只改这里 + App.tsx 加一条路由；
 *       不放「敬请期待」占位项——能出现在导航里的页面必须有对应后端。
 */
import type { AccountLevel } from '../utils/accountLevel';
import type { IconName } from './icons';

export interface NavItem {
  label: string;
  /** 项目内页面写相对路径（/overview），平台级页面写绝对路径（/directory/people） */
  path: string;
  icon: IconName;
  /** project：跟随项目切换；platform：全平台唯一 */
  scope: 'project' | 'platform';
  /** 该板块是否受「环境」维度影响；基础资料与设置这类全局页面设为 false。缺省视为 true。 */
  envScoped?: boolean;
  /** 该板块是否在顶栏暴露「上传包」主行动；只有应用包需要。缺省视为 false。 */
  canUpload?: boolean;
  /** 进入该页面需要的权限点；缺省表示不限（只要求登录）。前端仅做隐藏，服务端另有判定。 */
  permission?: string;
  /**
   * 进入该页面需要的最低管理身份层级。
   *
   * 优先级：minLevel > requireSuperAdmin > permission（见 canSeeNavItem）。
   * 管理能力改成「身份层级直接赋予」之后，靠这个字段投影菜单：
   * directory.write / settings.* 等权限点已从可勾选目录移除，只剩服务端按层级下发，
   * 前端再拿它们判断会失效。
   */
  minLevel?: AccountLevel;
  /**
   * 该页面仅超级管理员可见；与 minLevel 功能重叠，保留是为了兼容既有配置。
   * 新代码请用 minLevel——它表达的是"哪个层级起"，而不是"是不是超管"这个二维判断。
   */
  requireSuperAdmin?: boolean;
}

export interface NavSection {
  /** 一级菜单名 */
  title: string;
  items: ReadonlyArray<NavItem>;
  /** 整组仅超级管理员可见；前端只做隐藏，真正的边界是服务端 403 */
  requireSuperAdmin?: boolean;
  /** 整组需要的最低层级；优先级高于 requireSuperAdmin。 */
  minLevel?: AccountLevel;
  /** 该组在侧栏显示的角标（如 Admin），仅作视觉提示。 */
  badge?: string;
}

/**
 * canSeeNavItem 是菜单可见性判定的唯一实现。
 * 顺序：minLevel → requireSuperAdmin → permission。
 * me 为 null 时，任何带层级/超管/权限要求的项都不可见（宁可少画，不要画了再 403）。
 */
export function canSeeNavItem(
  item: NavItem,
  level: number,
  permissions: ReadonlyArray<string>,
  isSuperAdmin: boolean,
): boolean {
  if (item.minLevel) {
    return level >= minRank(item.minLevel);
  }
  if (item.requireSuperAdmin) {
    return isSuperAdmin;
  }
  return !item.permission || permissions.includes(item.permission);
}

/** canSeeNavSection 判断整组是否显示：组级门槛同样按 minLevel 优先。 */
export function canSeeNavSection(
  section: NavSection,
  level: number,
  isSuperAdmin: boolean,
  visibleItems: number,
): boolean {
  if (visibleItems === 0) {
    return false;
  }
  if (section.minLevel) {
    return level >= minRank(section.minLevel);
  }
  if (section.requireSuperAdmin) {
    return isSuperAdmin;
  }
  return true;
}

/** minRank 与 utils/accountLevel 的 LEVEL_RANK 同序，这里内联避免导航配置依赖运行时状态。 */
function minRank(level: AccountLevel): number {
  switch (level) {
    case 'super_admin':
      return 4;
    case 'platform_admin':
      return 3;
    case 'team_admin':
      return 2;
    default:
      return 1;
  }
}

export const NAV_SECTIONS: ReadonlyArray<NavSection> = [
  {
    title: '总览',
    items: [
      {
        label: '概览',
        path: '/overview',
        permission: 'build.read',
        icon: 'overview',
        scope: 'project',
      },
    ],
  },
  {
    title: '分发',
    items: [
      {
        label: '应用包',
        path: '/packages',
        permission: 'build.read',
        icon: 'packages',
        scope: 'project',
        canUpload: true,
      },
      { label: '应用', path: '/apps', permission: 'build.read', icon: 'apps', scope: 'project' },
    ],
  },
  {
    title: '基础资料',
    items: [
      {
        label: '人员管理',
        path: '/directory/people',
        permission: 'directory.read',
        icon: 'people',
        scope: 'platform',
        envScoped: false,
      },
      {
        label: '团队管理',
        path: '/directory/teams',
        permission: 'directory.read',
        icon: 'team',
        scope: 'platform',
        envScoped: false,
      },
      {
        label: '项目管理',
        path: '/directory/projects',
        permission: 'project.read',
        icon: 'project',
        scope: 'platform',
        envScoped: false,
      },
      {
        // 账号管理从「超管专区」下放到团队管理员：后端已放开（RequireMinLevel(team_admin)），
        // 团队管理员在这里建/管本团队的普通账号。放在基础资料区而不是平台管理区，
        // 因为平台管理区整组是超管门槛，会把团队管理员挡在外面。
        label: '账号管理',
        path: '/admin/accounts',
        minLevel: 'team_admin',
        icon: 'people',
        scope: 'platform',
        envScoped: false,
      },
    ],
  },
  {
    // 公司设置：平台管理员及以上。这一区是「我这家的配置」，不含跨公司的东西。
    // 角色管理放这里而不是超管区：后端已放开到 platform_admin，且只能改本公司自定义角色、
    // 平台内置角色只读——把公司管好自己角色的能力留在超管区会让它形同虚设。
    title: '公司设置',
    minLevel: 'platform_admin',
    items: [
      {
        label: '角色管理',
        path: '/directory/roles',
        minLevel: 'platform_admin',
        icon: 'shield',
        scope: 'platform',
        envScoped: false,
      },
      {
        // 审计日志：本公司范围内"谁改了什么"。放在公司设置而不是平台管理，
        // 因为平台管理整组是超管门槛，而审计的实际使用者是公司管理员。
        // 后端按 scope 过滤：平台管理员只看本公司，超管看全部。
        label: '审计日志',
        path: '/admin/audit-logs',
        minLevel: 'platform_admin',
        icon: 'overview',
        scope: 'platform',
        envScoped: false,
      },
      {
        label: '平台集成',
        path: '/settings',
        minLevel: 'platform_admin',
        icon: 'settings',
        scope: 'platform',
        envScoped: false,
      },
    ],
  },
  {
    // 平台管理：仅超级管理员。跨公司的动作都收在这里——建公司、维护平台管理员、看平台审计。
    // 与「公司设置」分开是为了让超管一眼看出"这两个菜单影响范围完全不同"。
    title: '平台管理',
    minLevel: 'super_admin',
    items: [
      {
        label: '公司管理',
        path: '/admin/tenants',
        minLevel: 'super_admin',
        icon: 'team',
        scope: 'platform',
        envScoped: false,
      },
    ],
  },
];

/** 展平后的导航项：所有查询函数共用一份。 */
const NAV_ITEMS: ReadonlyArray<NavItem> = NAV_SECTIONS.flatMap((section) => section.items);

/** 不在导航里的页面也要有面包屑名字，集中登记。 */
const EXTRA_LABELS: Record<string, string> = {
  '/upload': '上传包',
};

/**
 * projectRelativePath 去掉 /p/:key 前缀：`/p/shop/packages` → `/packages`。
 * 平台级路径原样返回，这样查询函数对两类路径都成立。
 */
export function projectRelativePath(pathname: string): string {
  const matched = /^\/p\/[^/]+(\/.*)?$/.exec(pathname);
  if (!matched) {
    return pathname;
  }
  return matched[1] ?? '/overview';
}

/** projectKeyFromPath 从 /p/:key/... 取出 key；不在项目内时返回空串。 */
export function projectKeyFromPath(pathname: string): string {
  const matched = /^\/p\/([^/]+)/.exec(pathname);
  return matched?.[1] ?? '';
}

/**
 * findNav 按路径找导航项：先精确匹配，再退回「最长前缀」，
 * 这样详情页（如 /directory/projects/shop）能归到它所属的列表页。
 */
function findNav(relativePath: string): NavItem | undefined {
  const exact = NAV_ITEMS.find((item) => item.path === relativePath);
  if (exact) {
    return exact;
  }
  return NAV_ITEMS.filter((item) => relativePath.startsWith(`${item.path}/`)).sort(
    (left, right) => right.path.length - left.path.length,
  )[0];
}

/** boardLabel 把当前路径映射成板块名；未命中时回落到「概览」。 */
export function boardLabel(pathname: string): string {
  const relative = projectRelativePath(pathname);
  return findNav(relative)?.label ?? EXTRA_LABELS[relative] ?? '概览';
}

/** isEnvScoped 判断当前板块是否按环境区分；未登记路径（如上传页）默认区分。 */
export function isEnvScoped(pathname: string): boolean {
  return findNav(projectRelativePath(pathname))?.envScoped !== false;
}

/** canUpload 判断当前板块是否要显示顶栏的「上传包」主行动。 */
export function canUpload(pathname: string): boolean {
  return findNav(projectRelativePath(pathname))?.canUpload === true;
}

/** isProjectScoped 判断当前板块是否落在项目下；未登记路径（如上传页）默认属于项目。 */
export function isProjectScoped(pathname: string): boolean {
  const item = findNav(projectRelativePath(pathname));
  return item ? item.scope === 'project' : true;
}

/**
 * navHref 拼出导航链接：项目内页面自动带上 /p/:key。
 * 还没有当前项目（首次访问且没记录）时退回首页 `/`，由 ProjectHome 决定落到哪个项目，
 * 这样侧栏不必为了拼链接而先取一次项目列表。
 */
export function navHref(item: NavItem, projectKey: string): string {
  if (item.scope === 'platform') {
    return item.path;
  }
  return projectKey ? `/p/${projectKey}${item.path}` : '/';
}
