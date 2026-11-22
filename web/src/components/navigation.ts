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
  /**
   * V1.0 研发中心 4 项携带的历史标记，当前没有任何读取方；
   * 保留为可选字段以免删改菜单项本身，仅用于让类型检查与既有配置一致。
   */
  envScoped?: boolean;
  /** 进入该页面需要的权限点；缺省表示不限（只要求登录）。前端仅做隐藏，服务端另有判定。 */
  permission?: string;
  /**
   * 进入该页面需要的最低管理身份层级。
   *
   * 优先级：minLevel > requireSuperAdmin > permission（见 canSeeNavItem）。
   * 目前只有「平台管理 / 公司管理」还用 minLevel: super_admin，
   * 其余管理菜单都已改为权限点门槛（普通账号靠角色配置进入）。
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
      },
      { label: '应用', path: '/apps', permission: 'build.read', icon: 'apps', scope: 'project' },
    ],
  },
  {
    // 研发中心：与「分发」平级的一级分组，只在团队模式下由角色菜单授权显示；
    // solo 白名单不含这些路径（见 Sidebar），后端 RequireTeamMode 是真正的边界。
    // path / permission 与 contract.Menus 的 4 项逐条一致。
    title: '研发中心',
    items: [
      {
        label: '需求中心',
        path: '/rdc/requirements',
        permission: 'rdc.requirement.read',
        icon: 'requirement',
        scope: 'platform',
        envScoped: false,
      },
      {
        label: 'AI研发中心',
        path: '/rdc/dev',
        permission: 'rdc.dev.read',
        icon: 'dev',
        scope: 'platform',
        envScoped: false,
      },
      {
        label: '人工审核中心',
        path: '/rdc/review',
        permission: 'rdc.review.read',
        icon: 'review',
        scope: 'platform',
        envScoped: false,
      },
      {
        label: '需求交付中心',
        path: '/rdc/delivery',
        permission: 'rdc.delivery.read',
        icon: 'delivery',
        scope: 'platform',
        envScoped: false,
      },
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
      },
      {
        label: '团队管理',
        path: '/directory/teams',
        permission: 'directory.read',
        icon: 'team',
        scope: 'platform',
      },
      {
        label: '项目管理',
        path: '/directory/projects',
        permission: 'project.read',
        icon: 'project',
        scope: 'platform',
      },
      {
        // 账号管理：只读门槛是 account.read（角色可配）；
        // 页面内的写操作另按平台管理员层级隐藏，与这里的菜单可见性无关。
        label: '账号管理',
        path: '/admin/accounts',
        permission: 'account.read',
        icon: 'people',
        scope: 'platform',
      },
    ],
  },
  {
    // 公司设置：这一区是「我这家的配置」，不含跨公司的东西。
    // 门槛由身份层级改为权限点：普通账号凭角色勾选即可管理本公司配置，
    // 平台管理员固有全部相关权限，超管始终全量。
    title: '公司设置',
    items: [
      {
        label: '角色管理',
        path: '/directory/roles',
        permission: 'role.read',
        icon: 'shield',
        scope: 'platform',
      },
      {
        // 审计日志：本公司范围内"谁改了什么"，由 audit.read 控制。
        // 后端按 scope 过滤：公司账号只看本公司，超管看全部。
        label: '审计日志',
        path: '/admin/audit-logs',
        permission: 'audit.read',
        icon: 'overview',
        scope: 'platform',
      },
      {
        label: '平台集成',
        path: '/settings',
        permission: 'settings.read',
        icon: 'settings',
        scope: 'platform',
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

/**
 * pathMatches 判断当前路径是否落在 base 或其子路径上。
 * 详情页（如 /directory/projects/new）仍要高亮列表菜单，所以按路径段前缀比较，
 * 而不是裸 startsWith——后者会让 /packages 误命中 /packages-export 这种路径。
 */
function pathMatches(pathname: string, base: string): boolean {
  const normalized = pathname.length > 1 ? pathname.replace(/\/+$/, '') : pathname;
  return normalized === base || normalized.startsWith(`${base}/`);
}

/**
 * isNavItemActive 判断单个菜单项是否高亮。
 * 不能复用 NavLink 的默认 URL 匹配：没有项目 key 时，所有项目级 navHref 都退回 `/`，
 * NavLink 会把它们（以及 `/` 前缀命中的平台页）同时判成 active。这里按 scope 精确处理：
 * 平台菜单匹配自身/子路径；项目菜单仅在拿到当前 key 且路径落在对应 /p/:key 下时高亮。
 */
export function isNavItemActive(item: NavItem, pathname: string, projectKey: string): boolean {
  if (item.scope === 'platform') {
    return pathMatches(pathname, item.path);
  }
  if (!projectKey) {
    return false;
  }
  return pathMatches(pathname, `/p/${projectKey}${item.path}`);
}
