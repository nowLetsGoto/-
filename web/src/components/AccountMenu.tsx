/**
 * 侧栏底部的账号区：头像 + 账号名，点击向上弹出账号卡。
 * 职责：展示当前账号、所处的登录模式与当前角色，并提供「修改密码」「退出登录」两个入口。
 * 输入：会话（令牌里的用户名与模式）、当前人员（补全姓名、角色与超管身份）。
 * 输出：底部账号按钮 + 向上弹出的账号卡 + 修改密码弹窗。
 * 边界：不取数（/me 走 useMe 缓存）、不做权限判断（模式与角色只用于展示，边界在服务端）；
 *       退出登录只清本地无状态会话，服务端没有登出接口。
 */
import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Popover } from '@seakoi/console-kit';
import { clearSession, readSession } from '../api/session';
import { clearMe, useMe } from '../hooks/useMe';
import { levelLabel } from '../utils/accountLevel';
import { MODE_HINTS, MODE_LABELS } from '../utils/format';
import { ChangePasswordDialog } from './ChangePasswordDialog';
import { Icon } from './icons';

export function AccountMenu(): JSX.Element | null {
  const navigate = useNavigate();
  const session = readSession();
  const { me } = useMe();

  const [open, setOpen] = useState(false);
  const [changing, setChanging] = useState(false);

  if (!session) {
    return null;
  }

  // 模式来自签名令牌：签发时就定下了，本页不做任何改写。
  const mode = session.mode;
  const displayName = me?.name || session.username;

  /** 退出即清空本地会话并回登录页；令牌是无状态的，服务端没有需要调用的登出接口。 */
  const handleLogout = (): void => {
    // 权限菜单随会话走：不清缓存会把上一个账号的菜单带到下一个账号
    clearMe();
    clearSession();
    navigate('/login', { replace: true });
  };

  /**
   * 改密成功后服务端已让旧令牌失效：必须先清会话再回登录页，
   * 否则下一次请求会因为带着废令牌而触发 401 整页跳转，看起来像"莫名其妙被踢"。
   */
  const handleChanged = (): void => {
    setChanging(false);
    setOpen(false);
    clearMe();
    clearSession();
    navigate('/login', { replace: true });
  };

  const card = (
    <div className="account-card">
      <div className="account-card-head">
        <span className="account-card-avatar" aria-hidden="true">
          {displayName.slice(0, 1).toUpperCase()}
        </span>
        <div className="account-card-id">
          <p className="account-card-name">{displayName}</p>
          <p className="account-card-username">@{session.username}</p>
        </div>
      </div>

      <div className="account-card-mode">
        <span className={`mode-tag mode-${mode}`}>{MODE_LABELS[mode]}</span>
        <span className="account-card-mode-hint">{MODE_HINTS[mode]}</span>
      </div>

      {/* 角色只对团队模式有意义：单人模式没有角色区分，显示一行空值反而让人以为配错了。
          未分配角色时统一显示「普通用户」——这是展示约定，后端不为此造一个假角色。 */}
      {mode === 'team' ? (
        <>
          <div className="account-card-role">
            <span className="account-card-role-label">当前角色</span>
            <span className="account-card-role-value">{me?.roleName || '普通用户'}</span>
          </div>
          {/* 管理身份与业务角色正交：一个 team_admin 也可以是 developer。
              分两行显示，避免让人以为"升级身份就会换掉角色"。 */}
          <div className="account-card-role">
            <span className="account-card-role-label">管理身份</span>
            <span className="account-card-role-value">{levelLabel(me)}</span>
          </div>
          {me?.tenantName ? (
            <div className="account-card-role">
              <span className="account-card-role-label">所属公司</span>
              <span className="account-card-role-value">{me.tenantName}</span>
            </div>
          ) : null}
        </>
      ) : null}

      <div className="account-card-actions">
        <button
          className="account-card-action"
          type="button"
          onClick={() => {
            setOpen(false);
            setChanging(true);
          }}
        >
          <Icon name="shield" />
          修改密码
        </button>
        <button
          className="account-card-action account-card-action-quit"
          type="button"
          onClick={handleLogout}
        >
          <Icon name="logout" />
          退出登录
        </button>
      </div>
    </div>
  );

  return (
    <>
      <Popover
        open={open}
        onOpenChange={setOpen}
        content={card}
        trigger="click"
        // 向上弹：账号区贴在侧栏底部，向下弹会被视口切掉
        placement="topLeft"
        arrow={false}
      >
        <button
          className={open ? 'sidebar-account sidebar-account-open' : 'sidebar-account'}
          type="button"
          aria-expanded={open}
          aria-label="账号信息"
        >
          <span className="avatar" aria-hidden="true">
            {displayName.slice(0, 1).toUpperCase()}
          </span>
          <span className="account-name" title="当前登录账号">
            {session.username}
          </span>
          <Icon name="chevron" />
        </button>
      </Popover>

      <ChangePasswordDialog
        open={changing}
        onCancel={() => setChanging(false)}
        onChanged={handleChanged}
      />
    </>
  );
}
