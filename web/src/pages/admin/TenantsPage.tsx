/**
 * 公司管理（平台管理 → 公司管理，仅平台超级管理员）。
 * 职责：分页列出全部公司及其平台管理员账号名；新建公司走独立路由；
 *       启用 / 停用公司；删除空公司。
 * 输入：URL 的 page / pageSize；输出：表格 + 新建入口 + 行内启停与删除。
 * 边界：不做权限判断——非超管进不来，接口也会 403；
 *       不在此页编辑公司资料（名称 / 标识创建后即固定，改名会牵连角色 key 与对外链接）。
 *
 * 关于删除：只对**空公司**有效。这是刻意的保守选择——公司是数据隔离的最高边界，
 * 级联删掉它意味着一次性丢弃整家公司的数据与磁盘上的包，且没有撤销。
 * 需要清空时由人逐项删，慢一点但每一步都看得见。
 */
import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import type { TableColumnsType } from '@seakoi/console-kit';
import { errorMessage } from '../../api/client';
import type { Tenant, TenantStatus } from '../../api/tenants';
import { deleteTenant, listTenants, setTenantStatus } from '../../api/tenants';
import { Button } from '../../components/Controls';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { DataTable } from '../../components/DataTable';
import { ErrorState } from '../../components/States';
import { useDeleteAction } from '../../hooks/useDeleteAction';
import { usePageState } from '../../hooks/usePageState';
import { formatDate } from '../../utils/format';

interface PageState {
  items: Tenant[];
  total: number;
  loading: boolean;
  error: string;
}

export function TenantsPage(): JSX.Element {
  const { page, pageSize, change } = usePageState();
  const [version, setVersion] = useState(0);
  const [state, setState] = useState<PageState>({ items: [], total: 0, loading: true, error: '' });
  // 正在切换状态的公司 id：用于只禁用那一行的按钮，不阻塞其他行。
  const [togglingId, setTogglingId] = useState('');
  // 启停是被后端拒绝时展示原因（例如 id 不存在），与删除的 error 分开管理，
  // 因为两者的确认框不是同一个，混用会让红字出现在错误的对话框里。
  const [statusError, setStatusError] = useState('');
  // 停用是最影响面最大的操作（整家公司立刻登不上），因此也要二次确认。
  const [pending, setPending] = useState<{ tenant: Tenant; next: TenantStatus } | null>(null);
  const removal = useDeleteAction<{ id: string; label: string }>(
    (target) => deleteTenant(target.id),
    () => setVersion((value) => value + 1),
  );

  /** 提交启停；成功后关闭确认框并刷新列表。 */
  const applyStatus = (): void => {
    if (!pending) {
      return;
    }
    setTogglingId(pending.tenant.id);
    setStatusError('');
    setTenantStatus(pending.tenant.id, pending.next)
      .then(() => {
        setPending(null);
        setVersion((value) => value + 1);
      })
      .catch((err: unknown) => {
        setStatusError(errorMessage(err));
      })
      .finally(() => {
        setTogglingId('');
      });
  };

  useEffect(() => {
    let cancelled = false;
    setState((prev) => ({ ...prev, loading: true, error: '' }));
    listTenants({ page, pageSize })
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
  }, [page, pageSize, version]);

  const columns: TableColumnsType<Tenant> = [
    {
      title: '公司',
      key: 'name',
      render: (_, tenant) => (
        <span className="cell-name">
          {tenant.name}
          <span className="cell-sub mono">{tenant.code}</span>
        </span>
      ),
    },
    {
      title: '平台管理员',
      key: 'adminUsername',
      render: (_, tenant) =>
        tenant.adminUsername ? (
          <span className="mono">{tenant.adminUsername}</span>
        ) : (
          // 每公司恰好一个平台管理员；为空说明它被停用或删除了，需要人工介入。
          <span className="sub">（无可用管理员）</span>
        ),
    },
    {
      title: '状态',
      key: 'status',
      render: (_, tenant) =>
        tenant.status === 'active' ? (
          <span className="status-dot status-online">启用</span>
        ) : (
          <span className="status-dot status-offline">停用</span>
        ),
    },
    {
      title: '创建时间',
      key: 'createdAt',
      render: (_, tenant) => <span className="sub">{formatDate(tenant.createdAt)}</span>,
    },
    {
      title: '',
      key: 'actions',
      width: 160,
      render: (_, tenant) => {
        const disabling = tenant.status === 'active';
        return (
          <div className="row-actions">
            <Button
              className="button button-ghost button-small"
              type="button"
              disabled={togglingId === tenant.id}
              onClick={() => {
                setStatusError('');
                setPending({ tenant, next: disabling ? 'disabled' : 'active' });
              }}
            >
              {disabling ? '停用' : '启用'}
            </Button>
            <Button
              className="button button-ghost button-small button-danger"
              type="button"
              onClick={() => removal.request({ id: tenant.id, label: tenant.name })}
            >
              删除
            </Button>
          </div>
        );
      },
    },
  ];

  if (state.error) {
    return <ErrorState message={state.error} onRetry={() => setVersion((v) => v + 1)} />;
  }

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>公司管理</h1>
          <p className="sub">
            建公司时会同时创建它的平台管理员账号。公司之间的人员、团队、项目与集成配置互相隔离。
          </p>
        </div>
        <div className="row-actions">
          <Link className="button button-primary" to="/admin/tenants/new">
            新建公司
          </Link>
        </div>
      </div>

      <DataTable<Tenant>
        columns={columns}
        dataSource={state.items}
        rowKey={(row) => row.id}
        loading={state.loading}
        empty={
          <div className="notice">
            还没有任何公司。先新建一家，系统会同时创建它的平台管理员账号。
          </div>
        }
        serverPagination={{
          page,
          pageSize,
          total: state.total,
          onChange: (nextPage, nextPageSize) => change(nextPage, nextPageSize),
        }}
      />

      <ConfirmDialog
        open={pending !== null}
        title={
          pending?.next === 'disabled'
            ? `停用公司「${pending?.tenant.name ?? ''}」？`
            : `启用公司「${pending?.tenant.name ?? ''}」？`
        }
        description={
          pending?.next === 'disabled' ? (
            <>
              <p>
                停用后该公司<strong>所有账号立即无法登录</strong>，包括它的平台管理员。
              </p>
              <p className="settings-hint">
                数据不会被删除，重新启用即可恢复；账号自身的启停状态保持原样。
              </p>
            </>
          ) : (
            <p>启用后该公司账号可以重新登录，权限与数据范围与停用前一致。</p>
          )
        }
        confirmText={pending?.next === 'disabled' ? '停用' : '启用'}
        danger={pending?.next === 'disabled'}
        loading={togglingId !== ''}
        onConfirm={applyStatus}
        onCancel={() => {
          setPending(null);
          setStatusError('');
        }}
      />
      {statusError ? <p className="field-error">{statusError}</p> : null}

      <ConfirmDialog
        open={removal.target !== null}
        title={`删除公司「${removal.target?.label ?? ''}」？`}
        description={
          <>
            <p>
              删除后该公司及其平台管理员账号一并移除，<strong>无法恢复</strong>。
            </p>
            <p className="settings-hint">
              只允许删除空公司。若还有人员、团队、项目、账号或自定义角色，
              删除会被拒绝并提示还剩什么，需先逐项清空。
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
    </div>
  );
}
