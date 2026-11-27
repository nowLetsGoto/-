/**
 * 系统设置与外部接口的前端唯一调用处。
 * 职责：读取设置、保存 Webhook 配置、触发测试投递。
 * 输入：契约生成的 WebhookConfig / Settings 类型；输出：同样的契约类型。
 * 边界：不写 localStorage、不做校验（服务端是唯一裁判）、不拼接非契约路径。
 */
import type { components } from './schema';
import { get, postJSON, putJSON } from './client';

type Schemas = components['schemas'];

export type Settings = Schemas['Settings'];
export type WebhookConfig = Schemas['WebhookConfig'];
export type WebhookProvider = Schemas['WebhookProvider'];
export type WebhookField = Schemas['WebhookField'];
export type WebhookTestResult = Schemas['WebhookTestResult'];
export type R2Config = Schemas['R2Config'];
export type R2UpdateRequest = Schemas['R2UpdateRequest'];
export type R2TestResult = Schemas['R2TestResult'];

/** 契约路径的前端唯一镜像。 */
const ROUTES = {
  settings: '/api/v1/settings',
  testWebhook: '/api/v1/settings/webhook/test',
  rotateApiKey: '/api/v1/settings/api-key/rotate',
  r2: '/api/v1/settings/r2',
  testR2: '/api/v1/settings/r2/test',
} as const;

export function getSettings(): Promise<Settings> {
  return get<Settings>(ROUTES.settings);
}

export function updateWebhook(input: WebhookConfig): Promise<Settings> {
  return putJSON<Settings>(ROUTES.settings, input);
}

/** 轮换 API Key：旧 Key 立即失效，新 Key 只在本响应里出现一次。 */
export function rotateApiKey(): Promise<{ apiKey: string }> {
  return postJSON<{ apiKey: string }>(ROUTES.rotateApiKey, {});
}

/** 测试投递：目标不可达也是 200，结果看 delivered / statusCode / message。 */
export function testWebhook(): Promise<WebhookTestResult> {
  return postJSON<WebhookTestResult>(ROUTES.testWebhook, {});
}

/** 读取 R2 配置：不含密钥明文，secretConfigured 只表达「配没配过」。 */
export function getR2(): Promise<R2Config> {
  return get<R2Config>(ROUTES.r2);
}

/** 保存 R2 配置；保存即热生效，无需重启进程。 */
export function updateR2(input: R2UpdateRequest): Promise<R2Config> {
  return putJSON<R2Config>(ROUTES.r2, input);
}

/**
 * 测试 R2 连通性。
 *
 * 测的是**已保存的配置**而不是表单里的值：页面应先保存再测试，
 * 否则会出现「测通了，但保存的不是测的那份」。
 */
export function testR2(): Promise<R2TestResult> {
  return postJSON<R2TestResult>(ROUTES.testR2, {});
}
