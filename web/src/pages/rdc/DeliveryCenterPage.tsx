/**
 * 职责：按需求聚合列出交付当前态，服务端分页和筛选均保持在 URL。
 * 输入：URL 查询与交付列表 API；输出：三态表格与详情抽屉。
 * 边界：不操作 01/02/03 菜单、不拼接口路径；空列表不挂分页条。
 */
import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { useSearchParams } from 'react-router-dom';
import type { TableColumnsType } from '@seakoi/console-kit';
import { listDeliveryRecords } from '../../api/rdcDelivery';
import type { AcceptanceStatus, DeliveryRecord } from '../../api/rdcDelivery';
import { errorMessage } from '../../api/client';
import { Button, Input, Segmented } from '../../components/Controls';
import { DataTable } from '../../components/DataTable';
import { Empty, ErrorState } from '../../components/States';
import { usePageState } from '../../hooks/usePageState';
import { ACCEPTANCE_STATUS_LABELS, formatDate, projectLabel } from '../../utils/format';
import { DeliveryDetailDrawer } from './DeliveryDetailDrawer';

type FilterStatus = AcceptanceStatus | 'all';
type ArchiveFilter = 'all' | 'true' | 'false';
interface ListState {
  loading: boolean;
  items: DeliveryRecord[];
  total: number;
  error: string;
}
const initialState: ListState = { loading: true, items: [], total: 0, error: '' };
export function DeliveryCenterPage(): JSX.Element {
  const { page, pageSize, change: changePage } = usePageState();
  const [params, setParams] = useSearchParams();
  const keyword = params.get('keyword') ?? '';
  const status = (params.get('acceptanceStatus') ?? 'all') as FilterStatus;
  const archived = (params.get('archived') ?? 'all') as ArchiveFilter;
  const [draft, setDraft] = useState(keyword);
  const [state, setState] = useState<ListState>(initialState);
  const [reload, setReload] = useState(0);
  const [detailID, setDetailID] = useState<string | null>(null);
  useEffect(() => setDraft(keyword), [keyword]);
  useEffect(() => {
    let cancelled = false;
    setState(initialState);
    listDeliveryRecords({
      page,
      pageSize,
      keyword,
      acceptanceStatus: status === 'all' ? undefined : status,
      archived: archived === 'all' ? undefined : archived === 'true',
    })
      .then((data) => {
        if (!cancelled)
          setState({ loading: false, items: data.items, total: data.total, error: '' });
      })
      .catch((error: unknown) => {
        if (!cancelled)
          setState({ loading: false, items: [], total: 0, error: errorMessage(error) });
      });
    return () => {
      cancelled = true;
    };
  }, [page, pageSize, keyword, status, archived, reload]);
  const setFilter = (key: string, value: string): void => {
    const updated = new URLSearchParams(params);
    if (value === 'all' || value === '') updated.delete(key);
    else updated.set(key, value);
    updated.delete('page');
    setParams(updated, { replace: true });
  };
  const submit = (event: FormEvent): void => {
    event.preventDefault();
    setFilter('keyword', draft.trim());
  };
  const columns: TableColumnsType<DeliveryRecord> = [
    {
      title: '需求',
      key: 'title',
      render: (_value, row) => (
        <Button
          type="button"
          className="button button-link"
          onClick={() => setDetailID(row.requirementId)}
        >
          {row.requirementTitle}
        </Button>
      ),
    },
    { title: '编号', dataIndex: 'reqKey', key: 'reqKey' },
    { title: '项目', key: 'projectName', render: (_value, row) => projectLabel(row.projectName) },
    {
      title: '测试',
      key: 'testResult',
      render: (_value, row) =>
        row.testResult === 'not_registered'
          ? '未登记'
          : row.testResult === 'passed'
            ? '通过'
            : '未通过',
    },
    {
      title: '验收',
      key: 'acceptanceStatus',
      render: (_value, row) => (
        <span className={`badge rdc-delivery-${row.acceptanceStatus}`}>
          {ACCEPTANCE_STATUS_LABELS[row.acceptanceStatus]}
        </span>
      ),
    },
    {
      title: '归档',
      key: 'archived',
      render: (_value, row) => (row.archived ? '已归档' : '未归档'),
    },
    {
      title: '最后登记',
      key: 'createdAt',
      render: (_value, row) => (row.createdAt ? formatDate(row.createdAt) : '—'),
    },
  ];
  return (
    <div className="rdc-delivery-page">
      <div className="rdc-filter-row">
        <form onSubmit={submit}>
          <Input
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            placeholder="需求编号或标题"
          />
          <Button type="submit" className="button button-primary">
            筛选
          </Button>
        </form>
        <Segmented<FilterStatus>
          value={status}
          options={[
            { value: 'all', label: '全部验收' },
            { value: 'pending', label: '待验收' },
            { value: 'passed', label: '已通过' },
            { value: 'failed', label: '未通过' },
          ]}
          onChange={(value) => setFilter('acceptanceStatus', value)}
        />
        <Segmented<ArchiveFilter>
          value={archived}
          options={[
            { value: 'all', label: '全部' },
            { value: 'false', label: '未归档' },
            { value: 'true', label: '已归档' },
          ]}
          onChange={(value) => setFilter('archived', value)}
        />
      </div>
      {state.error ? (
        <ErrorState message={state.error} onRetry={() => setReload((value) => value + 1)} />
      ) : (
        <DataTable<DeliveryRecord>
          columns={columns}
          dataSource={state.items}
          rowKey={(row) => row.requirementId}
          loading={state.loading}
          empty={<Empty title="暂无交付记录" description="需求中心新建的需求会显示在这里。" />}
          serverPagination={
            state.total > 0
              ? { page, pageSize, total: state.total, onChange: changePage }
              : undefined
          }
        />
      )}
      <DeliveryDetailDrawer
        requirementID={detailID}
        onClose={() => setDetailID(null)}
        onChanged={() => setReload((value) => value + 1)}
      />
    </div>
  );
}
