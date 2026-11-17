/**
 * 审计日志（超级管理员 → 审计日志）。
 * 职责：按动作 / 操作者 / 结果筛选，分页展示；点行看详情（detail 的 JSON 原文）。
 * 输入：URL 的 page / pageSize、页面内的筛选条件；输出：只读表格 + 详情抽屉。
 * 边界：只读页面，不提供任何修改/删除入口；日志由服务端在业务动作后自动写入；
 *       模式（单人 / 团队）是记录的固有属性，来自日志所处的分区，不可作为筛选条件。
 */
import { useEffect, useState } from 'react';
import type { TableColumnsType } from '@seakoi/console-kit';
import { errorMessage } from '../../api/client';
import type { AuditLog } from '../../api/audit';
import { listAuditLogs } from '../../api/audit';
import { Button, Input, Segmented } from '../../components/Controls';
import { DataTable } from '../../components/DataTable';
import { DetailDrawer } from '../../components/DetailDrawer';
import { Empty, ErrorState } from '../../components/States';
import { usePageState } from '../../hooks/usePageState';
import { formatDate, modeLabel } from '../../utils/format';

interface PageState {
  items: AuditLog[];
  total: number;
  loading: boolean;
  error: string;
}

/** 动作的中文说明；未登记的动作直接显示原始 action，避免"漏配就没法看"。 */
const ACTION_LABELS: Record<string, string> = {
  'build.created': '发布包',
  'user.created': '新建账号',
  'user.updated': '编辑账号',
  'user.deleted': '删除账号',
  'user.disabled': '禁用账号',
  'user.enabled': '启用账号',
  'user.password_reset': '重置密码',
  'user.password_changed': '修改密码',
};

/** 模式的中文说明统一由 utils/format 提供，这里不再重复一份映射。 */

type ResultFilter = '' | 'success' | 'failure';

const RESULT_OPTIONS: ReadonlyArray<{ value: ResultFilter; label: string }> = [
  { value: '', label: '全部' },
  { value: 'success', label: '成功' },
  { value: 'failure', label: '失败' },
];

export function AuditLogsPage(): JSX.Element {
  const { page, pageSize, change } = usePageState();

  const [form, setForm] = useState({
    action: '',
    actor: '',
    result: '' as ResultFilter,
  });
  const [applied, setApplied] = useState(form);
  const [version, setVersion] = useState(0);
  const [state, setState] = useState<PageState>({ items: [], total: 0, loading: true, error: '' });
  const [detail, setDetail] = useState<AuditLog | null>(null);

  useEffect(() => {
    let cancelled = false;
    setState((prev) => ({ ...prev, loading: true, error: '' }));
    listAuditLogs({
      page,
      pageSize,
      action: applied.action,
      actor: applied.actor,
      result: applied.result || undefined,
    })
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
  }, [page, pageSize, applied, version]);

  const columns: TableColumnsType<AuditLog> = [
    {
      title: '时间',
      key: 'createdAt',
      width: 170,
      render: (_, log) => <span className="cell-sub">{formatDate(log.createdAt)}</span>,
    },
    {
      title: '模式',
      key: 'mode',
      width: 100,
      render: (_, log) => (
        <span className={`mode-tag mode-${log.mode}`}>{modeLabel(log.mode)}</span>
      ),
    },
    {
      title: '操作者',
      key: 'actorName',
      width: 130,
      render: (_, log) => log.actorName || 'system',
    },
    {
      title: '动作',
      key: 'action',
      render: (_, log) => (
        <span className="audit-action">
          {ACTION_LABELS[log.action] ?? log.action}
          <code className="audit-code">{log.action}</code>
        </span>
      ),
    },
    {
      title: '对象',
      key: 'object',
      render: (_, log) => (
        <span className="cell-sub">{`${log.objectType}${log.objectId ? ` · ${shortId(log.objectId)}` : ''}`}</span>
      ),
    },
    {
      title: '结果',
      key: 'result',
      width: 90,
      render: (_, log) =>
        log.result === 'success' ? (
          <span className="status-dot status-online">成功</span>
        ) : (
          <span className="status-dot status-offline">失败</span>
        ),
    },
    {
      title: 'IP',
      key: 'ip',
      width: 130,
      render: (_, log) => <span className="cell-sub">{log.ip || '—'}</span>,
    },
    {
      title: '',
      key: 'actions',
      width: 80,
      render: (_, log) => (
        <div className="row-actions">
          <Button
            className="button button-ghost button-small"
            type="button"
            onClick={() => setDetail(log)}
          >
            详情
          </Button>
        </div>
      ),
    },
  ];

  const hasFilter = Boolean(applied.action || applied.actor || applied.result);

  return (
    <section className="page">
      <div className="page-head">
        <div>
          <h1>审计日志</h1>
          <p className="sub">共 {state.total} 条 · 只读，任何账号都无法修改或删除</p>
        </div>
      </div>

      <div className="card">
        <div className="toolbar">
          <Input
            className="text-input toolbar-search"
            placeholder="动作，如 user.disabled"
            value={form.action}
            onChange={(event) => setForm({ ...form, action: event.target.value })}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                setApplied(form);
              }
            }}
          />
          <Input
            className="text-input toolbar-search"
            placeholder="操作者账号"
            value={form.actor}
            onChange={(event) => setForm({ ...form, actor: event.target.value })}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                setApplied(form);
              }
            }}
          />
          <Segmented<ResultFilter>
            aria-label="结果"
            value={form.result}
            options={RESULT_OPTIONS.map((option) => ({
              value: option.value,
              label: option.label,
            }))}
            onChange={(value) => setForm({ ...form, result: value })}
          />
          <Button
            className="button button-ghost button-small"
            type="button"
            onClick={() => setApplied(form)}
          >
            查询
          </Button>
          {hasFilter ? (
            <Button
              className="button button-quiet button-small"
              type="button"
              onClick={() => {
                const cleared = { action: '', actor: '', result: '' as '' };
                setForm(cleared);
                setApplied(cleared);
              }}
            >
              清空
            </Button>
          ) : null}
        </div>

        {state.error ? (
          <ErrorState message={state.error} onRetry={() => setVersion((value) => value + 1)} />
        ) : (
          <DataTable<AuditLog>
            columns={columns}
            dataSource={state.items}
            rowKey={(log) => String(log.id)}
            loading={state.loading}
            serverPagination={{ page, pageSize, total: state.total, onChange: change }}
            empty={
              <Empty
                title={hasFilter ? '没有匹配的审计记录' : '还没有审计记录'}
                description={
                  hasFilter
                    ? '换个筛选条件试试，或清空后看全部记录。'
                    : '账号变更、发布包等关键动作发生后，会自动记录在这里。'
                }
              />
            }
          />
        )}
      </div>

      <DetailDrawer open={detail !== null} title="审计详情" onClose={() => setDetail(null)}>
        {detail ? (
          <div className="drawer-body">
            <dl className="kv-list">
              <div>
                <dt>时间</dt>
                <dd>{formatDate(detail.createdAt)}</dd>
              </div>
              <div>
                <dt>模式</dt>
                <dd>{modeLabel(detail.mode)}</dd>
              </div>
              <div>
                <dt>操作者</dt>
                <dd>{detail.actorName || 'system'}</dd>
              </div>
              <div>
                <dt>动作</dt>
                <dd>
                  {ACTION_LABELS[detail.action] ?? detail.action} <code>{detail.action}</code>
                </dd>
              </div>
              <div>
                <dt>对象</dt>
                <dd>
                  {detail.objectType}
                  {detail.objectId ? ` · ${detail.objectId}` : ''}
                </dd>
              </div>
              <div>
                <dt>结果</dt>
                <dd>{detail.result === 'success' ? '成功' : '失败'}</dd>
              </div>
              <div>
                <dt>来源 IP</dt>
                <dd>{detail.ip || '—'}</dd>
              </div>
            </dl>

            <section className="drawer-section">
              <h3>明细（detail）</h3>
              <pre className="audit-detail">{prettyJSON(detail.detail)}</pre>
            </section>
          </div>
        ) : null}
      </DetailDrawer>
    </section>
  );
}

/** UUID 太长，表格里只显示前 8 位，完整值在详情里看。 */
function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id;
}

/** detail 是 JSON 文本；格式化失败就原样显示，不能让详情页崩掉。 */
function prettyJSON(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw || '{}'), null, 2);
  } catch {
    return raw;
  }
}
