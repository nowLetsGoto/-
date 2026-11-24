/**
 * 前端唯一 HTTP 出入口。
 * 职责：拼接同源/代理基址、自动附带登录令牌、统一解包 {code,data,message}、把失败抛成 ApiError。
 * 输入：API 相对路径与 fetch 配置；输出：已解包的 data。
 * 边界：不业务判断、不缓存（只有 GET 的在途去重，落地即失效）、不拼 window.location.origin；
 *       FormData 不手动设 Content-Type（浏览器自带 boundary）；
 *       登录请求的 401 在这里处理（清会话 + 回登录页），分享错误留在分享页，页面无需各自兜底。
 */
import { clearSession, readSession } from './session';

const baseURL: string = import.meta.env.VITE_API_BASE ?? '';

/** 登录页路径：401 时整页跳到这里，避免在 fetch 层引入 react-router。 */
const LOGIN_PAGE = '/login';

/** ApiError 携带后端 code，文案可直接上屏。 */
export class ApiError extends Error {
  readonly code: number;

  constructor(message: string, code: number) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
  }
}

interface Envelope<T> {
  code: number;
  data: T | null;
  message: string;
}

async function request<T>(path: string, init?: RequestInit, shareToken?: string): Promise<T> {
  // 已登录就带上 Bearer；new Headers 兼容 init.headers 的三种写法（对象 / 元组数组 / Headers）。
  const headers = new Headers(init?.headers);
  const shared = shareToken !== undefined;
  const session = shared ? null : readSession();
  if (shared) {
    headers.set('X-Share-Token', shareToken);
  }
  if (session) {
    headers.set('Authorization', `Bearer ${session.token}`);
  }

  let response: Response;
  try {
    response = await fetch(`${baseURL}${path}`, {
      ...init,
      headers,
      ...(shared ? { credentials: 'omit', cache: 'no-store' } : {}),
    });
  } catch {
    throw new ApiError('网络异常，请检查服务是否可用', 0);
  }

  // 401 只有一个含义：令牌缺失或已失效（凭据错误走 400）。
  // 因此可以无条件清空会话并回到登录页；已在登录页时不再跳，避免打断登录失败提示。
  if (!shared && response.status === 401) {
    clearSession();
    if (window.location.pathname !== LOGIN_PAGE) {
      window.location.assign(LOGIN_PAGE);
    }
  }

  let envelope: Envelope<T> | null = null;
  try {
    envelope = (await response.json()) as Envelope<T>;
  } catch {
    envelope = null;
  }
  if (envelope === null) {
    throw new ApiError(`请求失败（HTTP ${response.status}）`, response.status);
  }
  if (envelope.code !== 0 || !response.ok) {
    throw new ApiError(envelope.message || '请求失败', envelope.code || response.status);
  }
  return envelope.data as T;
}

/**
 * GET 的在途去重：同一路径在这一个请求还没回来之前，后来的调用共享同一个 Promise。
 * 目的：React StrictMode 会在开发模式重复执行 effect，没有它同一个列表会打两枪；
 * 只在"请求进行中"去重，落地后立即出表，因此不会让人看到陈旧数据。
 */
const inflightGets = new Map<string, Promise<unknown>>();

export function get<T>(path: string): Promise<T> {
  return getWithCredential<T>(path);
}

/** 分享请求使用独立凭证，不携带账号令牌，失效时保留当前登录会话。 */
export function getShared<T>(path: string, shareToken: string): Promise<T> {
  return getWithCredential<T>(path, shareToken);
}

function getWithCredential<T>(path: string, shareToken?: string): Promise<T> {
  const credential =
    shareToken !== undefined ? `share:${shareToken}` : `session:${readSession()?.token ?? ''}`;
  const key = `${path}\0${credential}`;
  const existing = inflightGets.get(key);
  if (existing) {
    return existing as Promise<T>;
  }
  const pending = request<T>(path, { method: 'GET' }, shareToken).finally(() => {
    inflightGets.delete(key);
  });
  inflightGets.set(key, pending);
  return pending;
}

export function postJSON<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
}

export function putJSON<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
}

export function del<T>(path: string): Promise<T> {
  return request<T>(path, { method: 'DELETE' });
}

export function postForm<T>(path: string, form: FormData): Promise<T> {
  return request<T>(path, { method: 'POST', body: form });
}

/** errorMessage 把任意异常转成中文可读文案，供各页面统一使用。 */
export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    return err.message;
  }
  if (err instanceof Error && err.message) {
    return err.message;
  }
  return '请求失败，请稍后重试';
}
