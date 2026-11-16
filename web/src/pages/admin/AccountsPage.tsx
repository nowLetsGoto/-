/**
 * 账号管理（超级管理员 → 账号管理）。
 * 职责：服务端分页列出账号，支持新建、重置密码、启用/禁用；危险操作一律二次确认并写明影响面。
 * 输入：URL 的 page / pageSize、关键字搜索；输出：DataTable + 确认弹窗 + 临时密码提示条。
 * 边界：不内嵌编辑表单（新建走独立路由）；不做权限判断——非超管进不来，接口也会 403。
 */
import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import type { ColumnsType } from 'antd/es/table';
import { errorMessage } from '../../api/client';
import type { User } from '../../api/users';
import { listUsers, resetUserPassword, setUserStatus } from '../../api/users';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { DataTable } from '../../components/DataTable';
import { Empty, ErrorState } from '../../components/States';
import { useMe } from '../../hooks/useMe';
import { usePageState } from '../../hooks/usePageState';
import { formatDate } from '../../utils/format';

interface PageState {
  items: User[];
  total: number;
  loading: boolean;
  error: string;
}

/** 待确认的危险操作；null 表示没有弹窗。 */
interface PendingAction {
  kind: 'disable' | 'enable' | 'reset';
  user: User;
}

const ACTION_TITLES: Record<PendingAction['kind'], string> = {
  disable: '禁用该账号？',
  enable: '启用该账号？',
  reset: '重置该账号的密码？',
};

export function AccountsPage(): JSX.Element {
  const { page, pageSize, change } = usePageState();
  const { me } = useMe();

  const [keyword, setKeyword] = useState('');
  const [query, setQuery] = useState('');
  const [version, setVersion] = useState(0);
  const [state, setState] = useState<PageState>({ items: [], total: 0, loading: true, error: '' });

  const [pending, setPending] = useState<PendingAction | null>(null);
  const [busy, setBusy] = useState(false);
  // 临时密码只展示一次：刷新页面或点关闭就再也看不到
  const [tempPassword, setTempPassword] = useState<{ username: string; password: string } | null>(null);

  useEffect(() => {
    let cancelled = false;
    setState((prev) => ({ ...prev, loading: true, error: '' }));
    listUsers({ page, pageSize, keyword: query })
      .then((result) => {
        if (!cancelled) {
          setState({ items: result.items, total: result.total, loading: false, error: '' });
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setState({ items: [], total: 0, loading: false, error: errorMessage(err) });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [page, pageSize, query, version]);

  const reload = (): void => setVersion((value) => value + 1);

  const runPending = async (): Promise<void> => {
    if (!pending || busy) {
      return;
    }
    setBusy(true);
    try {
      if (pending.kind === 'reset') {
        const result = await resetUserPassword(pending.user.id);
        setTempPassword({ username: pending.user.username, password: result.password });
      } else {
        await setUserStatus(pending.user.id, pending.kind === 'disable' ? 'disabled' : 'active');
      }
      setPending(null);
      reload();
    } catch (err) {
      setState((prev) => ({ ...prev, error: errorMessage(err) }));
      setPending(null);
    } finally {
      setBusy(false);
    }
  };

  const columns: ColumnsType<User> = [
    {
      title: '账号',
      key: 'username',
      render: (_, user) => (
        <span className="cell-name">
          {user.username}
          {user.username === me?.username ? <span className="chip chip-self">当前登录</span> : null}
        </span>
      ),
    },
    {
      title: '绑定人员',
      key: 'personName',
      render: (_, user) => <span className="cell-sub">{user.personName || '未绑定'}</span>,
    },
    {
      title: '身份',
      key: 'isSuperAdmin',
      width: 120,
      render: (_, user) =>
        user.isSuperAdmin ? <span className="badge badge-admin">超级管理员</span> : <span className="cell-sub">普通用户</span>,
    },
    {
      title: '状态',
      key: 'status',
      width: 100,
      render: (_, user) =>
        user.status === 'active' ? (
          <span className="status-dot status-online">启用</span>
        ) : (
          <span className="status-dot status-offline">已禁用</span>
        ),
    },
    {
      title: '最近登录',
      key: 'lastLogin',
      render: (_, user) => (
        <span className="cell-sub">
          {user.lastLoginAt ? `${formatDate(user.lastLoginAt)}${user.lastLoginIp ? ` · ${user.lastLoginIp}` : ''}` : '从未登录'}
        </span>
      ),
    },
    {
      title: '',
      key: 'actions',
      width: 190,
      render: (_, user) => {
        const isSelf = user.username === me?.username;
        return (
          <div className="row-actions">
            <button
              className="button button-ghost button-small"
              type="button"
              onClick={() => setPending({ kind: 'reset', user })}
            >
              重置密码
            </button>
            <button
              className="button button-ghost button-small"
              type="button"
              // 自己禁自己会让当前会话立即失效，服务端也会拒绝——这里先禁掉按钮
              disabled={isSelf && user.status === 'active'}
              title={isSelf && user.status === 'active' ? '不能禁用当前登录的账号' : undefined}
              onClick={() => setPending({ kind: user.status === 'active' ? 'disable' : 'enable', user })}
            >
              {user.status === 'active' ? '禁用' : '启用'}
            </button>
          </div>
        );
      },
    },
  ];

  const emptyState = (
    <Empty
      title={query ? '没有匹配的账号' : '还没有账号'}
      description={query ? '换个关键字试试，或清空搜索看全部账号。' : '新建账号后，把一次性密码发给对方即可登录。'}
      action={
        query ? (
          <button
            className="button button-ghost"
            type="button"
            onClick={() => {
              setKeyword('');
              setQuery('');
            }}
          >
            清空搜索
          </button>
        ) : (
          <Link className="button button-primary" to="/admin/accounts/new">
            新建第一个账号
          </Link>
        )
      }
    />
  );

  return (
    <section className="page">
      <div className="page-head">
        <div>
          <h1>账号管理</h1>
          <p className="sub">共 {state.total} 个账号 · 禁用会立即让该账号的令牌失效</p>
        </div>
        <Link className="button button-primary" to="/admin/accounts/new">
          新建账号
        </Link>
      </div>

      {tempPassword ? (
        <div className="temp-password" role="status">
          <div>
            <strong>{tempPassword.username}</strong> 的临时密码：
            <code>{tempPassword.password}</code>
            <span className="temp-password-hint">只显示这一次，请立即转交并提醒对方登录后修改。</span>
          </div>
          <button className="button button-ghost button-small" type="button" onClick={() => setTempPassword(null)}>
            我已记下
          </button>
        </div>
      ) : null}

      <div className="card">
        <div className="toolbar">
          <input
            className="text-input toolbar-search"
            placeholder="搜索账号或姓名"
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                setQuery(keyword);
              }
            }}
          />
          <button className="button button-ghost button-small" type="button" onClick={() => setQuery(keyword)}>
            搜索
          </button>
        </div>

        {state.error ? (
          <ErrorState message={state.error} onRetry={reload} />
        ) : (
          <DataTable<User>
            columns={columns}
            dataSource={state.items}
            rowKey={(user) => user.id}
            loading={state.loading}
            serverPagination={{ page, pageSize, total: state.total, onChange: change }}
            empty={emptyState}
          />
        )}
      </div>

      <ConfirmDialog
        open={pending !== null}
        title={pending ? ACTION_TITLES[pending.kind] : ''}
        description={describeAction(pending)}
        confirmText={pending?.kind === 'disable' ? '禁用' : pending?.kind === 'reset' ? '重置' : '启用'}
        danger={pending?.kind !== 'enable'}
        loading={busy}
        onConfirm={() => void runPending()}
        onCancel={() => setPending(null)}
      />
    </section>
  );
}

/** 每个动作都要说清"会发生什么"，不能只问"确定吗"。 */
function describeAction(pending: PendingAction | null): JSX.Element | null {
  if (!pending) {
    return null;
  }
  const { kind, user } = pending;
  if (kind === 'disable') {
    return (
      <>
        <p>
          账号 <strong>{user.username}</strong> 将无法登录，<strong>已登录的会话会立即失效</strong>。
        </p>
        <p className="settings-hint">账号数据与历史记录都会保留，随时可以重新启用。</p>
      </>
    );
  }
  if (kind === 'enable') {
    return <p>账号 <strong>{user.username}</strong> 将恢复登录能力。</p>;
  }
  return (
    <>
      <p>
        将为 <strong>{user.username}</strong> 生成一个新的随机密码，<strong>旧密码与已登录会话立即失效</strong>。
      </p>
      <p className="settings-hint">新密码只会显示一次，请立即转交。</p>
    </>
  );
}
