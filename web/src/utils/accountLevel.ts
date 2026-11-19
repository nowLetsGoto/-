/**
 * 管理身份层级的唯一判定入口（前端视角）。
 * 职责：把「当前登录人 + 最低层级要求」判断成「这个菜单/页面要不要给」。
 * 输入：useMe 取回的 Me（可能还没加载完，为 null）、最低层级；输出：布尔值。
 * 边界：**只是体验层**——真正的边界在服务端（RequireMinLevel + scope）。
 *       前端隐藏菜单与路由拦截都挡不住构造请求，服务端一律 403。
 *
 * 为什么要有这一层：管理能力已从「角色勾选的权限点」改为「身份层级直接赋予」
 * （见后端 contract.Permissions 与 authz.managementPermissions）。因此前端不能再
 * 靠 directory.write 之类的权限点判断"能不能管人"——那个点已经不在可勾选目录里，
 * 只有服务端按层级下发。菜单投影必须跟着改成按层级判断。
 */
import type { Me } from '../api/me';

/** 四级管理身份，与后端 contract.AccountLevel* 一一对应。 */
export type AccountLevel = 'super_admin' | 'platform_admin' | 'team_admin' | 'member';

/**
 * LEVEL_RANK 是层级的数值序：super_admin=4 → member=1。
 * 与后端 contract.AccountLevelRank 保持同序；未知/空值为 0（失败关闭）。
 */
const LEVEL_RANK: Record<AccountLevel, number> = {
  super_admin: 4,
  platform_admin: 3,
  team_admin: 2,
  member: 1,
};

/** LEVEL_LABELS 用于层级门槛的提示文案（如无权访问页的「需要团队管理员及以上」）。 */
export const LEVEL_LABELS: Record<AccountLevel, string> = {
  super_admin: '平台超级管理员',
  platform_admin: '平台管理员',
  team_admin: '团队管理员',
  member: '普通成员',
};

/**
 * levelRank 把层级名转成序数；未知值给 0。
 * 给 0 而不是默认最高：库里出现脏值时它连 member 门槛都过不了，
 * 不会因为「没写层级」意外获得管理入口——与后端同一个安全取向。
 */
export function levelRank(level: string | null | undefined): number {
  if (!level) {
    return 0;
  }
  return LEVEL_RANK[level as AccountLevel] ?? 0;
}

/**
 * atLeastLevel 判断当前身份是否不低于 minLevel。
 * me 为 null（还没取回或取数失败）一律按「不满足」处理：
 * 宁可少渲染一个菜单，也不要先画出来再让它点了吃 403。
 */
export function atLeastLevel(me: Me | null, minLevel: AccountLevel): boolean {
  if (!me) {
    return false;
  }
  return levelRank(me.accountLevel) >= levelRank(minLevel);
}
