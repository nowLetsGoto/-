/**
 * 前端 API 函数与 URL 唯一消费处。
 * 职责：封装上传、查询、编辑与删除；三个对外地址一律使用服务端返回的 Build DTO 字段。
 * 输入：File / 表单值 / 查询条件 / 包 id；输出：契约生成的类型。
 * 边界：禁止 window.location.origin 拼接、禁止用 downloadPagePath 生成二维码/复制/下载链接；
 *       commitBuild 只发 JSON（恰好 8 字段），绝不携带文件与 metaSource；isShareableUrl 只做可达性判断，不生成地址。
 */
import type { components } from './schema';
import { ApiError, del, get, getShared, postForm, postJSON, putJSON } from './client';

type Schemas = components['schemas'];

export type Build = Schemas['Build'];
export type SyncProgress = Schemas['SyncProgress'];
export type DownloadBuild = Schemas['SharedBuild'];
export type StageResult = Schemas['StageResult'];
export type CommitBuildRequest = Schemas['CommitBuildRequest'];
export type UpdateBuildRequest = Schemas['UpdateBuildRequest'];
export type ListBuildsResult = Schemas['ListBuildsResult'];
export type Environment = Schemas['Environment'];
export type Platform = Schemas['Platform'];
export type MetaSource = Schemas['MetaSource'];
export type PublicBuildVersion = Schemas['PublicBuildVersion'];

/** 直传 R2 的票据。字段与 /builds/upload-ticket 响应一一对应。 */
export interface UploadTicket {
  uploadToken: string;
  putUrl: string;
  /** PUT 时必须原样带上的头：签名里含 content-type 与 length。 */
  header: Record<string, string>;
  expiresInSeconds: number;
}

/** 契约路径的前端唯一镜像；页面不得自行拼 /api/v1 路径。 */
const ROUTES = {
  sharedBuild: (id: string): string => `/api/v1/public/builds/${encodeURIComponent(id)}`,
  sharedDownloadCount: (id: string): string =>
    `/api/v1/public/builds/${encodeURIComponent(id)}/download-count`,
  sharedVersions: (id: string): string =>
    `/api/v1/public/builds/${encodeURIComponent(id)}/versions`,
  stage: '/api/v1/builds/stage',
  uploadTicket: '/api/v1/builds/upload-ticket',
  stageFromR2: '/api/v1/builds/stage-from-r2',
  uploadAbort: '/api/v1/builds/upload-abort',
  builds: '/api/v1/builds',
  buildById: (id: string): string => `/api/v1/builds/${encodeURIComponent(id)}`,
  buildVersions: (id: string): string => `/api/v1/builds/${encodeURIComponent(id)}/versions`,
  buildSyncToR2: (id: string): string => `/api/v1/builds/${encodeURIComponent(id)}/sync-to-r2`,
  buildSyncProgress: (id: string): string =>
    `/api/v1/builds/${encodeURIComponent(id)}/sync-progress`,
  storageUsage: '/api/v1/storage/usage',
} as const;

/**
 * StorageUsage 是当前租户的存储用量视图。
 *
 * 从 openapi 派生而非手写：手写一份就要跟服务端同步维护，
 * 而两处描述同一份结构时，漂移只是时间问题（这一批加四个字段时就差点漏改）。
 *
 * 所有派生量（剩余额度、可上传数量）都由服务端算好：
 * 前端只展示，不重复实现配额口径——两处各算一遍必然漂移。
 */
export type StorageUsage = Schemas['StorageUsage'];

export type StorageUsageGroup = Schemas['StorageUsageGroup'];

/** 读取当前租户的存储用量；租户由登录态决定，前端不传参。 */
export function fetchStorageUsage(): Promise<StorageUsage> {
  return get<StorageUsage>(ROUTES.storageUsage);
}

export interface ListQuery {
  /** 项目 key：不同项目的包互不可见 */
  project: string;
  environment: Environment | 'all';
  platform?: Platform | 'all';
}

/** 选文件后触发一次上传，服务端同步解析并返回 token；解析失败也只体现在 warnings。 */
export function stageBuild(file: File): Promise<StageResult> {
  const form = new FormData();
  form.append('file', file);
  return postForm<StageResult>(ROUTES.stage, form);
}

/**
 * 申请直传票据。用于超过 Tunnel 100 MB 上限的大包。
 *
 * project 必须传入：服务端要用它把"租户/项目"编进 R2 对象 key，
 * 使对象自带归属信息（数据库丢失时还能靠 key 还原归属）。
 * 未启用对象存储时服务端返回 400，调用方应回落到 stageBuild。
 */
export function requestUploadTicket(file: File, project: string): Promise<UploadTicket> {
  return postJSON<UploadTicket>(ROUTES.uploadTicket, {
    project,
    fileName: file.name,
    size: file.size,
    contentType: file.type || 'application/octet-stream',
  });
}

/**
 * 用 XHR 把文件直接 PUT 到 R2。返回 Promise，onProgress 收到 0-100 的百分比。
 *
 * 为什么用 XHR 而不是 fetch：fetch 没有上传进度事件，
 * 而 300 MB 的包在慢速上行下要传十几分钟，没有进度条用户会以为卡死。
 * 这里不带 Authorization —— 授权信息已经在预签名 URL 的查询参数里。
 */
export function putToR2(
  ticket: UploadTicket,
  file: File,
  onProgress?: (percent: number) => void,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('PUT', ticket.putUrl, true);
    for (const [key, value] of Object.entries(ticket.header)) {
      xhr.setRequestHeader(key, value);
    }
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable && onProgress) {
        onProgress(Math.round((event.loaded / event.total) * 100));
      }
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        onProgress?.(100);
        resolve();
        return;
      }
      // R2 的失败响应是 XML（S3 协议），这里只取状态码给用户，细节留给控制台。
      reject(new ApiError(`上传到对象存储失败（HTTP ${xhr.status}）`, xhr.status));
    };
    xhr.onerror = () => reject(new ApiError('上传到对象存储失败，请检查网络', 0));
    xhr.onabort = () => reject(new ApiError('上传已取消', 0));
    xhr.send(file);
  });
}

/** 回拉已直传到 R2 的对象并解析，返回与 stageBuild 一致的 StageResult。 */
export function stageFromR2(uploadToken: string): Promise<StageResult> {
  return postJSON<StageResult>(ROUTES.stageFromR2, { uploadToken });
}

/** 放弃直传，让服务端删掉会话与 R2 临时对象。失败可忽略（过期会兜底清理）。 */
export function abortUpload(uploadToken: string): Promise<unknown> {
  return postJSON<unknown>(ROUTES.uploadAbort, { uploadToken });
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

export function updateBuild(id: string, input: UpdateBuildRequest): Promise<Build> {
  return putJSON<Build>(ROUTES.buildById(id), input);
}

export function deleteBuild(id: string): Promise<void> {
  return del<void>(ROUTES.buildById(id));
}

/**
 * 把已被容量淘汰的包重新推回对象存储。
 *
 * 只对 r2State === 'evicted' 的包有意义：本机是权威副本、R2 是可回收的缓存，
 * 淘汰后下载会回落到本机流式（较慢），调用它可以恢复 CDN 直链。
 * 已同步时服务端幂等返回，重复点击不会报错。
 */
export function syncBuildToR2(id: string): Promise<Build> {
  return postJSON<Build>(ROUTES.buildSyncToR2(id), {});
}

/** 查询后台重推的实时进度，供包卡片画进度条。 */
export function getSyncProgress(id: string): Promise<SyncProgress> {
  return get<SyncProgress>(ROUTES.buildSyncProgress(id));
}

/** 独立分享读取当前授权版本，不依赖登录态。 */
export function getSharedBuild(id: string, token: string): Promise<DownloadBuild> {
  return getShared<DownloadBuild>(ROUTES.sharedBuild(id), token);
}

/** 给直接走 CDN 的分享下载补计数；凭证放 query，与文件接口保持一致。 */
export function recordSharedDownload(id: string, token: string): Promise<void> {
  const query = new URLSearchParams({ shareToken: token });
  return postJSON<void>(`${ROUTES.sharedDownloadCount(id)}?${query.toString()}`, {});
}

/** 先验证来源分享，再返回同应用的其它有效分享版本及各自的目标地址。 */
export function listSharedBuildVersions(id: string, token: string): Promise<PublicBuildVersion[]> {
  return getShared<PublicBuildVersion[]>(ROUTES.sharedVersions(id), token);
}

/** 登录预览的其它版本与平台包，由 hook 按平台、页面按环境选择。 */
export function listBuildVersions(id: string): Promise<PublicBuildVersion[]> {
  return get<PublicBuildVersion[]>(`${ROUTES.buildVersions(id)}?includePlatforms=true`);
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
