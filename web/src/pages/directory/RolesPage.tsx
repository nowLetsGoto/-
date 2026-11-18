/**
 * 角色管理（基础资料 → 角色管理）。
 * 职责：列出角色与各自权限数量，跳转到编辑页；顶部提供新建入口。
 * 输入：listRoles；输出：DataTable + 角色类型徽标。
 * 边界：只读页面——所有编辑都在独立路由；
 *       不做"是不是超管"的判断：路由已被 AdminGate 包住，能渲染出来就一定是超管。
 */
import { useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import type { TableColumnsType } from '@seakoi/console-kit';
import { errorMessage } from '../../api/client';
import type { Role } from '../../api/roles';
import { SUPER_ADMIN_ROLE_KEY, deleteRole, listRoles } from '../../api/roles';
import { Button } from '../../components/Controls';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { DataTable } from '../../components/DataTable';
import { Empty, ErrorState } from '../../components/States';
import { useDeleteAction } from '../../hooks/useDeleteAction';
import { useMe } from '../../hooks/useMe';
import { can } from '../../utils/permission';

interface PageState {
  items: Role[];
  loading: boolean;
  error: string;
}

export function RolesPage(): JSX.Element {
  const navigate = useNavigate();
  const { me } = useMe();
  const [state, setState] = useState<PageState>({ items: [], loading: true, error: '' });
  const [version, setVersion] = useState(0);
  // 只能删自定义角色（内置角色后端也会拒绝）；成功后递增 version 让列表重拉。
  const deletable = can(me, 'role.delete');
  const removal = useDeleteAction<{ id: string; label: string }>(
    (target) => deleteRole(target.id),
    () => setVersion((value) => value + 1),
  );

  useEffect(() => {
    let cancelled = false;
    setState((prev) => ({ ...prev, loading: true, error: '' }));
    listRoles()
      .then((result) => {
        if (!cancelled) {
          setState({ items: result.items, loading: false, error: '' });
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setState({ items: [], loading: false, error: errorMessage(err) });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [version]);

  const columns: TableColumnsType<Role> = [
    {
      title: '角色名称',
      key: 'name',
      render: (_, role) => (
        <Button
          className="project-link"
          type="button"
          onClick={() => navigate(`/directory/roles/${role.key}/edit`)}
        >
          {role.name}
        </Button>
      ),
    },
    {
      title: '标识',
      key: 'key',
      render: (_, role) => <code className="cell-sub">{role.key}</code>,
    },
    {
      title: '类型',
      key: 'isBuiltin',
      width: 110,
      render: (_, role) =>
        role.isBuiltin ? (
          <span className="badge badge-builtin">内置</span>
        ) : (
          <span className="cell-sub">自定义</span>
        ),
    },
    {
      title: '权限数',
      key: 'permissions',
      width: 100,
      render: (_, role) => <span>{role.permissions.length}</span>,
    },
    {
      title: '描述',
      key: 'description',
      render: (_, role) => <span className="cell-sub">{role.description || '—'}</span>,
    },
    {
      title: '',
      key: 'actions',
      width: 150,
      render: (_, role) => (
        <div className="row-actions">
          <Button
            className="button button-ghost button-small"
            type="button"
            onClick={() => navigate(`/directory/roles/${role.key}/edit`)}
          >
            {role.key === SUPER_ADMIN_ROLE_KEY ? '查看' : '编辑'}
          </Button>
          {/*
            内置角色与超管角色不显示删除：后端也会拒绝，但先把不可能成功的按钮藏起来，
            比让用户点了才收到"不可删除"要好。判据与后端一致（isBuiltin / 超管 key）。
          */}
          {deletable && !role.isBuiltin && role.key !== SUPER_ADMIN_ROLE_KEY ? (
            <Button
              className="button button-ghost button-small button-danger"
              type="button"
              onClick={() => removal.request({ id: role.key, label: role.name })}
            >
              删除
            </Button>
          ) : null}
        </div>
      ),
    },
  ];

  return (
    <section className="page">
      <div className="page-head">
        <div>
          <h1>角色管理</h1>
          <p className="sub">
            共 {state.items.length} 个角色 · 角色决定「能做什么」，分配给人由项目成员管理
          </p>
        </div>
        {/* 能进到这个页面就一定是超管（路由由 AdminGate 把关），新建入口无条件渲染。 */}
        <Link className="button button-primary" to="/directory/roles/new">
          新建角色
        </Link>
      </div>

      <div className="card">
        {state.error ? (
          <ErrorState message={state.error} onRetry={() => setVersion((value) => value + 1)} />
        ) : (
          <DataTable<Role>
            columns={columns}
            dataSource={state.items}
            rowKey={(role) => role.id}
            loading={state.loading}
            empty={<Empty title="还没有角色" description="内置角色会在服务启动时自动创建。" />}
          />
        )}
      </div>

      <ConfirmDialog
        open={removal.target !== null}
        title={`删除角色「${removal.target?.label ?? ''}」？`}
        description={
          <>
            <p>删除后该角色从列表中移除，无法恢复。</p>
            <p className="settings-hint">
              若该角色已分配给人员或项目，删除会被拒绝并提示还有多少个在用；
              请先给他们改挂其他角色。
            </p>
            {removal.error ? <p className="field-error">{removal.error}</p> : null}
          </>
        }
        confirmText="删除"
        danger
        loading={removal.loading}
        onConfirm={removal.confirm}
        onCancel={removal.cancel}
      />
    </section>
  );
}
