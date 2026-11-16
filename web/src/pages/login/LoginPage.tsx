/**
 * 登录页。
 * 职责：收集用户名 / 密码 → 调用 login → 写入会话 → 回到来源页（默认列表页）。
 * 输入：react-router 的 location.state.from（由路由守卫带上）；输出：登录表单与错误提示。
 * 边界：不直接 fetch（走 api/auth）、不写 localStorage（走 api/session）、不做权限判断；
 *       已登录再访问本页直接回首页，避免重复登录。
 */
import { useState } from 'react';
import type { FormEvent } from 'react';
import { Navigate, useLocation, useNavigate } from 'react-router-dom';
import { Button, Input, Radio } from '../../components/Controls';
import { login } from '../../api/auth';
import { errorMessage } from '../../api/client';
import type { SessionMode } from '../../api/session';
import { readSession, saveSession } from '../../api/session';
import { ENVIRONMENTS } from '../../utils/format';
import './login.scss';

/** 路由守卫跳转过来时带上来源路径，登录成功后原路返回。 */
interface LoginLocationState {
  from?: string;
}

export function LoginPage(): JSX.Element {
  const navigate = useNavigate();
  const location = useLocation();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  // 默认单人模式：用户明确要求；团队模式是主动选择后的全量视图。
  const [mode, setMode] = useState<SessionMode>('solo');
  const [showPassword, setShowPassword] = useState(false);
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const from = (location.state as LoginLocationState | null)?.from ?? '/';

  // 已有会话时不再展示表单：直接回首页（退出登录会先清会话，不会卡在这里）。
  if (readSession()) {
    return <Navigate to="/" replace />;
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    if (submitting) {
      return;
    }
    const trimmedUsername = username.trim();
    if (!trimmedUsername || !password) {
      setError('请输入用户名和密码');
      return;
    }

    setSubmitting(true);
    setError('');
    try {
      const result = await login({ username: trimmedUsername, password, mode });
      saveSession({ token: result.token, username: result.username, mode: result.mode });
      // replace：登录页不留历史，浏览器返回不会退回表单。
      navigate(from, { replace: true });
    } catch (err) {
      setError(errorMessage(err));
      setSubmitting(false);
    }
  }

  return (
    <div className="login-page">
      <aside className="login-brand">
        {/* 纯装饰层：不影响语义、不参与点击 */}
        <span className="login-orb" aria-hidden="true" />
        <span className="login-halo" aria-hidden="true" />
        <span className="login-seed login-seed-a" aria-hidden="true" />
        <span className="login-seed login-seed-b" aria-hidden="true" />
        <span className="login-seed login-seed-c" aria-hidden="true" />

        <div className="login-brand-top">
          <div className="login-lockup">
            <span className="login-mark" aria-hidden="true">
              D
            </span>
            <span className="login-wordmark">Dandelion</span>
          </div>
          <h1 className="login-tagline">
            每一次构建，
            <br />
            都值得被找到。
          </h1>
          <p className="login-lede">按环境与平台分发安装包，上传即得下载页与二维码。</p>
        </div>

        <div className="login-brand-bottom">
          <p className="login-envs-label">覆盖环境</p>
          <ul className="login-envs">
            {ENVIRONMENTS.map((item) => (
              <li key={item.value}>{item.label}</li>
            ))}
          </ul>
          <p className="login-brand-foot">内部工具 · 仅限团队成员使用</p>
        </div>
      </aside>

      <main className="login-main">
        <div className="login-form">
          <header>
            <p className="login-eyebrow">管理员登录</p>
            <h2>欢迎回来</h2>
            <p className="login-sub">登录后即可上传与管理应用包。</p>
          </header>

          <form onSubmit={handleSubmit} noValidate>
            <fieldset className="login-mode">
              <legend className="login-label">使用模式</legend>
              <div className="login-mode-options">
                <Radio
                  className={
                    mode === 'solo' ? 'login-mode-option login-mode-active' : 'login-mode-option'
                  }
                  name="mode"
                  value="solo"
                  checked={mode === 'solo'}
                  onChange={() => setMode('solo')}
                >
                  <span className="login-mode-title">单人模式</span>
                  <span className="login-mode-desc">只看自己上传的应用包</span>
                </Radio>
                <Radio
                  className={
                    mode === 'team' ? 'login-mode-option login-mode-active' : 'login-mode-option'
                  }
                  name="mode"
                  value="team"
                  checked={mode === 'team'}
                  onChange={() => setMode('team')}
                >
                  <span className="login-mode-title">团队模式</span>
                  <span className="login-mode-desc">查看团队全部数据与管理功能</span>
                </Radio>
              </div>
            </fieldset>

            <label className="login-field">
              <span className="login-label">用户名</span>
              <span className="login-control">
                <Input
                  className="login-input"
                  name="username"
                  type="text"
                  autoComplete="username"
                  autoFocus
                  required
                  aria-required="true"
                  placeholder="请输入用户名"
                  value={username}
                  onChange={(event) => setUsername(event.target.value)}
                />
              </span>
            </label>

            <label className="login-field">
              <span className="login-label">密码</span>
              <span className="login-control">
                <Input
                  className="login-input login-input-password"
                  name="password"
                  type={showPassword ? 'text' : 'password'}
                  autoComplete="current-password"
                  required
                  aria-required="true"
                  placeholder="请输入密码"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                />
                <Button
                  className="login-eye"
                  type="button"
                  aria-pressed={showPassword}
                  aria-label={showPassword ? '隐藏密码' : '显示密码'}
                  onClick={() => setShowPassword((value) => !value)}
                >
                  {showPassword ? '隐藏' : '显示'}
                </Button>
              </span>
            </label>

            {error ? (
              <p className="login-error" role="alert">
                {error}
              </p>
            ) : null}

            <Button className="login-submit" type="submit" disabled={submitting}>
              {submitting ? '正在登录…' : '登录'}
            </Button>
          </form>

          <p className="login-hint">账号由部署配置提供，忘记密码请联系平台维护者。</p>
        </div>
      </main>
    </div>
  );
}
