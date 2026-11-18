/**
 * 新建公司（平台管理 → 公司管理 → 新建）。
 * 职责：填公司名称 / 标识 / 平台管理员账号与初始密码，一次提交原子创建公司与该管理员。
 * 输入：表单；输出：创建成功后就地切换为「展示一次性明文密码」形态。
 * 边界：不在此页编辑公司；密码规则最终以服务端为准（400 提示直接展示）。
 *
 * 为什么成功后停在原地而不是跳回列表：平台管理员的初始密码只在这一次响应里出现，
 * 跳走就再也看不到了——用户必须先把它抄走。
 */
import { useState } from 'react';
import { Link } from 'react-router-dom';
import { errorMessage } from '../../api/client';
import type { TenantInput, TenantResult } from '../../api/tenants';
import { createTenant } from '../../api/tenants';
import { Input } from '../../components/Controls';
import { FormPage } from '../../components/FormPage';

const EMPTY: TenantInput = { name: '', code: '', adminUsername: '', adminPassword: '' };

const LIST_PATH = '/admin/tenants';

export function TenantFormPage(): JSX.Element {
  const [form, setForm] = useState<TenantInput>(EMPTY);
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState('');
  // 创建结果：非 null 表示已成功，页面切换为「展示一次性密码」形态。
  const [result, setResult] = useState<TenantResult | null>(null);

  const submit = async (): Promise<void> => {
    setSaving(true);
    setNotice('');
    try {
      const created = await createTenant({
        name: form.name.trim(),
        code: form.code.trim().toLowerCase(),
        adminUsername: form.adminUsername.trim(),
        adminPassword: form.adminPassword,
      });
      setResult(created);
    } catch (err) {
      setNotice(errorMessage(err));
    } finally {
      setSaving(false);
    }
  };

  if (result) {
    return (
      <div className="page">
        <div className="page-head">
          <div>
            <h1>公司已创建</h1>
            <p className="sub">
              {result.tenant.name}（{result.tenant.code}）已就绪，平台管理员账号同时创建完成。
            </p>
          </div>
        </div>

        <div className="temp-password" role="status">
          <div>
            平台管理员 <strong>{result.tenant.adminUsername}</strong> 的初始密码：
            <code>{result.password}</code>
            <span className="temp-password-hint">
              只显示这一次，离开本页后无法再次查看。请立即转交给该公司平台管理员，并提醒其登录后修改。
            </span>
          </div>
        </div>

        <div className="row-actions">
          <Link className="button button-primary" to={LIST_PATH}>
            返回公司列表
          </Link>
        </div>
      </div>
    );
  }

  return (
    <FormPage
      title="新建公司"
      subtitle="公司与它的平台管理员会在同一次提交里原子创建，任一步失败都不会留下「有公司没管理员」的孤岛。"
      backTo={LIST_PATH}
      saving={saving}
      notice={notice}
      onSubmit={() => void submit()}
    >
      <div className="field-grid">
        <label className="field">
          <span>公司名称 *</span>
          <Input
            className="text-input"
            maxLength={60}
            placeholder="如 景云销科技"
            value={form.name}
            onChange={(event) => setForm({ ...form, name: event.target.value })}
          />
        </label>
        <label className="field">
          <span>公司标识 *</span>
          <Input
            className="text-input"
            maxLength={32}
            placeholder="如 jingyunxiao"
            value={form.code}
            onChange={(event) => setForm({ ...form, code: event.target.value })}
          />
        </label>
      </div>
      <p className="settings-hint">
        公司标识用小写字母、数字与短横线，2-32
        位且以字母数字开头。它用于展示与日志，不参与权限判定。
      </p>

      <div className="field-grid">
        <label className="field">
          <span>平台管理员账号 *</span>
          <Input
            className="text-input"
            maxLength={32}
            placeholder="如 jyx-admin"
            value={form.adminUsername}
            onChange={(event) => setForm({ ...form, adminUsername: event.target.value })}
          />
        </label>
        <label className="field">
          <span>初始密码 *</span>
          <Input
            className="text-input"
            type="password"
            maxLength={64}
            placeholder="至少 8 位"
            value={form.adminPassword}
            onChange={(event) => setForm({ ...form, adminPassword: event.target.value })}
          />
        </label>
      </div>
      <p className="settings-hint">
        每家公司恰好一个平台管理员：能管理本公司全部团队与账号，但看不到其他公司。初始密码创建成功后只显示一次。
      </p>
    </FormPage>
  );
}
