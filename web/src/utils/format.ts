/**
 * 展示层格式化与契约枚举的展示镜像。
 * 职责：字节 / 时间格式化，环境与平台的中文标签、文件扩展名预检。
 * 输入：原始数值、ISO UTC 字符串、契约枚举值、文件名。
 * 输出：可直接上屏的字符串与选项数组。
 * 边界：标签键集合由契约生成的 Environment / Platform 类型约束，改枚举时这里会编译报错；
 *       不生成任何 URL、不发请求。
 */
import type { Environment, Platform } from '../api/builds';

export interface Option<T extends string> {
  value: T;
  label: string;
}

/**
 * 四环境常驻切换项。
 * 镜像自 api/openapi.yaml 的 x-environments / x-platforms，改动须同步契约（x-environments 的 key 与 label）。
 */
export const ENVIRONMENTS: ReadonlyArray<Option<Environment>> = [
  { value: 'test', label: '测试' },
  { value: 'hotfix', label: '紧急修复' },
  { value: 'pre', label: '预发' },
  { value: 'prd', label: '生产' },
];

/** 平台筛选项。镜像自 api/openapi.yaml 的 x-platforms（label 与 ext），改动须同步契约。 */
export const PLATFORMS: ReadonlyArray<Option<Platform>> = [
  { value: 'android', label: 'Android' },
  { value: 'ios', label: 'iOS' },
  { value: 'harmony', label: '鸿蒙' },
];

export function environmentLabel(value: string): string {
  return ENVIRONMENTS.find((item) => item.value === value)?.label ?? value;
}

export function platformLabel(value: string): string {
  return PLATFORMS.find((item) => item.value === value)?.label ?? value;
}

/**
 * 由扩展名预选平台提示（.apk → android、.ipa → ios、.hap → harmony）；
 * 最终落盘平台仍由用户表单值决定。acceptFile 也依赖它，扩展名白名单只有这一处。
 */
export function platformFromFileName(name: string): Platform | null {
  const lower = name.toLowerCase();
  if (lower.endsWith('.apk')) {
    return 'android';
  }
  if (lower.endsWith('.ipa')) {
    return 'ios';
  }
  if (lower.endsWith('.hap')) {
    return 'harmony';
  }
  return null;
}

export function acceptFile(name: string): boolean {
  return platformFromFileName(name) !== null;
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) {
    return '-';
  }
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  const units = ['KB', 'MB', 'GB', 'TB'];
  let value = bytes / 1024;
  let index = 0;
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024;
    index += 1;
  }
  return `${value.toFixed(value >= 10 ? 1 : 2)} ${units[index] ?? 'MB'}`;
}

/** ISO 8601 UTC → 本地可读的「年-月-日 时:分」。 */
export function formatDate(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) {
    return iso;
  }
  const pad = (value: number): string => String(value).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

export function shortText(text: string, max = 40): string {
  if (text.length <= max) {
    return text;
  }
  return `${text.slice(0, max)}…`;
}
