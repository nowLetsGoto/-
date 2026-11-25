/**
 * 软限流阈值设置弹窗（研发中心 → AI模型 → 设置调用上限）。
 * 职责：采集 enabled / maxCalls / windowMinutes / action 四项并整行提交，成功后回调最新配置。
 * 输入：当前阈值（含默认值）、超管代配的 tenantId、保存回调。
 * 输出：updateAIModelQuota 请求；保存后阈值立即影响下一次调用的放行判定。
 * 边界：不做校验口径定义（区间与动作白名单由 contract.ValidateAIQuotaInput 在后端裁决），
 *       前端只做"提前提示"，不阻止提交；不读改写 extra_config（retentionDays 由后端原样保留）。
 */
import { useEffect, useState } from 'react';
import { Modal } from '@seakoi/console-kit';
import { updateAIModelQuota } from '../../api/rdcModel';
import type { AIQuotaAction, AIQuotaConfig } from '../../api/rdcModel';
import { errorMessage } from '../../api/client';
import { Checkbox, Input } from '../../components/Controls';
import { Select } from '../../components/Select';
import { ErrorState } from '../../components/States';
import { AI_QUOTA_ACTION_OPTIONS, formatDate } from '../../utils/format';

interface ModelQuotaModalProps {
  open: boolean;
  quota: AIQuotaConfig;
  /** 仅超管需要：目标公司 id；非超管传 undefined。 */
  tenantId?: string;
  onClose: () => void;
  onSaved: (saved: AIQuotaConfig) => void;
}

interface FormState {
  enabled: boolean;
  maxCalls: string;
  windowMinutes: string;
  action: AIQuotaAction;
}

/** 把服务端配置转成表单字符串；数字用 string 承载，避免输入过程中的半成品值被强转。 */
function toForm(quota: AIQuotaConfig): FormState {
  return {
    enabled: quota.enabled,
    maxCalls: String(quota.maxCalls),
    windowMinutes: String(quota.windowMinutes),
    action: quota.action,
  };
}

export function ModelQuotaModal({
  open,
  quota,
  tenantId,
  onClose,
  onSaved,
}: ModelQuotaModalProps): JSX.Element {
  const [form, setForm] = useState<FormState>(toForm(quota));
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState('');

  // 每次打开时按最新配置重填：阈值可能已被其它标签页改过。
  useEffect(() => {
    if (open) {
      setForm(toForm(quota));
      setNotice('');
    }
  }, [open, quota]);

  const update = <K extends keyof FormState>(key: K, value: FormState[K]): void => {
    setForm((current) => ({ ...current, [key]: value }));
  };

  const maxCalls = Number(form.maxCalls);
  const windowMinutes = Number(form.windowMinutes);
  const maxCallsInvalid = !Number.isInteger(maxCalls) || maxCalls < 1 || maxCalls > 1_000_000;
  const windowInvalid =
    !Number.isInteger(windowMinutes) || windowMinutes < 1 || windowMinutes > 10_080;

  const save = async (): Promise<void> => {
    if (maxCallsInvalid || windowInvalid) {
      // 只做本地提示，真正的裁决仍在后端：这里拦一下能省一次往返，但不是唯一防线。
      setNotice('请先修正取值：次数 1–1000000，窗口 1–10080 分钟');
      return;
    }
    setSaving(true);
    setNotice('');
    try {
      const saved = await updateAIModelQuota(
        {
          enabled: form.enabled,
          maxCalls,
          windowMinutes,
          action: form.action,
        },
        tenantId,
      );
      onSaved(saved);
    } catch (err) {
      setNotice(errorMessage(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      title="设置调用上限"
      okText="保存"
      cancelText="取消"
      okButtonProps={{ loading: saving }}
      onOk={() => void save()}
      onCancel={onCancel(onClose, saving)}
      destroyOnHidden
      maskClosable={false}
      width={520}
    >
      <div className="rdc-quota-form">
        <p className="rdc-quota-intro">
          上限按公司统计「已出站调用」的次数；被本上限拒绝的请求不会请求上游，也不计入下一次统计。
          {quota.updatedAt
            ? `当前配置更新于 ${formatDate(quota.updatedAt)}。`
            : '当前展示的是默认值，尚未保存过配置。'}
        </p>

        <label className="field-check">
          <Checkbox
            checked={form.enabled}
            onChange={(event) => update('enabled', event.target.checked)}
          >
            启用调用上限
          </Checkbox>
        </label>
        <p className="settings-hint">默认关闭：不启用时本公司的调用行为与设置上限之前完全一致。</p>

        <label className="field">
          <span>每窗口最大调用次数（1–1000000）</span>
          <Input
            className="text-input"
            type="number"
            min={1}
            max={1000000}
            value={form.maxCalls}
            onChange={(event) => update('maxCalls', event.target.value)}
          />
          {maxCallsInvalid ? (
            <span className="field-error">次数必须是 1–1000000 之间的整数</span>
          ) : null}
        </label>

        <label className="field">
          <span>统计窗口（分钟，1–10080）</span>
          <Input
            className="text-input"
            type="number"
            min={1}
            max={10080}
            value={form.windowMinutes}
            onChange={(event) => update('windowMinutes', event.target.value)}
          />
          {windowInvalid ? (
            <span className="field-error">窗口必须是 1–10080 分钟之间的整数</span>
          ) : null}
        </label>

        <label className="field">
          <span>超限后的动作</span>
          <Select
            value={form.action}
            options={AI_QUOTA_ACTION_OPTIONS}
            onChange={(value) => update('action', value as AIQuotaAction)}
          />
        </label>
        <p className="settings-hint">
          「仅告警」只记录日志、照常调用；「拒绝调用」直接返回限流提示，上游不会收到请求。
          首次配置建议先用「仅告警」观察真实用量。
        </p>

        {notice ? <ErrorState message={notice} /> : null}
      </div>
    </Modal>
  );
}

/** 保存中不允许关闭，避免请求回来时弹窗已经不在了。 */
function onCancel(close: () => void, saving: boolean): () => void {
  return () => {
    if (!saving) {
      close();
    }
  };
}
