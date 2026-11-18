/**
 * 公司管理接口（仅平台超级管理员可用）。
 * 职责：公司列表、新建公司（连同其唯一平台管理员）、启用/停用、删除空公司。
 * 输入：契约生成的 Tenant 相关类型；输出：同样的契约类型。
 * 边界：不做缓存（公司变动极少，但列表页本就分页）；
 *       不传公司标识给业务接口——归属由登录身份推导，前端无法也不该指定。
 */
import type { components } from './schema';
import { del, get, postJSON } from './client';
import type { PageQuery, PagedList } from './query';
import { pageQuery } from './query';

type Schemas = components['schemas'];

export type Tenant = Schemas['Tenant'];
export type TenantInput = Schemas['TenantInput'];
export type TenantResult = Schemas['TenantResult'];

const TENANTS_PATH = '/api/v1/platform/tenants';

/** 列表：按创建顺序分页返回公司及其平台管理员账号名。 */
export function listTenants(query: PageQuery = {}): Promise<PagedList<Tenant>> {
  return get<PagedList<Tenant>>(`${TENANTS_PATH}${pageQuery(query)}`);
}

/**
 * 新建公司：公司与平台管理员原子创建。
 * 响应里的 password 是该管理员的一次性明文密码，只出现这一次，必须当场交代给用户。
 */
export function createTenant(input: TenantInput): Promise<TenantResult> {
  return postJSON<TenantResult>(TENANTS_PATH, input);
}

/** 公司状态取值；与后端 tenants.status 一致。 */
export type TenantStatus = 'active' | 'disabled';

/**
 * 启用 / 停用公司。停用后该公司所有账号立即无法登录——
 * 判定在登录链路上做（后端校验公司状态），前端不需要额外处理。
 */
export function setTenantStatus(id: string, status: TenantStatus): Promise<Tenant> {
  return postJSON<Tenant>(`${TENANTS_PATH}/${id}/status`, { status });
}

/**
 * 删除空公司；还有人员/团队/项目/账号/自定义角色时服务端返回 400 并说明还剩什么。
 * 不做级联删除——公司是数据隔离的最高边界，一次性清空不可撤销。
 */
export function deleteTenant(id: string): Promise<void> {
  return del<void>(`${TENANTS_PATH}/${id}`);
}
