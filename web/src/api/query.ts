/**
 * 列表接口的公共分页参数。
 * 职责：统一的 PageQuery 类型与查询串拼装，避免每个 api 文件各写一遍 URLSearchParams。
 * 输入：{ page?, pageSize? }；输出：可直接拼到路径后的查询串（无参数时为空串）。
 * 边界：只负责拼串，不校验取值范围（夹紧在服务端 handler 里做）。
 */
export interface PageQuery {
  page?: number;
  pageSize?: number;
}

/** pageSize 显式传 0 表示「不分页、取全量」（下拉选项与聚合用），所以要用 !== undefined 判断。 */
export function pageQuery(query: PageQuery = {}): string {
  const params = new URLSearchParams();
  if (query.page !== undefined) {
    params.set('page', String(query.page));
  }
  if (query.pageSize !== undefined) {
    params.set('pageSize', String(query.pageSize));
  }
  const text = params.toString();
  return text ? `?${text}` : '';
}

/** 分页列表的统一响应形状：items 与分页元信息平铺。 */
export interface PagedList<T> {
  items: T[];
  total: number;
  page: number;
  pageSize: number;
}
