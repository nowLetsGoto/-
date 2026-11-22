/**
 * 展示层格式化与契约枚举的展示镜像。
 * 职责：字节 / 时间格式化，环境与平台的中文标签、文件扩展名预检，登录模式的标签与说明，
 *       需求状态/项目名/图片约束的展示文案。
 * 输入：原始数值、ISO UTC 字符串、契约枚举值、文件名。
 * 输出：可直接上屏的字符串与选项数组。
 * 边界：标签键集合由契约生成的 Environment / Platform 类型约束，改枚举时这里会编译报错；
 *       不生成任何 URL、不发请求。
 */
import type { Environment, Platform } from '../api/builds';
import type { RequirementStatus } from '../api/rdc';
import type { SessionMode } from '../api/session';

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

/**
 * 登录模式的中文标签。
 * 侧栏账号卡与审计日志都要展示模式，标签只能有一处定义，否则同一模式会出现两种叫法。
 */
export const MODE_LABELS: Record<SessionMode, string> = {
  solo: '单人模式',
  team: '团队模式',
};

/** 模式各自能看到什么，用于账号卡里的一行说明——用户自己就能判断"我为什么看不到某个菜单"。 */
export const MODE_HINTS: Record<SessionMode, string> = {
  solo: '只看自己上传的应用包',
  team: '查看团队全部数据与管理功能',
};

/**
 * 模式值 → 中文标签。
 * 兜底成「未知」而不是回显原值：后端旧版本不返回 mode 时原值是空串，
 * 回显会渲染出一个既没文字也没底色的空标签，看起来像样式坏了，实际是数据缺失。
 */
export function modeLabel(value: string): string {
  return MODE_LABELS[value as SessionMode] ?? (value || '未知');
}

export function platformLabel(value: string): string {
  return PLATFORMS.find((item) => item.value === value)?.label ?? value;
}

/**
 * 需求状态的中文标签，键集合由契约生成的 RequirementStatus 约束：
 * 契约新增/删除枚举值时这里会编译报错，避免出现"有状态却没有中文名"的裸值。
 */
export const REQUIREMENT_STATUS_LABELS: Record<RequirementStatus, string> = {
  pending_review: '待评审',
  confirmed: '已确认',
  in_development: '研发中',
  pending_acceptance: '待验收',
  delivered: '已交付',
  archived: '已归档',
};

/** 状态筛选项：全部 + 契约里的每个状态，供 Segmented 与表单下拉共用一份顺序。 */
export const REQUIREMENT_STATUS_OPTIONS: ReadonlyArray<Option<RequirementStatus>> = [
  { value: 'pending_review', label: '待评审' },
  { value: 'confirmed', label: '已确认' },
  { value: 'in_development', label: '研发中' },
  { value: 'pending_acceptance', label: '待验收' },
  { value: 'delivered', label: '已交付' },
  { value: 'archived', label: '已归档' },
];

/** V1.0 存量需求没有归属项目，列表列与详情统一显示这个文案，不回显空串。 */
export const UNASSIGNED_PROJECT_LABEL = '未归属项目';

/** 项目名展示的唯一入口：空串（未归属或项目已删除）都降级为统一文案。 */
export function projectLabel(projectName: string): string {
  return projectName || UNASSIGNED_PROJECT_LABEL;
}

/**
 * 需求图片约束的中文提示，与后端 contract / 前端 rdcUpload.ts 的常量一一对应。
 * 文案集中在这里，列表、表单与详情抽屉不会出现三种说法。
 */
export const RDC_IMAGE_ERRORS = {
  format: '图片仅支持 png / jpg / jpeg / webp / gif 格式',
  size: '单张图片不能超过 5MB',
  count: '每个需求最多上传 9 张图片',
} as const;

/** 服务端返回的历史状态或空值时兜底回显原值/未知，不渲染出空标签。 */
export function requirementStatusLabel(value: string): string {
  return REQUIREMENT_STATUS_LABELS[value as RequirementStatus] ?? (value || '未知');
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
