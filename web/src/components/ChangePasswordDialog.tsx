/**
 * 修改自己的密码弹窗。
 * 职责：收集旧密码 + 新密码 + 确认密码，校验一致后调用 changeOwnPassword。
 * 输入：open / onCancel / onChanged（改密成功后由调用方处理"回登录页"）。
 * 输出：居中弹窗；失败提示就地展示，弹窗不关闭。
 * 边界：只做本地一致性校验（两次新密码是否相同、长度），密码强度与"新密码不得与旧密码相同"
 *       都由服务端判定；成功后旧令牌立即失效，因此这里绝不能让调用方继续留在原页面。
 */
import { useState } from 'react';
import { Modal } from '@seakoi/console-kit';
import { errorMessage } from '../api/client';
import { changeOwnPassword } from '../api/me';
import { Input } from './Controls';

/** 与后端 user.service 的 minPasswordLen / maxPasswordLen 保持一致。 */
const MIN_PASSWORD_LEN = 8;
const MAX_PASSWORD_LEN = 64;

interface ChangePasswordDialogProps {
  open: boolean;
  onCancel: () => void;
  /** 改密成功：服务端已让旧令牌失效，调用方必须清会话并回登录页。 */
  onChanged: () => void;
}

export function ChangePasswordDialog({
  open,
  onCancel,
  onChanged,
}: ChangePasswordDialogProps): JSX.Element {
  const [oldPassword, setOldPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  /** 每次打开都回到空白状态：残留的密码既没用也不安全。 */
  const reset = (): void => {
    setOldPassword('');
    setNewPassword('');
    setConfirmPassword('');
    setError('');
    setSaving(false);
  };

  const close = (): void => {
    reset();
    onCancel();
  };

  const submit = async (): Promise<void> => {
    if (saving) {
      return;
    }
    if (!oldPassword || !newPassword) {
      setError('请填写当前密码与新密码');
      return;
    }
    if (newPassword.length < MIN_PASSWORD_LEN) {
      setError(`新密码至少 ${MIN_PASSWORD_LEN} 位`);
      return;
    }
    // 两次输入不一致是纯前端能判的错，先拦住，免得白跑一次请求
    if (newPassword !== confirmPassword) {
      setError('两次输入的新密码不一致');
      return;
    }

    setSaving(true);
    setError('');
    try {
      await changeOwnPassword({ oldPassword, newPassword });
      reset();
      onChanged();
    } catch (err) {
      // 旧密码错、新旧相同、长度超限都由服务端返回可读文案，原样上屏
      setError(errorMessage(err));
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      title="修改密码"
      okText="保存"
      cancelText="取消"
      okButtonProps={{ loading: saving }}
      onOk={() => void submit()}
      onCancel={close}
      destroyOnHidden
      maskClosable={false}
      width={420}
    >
      <div className="password-form">
        <label className="field">
          <span>当前密码 *</span>
          <Input
            className="text-input"
            type="password"
            autoComplete="current-password"
            maxLength={MAX_PASSWORD_LEN}
            value={oldPassword}
            onChange={(event) => setOldPassword(event.target.value)}
          />
        </label>
        <label className="field">
          <span>新密码 *</span>
          <Input
            className="text-input"
            type="password"
            autoComplete="new-password"
            maxLength={MAX_PASSWORD_LEN}
            placeholder={`${MIN_PASSWORD_LEN}-${MAX_PASSWORD_LEN} 位`}
            value={newPassword}
            onChange={(event) => setNewPassword(event.target.value)}
          />
        </label>
        <label className="field">
          <span>确认新密码 *</span>
          <Input
            className="text-input"
            type="password"
            autoComplete="new-password"
            maxLength={MAX_PASSWORD_LEN}
            value={confirmPassword}
            onChange={(event) => setConfirmPassword(event.target.value)}
          />
        </label>

        {error ? (
          <p className="field-error" role="alert">
            {error}
          </p>
        ) : null}

        <p className="settings-hint">
          保存后<strong>当前登录状态立即失效</strong>，需要用新密码重新登录一次。
        </p>
      </div>
    </Modal>
  );
}
