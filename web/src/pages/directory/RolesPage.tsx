/**
 * 角色管理（基础资料 → 角色管理）。
 * 职责：列出角色与各自权限数量，跳转到编辑页；顶部提供新建入口。
 * 输入：listRoles；输出：DataTable + 角色类型徽标。
 * 边界：只读页面——所有编辑都在独立路由；
 *       不做身份判断：路由由 PermissionGate(role.read) 把关，能渲染出来就一定有读角色定义的权限。
 *       角色按公司隔离——每个人只看得到本公司的角色（超管跨公司看全部）。
 */
import { useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import type { TableColumnsType } from '@seakoi/console-kit';
import { errorMessage } from '../../api/client';
import type { Role } from '../../api/roles';
import { deleteRole, listRoles } from '../../api/roles';
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
  // 写权限按权限点判定，与后端路由守卫同一口径：
  // 没有 role.write 就别给新建入口，避免点了才吃 403。
  // 删除同理，且只能删自定义角色（内置角色后端也会拒绝）。
  const writable = can(me, 'role.write');
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
          onClick={() => navigate(`/directory/roles/${role.id}/edit`)}
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
    // 归属公司列只对超管有意义：他跨公司看全部角色，同名角色会出现多份
    // （每家公司的 developer 各一条），没有这一列就分不清哪条属于谁。
    // 公司管理员只看本公司，所有行的公司名相同，显示出来是噪音。
    ...(me?.isSuperAdmin
      ? [
          {
            title: '归属公司',
            key: 'tenantName',
            render: (_: unknown, role: Role) => (
              <span className="cell-sub">{role.tenantName || '—'}</span>
            ),
          } as TableColumnsType<Role>[number],
        ]
      : []),
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
            onClick={() => navigate(`/directory/roles/${role.id}/edit`)}
          >
            编辑
          </Button>
          {/*
            内置角色不显示删除：它是本公司预置的默认角色，删掉之后新成员就没有可分配的
            起点（后端也会拒绝）。可以改它的权限——这正是内置角色作为"模板"的用法。
          */}
          {deletable && !role.isBuiltin ? (
            <Button
              className="button button-ghost button-small button-danger"
              type="button"
              onClick={() => removal.request({ id: role.id, label: role.name })}
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
            共 {state.items.length} 个角色 ·
            角色决定「能做什么」；超级管理员与平台管理员是账号身份，不在此列
          </p>
        </div>
        {writable ? (
          <Link className="button button-primary" to="/directory/roles/new">
            新建角色
          </Link>
        ) : null}
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
