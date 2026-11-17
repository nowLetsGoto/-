/**
 * 前端唯一的权限点判定入口。
 * 职责：把「当前登录人 + 权限点」判断成「这个操作按钮要不要渲染」。
 * 输入：useMe 取回的 Me（可能还没加载完，为 null）；输出：布尔值。
 * 边界：**只做隐藏，不做拦截**——真正的边界在服务端中间件与 authz.Check，
 *       即使有人绕过前端直接发请求，服务端仍会回 403；
 *       不解析模式：单人模式的基线权限已由服务端算进 permissions，前端不必再分模式判断。
 */
import type { Me } from '../api/me';

/**
 * can 判断当前人员是否拥有某个权限点。
 * me 为 null 表示 /me 还没回来（或取数失败）：一律按「无权限」处理。
 * 理由是宁可少渲染一个按钮，也不要先画出按钮再让它点了报 403。
 */
export function can(me: Me | null, permission: string): boolean {
  return me?.permissions.includes(permission) === true;
}
