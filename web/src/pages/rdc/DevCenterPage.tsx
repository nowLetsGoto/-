/**
 * AI 研发中心看板（/rdc/dev）。
 * 职责：按需求编号与整体状态筛选研发任务，服务端分页展示，点任务名打开详情抽屉。
 * 输入：URL 的 page / pageSize / reqKey / status、需求下拉数据与任务详情抽屉的变更回调。
 * 输出：筛选工具栏 + DataTable + 详情抽屉；筛选条件与页码都留在 URL 上。
 * 边界：只经 api/rdcDev 取任务、经 api/rdc 取需求下拉；不按写点隐藏入口（本版统一用读点），
 *       不接 Agent / Git / 测试执行器，详情里的 5 个子功能只做文字说明。
 */
import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { Link, useLocation, useSearchParams } from 'react-router-dom';
import type { TableColumnsType } from '@seakoi/console-kit';
import type { DevSubtaskStatus, DevTask } from '../../api/rdcDev';
import { listDevTasks } from '../../api/rdcDev';
import type { Requirement } from '../../api/rdc';
import { listRequirements } from '../../api/rdc';
import { errorMessage } from '../../api/client';
import { Button, Input, Segmented } from '../../components/Controls';
import { DataTable } from '../../components/DataTable';
import { Select } from '../../components/Select';
import { Empty, ErrorState } from '../../components/States';
import { usePageState } from '../../hooks/usePageState';
import { DEV_STATUS_OPTIONS, devStatusLabel, formatDate, projectLabel } from '../../utils/format';
import { DevTaskDetailDrawer } from './DevTaskDetailDrawer';

const LIST_PATH = '/rdc/dev';
const ALL_STATUSES = 'all';
type StatusFilter = DevSubtaskStatus | typeof ALL_STATUSES;

/** 非法 status 参数按"全部"处理，与后端筛选的失败关闭口径一致但不报错。 */
function readStatus(raw: string | null): StatusFilter {
  return DEV_STATUS_OPTIONS.some((option) => option.value === raw)
    ? (raw as DevSubtaskStatus)
    : ALL_STATUSES;
}

interface ListState {
  loading: boolean;
  items: DevTask[];
  total: number;
  error: string;
}

const INITIAL_STATE: ListState = { loading: true, items: [], total: 0, error: '' };

function statusBadge(status: string): JSX.Element {
  return (
    <span className={`badge rdc-status rdc-dev-status-${status}`}>{devStatusLabel(status)}</span>
  );
}

export function DevCenterPage(): JSX.Element {
  const { page, pageSize, change: changePage } = usePageState();
  const [params, setParams] = useSearchParams();
  const location = useLocation();
  const createdTask = (location.state as { createdTask?: string } | null)?.createdTask ?? '';
  const status = readStatus(params.get('status'));
  const reqKey = (params.get('reqKey') ?? '').trim();
  const [reqKeyDraft, setReqKeyDraft] = useState(reqKey);
  const [requirements, setRequirements] = useState<Requirement[]>([]);
  const [state, setState] = useState<ListState>(INITIAL_STATE);
  const [reloadKey, setReloadKey] = useState(0);
  const [detailID, setDetailID] = useState<string | null>(null);

  // 关键字输入先落本地，提交时才写 URL，避免每敲一个字就发一次请求。
  useEffect(() => {
    setReqKeyDraft(reqKey);
  }, [reqKey]);

  // 需求下拉只列本公司需求（pageSize=0 取全量）；加载失败时仍允许手动输入编号。
  useEffect(() => {
    let cancelled = false;
    listRequirements({ pageSize: 0 })
      .then((data) => {
        if (!cancelled) {
          setRequirements(data.items);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setRequirements([]);
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    setState(INITIAL_STATE);
    listDevTasks({
      page,
      pageSize,
      reqKey: reqKey || undefined,
      status: status === ALL_STATUSES ? undefined : status,
    })
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
  }, [page, pageSize, reqKey, status, reloadKey]);

  /** 换筛选条件时回到第一页，避免停在一个已不存在的结果页上。 */
  const changeFilter = (mutate: (updated: URLSearchParams) => void): void => {
    const updated = new URLSearchParams(params);
    mutate(updated);
    updated.delete('page');
    setParams(updated, { replace: true });
  };

  const applyReqKey = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    const text = reqKeyDraft.trim();
    changeFilter((updated) => {
      if (text) {
        updated.set('reqKey', text);
      } else {
        updated.delete('reqKey');
      }
    });
  };

  const columns: TableColumnsType<DevTask> = [
    {
      title: '任务名称',
      key: 'title',
      render: (_, item) => (
        <Button className="project-link" type="button" onClick={() => setDetailID(item.id)}>
          {item.title}
        </Button>
      ),
    },
    { title: '关联需求', key: 'reqKey', width: 170, render: (_, item) => item.reqKey || '—' },
    {
      title: '项目',
      key: 'project',
      width: 140,
      render: (_, item) => <span className="cell-sub">{projectLabel(item.projectName)}</span>,
    },
    {
      title: '前端状态',
      key: 'frontend',
      width: 130,
      render: (_, item) => statusBadge(item.frontendStatus),
    },
    {
      title: '后端状态',
      key: 'backend',
      width: 130,
      render: (_, item) => statusBadge(item.backendStatus),
    },
    {
      title: '整体状态',
      key: 'overall',
      width: 140,
      render: (_, item) => statusBadge(item.overallStatus),
    },
    {
      title: '创建人',
      key: 'creator',
      width: 120,
      render: (_, item) => <span className="cell-sub">{item.creatorName || '—'}</span>,
    },
    {
      title: '创建时间',
      key: 'createdAt',
      width: 160,
      render: (_, item) => <span className="cell-sub">{formatDate(item.createdAt)}</span>,
    },
  ];

  const filtered = status !== ALL_STATUSES || reqKey !== '';
  const emptyState = (
    <Empty
      title={filtered ? '没有符合条件的研发任务' : '还没有研发任务'}
      description={
        filtered
          ? '换一个状态或需求试试，或清除筛选查看全部任务。'
          : '研发任务从一条需求发起，系统自动拆出前端与后端两条子任务；状态推进由人工在详情中完成。'
      }
      action={
        filtered ? (
          <Button
            className="button button-ghost"
            type="button"
            onClick={() => {
              setReqKeyDraft('');
              setParams(new URLSearchParams(), { replace: true });
            }}
          >
            清除筛选
          </Button>
        ) : (
          <Link className="button button-primary" to={`${LIST_PATH}/new`}>
            新建第一个研发任务
          </Link>
        )
      }
    />
  );

  return (
    <section className="page">
      <div className="page-head">
        <div>
          <h1>AI研发中心</h1>
          <p className="sub">
            共 {state.total} 条研发任务 · 从需求发起，人工编排前后端进度（不触发真实研发动作）
          </p>
        </div>
        <Link className="button button-primary" to={`${LIST_PATH}/new`}>
          新建研发任务
        </Link>
      </div>

      {createdTask ? (
        <p className="rdc-success">研发任务「{createdTask}」已创建，前端与后端子任务均为待开发。</p>
      ) : null}

      <div className="card">
        <div className="rdc-filter-bar">
          <Segmented<StatusFilter>
            aria-label="整体状态筛选"
            value={status}
            options={[
              { value: ALL_STATUSES, label: '全部' },
              ...DEV_STATUS_OPTIONS.map((option) => ({ value: option.value, label: option.label })),
            ]}
            onChange={(next) =>
              changeFilter((updated) => {
                if (next === ALL_STATUSES) {
                  updated.delete('status');
                } else {
                  updated.set('status', next);
                }
              })
            }
          />
          <form className="rdc-search-form" onSubmit={applyReqKey}>
            <Select
              value={reqKey}
              width={230}
              placeholder="按需求编号筛选"
              searchable
              options={[
                { value: '', label: '全部需求' },
                ...requirements.map((item) => ({
                  value: item.reqKey,
                  label: `${item.reqKey} · ${item.title}`,
                })),
              ]}
              onChange={(next) => {
                setReqKeyDraft(next);
                changeFilter((updated) => {
                  if (next) {
                    updated.set('reqKey', next);
                  } else {
                    updated.delete('reqKey');
                  }
                });
              }}
            />
            <Input
              className="text-input toolbar-search"
              placeholder="需求编号"
              value={reqKeyDraft}
              onChange={(event) => setReqKeyDraft(event.target.value)}
            />
            <Button className="button button-ghost" type="submit">
              筛选
            </Button>
          </form>
        </div>

        {state.error ? (
          <ErrorState message={state.error} onRetry={() => setReloadKey((value) => value + 1)} />
        ) : (
          <DataTable<DevTask>
            columns={columns}
            dataSource={state.items}
            rowKey={(item) => item.id}
            loading={state.loading}
            serverPagination={
              state.items.length > 0
                ? { page, pageSize, total: state.total, onChange: changePage }
                : undefined
            }
            empty={emptyState}
          />
        )}
      </div>

      <DevTaskDetailDrawer
        taskID={detailID}
        onClose={() => setDetailID(null)}
        onChanged={() => setReloadKey((value) => value + 1)}
      />
    </section>
  );
}
