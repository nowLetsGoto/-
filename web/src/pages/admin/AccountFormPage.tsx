/**
 * 新建账号（超级管理员 → 账号管理 → 新建）。
 * 职责：填账号名、初始密码、是否超级管理员；创建成功后回列表。
 * 输入：无（表单新值）；输出：createUser 请求。
 * 边界：不做校验（服务端统一判）：账号名格式、密码长度、重名都由后端给可读提示。
 */
import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import type { UserInput } from '../../api/users';
import { createUser } from '../../api/users';
import { errorMessage } from '../../api/client';
import { FormPage } from '../../components/FormPage';

const EMPTY: UserInput = { username: '', password: '', personId: '', isSuperAdmin: false };

const LIST_PATH = '/admin/accounts';

export function AccountFormPage(): JSX.Element {
  const navigate = useNavigate();
  const [form, setForm] = useState<UserInput>(EMPTY);
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState('');

  const submit = async (): Promise<void> => {
    setSaving(true);
    setNotice('');
    try {
      await createUser({
        username: form.username.trim(),
        password: form.password,
        personId: '',
        isSuperAdmin: form.isSuperAdmin,
      });
      navigate(LIST_PATH, { replace: true });
    } catch (err) {
      setNotice(errorMessage(err));
      setSaving(false);
    }
  };

  return (
    <FormPage
      title="新建账号"
      subtitle="创建后把账号与初始密码转交对方；建议对方首次登录后改密码"
      backTo={LIST_PATH}
      saving={saving}
      notice={notice}
      onSubmit={() => void submit()}
    >
      <div className="field-grid">
        <label className="field">
          <span>账号名 *</span>
          <input
            className="text-input"
            maxLength={32}
            placeholder="字母、数字、点、下划线、短横线，3-32 位"
            value={form.username}
            onChange={(event) => setForm({ ...form, username: event.target.value })}
          />
        </label>
        <label className="field">
          <span>初始密码 *</span>
          <input
            className="text-input"
            type="text"
            maxLength={64}
            placeholder="8-64 位，创建后请立即转交"
            value={form.password}
            onChange={(event) => setForm({ ...form, password: event.target.value })}
          />
        </label>
      </div>

      <label className="field-check field-check-admin">
        <input
          type="checkbox"
          checked={form.isSuperAdmin}
          onChange={(event) => setForm({ ...form, isSuperAdmin: event.target.checked })}
        />
        设为超级管理员
        <span className="settings-hint">可进入「超级管理员」专区：管理账号与平台配置。请谨慎授予。</span>
      </label>
    </FormPage>
  );
}
