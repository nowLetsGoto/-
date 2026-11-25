/**
 * 职责：交付中心六接口的唯一消费处。
 * 输入：需求 ID、分页筛选及人工登记；输出：契约生成类型。
 * 边界：只经 client 发请求，不拼非契约路径、不判权限。
 */
import type { components } from './schema';
import { get, postJSON } from './client';
import type { PageQuery, PagedList } from './query';
import { pageQuery } from './query';

type Schemas = components['schemas'];
export type DeliveryRecord = Schemas['DeliveryRecord'];
export type DeliveryDetail = Schemas['DeliveryDetail'];
export type GitSnapshot = Schemas['GitSnapshot'];
export type TestResult = Schemas['TestResult'];
export type AcceptanceStatus = Schemas['AcceptanceStatus'];
export type GitStatus = Schemas['GitStatus'];
export type TestRegisterInput = Schemas['TestRegisterInput'];
export type DeliveryAcceptInput = Schemas['DeliveryAcceptInput'];
export interface DeliveryListQuery extends PageQuery {
  keyword?: string;
  acceptanceStatus?: AcceptanceStatus;
  archived?: boolean;
}
const route = '/api/v1/rdc/delivery/records';
const detail = (id: string): string => `${route}/${encodeURIComponent(id)}`;
export function listDeliveryRecords(
  query: DeliveryListQuery = {},
): Promise<PagedList<DeliveryRecord>> {
  const params = new URLSearchParams(pageQuery(query).replace(/^\?/, ''));
  if (query.keyword) params.set('keyword', query.keyword);
  if (query.acceptanceStatus) params.set('acceptanceStatus', query.acceptanceStatus);
  if (query.archived !== undefined) params.set('archived', String(query.archived));
  return get<PagedList<DeliveryRecord>>(`${route}?${params.toString()}`);
}
export function getDeliveryDetail(id: string): Promise<DeliveryDetail> {
  return get<DeliveryDetail>(detail(id));
}
export function refreshDeliveryGit(id: string): Promise<GitSnapshot> {
  return postJSON<GitSnapshot>(`${detail(id)}/git/refresh`, {});
}
export function registerDeliveryTest(
  id: string,
  input: TestRegisterInput,
): Promise<DeliveryRecord> {
  return postJSON<DeliveryRecord>(`${detail(id)}/test`, input);
}
export function acceptDelivery(id: string, input: DeliveryAcceptInput): Promise<DeliveryRecord> {
  return postJSON<DeliveryRecord>(`${detail(id)}/accept`, input);
}
export function archiveDelivery(id: string): Promise<DeliveryRecord> {
  return postJSON<DeliveryRecord>(`${detail(id)}/archive`, {});
}
