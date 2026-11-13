/**
 * 前端唯一 HTTP 出入口。
 * 职责：拼接同源/代理基址、统一解包 {code,data,message}、把失败抛成 ApiError。
 * 输入：API 相对路径与 fetch 配置；输出：已解包的 data。
 * 边界：不业务判断、不缓存、不拼 window.location.origin；FormData 不手动设 Content-Type（浏览器自带 boundary）。
 */
const baseURL: string = import.meta.env.VITE_API_BASE ?? '';

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

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let response: Response;
  try {
    response = await fetch(`${baseURL}${path}`, init);
  } catch {
    throw new ApiError('网络异常，请检查服务是否可用', 0);
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

export function get<T>(path: string): Promise<T> {
  return request<T>(path, { method: 'GET' });
}

export function postJSON<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
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
