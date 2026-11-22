/**
 * 前端唯一 HTTP 出入口。
 * 职责：拼接同源/代理基址、自动附带登录令牌、统一解包 {code,data,message}、把失败抛成 ApiError。
 * 输入：API 相对路径与 fetch 配置；输出：已解包的 data。
 * 边界：不业务判断、不缓存（只有 GET 的在途去重，落地即失效）、不拼 window.location.origin；
 *       FormData 不手动设 Content-Type（浏览器自带 boundary）；
 *       401 统一在这里处理（清会话 + 回登录页），页面无需各自兜底；
 *       每个请求带超时兜底（AbortController + setTimeout），避免服务端挂起时页面 loading 永久转圈。
 */
import { clearSession, readSession } from './session';

const baseURL: string = import.meta.env.VITE_API_BASE ?? '';

/** 登录页路径：401 时整页跳到这里，避免在 fetch 层引入 react-router。 */
const LOGIN_PAGE = '/login';

/** 单个请求超时（毫秒）：连上服务端却迟迟不响应时 abort 并抛超时 ApiError，兜住永久转圈。导出便于调整与测试。 */
export const REQUEST_TIMEOUT_MS = 15_000;

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

async function request<T>(path: string, init?: RequestInit, binary = false): Promise<T> {
  // 已登录就带上 Bearer；new Headers 兼容 init.headers 的三种写法（对象 / 元组数组 / Headers）。
  const headers = new Headers(init?.headers);
  const session = readSession();
  if (session) {
    headers.set('Authorization', `Bearer ${session.token}`);
  }

  // AbortController + 超时兜底：只建连不响应（端口错、代理挂起、服务端卡死）时 fetch 的 Promise
  // 永不 settle，页面会卡在 loading。超时后 abort 请求，并在 catch 里抛统一文案的 ApiError。
  const controller = new AbortController();
  const externalSignal = init?.signal;
  const abortFromExternal = (): void => controller.abort();
  if (externalSignal) {
    // 兼容调用方传入的 signal：外部中止要同步给内部 controller；已中止则直接中止。
    if (externalSignal.aborted) {
      controller.abort();
    } else {
      externalSignal.addEventListener('abort', abortFromExternal, { once: true });
    }
  }

  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, REQUEST_TIMEOUT_MS);

  try {
    let response: Response;
    try {
      response = await fetch(`${baseURL}${path}`, { ...init, headers, signal: controller.signal });
    } catch {
      // timedOut 只在定时器真正触发后置位：据此区分「请求超时」与「一般网络异常」。
      if (timedOut) {
        throw new ApiError('请求超时，请检查服务是否可用', 0);
      }
      throw new ApiError('网络异常，请检查服务是否可用', 0);
    }

    // 401 只有一个含义：令牌缺失或已失效（凭据错误走 400）。
    // 因此可以无条件清空会话并回到登录页；已在登录页时不再跳，避免打断登录失败提示。
    if (response.status === 401) {
      clearSession();
      if (window.location.pathname !== LOGIN_PAGE) {
        window.location.assign(LOGIN_PAGE);
      }
    }

    // 受保护的二进制资源（需求图片）：不对响应解 {code,data,message} 外壳，直接取 Blob。
    // 失败时仍尝试读一次外壳以拿到后端中文文案；读不到就退回 HTTP 状态码。
    if (binary) {
      if (timedOut) {
        throw new ApiError('请求超时，请检查服务是否可用', 0);
      }
      if (!response.ok) {
        let message = `请求失败（HTTP ${response.status}）`;
        try {
          const envelope = (await response.json()) as Envelope<unknown>;
          message = envelope.message || message;
        } catch {
          // 非 JSON 错误体保持默认文案。
        }
        throw new ApiError(message, response.status);
      }
      try {
        return (await response.blob()) as T;
      } catch {
        if (timedOut) {
          throw new ApiError('请求超时，请检查服务是否可用', 0);
        }
        throw new ApiError('读取图片失败，请稍后重试', response.status);
      }
    }

    let envelope: Envelope<T> | null = null;
    try {
      envelope = (await response.json()) as Envelope<T>;
    } catch {
      envelope = null;
    }
    if (envelope === null) {
      // body 读取阶段同样可能被超时 abort，这种情形是超时而不是「HTTP 失败」。
      if (timedOut) {
        throw new ApiError('请求超时，请检查服务是否可用', 0);
      }
      throw new ApiError(`请求失败（HTTP ${response.status}）`, response.status);
    }
    if (envelope.code !== 0 || !response.ok) {
      throw new ApiError(envelope.message || '请求失败', envelope.code || response.status);
    }
    return envelope.data as T;
  } finally {
    // 成功 / 失败 / 超时三条路径都清掉定时器与外部监听，避免泄漏。
    clearTimeout(timer);
    if (externalSignal) {
      externalSignal.removeEventListener('abort', abortFromExternal);
    }
  }
}

/**
 * GET 的在途去重：同一路径在这一个请求还没回来之前，后来的调用共享同一个 Promise。
 * 目的：React StrictMode 会在开发模式重复执行 effect，没有它同一个列表会打两枪；
 * 只在"请求进行中"去重，落地后立即出表，因此不会让人看到陈旧数据。
 */
const inflightGets = new Map<string, Promise<unknown>>();

export function get<T>(path: string): Promise<T> {
  const existing = inflightGets.get(path);
  if (existing) {
    return existing as Promise<T>;
  }
  const pending = request<T>(path, { method: 'GET' }).finally(() => {
    inflightGets.delete(path);
  });
  inflightGets.set(path, pending);
  return pending;
}

/** 受保护图片的二进制 GET：复用 Bearer / 超时 / 401 处理，但不解 {code,data,message} 外壳。 */
export function getBlob(path: string): Promise<Blob> {
  return request<Blob>(path, { method: 'GET' }, true);
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
