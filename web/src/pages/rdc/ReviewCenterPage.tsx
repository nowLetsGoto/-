/**
 * 人工审核中心列表页（研发中心 → 人工审核中心）。
 * 职责：服务端分页列出含待审子任务的任务，按需求编号筛选，点任务名打开审核详情抽屉。
 * 输入：URL 的 page / pageSize / reqKey（关键字输入提交后写入）。
 * 输出：筛选工具栏 + DataTable + 审核详情抽屉；筛选与页码都留在 URL 上。
 * 边界：不拼 API 路径（走 api/rdcReview）、不放执行 Diff / 打开编辑器等假按钮；
 *       空列表不挂分页条；审核操作在详情抽屉内完成，成功后刷新列表。
 */
import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { useSearchParams } from 'react-router-dom';
import type { TableColumnsType } from '@seakoi/console-kit';
import type { ReviewTask } from '../../api/rdcReview';
import { listReviewTasks } from '../../api/rdcReview';
import { errorMessage } from '../../api/client';
import { Button, Input } from '../../components/Controls';
import { DataTable } from '../../components/DataTable';
import { Empty, ErrorState } from '../../components/States';
import { usePageState } from '../../hooks/usePageState';
import { devStatusLabel, formatDate, projectLabel } from '../../utils/format';
import { ReviewDetailDrawer } from './ReviewDetailDrawer';

interface ListState {
  loading: boolean;
  items: ReviewTask[];
  total: number;
  error: string;
}

const INITIAL_STATE: ListState = { loading: true, items: [], total: 0, error: '' };

export function ReviewCenterPage(): JSX.Element {
  const { page, pageSize, change: changePage } = usePageState();
  const [params, setParams] = useSearchParams();
  const reqKey = (params.get('reqKey') ?? '').trim();
  const [keywordDraft, setKeywordDraft] = useState(reqKey);
  const [state, setState] = useState<ListState>(INITIAL_STATE);
  const [reloadKey, setReloadKey] = useState(0);
  const [detailID, setDetailID] = useState<string | null>(null);

  useEffect(() => {
    setKeywordDraft(reqKey);
  }, [reqKey]);

  useEffect(() => {
    let cancelled = false;
    setState(INITIAL_STATE);
    listReviewTasks({ page, pageSize, reqKey: reqKey || undefined })
      .then((data) => {
        if (!cancelled) {
          setState({ loading: false, items: data.items, total: data.total, error: '' });
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setState({ loading: false, items: [], total: 0, error: errorMessage(err) });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [page, pageSize, reqKey, reloadKey]);

  const submitKeyword = (event: FormEvent): void => {
    event.preventDefault();
    const updated = new URLSearchParams(params);
    const value = keywordDraft.trim();
    if (value) {
      updated.set('reqKey', value);
    } else {
      updated.delete('reqKey');
    }
    updated.delete('page'); // 一次新的筛选回到第一页
    setParams(updated, { replace: true });
  };

  const columns: TableColumnsType<ReviewTask> = [
    {
      title: '任务名称',
      dataIndex: 'title',
      key: 'title',
      render: (_value, record) => (
        <Button className="button button-link" type="button" onClick={() => setDetailID(record.id)}>
          {record.title}
        </Button>
      ),
    },
    {
      title: '需求编号',
      dataIndex: 'reqKey',
      key: 'reqKey',
      render: (value: string) => value || '—',
    },
    {
      title: '所属项目',
      key: 'projectName',
      render: (_value, record) => projectLabel(record.projectName),
    },
    {
      title: '整体状态',
      key: 'overallStatus',
      render: (_value, record) => (
        <span className={`badge rdc-status rdc-dev-status-${record.overallStatus}`}>
          {devStatusLabel(record.overallStatus)}
        </span>
      ),
    },
    {
      title: '待审子任务',
      dataIndex: 'reviewableCount',
      key: 'reviewableCount',
    },
    {
      title: '创建时间',
      dataIndex: 'createdAt',
      key: 'createdAt',
      render: (value: string) => formatDate(value),
    },
  ];

  return (
    <div className="rdc-review-page">
      <form className="rdc-filter-row" onSubmit={submitKeyword}>
        <Input
          value={keywordDraft}
          onChange={(event) => setKeywordDraft(event.target.value)}
          placeholder="按需求编号筛选，如 REQ-20261120-001"
        />
        <Button className="button button-primary" type="submit">
          筛选
        </Button>
      </form>

      {state.error ? (
        <ErrorState message={state.error} onRetry={() => setReloadKey((v) => v + 1)} />
      ) : (
        <DataTable<ReviewTask>
          columns={columns}
          dataSource={state.items}
          rowKey={(item) => item.id}
          loading={state.loading}
          empty={
            <Empty
              title="暂无待审任务"
              description="当前没有处于待审核 / 待提交 / 已驳回状态的子任务。"
            />
          }
          serverPagination={
            state.total > 0
              ? { page, pageSize, total: state.total, onChange: changePage }
              : undefined
          }
        />
      )}

      <ReviewDetailDrawer
        taskID={detailID}
        onClose={() => setDetailID(null)}
        onChanged={() => setReloadKey((v) => v + 1)}
      />
    </div>
  );
}
