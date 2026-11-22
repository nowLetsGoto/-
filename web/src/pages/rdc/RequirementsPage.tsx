/**
 * 需求池列表页（研发中心 → 需求中心）。
 * 职责：服务端分页拉列表，按状态/项目/关键字筛选，点需求编号打开只读详情抽屉。
 * 输入：URL 的 page / pageSize / status / keyword / projectId，以及新建成功回传的 reqKey。
 * 输出：筛选工具栏 + DataTable + 详情抽屉；筛选与页码都留在 URL 上。
 * 边界：不拼 API 路径（走 api/rdc）、不承载表单、不放编辑/删除假按钮；
 *       新建入口按 can(me,'rdc.requirement.write') 隐藏；空列表不挂分页条。
 */
import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { Link, useLocation, useSearchParams } from 'react-router-dom';
import type { TableColumnsType } from '@seakoi/console-kit';
import type { Requirement, RequirementStatus } from '../../api/rdc';
import { listRequirements } from '../../api/rdc';
import { errorMessage } from '../../api/client';
import { Button, Input, Segmented } from '../../components/Controls';
import { DataTable } from '../../components/DataTable';
import { Select } from '../../components/Select';
import { Empty, ErrorState } from '../../components/States';
import { useMe } from '../../hooks/useMe';
import { usePageState } from '../../hooks/usePageState';
import { useProjects } from '../../hooks/useProjects';
import {
  REQUIREMENT_STATUS_OPTIONS,
  formatDate,
  projectLabel,
  requirementStatusLabel,
} from '../../utils/format';
import { can } from '../../utils/permission';
import { RequirementDetailDrawer } from './RequirementDetailDrawer';

const LIST_PATH = '/rdc/requirements';
const ALL_STATUSES = 'all';
type StatusFilter = RequirementStatus | typeof ALL_STATUSES;

/** 非法 status 参数按「全部」处理，与后端筛选的失败关闭口径一致但不报错。 */
function readStatus(raw: string | null): StatusFilter {
  return REQUIREMENT_STATUS_OPTIONS.some((option) => option.value === raw)
    ? (raw as RequirementStatus)
    : ALL_STATUSES;
}

interface ListState {
  loading: boolean;
  items: Requirement[];
  total: number;
  error: string;
}

const INITIAL_STATE: ListState = { loading: true, items: [], total: 0, error: '' };

export function RequirementsPage(): JSX.Element {
  const { page, pageSize, change: changePage } = usePageState();
  const [params, setParams] = useSearchParams();
  const status = readStatus(params.get('status'));
  const keyword = (params.get('keyword') ?? '').trim();
  const projectId = (params.get('projectId') ?? '').trim();
  const location = useLocation();
  const { projects } = useProjects();
  const createdKey = (location.state as { createdKey?: string } | null)?.createdKey ?? '';
  const { me } = useMe();
  const writable = can(me, 'rdc.requirement.write');

  // 关键字输入先落在本地，提交时才写 URL，避免每敲一个字就发一次请求。
  const [keywordDraft, setKeywordDraft] = useState(keyword);
  useEffect(() => {
    setKeywordDraft(keyword);
  }, [keyword]);

  const [state, setState] = useState<ListState>(INITIAL_STATE);
  const [reloadKey, setReloadKey] = useState(0);
  const [detailKey, setDetailKey] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setState(INITIAL_STATE);
    listRequirements({
      page,
      pageSize,
      status: status === ALL_STATUSES ? undefined : status,
      keyword: keyword || undefined,
      projectId: projectId || undefined,
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
  }, [page, pageSize, status, keyword, projectId, reloadKey]);

  /** 换筛选条件时回到第一页，否则会停在一个已不存在的结果页上。 */
  const changeFilter = (mutate: (updated: URLSearchParams) => void): void => {
    const updated = new URLSearchParams(params);
    mutate(updated);
    updated.delete('page');
    setParams(updated, { replace: true });
  };

  const applyKeyword = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    const text = keywordDraft.trim();
    changeFilter((updated) => {
      if (text) {
        updated.set('keyword', text);
      } else {
        updated.delete('keyword');
      }
    });
  };

  const columns: TableColumnsType<Requirement> = [
    {
      title: '需求编号',
      key: 'reqKey',
      width: 170,
      render: (_, item) => (
        <Button className="project-link" type="button" onClick={() => setDetailKey(item.reqKey)}>
          {item.reqKey}
        </Button>
      ),
    },
    { title: '标题', key: 'title', render: (_, item) => item.title },
    {
      title: '项目',
      key: 'project',
      width: 150,
      render: (_, item) => <span className="cell-sub">{projectLabel(item.projectName)}</span>,
    },
    {
      title: '状态',
      key: 'status',
      width: 120,
      render: (_, item) => (
        <span className={`badge rdc-status rdc-status-${item.status}`}>
          {requirementStatusLabel(item.status)}
        </span>
      ),
    },
    {
      title: '创建人',
      key: 'creator',
      width: 130,
      render: (_, item) => <span className="cell-sub">{item.creatorName || '—'}</span>,
    },
    {
      title: '创建时间',
      key: 'createdAt',
      width: 160,
      render: (_, item) => <span className="cell-sub">{formatDate(item.createdAt)}</span>,
    },
  ];

  const filtered = status !== ALL_STATUSES || keyword !== '' || projectId !== '';
  const emptyState = (
    <Empty
      title={filtered ? '没有符合条件的需求' : '还没有需求'}
      description={
        filtered
          ? '换一个状态或关键字试试，或清除筛选查看全部需求。'
          : '需求是研发流程的起点：记录背景与验收标准后，即可作为后续研发、审核与交付的引用编号。'
      }
      action={
        filtered ? (
          <Button
            className="button button-ghost"
            type="button"
            onClick={() => {
              setKeywordDraft('');
              setParams(new URLSearchParams(), { replace: true });
            }}
          >
            清除筛选
          </Button>
        ) : writable ? (
          <Link className="button button-primary" to={`${LIST_PATH}/new`}>
            新建第一个需求
          </Link>
        ) : undefined
      }
    />
  );

  return (
    <section className="page">
      <div className="page-head">
        <div>
          <h1>需求中心</h1>
          <p className="sub">共 {state.total} 条需求 · 按创建时间倒序</p>
        </div>
        {writable ? (
          <Link className="button button-primary" to={`${LIST_PATH}/new`}>
            新建需求
          </Link>
        ) : null}
      </div>

      {createdKey ? (
        <p className="rdc-success">需求 {createdKey} 已创建，编号可用于后续流程引用。</p>
      ) : null}

      <div className="card">
        <div className="rdc-filter-project">
          <span>项目</span>
          <Select
            value={projectId}
            width={220}
            placeholder="全部项目"
            options={[
              { value: '', label: '全部项目' },
              ...projects.map((project) => ({ value: project.id, label: project.name })),
            ]}
            onChange={(next) =>
              changeFilter((updated) => {
                if (next) {
                  updated.set('projectId', next);
                } else {
                  updated.delete('projectId');
                }
              })
            }
          />
        </div>
        <div className="rdc-filter-bar">
          <Segmented<StatusFilter>
            aria-label="状态筛选"
            value={status}
            options={[
              { value: ALL_STATUSES, label: '全部' },
              ...REQUIREMENT_STATUS_OPTIONS.map((option) => ({
                value: option.value,
                label: option.label,
              })),
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
          <form className="rdc-search-form" onSubmit={applyKeyword}>
            <Input
              className="text-input toolbar-search"
              placeholder="搜索需求编号或标题"
              value={keywordDraft}
              onChange={(event) => setKeywordDraft(event.target.value)}
            />
            <Button className="button button-ghost" type="submit">
              搜索
            </Button>
          </form>
        </div>

        {state.error ? (
          <ErrorState message={state.error} onRetry={() => setReloadKey((value) => value + 1)} />
        ) : (
          <DataTable<Requirement>
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

      <RequirementDetailDrawer reqKey={detailKey} onClose={() => setDetailKey(null)} />
    </section>
  );
}
