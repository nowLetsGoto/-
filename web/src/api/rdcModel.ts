/**
 * 第 5 菜单「AI模型」接口的前端唯一调用处。
 * 职责：列表 / 详情 / 新增 / 编辑 / 设默认 / 启停 / 软删 / 实测 / 统计 / 阈值读写共 11 个操作。
 * 输入：契约生成的 AIModel / AIModelInput / AIModelStatusInput / AIModelTestResult /
 *       AIModelCallStats / AIQuotaConfig / AIQuotaInput 与查询条件。
 * 输出：同样的契约类型与统一分页形状 PagedList<AIModel>。
 * 边界：类型只从 ./schema 引、请求只经 ./client；不拼业务 URL、不判权限、
 *       不接触 Key 明文之外的加密细节（表单明文只作为请求体字段传一次）。
 */
import type { components } from './schema';
import { del, get, postJSON, putJSON } from './client';
import type { PageQuery, PagedList } from './query';
import { pageQuery } from './query';

type Schemas = components['schemas'];

export type AIProvider = Schemas['AIProvider'];
export type AIProtocol = Schemas['AIProtocol'];
export type AIReasoningEffort = Schemas['AIReasoningEffort'];
export type AICapability = Schemas['AICapability'];
export type AITestCategory = Schemas['AITestCategory'];
export type AIExtraConfig = Schemas['AIExtraConfig'];
export type AIModel = Schemas['AIModel'];
export type AIModelInput = Schemas['AIModelInput'];
export type AIModelStatusInput = Schemas['AIModelStatusInput'];
export type AIModelTestResult = Schemas['AIModelTestResult'];
export type AIQuotaAction = Schemas['AIQuotaAction'];
export type AIQuotaConfig = Schemas['AIQuotaConfig'];
export type AIQuotaInput = Schemas['AIQuotaInput'];
export type AIModelCallGroup = Schemas['AIModelCallGroup'];
export type AIModelCallStats = Schemas['AIModelCallStats'];

const MODELS_PATH = '/api/v1/rdc/models';

/** 后端硬超时 120 秒，前端留 5 秒余量；仅实测接口使用，避免默认 15 秒误判。 */
const MODEL_TEST_TIMEOUT_MS = 125_000;

export type AIModelPage = PagedList<AIModel>;

export interface AIModelListQuery extends PageQuery {
  enabled?: boolean;
  /** 按协议筛选；空串 = 不筛。 */
  protocol?: AIProtocol | '';
  /** 仅超管需要：目标公司 id；非超管传入也会被后端忽略。 */
  tenantId?: string;
}

/** 复用 pageQuery() 拼分页，再追加启用态 / 协议 / 目标公司。 */
function aiModelQuery(query: AIModelListQuery): string {
  const base = pageQuery(query);
  const params = new URLSearchParams(base.startsWith('?') ? base.slice(1) : base);
  if (query.enabled !== undefined) {
    params.set('enabled', String(query.enabled));
  }
  if (query.protocol) {
    params.set('protocol', query.protocol);
  }
  if (query.tenantId) {
    params.set('tenantId', query.tenantId);
  }
  const text = params.toString();
  return text ? `?${text}` : '';
}

/** 列表：超管未选公司时后端返回空数组，不发额外请求。 */
export function listAIModels(query: AIModelListQuery = {}): Promise<AIModelPage> {
  return get<AIModelPage>(`${MODELS_PATH}${aiModelQuery(query)}`);
}

/** 详情：只返回掩码，不返回完整 Key。 */
export function getAIModel(id: string, tenantId?: string): Promise<AIModel> {
  const suffix = tenantId ? `?tenantId=${encodeURIComponent(tenantId)}` : '';
  return get<AIModel>(`${MODELS_PATH}/${encodeURIComponent(id)}${suffix}`);
}

/** 新建：Key 由表单一次性传入，服务端加密后只回掩码。 */
export function createAIModel(input: AIModelInput, tenantId?: string): Promise<AIModel> {
  const suffix = tenantId ? `?tenantId=${encodeURIComponent(tenantId)}` : '';
  return postJSON<AIModel>(`${MODELS_PATH}${suffix}`, input);
}

/** 编辑：apiKey 留空表示不更新，前端不回填掩码到输入框。 */
export function updateAIModel(
  id: string,
  input: AIModelInput,
  tenantId?: string,
): Promise<AIModel> {
  const suffix = tenantId ? `?tenantId=${encodeURIComponent(tenantId)}` : '';
  return putJSON<AIModel>(`${MODELS_PATH}/${encodeURIComponent(id)}${suffix}`, input);
}

/** 设为默认：同一公司内事务互斥，影响下一次调用选模。 */
export function setAIModelDefault(id: string, tenantId?: string): Promise<AIModel> {
  const suffix = tenantId ? `?tenantId=${encodeURIComponent(tenantId)}` : '';
  return postJSON<AIModel>(`${MODELS_PATH}/${encodeURIComponent(id)}/default${suffix}`, {});
}

/** 启停：停用默认模型且另有启用项时后端返回 400，需先指定新默认。 */
export function setAIModelStatus(
  id: string,
  enabled: boolean,
  tenantId?: string,
): Promise<AIModel> {
  const suffix = tenantId ? `?tenantId=${encodeURIComponent(tenantId)}` : '';
  return postJSON<AIModel>(`${MODELS_PATH}/${encodeURIComponent(id)}/status${suffix}`, {
    enabled,
  } satisfies AIModelStatusInput);
}

/** 软删：enabled=false，不物理删除；审计仍可追溯。 */
export function deleteAIModel(id: string, tenantId?: string): Promise<null> {
  const suffix = tenantId ? `?tenantId=${encodeURIComponent(tenantId)}` : '';
  return del<null>(`${MODELS_PATH}/${encodeURIComponent(id)}${suffix}`);
}

/** 实测：与内部 Chat 共用实现；成功与失败都以 200 + 分类结果返回。 */
export function testAIModel(id: string, tenantId?: string): Promise<AIModelTestResult> {
  const suffix = tenantId ? `?tenantId=${encodeURIComponent(tenantId)}` : '';
  return postJSON<AIModelTestResult>(
    `${MODELS_PATH}/${encodeURIComponent(id)}/test${suffix}`,
    {},
    MODEL_TEST_TIMEOUT_MS,
  );
}

/** 统计查询条件；时间范围为半开区间 [start, end)，缺省由后端补最近 7 天。 */
export interface AIUsageQuery {
  start?: string;
  end?: string;
  groupBy?: string[];
  includeLocal?: boolean;
  /** 仅超管需要：目标公司 id；非超管传入也会被后端忽略。 */
  tenantId?: string;
}

/** 统计查询：读点 rdc.model.read；token / 耗时可能为 null，表示"没有可用数据"。 */
export function getAIModelUsage(query: AIUsageQuery = {}): Promise<AIModelCallStats> {
  const params = new URLSearchParams();
  if (query.start) {
    params.set('start', query.start);
  }
  if (query.end) {
    params.set('end', query.end);
  }
  if (query.groupBy && query.groupBy.length > 0) {
    params.set('groupBy', query.groupBy.join(','));
  }
  if (query.includeLocal) {
    params.set('includeLocal', 'true');
  }
  if (query.tenantId) {
    params.set('tenantId', query.tenantId);
  }
  const text = params.toString();
  return get<AIModelCallStats>(`${MODELS_PATH}/usage${text ? `?${text}` : ''}`);
}

/** 读阈值：从未配置过时后端返回默认值（enabled=false），updatedAt 为空串。 */
export function getAIModelQuota(tenantId?: string): Promise<AIQuotaConfig> {
  const suffix = tenantId ? `?tenantId=${encodeURIComponent(tenantId)}` : '';
  return get<AIQuotaConfig>(`${MODELS_PATH}/quota${suffix}`);
}

/** 写阈值：整行覆盖；写点 rdc.model.write，后端会记一条审计（不含 Key）。 */
export function updateAIModelQuota(input: AIQuotaInput, tenantId?: string): Promise<AIQuotaConfig> {
  const suffix = tenantId ? `?tenantId=${encodeURIComponent(tenantId)}` : '';
  return putJSON<AIQuotaConfig>(`${MODELS_PATH}/quota${suffix}`, input);
}
