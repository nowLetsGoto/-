/**
 * 账号管理（超级管理员 → 账号管理）。
 * 职责：服务端分页列出**当前模式下的**账号，支持新建、编辑、重置密码、启用/禁用、删除；
 *       危险操作一律二次确认并写明影响面。
 * 输入：URL 的 page / pageSize、关键字搜索；输出：DataTable + 确认弹窗 + 临时密码 / 结果提示条。
 * 边界：不内嵌编辑表单（编辑与新建走同一套独立路由）；
 *       不传模式参数——列表范围由令牌里的登录模式决定，前端无法也不该越权查看另一种模式的账号。
 *
 * 两种视图：账号管理页对「有 account.read 的角色」开放只读；
 * 写操作（新建/编辑/重置密码/启停/删除）只属于平台管理员及以上，
 * 因此按 canWrite 把相关入口整体隐藏——只读用户看到的是一张纯列表。
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
import { atLeastLevel } from '../../utils/accountLevel';
import { usePageState } from '../../hooks/usePageState';
import { formatDate, MODE_LABELS } from '../../utils/format';

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
  // 写操作是平台治理动作，只按身份层级判断，不看角色权限点。
  // 与路由 LevelGate(minLevel="platform_admin") 同一口径，服务端另有 RequireMinLevel 兜底。
  const canWrite = atLeastLevel(me, 'platform_admin');

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
      title: '模式',
      key: 'mode',
      width: 110,
      // 模式决定这行账号属于哪套数据，也是本列表的存在前提，因此紧跟账号名展示。
      render: (_, user) => (
        <span className={`mode-tag mode-${user.mode}`}>{MODE_LABELS[user.mode]}</span>
      ),
    },
    {
      title: '绑定人员',
      key: 'personName',
      render: (_, user) => <span className="cell-sub">{user.personName || '未绑定'}</span>,
    },
    {
      title: '角色',
      key: 'roleName',
      width: 140,
      // 角色挂在绑定人员身上：未绑定人员就没有角色可谈，未分配时显示「普通用户」
      // ——「没有角色」与「角色叫普通用户」在展示上是同一件事。
      render: (_, user) => <span className="cell-sub">{user.roleName || '普通用户'}</span>,
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
      width: 170,
      // 只读用户不生成这一列：空列头会让人以为按钮丢了。
      render: (_, user) => {
        if (!canWrite) {
          return null;
        }
        const isSelf = user.username === me?.username;
        return (
          <div className="row-actions row-actions-wrap">
            <Link
              className="button button-ghost button-small"
              to={`/admin/accounts/${user.id}/edit`}
            >
              编辑
            </Link>
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
        ) : canWrite ? (
          <Link className="button button-primary" to="/admin/accounts/new">
            新建第一个账号
          </Link>
        ) : undefined
      }
    />
  );

  return (
    <section className="page">
      <div className="page-head">
        <div>
          <h1>账号管理</h1>
          <p className="sub">
            {canWrite ? '管理登录账号；禁用会立即让该账号的令牌失效' : '当前为只读视图'}
          </p>
        </div>
        {canWrite ? (
          <Link className="button button-primary" to="/admin/accounts/new">
            新建账号
          </Link>
        ) : null}
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
          <span className="toolbar-title">账号列表</span>
          <span className="toolbar-meta">共 {state.total} 个账号</span>
          <span className="toolbar-spacer" />
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
        {/* 影响面按模式分开说：两种模式的清理范围不同，含糊成一句话会让人误删团队资产。 */}
        <p className="settings-hint">
          {user.mode === 'solo'
            ? '该账号在单人模式下创建的项目与应用包（含已上传的文件）会一并清理，删除后无法找回；审计日志保留。'
            : '团队模式的项目与应用包属于团队，不会随之删除；审计日志同样保留。'}
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
