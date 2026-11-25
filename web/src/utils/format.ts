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
import type { DevSubtaskKind, DevSubtaskStatus } from '../api/rdcDev';
import type { SessionMode } from '../api/session';
import type { TestResult, AcceptanceStatus, GitStatus } from '../api/rdcDelivery';
import type {
  Decision,
  FileStatus,
  RiskLevel,
  Intervention,
  CheckpointStatus,
} from '../api/rdcInteraction';
import type {
  AIProvider,
  AIProtocol,
  AIReasoningEffort,
  AICapability,
  AITestCategory,
  AIModelInput,
  AIQuotaAction,
} from '../api/rdcModel';

export const TEST_RESULT_LABELS: Record<TestResult, string> = {
  not_registered: '未登记',
  passed: '通过',
  failed: '未通过',
};
export const ACCEPTANCE_STATUS_LABELS: Record<AcceptanceStatus, string> = {
  pending: '待验收',
  passed: '通过',
  failed: '未通过',
};
export const GIT_STATUS_LABELS: Record<GitStatus, string> = {
  ok: '已读取',
  not_configured: '未配置',
  path_forbidden: '路径受限',
  not_repo: '无效仓库',
  timeout: '读取超时',
  error: '读取失败',
};

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

/**
 * 可空数字的统一展示：null / undefined 显示破折号，而不是 0。
 * 这对应后端的 `*int64` 口径——「没有数据」与「确实是 0」是两件事，
 * 前端若把 null 渲染成 0，用户会按错的数字做容量规划。
 */
export function numberOrDash(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) {
    return '—';
  }
  return String(value);
}

/**
 * 研发子任务状态中文标签。键集合由契约生成的 DevSubtaskStatus 约束：
 * 契约十值（8 进度 + rejected + skipped）任一变化都会编译报错，
 * 不会出现"有状态却没有中文名"的裸值。
 */
export const DEV_STATUS_LABELS: Record<DevSubtaskStatus, string> = {
  pending: '待开发',
  ai_running: 'AI 执行中',
  ai_completed: 'AI 已完成',
  reviewing: '审核中',
  waiting_commit: '待提交',
  committed: '已提交',
  validated: '已验证',
  completed: '已完成',
  rejected: '已驳回（待重新研发）',
  skipped: '已跳过（无需开发）',
};

/** 子任务类型标签，键集合由契约 DevSubtaskKind 约束。 */
export const DEV_KIND_LABELS: Record<DevSubtaskKind, string> = {
  frontend: '前端',
  backend: '后端',
};

/**
 * 整体状态筛选项：九值，含 rejected（驳回是任务整体态，可在看板筛出），
 * 不含 skipped（它是子任务终态，不是任务整体态）。
 * 顺序与 x-rdc-dev-statuses 一致，供 Segmented 与列表标签共用。
 */
export const DEV_STATUS_OPTIONS: ReadonlyArray<Option<DevSubtaskStatus>> = [
  { value: 'pending', label: '待开发' },
  { value: 'ai_running', label: 'AI 执行中' },
  { value: 'ai_completed', label: 'AI 已完成' },
  { value: 'reviewing', label: '审核中' },
  { value: 'waiting_commit', label: '待提交' },
  { value: 'committed', label: '已提交' },
  { value: 'validated', label: '已验证' },
  { value: 'completed', label: '已完成' },
  { value: 'rejected', label: '已驳回' },
];

/**
 * 前端的"下一状态"镜像，逐项对应 api/openapi.yaml 的 x-rdc-dev-transitions。
 * 仅用于决定按钮是否可见与文案；真正合法性由后端 contract.DevNextStatus 兜底。
 */
export const DEV_NEXT_STATUS: Partial<Record<DevSubtaskStatus, DevSubtaskStatus>> = {
  pending: 'ai_running',
  ai_running: 'ai_completed',
  ai_completed: 'reviewing',
  reviewing: 'waiting_commit',
  waiting_commit: 'committed',
  committed: 'validated',
  validated: 'completed',
};

/** 序 0..6 才允许标跳过；completed / skipped 按钮不可用。 */
export function canSkipDevStatus(status: string): boolean {
  return status in DEV_NEXT_STATUS;
}

/** 状态标签统一入口：历史脏值兜底"未知"，不渲染空白徽标。 */
export function devStatusLabel(value: string): string {
  return DEV_STATUS_LABELS[value as DevSubtaskStatus] ?? (value || '未知');
}

/** 子任务类型标签统一入口。 */
export function devKindLabel(value: string): string {
  return DEV_KIND_LABELS[value as DevSubtaskKind] ?? (value || '未知');
}

/**
 * AI 交互层枚举中文标签。键集合由 api/schema 生成的契约类型约束：
 * 契约新增 / 删除枚举值时这里会编译报错，不会出现"有枚举却没有中文名"的裸值。
 */
export const REVIEW_DECISION_LABELS: Record<Decision, string> = {
  pending: '未处理',
  accepted: '已接受',
  rejected: '已拒绝',
};

export const INTERACTION_FILE_STATUS_LABELS: Record<FileStatus, string> = {
  added: '新增',
  modified: '修改',
  deleted: '删除',
  renamed: '重命名',
};

export const RISK_LEVEL_LABELS: Record<RiskLevel, string> = {
  low: '低风险',
  medium: '中风险',
  high: '高风险',
};

export const INTERVENTION_LABELS: Record<Intervention, string> = {
  exempt: '免审批',
  sample: '抽样检查',
  strict: '强制审批',
};

export const CHECKPOINT_STATUS_LABELS: Record<CheckpointStatus, string> = {
  active: '可回滚',
  rolled_back: '已回滚',
  partially_rolled_back: '部分回滚',
};

/** 交互证据动作的审核历史文案；既有状态动作与新增证据动作共用一份标签表。 */
export const REVIEW_ACTION_LABELS: Record<string, string> = {
  approve: '审核通过',
  reject: '驳回',
  commit: '提交确认',
  resubmit: '重新研发',
  diff_submitted: 'Diff 审查已提交',
  plan_confirmed: '计划已确认',
  checkpoint_rolled_back: '检查点已回滚',
  approval_decided: '审批结论已记录',
  rollback_blocked: '回滚被阻断',
};

/** 三态标签统一入口；历史脏值兜底"未处理"，避免渲染空白徽标。 */
export function decisionLabel(value: string): string {
  return REVIEW_DECISION_LABELS[value as Decision] ?? (value || '未处理');
}

/** 风险等级标签统一入口。 */
export function riskLabel(value: string): string {
  return RISK_LEVEL_LABELS[value as RiskLevel] ?? (value || '未知风险');
}

/** 介入强度标签统一入口。 */
export function interventionLabel(value: string): string {
  return INTERVENTION_LABELS[value as Intervention] ?? (value || '未知强度');
}

/** 审核历史动作标签统一入口（含交互证据动作）。 */
export function reviewActionLabel(value: string): string {
  return REVIEW_ACTION_LABELS[value] ?? (value || '未知动作');
}

/**
 * AI 模型模块的契约标签与可选模板镜像。
 * 键集合由 api/schema.d.ts 的枚举类型约束；与后端 contract/ai_model.go、openapi 的 x-rdc-ai-* 逐项对齐。
 * 模板只供表单"从模板填入"，不自动插库、不含任何 Key。
 */
export const AI_PROVIDER_LABELS: Record<AIProvider, string> = { scnet: 'scnet' };
/** 三种上游报文协议；顺序与后端 contract.AIProtocols、openapi 的 AIProtocol enum 一致。 */
export const AI_PROTOCOL_LABELS: Record<AIProtocol, string> = {
  openai: 'OpenAI 兼容（chat/completions）',
  anthropic: 'Anthropic（messages）',
  openai_responses: 'OpenAI Responses（responses）',
};
export const AI_REASONING_LABELS: Record<AIReasoningEffort, string> = {
  none: '关闭（none）',
  low: '低（low）',
  high: '高（high）',
  max: '最高（max）',
};
export const AI_CAPABILITY_LABELS: Record<AICapability, string> = {
  文本生成: '文本生成',
  深度思考: '深度思考',
  视觉理解: '视觉理解',
};
export const AI_TEST_CATEGORY_LABELS: Record<AITestCategory, string> = {
  ok: '调用成功',
  config_missing: '尚未配置可用模型',
  auth_failed: '密钥无效或已失效',
  parameter_error: '模型或参数不被支持',
  rate_limited: '供应商繁忙或达到公司调用上限',
  network_error: '网络不可达或调用超时',
  response_error: '供应商响应异常',
  // 能力不匹配的中文文案唯一入口：配了模型但没有一条满足声明的能力 / 上下文 / 思考强度。
  // 与 config_missing 的区别必须在界面上说清 —— 一个是"没配"，一个是"配了但不够用"，处置完全不同。
  capability_mismatch: '没有满足条件的模型，请调整能力 / 上下文 / 思考强度要求，或补充启用模型',
};

/** 统计分组维度标签；顺序与后端 AIUsageGroupBy 一致。 */
export const AI_USAGE_GROUP_LABELS: Record<string, string> = {
  model: '按模型',
  protocol: '按协议',
  scene: '按场景',
  day: '按天（UTC）',
};

/** 超阈值动作标签；warn 只告警照常调用，reject 直接拒绝且上游零请求。 */
export const AI_QUOTA_ACTION_LABELS: Record<AIQuotaAction, string> = {
  warn: '仅告警（照常调用）',
  reject: '拒绝调用',
};

export const AI_PROVIDER_OPTIONS: ReadonlyArray<Option<AIProvider>> = [
  { value: 'scnet', label: 'scnet' },
];
export const AI_PROTOCOL_OPTIONS: ReadonlyArray<Option<AIProtocol>> = [
  { value: 'openai', label: AI_PROTOCOL_LABELS.openai },
  { value: 'anthropic', label: AI_PROTOCOL_LABELS.anthropic },
  { value: 'openai_responses', label: AI_PROTOCOL_LABELS.openai_responses },
];
export const AI_REASONING_OPTIONS: ReadonlyArray<Option<AIReasoningEffort>> = [
  { value: 'none', label: '关闭（none）' },
  { value: 'low', label: '低（low）' },
  { value: 'high', label: '高（high）' },
  { value: 'max', label: '最高（max）' },
];
export const AI_QUOTA_ACTION_OPTIONS: ReadonlyArray<Option<AIQuotaAction>> = [
  { value: 'warn', label: AI_QUOTA_ACTION_LABELS.warn },
  { value: 'reject', label: AI_QUOTA_ACTION_LABELS.reject },
];

/** 标签统一入口；历史脏值回显原值，不渲染空白徽标。 */
export function aiProviderLabel(value: string): string {
  return AI_PROVIDER_LABELS[value as AIProvider] ?? (value || '未知');
}
export function aiProtocolLabel(value: string): string {
  return AI_PROTOCOL_LABELS[value as AIProtocol] ?? (value || '未知');
}
export function aiTestCategoryLabel(value: string): string {
  return AI_TEST_CATEGORY_LABELS[value as AITestCategory] ?? (value || '未知分类');
}
export function aiUsageGroupLabel(value: string): string {
  return AI_USAGE_GROUP_LABELS[value] ?? (value || '未知维度');
}
export function aiQuotaActionLabel(value: string): string {
  return AI_QUOTA_ACTION_LABELS[value as AIQuotaAction] ?? (value || '未知动作');
}

/**
 * 按协议返回 temperature 上限：anthropic 只接 0–1，其余 0–2。
 * 前端这份用于输入框提示与提交前拦截；真正的拦截仍在后端 validateAIModelInput（配置期 400）。
 */
export function aiTemperatureLimit(protocol: string): number {
  return protocol === 'anthropic' ? 1 : 2;
}

/**
 * temperature 越界时的中文提示（唯一来源）：协议上限不同，提示语也不同。
 * 参数用上限值而非协议名，是因为调用方手里刚好有 aiTemperatureLimit() 的结果，
 * 这样本函数不必再判断一次协议，两处上限口径不会分叉。
 */
export function aiTemperatureRangeHint(limit: number): string {
  return limit === 1 ? 'Anthropic 协议的温度取值范围为 0–1' : 'temperature 必须在 0-2 之间';
}

/**
 * 协议端点与认证方式的只读提示：用户据此确认基础地址是否与服务商匹配。
 * 这里只描述事实，不参与请求构造（请求头与端点全部由后端按协议决定）。
 */
export function aiProtocolHint(protocol: string): { endpoint: string; auth: string; note: string } {
  if (protocol === 'anthropic') {
    return {
      endpoint: '{基础地址}/messages',
      auth: 'x-api-key 请求头（版本头由后端写死，无需填写）',
      note: 'Anthropic 协议的 temperature 只能取 0–1；system 消息会合并为顶层字段。',
    };
  }
  if (protocol === 'openai_responses') {
    return {
      endpoint: '{基础地址}/responses',
      auth: 'Authorization: Bearer <Key>',
      note: 'Responses 协议的输出上限字段为 max_output_tokens，思考强度为三档枚举。',
    };
  }
  return {
    endpoint: '{基础地址}/chat/completions',
    auth: 'Authorization: Bearer <Key>',
    note: 'OpenAI 兼容协议：system 消息作为 messages 的一员下发。',
  };
}

/** scnet DeepSeek 可选模板：点击后填入表单，用户仍需自行录入 Key 并确认保存。 */
export const SC_AI_TEMPLATE: AIModelInput = {
  name: 'DeepSeek-V4.1-Flash-1017',
  provider: 'scnet',
  protocol: 'openai',
  baseUrl: 'https://api.scnet.cn/api/llm/v1',
  modelId: 'DeepSeek-V4.1-Flash-1017',
  thinkingEnabled: true,
  contextTokens: 8192,
  extraConfig: { temperature: 1, topP: 1, reasoningEffort: 'high', timeoutMs: 30000 },
  capabilities: ['文本生成', '深度思考', '视觉理解'],
};
