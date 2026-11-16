/**
 * 审计日志接口（仅超级管理员，只读）。
 * 职责：按动作 / 操作者 / 结果筛选并分页查询。
 * 输入：分页与筛选条件；输出：AuditLog 列表。
 * 边界：只读——没有写入接口，写入发生在服务端各业务 handler 内部。
 */
import type { components } from './schema';
import { get } from './client';
import type { PageQuery, PagedList } from './query';
import { pageQuery } from './query';

type Schemas = components['schemas'];

export type AuditLog = Schemas['AuditLog'];

const AUDIT_PATH = '/api/v1/admin/audit-logs';

export interface AuditFilters {
  action?: string;
  actor?: string;
  result?: 'success' | 'failure';
}

export function listAuditLogs(query: PageQuery & AuditFilters = {}): Promise<PagedList<AuditLog>> {
  const params = new URLSearchParams(pageQuery(query).replace(/^\?/, ''));
  if (query.action?.trim()) {
    params.set('action', query.action.trim());
  }
  if (query.actor?.trim()) {
    params.set('actor', query.actor.trim());
  }
  if (query.result) {
    params.set('result', query.result);
  }
  const text = params.toString();
  return get<PagedList<AuditLog>>(`${AUDIT_PATH}${text ? `?${text}` : ''}`);
}
