/**
 * 公司管理（平台管理 → 公司管理，仅平台超级管理员）。
 * 职责：分页列出全部公司及其平台管理员账号名；新建公司走独立路由。
 * 输入：URL 的 page / pageSize；输出：表格 + 新建入口。
 * 边界：不在此页编辑或删除公司（牵连面太大，需运维确认）；不做权限判断——非超管进不来，接口也会 403。
 */
import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import type { TableColumnsType } from '@seakoi/console-kit';
import { errorMessage } from '../../api/client';
import type { Tenant } from '../../api/tenants';
import { listTenants } from '../../api/tenants';
import { DataTable } from '../../components/DataTable';
import { ErrorState } from '../../components/States';
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
    </div>
  );
}
