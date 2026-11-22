/**
 * 研发中心需求池接口的前端唯一调用处。
 * 职责：列出需求（分页 + 状态 + 项目 + 关键字）、按编号查详情、以 multipart 新建需求、
 *       以带 Bearer 的 Blob 请求读取受保护图片。
 * 输入：契约生成的 Requirement / RequirementDetail / RequirementInput 类型与查询条件。
 * 输出：同样的契约类型与统一分页形状 RequirementPage；图片以 Blob 返回。
 * 边界：只从 ./schema 引类型、只经 ./client 发请求；路径常量是后端契约的唯一镜像，
 *       不在这里拼业务参数以外的 URL，也不做本地缓存与权限判断。
 */
import type { components } from './schema';
import { get, postForm, getBlob } from './client';
import type { PageQuery, PagedList } from './query';
import { pageQuery } from './query';

type Schemas = components['schemas'];

export type Requirement = Schemas['Requirement'];
export type RequirementInput = Schemas['RequirementInput'];
export type RequirementStatus = Schemas['RequirementStatus'];
export type RequirementDetail = Schemas['RequirementDetail'];
export type RequirementAttachment = Schemas['RequirementAttachment'];

/** 契约路径的前端唯一镜像，与 contract 的路由常量对齐。 */
const REQUIREMENTS_PATH = '/api/v1/rdc/requirements';
const ATTACHMENTS_PATH = '/api/v1/rdc/requirement-attachments';

export type RequirementPage = PagedList<Requirement>;

/** 列表查询条件：分页由 pageQuery() 处理，状态 / 项目 / 关键字为空时不写入查询串。 */
export interface RequirementListQuery extends PageQuery {
  status?: RequirementStatus;
  keyword?: string;
  projectId?: string;
}

/**
 * 复用 pageQuery() 拼 page/pageSize，再追加状态、项目与关键字。
 * 用 URLSearchParams 承载，避免手写 '?' / '&' 拼接时漏掉空参数的前导符号。
 */
function requirementQuery(query: RequirementListQuery): string {
  const base = pageQuery(query);
  const params = new URLSearchParams(base.startsWith('?') ? base.slice(1) : base);
  if (query.status) {
    params.set('status', query.status);
  }
  if (query.projectId) {
    params.set('projectId', query.projectId);
  }
  if (query.keyword) {
    params.set('keyword', query.keyword);
  }
  const text = params.toString();
  return text ? `?${text}` : '';
}

/** 需求列表：数据范围由登录身份决定，前端不传任何公司字段。 */
export function listRequirements(query: RequirementListQuery = {}): Promise<RequirementPage> {
  return get<RequirementPage>(`${REQUIREMENTS_PATH}${requirementQuery(query)}`);
}

/** 需求详情（含项目名与图片附件列表）：reqKey 是稳定编号，编码后再拼路径。 */
export function getRequirement(reqKey: string): Promise<RequirementDetail> {
  return get<RequirementDetail>(`${REQUIREMENTS_PATH}/${encodeURIComponent(reqKey)}`);
}

/**
 * 新建需求：文本字段 + 图片装进 FormData 一次提交。
 * 不手动设 Content-Type（浏览器自带 multipart boundary）；编号与归属由服务端推导。
 */
export function createRequirement(input: RequirementInput, images: File[]): Promise<Requirement> {
  const form = new FormData();
  form.set('title', input.title);
  form.set('description', input.description);
  form.set('status', input.status ?? '');
  form.set('acceptanceCriteria', input.acceptanceCriteria ?? '');
  form.set('projectId', input.projectId);
  images.forEach((image) => form.append('images', image));
  return postForm<Requirement>(REQUIREMENTS_PATH, form);
}

/**
 * 受保护图片读取：裸 <img src> 不带 Bearer 会 401，因此取 Blob 后由页面 createObjectURL 渲染。
 */
export function getRequirementAttachment(id: string): Promise<Blob> {
  return getBlob(`${ATTACHMENTS_PATH}/${encodeURIComponent(id)}`);
}
