/**
 * 账号管理（超级管理员 → 账号管理）。
 * 职责：服务端分页列出账号，支持新建、重置密码、启用/禁用、删除；危险操作一律二次确认并写明影响面。
 * 输入：URL 的 page / pageSize、关键字搜索；输出：DataTable + 确认弹窗 + 临时密码 / 结果提示条。
 * 边界：不内嵌编辑表单（新建走独立路由）；不做权限判断——非超管进不来，接口也会 403。
 */
import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import type { TableColumnsType } from '@seakoi/console-kit';
import { errorMessage } from '../../api/client';
import type { User } from '../../api/users';
import { deleteUser, listUsers, resetUserPassword, setUserStatus } from '../../api/users';
import { Button, Input } from '../../components/Controls';
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
  kind: 'disable' | 'enable' | 'reset' | 'delete';
  user: User;
}

const ACTION_TITLES: Record<PendingAction['kind'], string> = {
  disable: '禁用该账号？',
  enable: '启用该账号？',
  reset: '重置该账号的密码？',
  delete: '删除该账号？',
};

/** 确认按钮文案跟着动作走，避免每个弹窗都写含糊的「确认」。 */
const ACTION_CONFIRMS: Record<PendingAction['kind'], string> = {
  disable: '禁用',
  enable: '启用',
  reset: '重置',
  delete: '删除',
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
  // 一次性成功提示（如"已删除账号 xxx"）：开新的危险操作时就清掉，避免和旧动作混淆
  const [flash, setFlash] = useState('');
  // 临时密码只展示一次：刷新页面或点关闭就再也看不到
  const [tempPassword, setTempPassword] = useState<{ username: string; password: string } | null>(
    null,
  );

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

  /** 打开二次确认弹窗，同时清掉上一条一次性提示。 */
  const ask = (action: PendingAction): void => {
    setFlash('');
    setPending(action);
  };

  const runPending = async (): Promise<void> => {
    if (!pending || busy) {
      return;
    }
    setBusy(true);
    try {
      if (pending.kind === 'reset') {
        const result = await resetUserPassword(pending.user.id);
        setTempPassword({ username: pending.user.username, password: result.password });
      } else if (pending.kind === 'delete') {
        const removed = await deleteUser(pending.user.id);
        setTempPassword(null);
        setFlash(`已删除账号 ${removed.username}。`);
        // 删的是本页最后一条时回退一页，否则列表会停在一个空页上
        if (state.items.length === 1 && page > 1) {
          change(page - 1, pageSize);
        }
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

  const columns: TableColumnsType<User> = [
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
        user.isSuperAdmin ? (
          <span className="badge badge-admin">超级管理员</span>
        ) : (
          <span className="cell-sub">普通用户</span>
        ),
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
          {user.lastLoginAt
            ? `${formatDate(user.lastLoginAt)}${user.lastLoginIp ? ` · ${user.lastLoginIp}` : ''}`
            : '从未登录'}
        </span>
      ),
    },
    {
      title: '',
      key: 'actions',
      width: 270,
      render: (_, user) => {
        const isSelf = user.username === me?.username;
        return (
          <div className="row-actions">
            <Button
              className="button button-ghost button-small"
              type="button"
              onClick={() => ask({ kind: 'reset', user })}
            >
              重置密码
            </Button>
            <Button
              className="button button-ghost button-small"
              type="button"
              // 自己禁自己会让当前会话立即失效，服务端也会拒绝——这里先禁掉按钮
              disabled={isSelf && user.status === 'active'}
              title={isSelf && user.status === 'active' ? '不能禁用当前登录的账号' : undefined}
              onClick={() => ask({ kind: user.status === 'active' ? 'disable' : 'enable', user })}
            >
              {user.status === 'active' ? '禁用' : '启用'}
            </Button>
            <Button
              className="button button-ghost button-small button-danger"
              type="button"
              // 删掉自己等于当场把当前会话废掉，服务端也会拒绝——按钮同样先禁用
              disabled={isSelf}
              title={isSelf ? '不能删除当前登录的账号' : undefined}
              onClick={() => ask({ kind: 'delete', user })}
            >
              删除
            </Button>
          </div>
        );
      },
    },
  ];

  const emptyState = (
    <Empty
      title={query ? '没有匹配的账号' : '还没有账号'}
      description={
        query
          ? '换个关键字试试，或清空搜索看全部账号。'
          : '新建账号后，把一次性密码发给对方即可登录。'
      }
      action={
        query ? (
          <Button
            className="button button-ghost"
            type="button"
            onClick={() => {
              setKeyword('');
              setQuery('');
            }}
          >
            清空搜索
          </Button>
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

      {flash ? (
        <p className="notice notice-ok" role="status">
          {flash}
        </p>
      ) : null}

      {tempPassword ? (
        <div className="temp-password" role="status">
          <div>
            <strong>{tempPassword.username}</strong> 的临时密码：
            <code>{tempPassword.password}</code>
            <span className="temp-password-hint">
              只显示这一次，请立即转交并提醒对方登录后修改。
            </span>
          </div>
          <Button
            className="button button-ghost button-small"
            type="button"
            onClick={() => setTempPassword(null)}
          >
            我已记下
          </Button>
        </div>
      ) : null}

      <div className="card">
        <div className="toolbar">
          <Input
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
          <Button
            className="button button-ghost button-small"
            type="button"
            onClick={() => setQuery(keyword)}
          >
            搜索
          </Button>
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
        confirmText={pending ? ACTION_CONFIRMS[pending.kind] : '确认'}
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
          账号 <strong>{user.username}</strong> 将无法登录，<strong>已登录的会话会立即失效</strong>
          。
        </p>
        <p className="settings-hint">账号数据与历史记录都会保留，随时可以重新启用。</p>
      </>
    );
  }
  if (kind === 'enable') {
    return (
      <p>
        账号 <strong>{user.username}</strong> 将恢复登录能力。
      </p>
    );
  }
  if (kind === 'delete') {
    return (
      <>
        <p>
          账号 <strong>{user.username}</strong> 将被<strong>永久删除</strong>
          ，该账号立即无法登录，已登录的会话随即失效。
        </p>
        <p className="settings-hint">
          删除<strong>不可撤销</strong>
          ，也无法恢复。若只是想暂时停用，请改用「禁用」——禁用会保留账号与历史记录。
        </p>
        <p className="settings-hint">
          项目、构建记录与审计日志不会随之删除，它们只保留账号 ID，不会因为这个账号消失而丢失。
        </p>
      </>
    );
  }
  return (
    <>
      <p>
        将为 <strong>{user.username}</strong> 生成一个新的随机密码，
        <strong>旧密码与已登录会话立即失效</strong>。
      </p>
      <p className="settings-hint">新密码只会显示一次，请立即转交。</p>
    </>
  );
}
