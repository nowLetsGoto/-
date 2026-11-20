/**
 * 前端 API 函数与 URL 唯一消费处。
 * 职责：定义五个接口调用；三个对外地址一律直接使用服务端返回的 Build DTO 字段。
 * 输入：File / 表单值 / 查询条件 / 包 id；输出：契约生成的类型。
 * 边界：禁止 window.location.origin 拼接、禁止用 downloadPagePath 生成二维码/复制/下载链接；
 *       commitBuild 只发 JSON（恰好 8 字段），绝不携带文件与 metaSource；isShareableUrl 只做可达性判断，不生成地址。
 */
import type { components } from './schema';
import { get, postForm, postJSON } from './client';

type Schemas = components['schemas'];

export type Build = Schemas['Build'];
export type StageResult = Schemas['StageResult'];
export type CommitBuildRequest = Schemas['CommitBuildRequest'];
export type ListBuildsResult = Schemas['ListBuildsResult'];
export type Environment = Schemas['Environment'];
export type Platform = Schemas['Platform'];
export type MetaSource = Schemas['MetaSource'];
export type PublicBuildVersion = Schemas['PublicBuildVersion'];

/** 契约路径的前端唯一镜像；页面不得自行拼 /api/v1 路径。 */
const ROUTES = {
  stage: '/api/v1/builds/stage',
  builds: '/api/v1/builds',
  buildById: (id: string): string => `/api/v1/builds/${encodeURIComponent(id)}`,
  buildVersions: (id: string): string => `/api/v1/builds/${encodeURIComponent(id)}/versions`,
} as const;

export interface ListQuery {
  /** 项目 key：不同项目的包互不可见 */
  project: string;
  environment: Environment;
  platform?: Platform | 'all';
}

/** 选文件后触发一次上传，服务端同步解析并返回 token；解析失败也只体现在 warnings。 */
export function stageBuild(file: File): Promise<StageResult> {
  const form = new FormData();
  form.append('file', file);
  return postForm<StageResult>(ROUTES.stage, form);
}

/** 提交发布 commit：只发 token 与表单最终值（恰好 8 字段），不携带文件、不携带 metaSource。 */
export function commitBuild(input: CommitBuildRequest): Promise<Build> {
  return postJSON<Build>(ROUTES.builds, input);
}

export function listBuilds(query: ListQuery): Promise<ListBuildsResult> {
  const params = new URLSearchParams({ project: query.project, environment: query.environment });
  if (query.platform && query.platform !== 'all') {
    params.set('platform', query.platform);
  }
  return get<ListBuildsResult>(`${ROUTES.builds}?${params.toString()}`);
}

export function getBuild(id: string): Promise<Build> {
  return get<Build>(ROUTES.buildById(id));
}

/** 公开下载页的「其它版本」：接口失败时页面直接隐藏板块，不阻断主下载流程。 */
export function listBuildVersions(id: string): Promise<PublicBuildVersion[]> {
  return get<PublicBuildVersion[]>(ROUTES.buildVersions(id));
}

/**
 * 只判断服务端给出的绝对地址能否被手机访问，用于二维码区的地址兜底；
 * 不参与地址生成，也不读 window.location。
 */
export function isShareableUrl(value: string): boolean {
  if (!value) {
    return false;
  }
  try {
    const url = new URL(value);
    if (url.protocol !== 'http:' && url.protocol !== 'https:') {
      return false;
    }
    const host = url.hostname.toLowerCase();
    return (
      host !== '' &&
      host !== 'localhost' &&
      host !== '127.0.0.1' &&
      host !== '0.0.0.0' &&
      host !== '::1'
    );
  } catch {
    return false;
  }
}
