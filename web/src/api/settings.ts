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

/** 契约路径的前端唯一镜像。 */
const ROUTES = {
  settings: '/api/v1/settings',
  testWebhook: '/api/v1/settings/webhook/test',
  rotateApiKey: '/api/v1/settings/api-key/rotate',
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
