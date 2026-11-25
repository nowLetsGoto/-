/**
 * AI模型新增 / 编辑页（同一组件复用两条路由）。
 * 职责：采集名称 / provider / protocol / baseUrl / modelId / Key / 思考 / 上下文 / 四个扩展字段，
 *       提供 scnet 可选模板按钮，保存后回到列表。
 * 输入：路由 id、URL 的 tenantId（仅超管代配）、用户填写的契约字段与已存模型详情。
 * 输出：createAIModel / updateAIModel 请求；保存成功即时生效于下一次调用。
 * 边界：编辑时 Key 输入框留空即不修改，不回填掩码作为值；能力标签参与能力调度（请求声明的能力必须全部具备），
 *       但本页只负责采集，不做匹配判定；
 *       不写完整 Key 到页面状态以外的任何位置，不发 LLM 请求；协议端点与认证方式只做只读提示，
 *       真正的请求头与端点由后端按协议决定（前端不构造报文）。
 */
import { useEffect, useState } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { errorMessage } from '../../api/client';
import { createAIModel, getAIModel, updateAIModel } from '../../api/rdcModel';
import type {
  AIModelInput,
  AIProvider,
  AIProtocol,
  AIReasoningEffort,
  AICapability,
} from '../../api/rdcModel';
import { Button, Checkbox, Input } from '../../components/Controls';
import { FormPage } from '../../components/FormPage';
import { Select } from '../../components/Select';
import { Loading } from '../../components/States';
import { useMe } from '../../hooks/useMe';
import {
  AI_PROTOCOL_OPTIONS,
  AI_PROVIDER_OPTIONS,
  AI_REASONING_OPTIONS,
  SC_AI_TEMPLATE,
  aiProtocolHint,
  aiTemperatureLimit,
  aiTemperatureRangeHint,
} from '../../utils/format';

const LIST_PATH = '/rdc/models';

interface FormState {
  name: string;
  provider: AIProvider;
  protocol: AIProtocol;
  baseUrl: string;
  modelId: string;
  apiKey: string;
  thinkingEnabled: boolean;
  contextTokens: string;
  temperature: string;
  topP: string;
  reasoningEffort: AIReasoningEffort;
  timeoutMs: string;
  capabilities: AICapability[];
}

const TEMPLATE_CAPABILITIES = SC_AI_TEMPLATE.capabilities ?? [];

function fromTemplate(): FormState {
  return {
    name: SC_AI_TEMPLATE.name,
    provider: SC_AI_TEMPLATE.provider,
    protocol: SC_AI_TEMPLATE.protocol,
    baseUrl: SC_AI_TEMPLATE.baseUrl,
    modelId: SC_AI_TEMPLATE.modelId,
    apiKey: '',
    thinkingEnabled: SC_AI_TEMPLATE.thinkingEnabled,
    contextTokens: String(SC_AI_TEMPLATE.contextTokens),
    temperature: String(SC_AI_TEMPLATE.extraConfig?.temperature ?? 1),
    topP: String(SC_AI_TEMPLATE.extraConfig?.topP ?? 1),
    reasoningEffort: SC_AI_TEMPLATE.extraConfig?.reasoningEffort ?? 'high',
    timeoutMs: String(SC_AI_TEMPLATE.extraConfig?.timeoutMs ?? 30000),
    capabilities: [...TEMPLATE_CAPABILITIES],
  };
}

const EMPTY_FORM: FormState = {
  name: '',
  provider: 'scnet',
  protocol: 'openai',
  baseUrl: '',
  modelId: '',
  apiKey: '',
  thinkingEnabled: false,
  contextTokens: '8192',
  temperature: '1',
  topP: '1',
  reasoningEffort: 'high',
  timeoutMs: '30000',
  capabilities: [],
};

export function ModelFormPage(): JSX.Element {
  const navigate = useNavigate();
  const { id } = useParams<{ id: string }>();
  const [params] = useSearchParams();
  const tenantId = (params.get('tenantId') ?? '').trim();
  const { me } = useMe();
  const editing = Boolean(id);

  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [mask, setMask] = useState('');
  const [loading, setLoading] = useState(editing);
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState('');

  useEffect(() => {
    if (!id) {
      return;
    }
    let cancelled = false;
    getAIModel(id, tenantId || undefined)
      .then((model) => {
        if (cancelled) {
          return;
        }
        setForm({
          name: model.name,
          provider: model.provider,
          protocol: model.protocol,
          baseUrl: model.baseUrl,
          modelId: model.modelId,
          apiKey: '',
          thinkingEnabled: model.thinkingEnabled,
          contextTokens: String(model.contextTokens),
          temperature: String(model.extraConfig.temperature),
          topP: String(model.extraConfig.topP),
          reasoningEffort: model.extraConfig.reasoningEffort ?? 'high',
          timeoutMs: String(model.extraConfig.timeoutMs),
          capabilities: [...model.capabilities],
        });
        setMask(model.apiKeyMask);
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setNotice(errorMessage(err));
          setLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [id, tenantId]);

  const update = <K extends keyof FormState>(key: K, value: FormState[K]): void => {
    setForm((current) => ({ ...current, [key]: value }));
  };

  const toggleCapability = (capability: AICapability, checked: boolean): void => {
    setForm((current) => ({
      ...current,
      capabilities: checked
        ? [...current.capabilities, capability]
        : current.capabilities.filter((item) => item !== capability),
    }));
  };

  const submit = async (): Promise<void> => {
    const name = form.name.trim();
    const baseUrl = form.baseUrl.trim();
    const modelId = form.modelId.trim();
    const contextTokens = Number(form.contextTokens);
    const temperature = Number(form.temperature);
    const topP = Number(form.topP);
    const timeoutMs = Number(form.timeoutMs);
    if (!name || name.length > 100 || !baseUrl || !modelId) {
      setNotice('请填写名称、API 地址与模型 ID（名称不超过 100 字）');
      return;
    }
    if (!Number.isInteger(contextTokens) || contextTokens < 1024 || contextTokens > 393216) {
      setNotice('上下文长度必须是 1024-393216 之间的整数');
      return;
    }
    // 温度上限按协议收窄：anthropic 只接 0–1，其余 0–2。这里提前拦一次能省一次往返，
    // 真正的裁决仍在后端 validateAIModelInput（配置期 400 优于调用期被上游拒）。
    const temperatureLimit = aiTemperatureLimit(form.protocol);
    if (!Number.isFinite(temperature) || temperature < 0 || temperature > temperatureLimit) {
      setNotice(aiTemperatureRangeHint(temperatureLimit));
      return;
    }
    if (!Number.isFinite(topP) || topP <= 0 || topP > 1) {
      setNotice('topP 必须在 0-1 之间（不含 0）');
      return;
    }
    if (!Number.isInteger(timeoutMs) || timeoutMs < 1000 || timeoutMs > 120000) {
      setNotice('请求超时必须是 1000-120000 毫秒之间的整数');
      return;
    }
    if (me?.isSuperAdmin && !tenantId) {
      setNotice('请先从列表选择目标公司，再代配该公司的模型');
      return;
    }
    const input: AIModelInput = {
      name,
      provider: form.provider,
      protocol: form.protocol,
      baseUrl,
      modelId,
      apiKey: form.apiKey,
      thinkingEnabled: form.thinkingEnabled,
      contextTokens,
      extraConfig: { temperature, topP, reasoningEffort: form.reasoningEffort, timeoutMs },
      capabilities: form.capabilities,
    };
    setSaving(true);
    setNotice('');
    try {
      if (id) {
        await updateAIModel(id, input, tenantId || undefined);
      } else {
        await createAIModel(input, tenantId || undefined);
      }
      navigate(tenantId ? `${LIST_PATH}?tenantId=${encodeURIComponent(tenantId)}` : LIST_PATH, {
        replace: true,
      });
    } catch (err) {
      setNotice(errorMessage(err));
      setSaving(false);
    }
  };

  if (loading) {
    return <Loading text="正在读取模型配置…" />;
  }

  return (
    <FormPage
      title={editing ? '编辑模型' : '新增模型'}
      subtitle="Key 经加密后入库，页面只显示末 4 位掩码；保存后即时生效于下一次调用"
      backTo={tenantId ? `${LIST_PATH}?tenantId=${encodeURIComponent(tenantId)}` : LIST_PATH}
      saving={saving}
      notice={notice}
      onSubmit={() => void submit()}
    >
      <h3 className="rdc-model-section-title">基础信息</h3>
      <div className="field-grid">
        <label className="field field-block">
          <span className="field-label">模型名称 *</span>
          <Input
            className="text-input"
            maxLength={100}
            value={form.name}
            onChange={(event) => update('name', event.target.value)}
          />
        </label>
        <label className="field">
          <span>供应商</span>
          <Select
            value={form.provider}
            options={AI_PROVIDER_OPTIONS}
            onChange={(value) => update('provider', value as AIProvider)}
          />
        </label>
        <label className="field">
          <span>协议</span>
          <Select
            value={form.protocol}
            options={AI_PROTOCOL_OPTIONS}
            onChange={(value) => update('protocol', value as AIProtocol)}
          />
        </label>
        <label className="field">
          <span>模型 ID *</span>
          <Input
            className="text-input"
            maxLength={100}
            value={form.modelId}
            onChange={(event) => update('modelId', event.target.value)}
          />
        </label>{' '}
        <label className="field field-block">
          <span className="field-label">API 地址 *</span>
          <Input
            className="text-input"
            placeholder="https://api.scnet.cn/api/llm/v1"
            value={form.baseUrl}
            onChange={(event) => update('baseUrl', event.target.value)}
          />
        </label>
        <label className="field field-block">
          <span className="field-label">
            API Key {editing ? `（当前掩码：${mask || '未配置'}）` : ''}
          </span>
          <Input
            className="text-input"
            type="password"
            autoComplete="off"
            placeholder={editing ? '留空即不修改' : '粘贴新 Key，保存后仅显示掩码'}
            value={form.apiKey}
            onChange={(event) => update('apiKey', event.target.value)}
          />
          <span className="settings-hint">
            留空即不修改；明文不会回显，也不要写进备注或文件名。
          </span>
        </label>
      </div>

      <div className="rdc-model-protocol-hint" role="note">
        <strong>该协议的端点与认证方式（只读，由后端按协议装配）</strong>
        <dl>
          <dt>请求端点</dt>
          <dd>
            <code>{aiProtocolHint(form.protocol).endpoint}</code>
          </dd>
          <dt>认证方式</dt>
          <dd>{aiProtocolHint(form.protocol).auth}</dd>
        </dl>
        <p className="settings-hint">{aiProtocolHint(form.protocol).note}</p>
        <p className="settings-hint">
          切换协议不会清空已填的基础地址，请自行确认地址与服务商匹配。
        </p>
      </div>

      <div className="rdc-model-template">
        <Button
          type="button"
          className="button button-ghost"
          onClick={() => setForm(fromTemplate())}
        >
          从 scnet / DeepSeek 模板填入
        </Button>
        <span className="settings-hint">只填入表单，不自动创建配置；Key 仍需自行录入。</span>
      </div>

      <h3 className="rdc-model-section-title">思考与上下文</h3>
      <div className="field-grid">
        <Checkbox
          className="field-check"
          checked={form.thinkingEnabled}
          onChange={(event) => update('thinkingEnabled', event.target.checked)}
        >
          开启深度思考
        </Checkbox>
        <label className="field">
          <span>上下文 / max_tokens（1024-393216）</span>
          <Input
            className="text-input"
            type="number"
            min={1024}
            max={393216}
            value={form.contextTokens}
            onChange={(event) => update('contextTokens', event.target.value)}
          />
        </label>
        <label className="field">
          <span>reasoningEffort（思考开启时生效）</span>
          <Select
            value={form.reasoningEffort}
            options={AI_REASONING_OPTIONS}
            onChange={(value) => update('reasoningEffort', value as AIReasoningEffort)}
          />
        </label>
      </div>
      {!form.thinkingEnabled && form.reasoningEffort !== 'none' ? (
        // 开关与档位矛盾时的处理方式是「extra_config 优先」（沿用 P0）：这里只提示，不阻断保存。
        <p className="settings-hint rdc-model-conflict">
          当前关闭了深度思考，扩展配置里的档位（{form.reasoningEffort}）不会生效，调用时按 none
          下发。
        </p>
      ) : null}

      <h3 className="rdc-model-section-title">扩展配置</h3>
      <div className="field-grid rdc-model-extra">
        <label className="field">
          <span>temperature（0–{aiTemperatureLimit(form.protocol)}）</span>
          <Input
            className="text-input"
            type="number"
            step="0.1"
            min={0}
            max={aiTemperatureLimit(form.protocol)}
            value={form.temperature}
            onChange={(event) => update('temperature', event.target.value)}
          />
        </label>
        <label className="field">
          <span>topP（0-1）</span>
          <Input
            className="text-input"
            type="number"
            step="0.1"
            min={0}
            max={1}
            value={form.topP}
            onChange={(event) => update('topP', event.target.value)}
          />
        </label>
        <label className="field">
          <span>timeoutMs（1000-120000）</span>
          <Input
            className="text-input"
            type="number"
            min={1000}
            max={120000}
            value={form.timeoutMs}
            onChange={(event) => update('timeoutMs', event.target.value)}
          />
        </label>
      </div>

      <h3 className="rdc-model-section-title">能力标签（决定能力调度是否命中本模型）</h3>
      <div className="rdc-model-capabilities">
        {TEMPLATE_CAPABILITIES.map((capability) => (
          <Checkbox
            key={capability}
            className="field-check"
            checked={form.capabilities.includes(capability)}
            onChange={(event) => toggleCapability(capability, event.target.checked)}
          >
            {capability}
          </Checkbox>
        ))}
      </div>
    </FormPage>
  );
}
