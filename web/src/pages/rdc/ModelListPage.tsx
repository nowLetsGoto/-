/**
 * AI模型列表页（研发中心 → AI模型）。
 * 职责：按公司列出模型、筛选启用态与协议、服务端分页、详情抽屉、写操作入口与统计入口。
 * 输入：URL 的 page / pageSize / enabled / protocol / tenantId，列表 API 与当前登录人权限。
 * 输出：三态表格；写操作均二次确认，实测走同一 ModelChatService 的分类结果。
 * 边界：不拼接口 URL、不显示完整 Key（只有 apiKeyMask）；写入口按 rdc.model.write 隐藏；
 *       超管未选目标公司时不发列表请求，单人模式由路由守卫挡在页面之外。
 */
import { useEffect, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import type { TableColumnsType } from '@seakoi/console-kit';
import {
  deleteAIModel,
  listAIModels,
  setAIModelDefault,
  setAIModelStatus,
} from '../../api/rdcModel';
import type { AIModel, AIProtocol as AIModelProtocol } from '../../api/rdcModel';
import { errorMessage } from '../../api/client';
import { listTenants } from '../../api/tenants';
import type { Tenant } from '../../api/tenants';
import { Button, Segmented } from '../../components/Controls';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { DataTable } from '../../components/DataTable';
import { DetailDrawer } from '../../components/DetailDrawer';
import { Select } from '../../components/Select';
import { Empty, ErrorState } from '../../components/States';
import { useMe } from '../../hooks/useMe';
import { usePageState } from '../../hooks/usePageState';
import {
  AI_PROTOCOL_LABELS,
  aiProviderLabel,
  aiProtocolLabel,
  formatDate,
  shortText,
} from '../../utils/format';
import { can } from '../../utils/permission';
import { ModelTestPanel } from './ModelTestPanel';

const LIST_PATH = '/rdc/models';
const USAGE_PATH = '/rdc/models/usage';
type EnabledFilter = 'all' | 'true' | 'false';
type PendingAction =
  | { kind: 'default'; model: AIModel }
  | { kind: 'status'; model: AIModel; enabled: boolean }
  | { kind: 'delete'; model: AIModel };

interface ListState {
  loading: boolean;
  items: AIModel[];
  total: number;
  error: string;
}
const INITIAL_STATE: ListState = { loading: true, items: [], total: 0, error: '' };

export function ModelListPage(): JSX.Element {
  const { page, pageSize, change: changePage } = usePageState();
  const [params, setParams] = useSearchParams();
  const tenantId = (params.get('tenantId') ?? '').trim();
  const enabledFilter = (params.get('enabled') ?? 'all') as EnabledFilter;
  const protocolFilter = (params.get('protocol') ?? '').trim();
  const { me } = useMe();
  const superAdmin = me?.isSuperAdmin === true;
  const writable = can(me, 'rdc.model.write');

  const [state, setState] = useState<ListState>(INITIAL_STATE);
  const [reloadKey, setReloadKey] = useState(0);
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [detail, setDetail] = useState<AIModel | null>(null);
  const [testID, setTestID] = useState<string | null>(null);
  const [pending, setPending] = useState<PendingAction | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState('');

  // 超管的公司下拉只拉一次；普通账号没有代配入口，不请求平台公司列表。
  useEffect(() => {
    if (!superAdmin) {
      return;
    }
    let cancelled = false;
    listTenants({ pageSize: 0 })
      .then((data) => {
        if (!cancelled) {
          setTenants(data.items);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setTenants([]);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [superAdmin]);

  const needsTenant = superAdmin && !tenantId;
  useEffect(() => {
    if (needsTenant) {
      setState({ loading: false, items: [], total: 0, error: '' });
      return;
    }
    let cancelled = false;
    setState(INITIAL_STATE);
    listAIModels({
      page,
      pageSize,
      enabled: enabledFilter === 'all' ? undefined : enabledFilter === 'true',
      protocol: (protocolFilter as AIModelProtocol) || undefined,
      tenantId: superAdmin ? tenantId : undefined,
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
  }, [page, pageSize, enabledFilter, protocolFilter, tenantId, superAdmin, needsTenant, reloadKey]);

  /** 换筛选条件回到第一页；replace 避免后退键在页码之间反复跳。 */
  const changeFilter = (key: string, value: string): void => {
    const updated = new URLSearchParams(params);
    if (value && value !== 'all') {
      updated.set(key, value);
    } else {
      updated.delete(key);
    }
    updated.delete('page');
    setParams(updated, { replace: true });
  };

  const backTo = tenantId ? `${LIST_PATH}?tenantId=${encodeURIComponent(tenantId)}` : LIST_PATH;
  const columns: TableColumnsType<AIModel> = [
    {
      title: '模型名称',
      key: 'name',
      render: (_, item) => (
        <Button className="button button-link" type="button" onClick={() => setDetail(item)}>
          {item.name}
        </Button>
      ),
    },
    { title: '模型 ID', dataIndex: 'modelId', key: 'modelId', width: 190 },
    {
      title: '供应商 / 协议',
      key: 'provider',
      width: 150,
      render: (_, item) => `${aiProviderLabel(item.provider)} · ${aiProtocolLabel(item.protocol)}`,
    },
    {
      title: 'API 地址',
      key: 'baseUrl',
      width: 200,
      render: (_, item) => <span className="cell-sub">{shortText(item.baseUrl, 36)}</span>,
    },
    {
      title: '默认',
      key: 'isDefault',
      width: 80,
      render: (_, item) =>
        item.isDefault ? (
          <span className="badge rdc-model-default">默认</span>
        ) : (
          <span className="cell-sub">—</span>
        ),
    },
    {
      title: '思考',
      key: 'thinkingEnabled',
      width: 80,
      render: (_, item) => (item.thinkingEnabled ? '开启' : '关闭'),
    },
    { title: '上下文', dataIndex: 'contextTokens', key: 'contextTokens', width: 100 },
    {
      title: 'Key',
      key: 'apiKeyMask',
      width: 130,
      render: (_, item) => <code className="rdc-model-mask">{item.apiKeyMask || '未配置'}</code>,
    },
    {
      title: '状态',
      key: 'enabled',
      width: 90,
      render: (_, item) => (
        <span className={`badge ${item.enabled ? 'rdc-model-on' : 'rdc-model-off'}`}>
          {item.enabled ? '启用' : '停用'}
        </span>
      ),
    },
    {
      title: '更新时间',
      key: 'updatedAt',
      width: 160,
      render: (_, item) => <span className="cell-sub">{formatDate(item.updatedAt)}</span>,
    },
    ...(writable
      ? [
          {
            title: '操作',
            key: 'actions',
            width: 260,
            render: (_: unknown, item: AIModel) => (
              <div className="rdc-model-actions">
                <Link
                  className="button button-link"
                  to={`${LIST_PATH}/${item.id}/edit${tenantId ? `?tenantId=${encodeURIComponent(tenantId)}` : ''}`}
                >
                  编辑
                </Link>
                <Button
                  type="button"
                  className="button button-link"
                  onClick={() => setTestID(item.id)}
                >
                  实测
                </Button>
                {item.enabled && !item.isDefault ? (
                  <Button
                    type="button"
                    className="button button-link"
                    onClick={() => setPending({ kind: 'default', model: item })}
                  >
                    设为默认
                  </Button>
                ) : null}
                <Button
                  type="button"
                  className="button button-link"
                  onClick={() =>
                    setPending({ kind: 'status', model: item, enabled: !item.enabled })
                  }
                >
                  {item.enabled ? '停用' : '启用'}
                </Button>
                <Button
                  type="button"
                  className="button button-link button-danger"
                  onClick={() => setPending({ kind: 'delete', model: item })}
                >
                  删除
                </Button>
              </div>
            ),
          },
        ]
      : []),
  ];

  const runPending = async (): Promise<void> => {
    if (!pending) {
      return;
    }
    setBusy(true);
    setActionError('');
    try {
      if (pending.kind === 'default') {
        await setAIModelDefault(pending.model.id, superAdmin ? tenantId : undefined);
      } else if (pending.kind === 'status') {
        await setAIModelStatus(
          pending.model.id,
          pending.enabled,
          superAdmin ? tenantId : undefined,
        );
      } else {
        await deleteAIModel(pending.model.id, superAdmin ? tenantId : undefined);
      }
      setPending(null);
      setDetail(null);
      setReloadKey((value) => value + 1);
    } catch (err) {
      setActionError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const actionDescription = (() => {
    if (!pending) {
      return '';
    }
    if (pending.kind === 'default') {
      return `设为默认后，下一次模型调用将改用「${pending.model.name}」，本公司当前默认会取消。该操作只影响本公司。`;
    }
    if (pending.kind === 'status') {
      return pending.enabled
        ? `启用「${pending.model.name}」后，它将参与下一次选模；若本公司尚无默认模型，会自动成为默认。`
        : `停用「${pending.model.name}」后，它不再参与任何调用；若它是默认且另有启用模型，需先指定新默认。配置本身不会丢失。`;
    }
    return `软删「${pending.model.name}」会将其标记为停用，历史审计仍可追溯，不会物理删除数据；若它是默认且另有启用模型，需先指定新默认。`;
  })();

  const emptyState = (
    <Empty
      title={needsTenant ? '请先选择目标公司' : '还没有模型配置'}
      description={
        needsTenant
          ? '超管代配不会默认展示任何公司的 Key 配置，选定公司后才能查看与操作。'
          : '点击「新增模型」把可用的模型接入进来。scnet 模板只填入表单，不会自动创建配置。'
      }
      action={
        writable && !needsTenant ? (
          <Link
            className="button button-primary"
            to={`${LIST_PATH}/new${tenantId ? `?tenantId=${encodeURIComponent(tenantId)}` : ''}`}
          >
            新增模型
          </Link>
        ) : undefined
      }
    />
  );

  return (
    <section className="page rdc-model-page">
      <div className="page-head">
        <div>
          <h1>AI模型</h1>
          <p className="sub">按公司配置可用的模型与调用参数，供平台各模块统一使用</p>
        </div>
        {writable && !needsTenant ? (
          <Link
            className="button button-primary"
            to={`${LIST_PATH}/new${tenantId ? `?tenantId=${encodeURIComponent(tenantId)}` : ''}`}
          >
            新增模型
          </Link>
        ) : null}
      </div>

      <div className="card">
        <div className="rdc-model-toolbar">
          {superAdmin ? (
            <label className="rdc-model-tenant">
              <span>目标公司</span>
              <Select
                value={tenantId}
                searchable
                placeholder="请选择要代配的公司"
                options={[
                  { value: '', label: '请选择目标公司' },
                  ...tenants.map((tenant) => ({ value: tenant.id, label: tenant.name })),
                ]}
                onChange={(value) => changeFilter('tenantId', value)}
              />
            </label>
          ) : null}
          <Segmented<EnabledFilter>
            aria-label="启用态筛选"
            value={enabledFilter}
            options={[
              { value: 'all', label: '全部' },
              { value: 'true', label: '启用' },
              { value: 'false', label: '停用' },
            ]}
            onChange={(value) => changeFilter('enabled', value)}
          />
          {/* 协议筛选：三协议并存后，"这公司用了哪几种报文"是排障第一眼要看的信息。 */}
          <label className="rdc-model-protocol-filter">
            <span>协议</span>
            <Select
              value={protocolFilter}
              options={[
                { value: '', label: '全部协议' },
                ...Object.entries(AI_PROTOCOL_LABELS).map(([value, label]) => ({ value, label })),
              ]}
              onChange={(value) => changeFilter('protocol', value)}
            />
          </label>
          {needsTenant ? null : (
            <Link
              className="button"
              to={tenantId ? `${USAGE_PATH}?tenantId=${encodeURIComponent(tenantId)}` : USAGE_PATH}
            >
              调用统计 / 上限
            </Link>
          )}
        </div>

        {actionError ? <ErrorState message={actionError} /> : null}
        {state.error ? (
          <ErrorState message={state.error} onRetry={() => setReloadKey((value) => value + 1)} />
        ) : needsTenant ? (
          emptyState
        ) : (
          <DataTable<AIModel>
            columns={columns}
            dataSource={state.items}
            rowKey={(item) => item.id}
            loading={state.loading}
            empty={emptyState}
            serverPagination={
              state.items.length > 0
                ? { page, pageSize, total: state.total, onChange: changePage }
                : undefined
            }
          />
        )}
      </div>

      <DetailDrawer
        open={detail !== null}
        title={detail?.name ?? '模型详情'}
        onClose={() => setDetail(null)}
        width={640}
      >
        {detail ? (
          <dl className="rdc-model-detail">
            <dt>模型 ID</dt>
            <dd>{detail.modelId}</dd>
            <dt>供应商 / 协议</dt>
            <dd>
              {aiProviderLabel(detail.provider)} · {aiProtocolLabel(detail.protocol)}
            </dd>
            <dt>API 地址</dt>
            <dd className="cell-break">{detail.baseUrl}</dd>
            <dt>Key 掩码</dt>
            <dd>
              <code className="rdc-model-mask">{detail.apiKeyMask || '未配置'}</code>
            </dd>
            <dt>思考 / 上下文</dt>
            <dd>
              {detail.thinkingEnabled ? '开启' : '关闭'} · {detail.contextTokens} tokens
            </dd>
            <dt>扩展配置</dt>
            <dd>
              <pre className="rdc-model-json">{JSON.stringify(detail.extraConfig, null, 2)}</pre>
            </dd>
            <dt>能力标签</dt>
            <dd>{detail.capabilities.join('、') || '—'}</dd>
            <dt>状态</dt>
            <dd>
              {detail.enabled ? '启用' : '停用'}
              {detail.isDefault ? ' · 默认' : ''}
            </dd>
          </dl>
        ) : null}
      </DetailDrawer>

      <ModelTestPanel
        modelID={testID}
        tenantId={superAdmin ? tenantId : undefined}
        backTo={backTo}
        onClose={() => setTestID(null)}
      />

      <ConfirmDialog
        open={pending !== null}
        title={
          pending?.kind === 'delete'
            ? '软删这个模型？'
            : pending?.kind === 'default'
              ? '切换默认模型？'
              : '调整模型状态？'
        }
        description={actionDescription}
        confirmText={pending?.kind === 'delete' ? '确认软删' : '确认'}
        danger={pending?.kind === 'delete' || (pending?.kind === 'status' && !pending.enabled)}
        loading={busy}
        onConfirm={() => void runPending()}
        onCancel={() => {
          setPending(null);
          setActionError('');
        }}
      />
    </section>
  );
}
