/**
 * AI模型调用统计页（研发中心 → AI模型 → 调用统计）。
 * 职责：页面级编排——读取 URL 筛选状态、请求统计与阈值、组织布局与三态、
 *       串起三个展示子组件（摘要卡 / 失败分布 / 阈值提示条）与阈值弹窗。
 * 输入：URL 的 range / groupBy / includeLocal / tenantId；统计与阈值 API；当前登录人权限。
 * 输出：页头 + 超阈值提示条 + 筛选工具条 + 摘要卡 + 失败分布 + 分组表格。
 * 边界：不做业务口径计算（成功/失败、平均耗时、token 过滤全由后端 SQL 的 FILTER 决定）；
 *       不渲染摘要卡/失败分布/表格列的具体结构（分别拆到 ModelUsageSummaryCards /
 *       ModelUsageFailurePanel / modelUsageView）；写阈值按钮按 rdc.model.write 隐藏。
 */
import { useEffect, useMemo, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { getAIModelQuota, getAIModelUsage } from '../../api/rdcModel';
import type { AIModelCallStats, AIQuotaConfig } from '../../api/rdcModel';
import { errorMessage } from '../../api/client';
import { listTenants } from '../../api/tenants';
import type { Tenant } from '../../api/tenants';
import { Button, Checkbox, Segmented } from '../../components/Controls';
import { Select } from '../../components/Select';
import { Empty, ErrorState, Loading } from '../../components/States';
import { useMe } from '../../hooks/useMe';
import { AI_USAGE_GROUP_LABELS } from '../../utils/format';
import { can } from '../../utils/permission';
import { ModelQuotaModal } from './ModelQuotaModal';
import { ModelUsageFailurePanel } from './ModelUsageFailurePanel';
import { ModelUsageGroupTable } from './ModelUsageGroupTable';
import { ModelUsageSummaryCards } from './ModelUsageSummaryCards';
import { GROUP_OPTIONS, RANGE_OPTIONS, rangeFromDays, readGroupBy } from './modelUsageView';
import type { GroupDimension } from './modelUsageView';

const LIST_PATH = '/rdc/models';

interface UsageState {
  loading: boolean;
  stats: AIModelCallStats | null;
  error: string;
}

const INITIAL_STATE: UsageState = { loading: true, stats: null, error: '' };

export function ModelUsagePage(): JSX.Element {
  const [params, setParams] = useSearchParams();
  const tenantId = (params.get('tenantId') ?? '').trim();
  const rangeDays = params.get('range') ?? '7';
  const groupBy = useMemo(() => readGroupBy(params), [params]);
  const includeLocal = params.get('includeLocal') === 'true';

  const { me } = useMe();
  const superAdmin = me?.isSuperAdmin === true;
  const writable = can(me, 'rdc.model.write');

  const [state, setState] = useState<UsageState>(INITIAL_STATE);
  const [quota, setQuota] = useState<AIQuotaConfig | null>(null);
  const [seconds, setSeconds] = useState(0);
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [quotaOpen, setQuotaOpen] = useState(false);

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
      setState({ loading: false, stats: null, error: '' });
      return;
    }
    let cancelled = false;
    setState(INITIAL_STATE);
    const range = rangeFromDays(Number(rangeDays) || 7);
    getAIModelUsage({
      start: range.start,
      end: range.end,
      groupBy,
      includeLocal,
      tenantId: superAdmin ? tenantId : undefined,
    })
      .then((stats) => {
        if (!cancelled) {
          setState({ loading: false, stats, error: '' });
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setState({ loading: false, stats: null, error: errorMessage(err) });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [rangeDays, groupBy, includeLocal, tenantId, superAdmin, needsTenant, seconds]);

  // 阈值单独读一次：它同时决定提示条是否出现，与统计区间无关。
  useEffect(() => {
    if (needsTenant) {
      setQuota(null);
      return;
    }
    let cancelled = false;
    getAIModelQuota(superAdmin ? tenantId : undefined)
      .then((cfg) => {
        if (!cancelled) {
          setQuota(cfg);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setQuota(null);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [tenantId, superAdmin, needsTenant, seconds]);

  /** 改 URL 上的筛选条件；一律 replace，避免后退键在筛选之间来回跳。 */
  const changeFilter = (key: string, value: string): void => {
    const updated = new URLSearchParams(params);
    if (value) {
      updated.set(key, value);
    } else {
      updated.delete(key);
    }
    setParams(updated, { replace: true });
  };

  const toggleGroup = (dimension: GroupDimension, checked: boolean): void => {
    const next = checked ? [...groupBy, dimension] : groupBy.filter((item) => item !== dimension);
    // 至少保留一个维度：空分组等于"不分组的汇总"，那是摘要卡已经给出的信息。
    changeFilter('groupBy', (next.length > 0 ? next : ['model']).join(','));
  };

  const stats = state.stats;
  const exceeded = stats?.quotaState === 'exceeded';
  const backTo = tenantId ? `${LIST_PATH}?tenantId=${encodeURIComponent(tenantId)}` : LIST_PATH;
  const emptyState = (
    <Empty
      title={needsTenant ? '请先选择目标公司' : '所选区间内还没有调用记录'}
      description={
        needsTenant
          ? '统计是公司私有数据，超管代查需要先选定公司，不会跨公司汇总。'
          : '统计只包含真实发生的调用；调整时间范围或先触发一次调用后再回来看。'
      }
    />
  );

  if (state.loading) {
    return <Loading text="正在汇总调用统计…" />;
  }

  return (
    <section className="page rdc-model-usage-page">
      <div className="page-head">
        <div>
          <h1>调用统计</h1>
          <p className="sub">
            口径：成功 / 失败按调用结果；平均耗时只算「成功且已出站」；token 只对上报了 usage
            的调用求和
          </p>
        </div>
        <div className="rdc-model-head-actions">
          <Link className="button" to={backTo}>
            返回模型列表
          </Link>
          {writable && !needsTenant ? (
            <Button
              type="button"
              className="button button-primary"
              onClick={() => setQuotaOpen(true)}
            >
              设置调用上限
            </Button>
          ) : null}
        </div>
      </div>

      {exceeded ? (
        <QuotaBanner stats={stats} writable={writable} onAdjust={() => setQuotaOpen(true)} />
      ) : null}

      <div className="card">
        <div className="rdc-model-toolbar">
          {superAdmin ? (
            <label className="rdc-model-tenant">
              <span>目标公司</span>
              <Select
                value={tenantId}
                searchable
                placeholder="请选择要查看的公司"
                options={[
                  { value: '', label: '请选择目标公司' },
                  ...tenants.map((tenant) => ({ value: tenant.id, label: tenant.name })),
                ]}
                onChange={(value) => changeFilter('tenantId', value)}
              />
            </label>
          ) : null}
          <Segmented
            aria-label="时间范围"
            value={rangeDays}
            options={RANGE_OPTIONS.map((item) => ({ value: item.value, label: item.label }))}
            onChange={(value) => changeFilter('range', value)}
          />
          <label className="field-check rdc-model-inline-check">
            <Checkbox
              checked={includeLocal}
              onChange={(event) => changeFilter('includeLocal', event.target.checked ? 'true' : '')}
            >
              含本地拒绝（未出站）
            </Checkbox>
          </label>
        </div>

        <div className="rdc-model-toolbar">
          <span className="settings-hint">分组维度（可多选，至少一个）：</span>
          {GROUP_OPTIONS.map((dimension) => (
            <label className="field-check rdc-model-inline-check" key={dimension}>
              <Checkbox
                checked={groupBy.includes(dimension)}
                onChange={(event) => toggleGroup(dimension, event.target.checked)}
              >
                {AI_USAGE_GROUP_LABELS[dimension]}
              </Checkbox>
            </label>
          ))}
        </div>

        {state.error ? (
          <ErrorState message={state.error} onRetry={() => setSeconds((value) => value + 1)} />
        ) : needsTenant ? (
          emptyState
        ) : stats ? (
          <>
            <ModelUsageSummaryCards stats={stats} includeLocal={includeLocal} />
            <ModelUsageFailurePanel failureCategories={stats.failureCategories} />
            <h3 className="rdc-model-section-title">分组明细</h3>
            <ModelUsageGroupTable groups={stats.groups} groupBy={groupBy} />
          </>
        ) : (
          emptyState
        )}
      </div>

      {quota ? (
        <ModelQuotaModal
          open={quotaOpen}
          quota={quota}
          tenantId={superAdmin ? tenantId : undefined}
          onClose={() => setQuotaOpen(false)}
          onSaved={(saved) => {
            setQuota(saved);
            setQuotaOpen(false);
            setSeconds((value) => value + 1);
          }}
        />
      ) : null}
    </section>
  );
}

/** 超阈值提示条：琥珀色表示"提醒"而非"故障"——warn 模式下调用其实照常成功。 */
function QuotaBanner({
  stats,
  writable,
  onAdjust,
}: {
  stats: AIModelCallStats | null;
  writable: boolean;
  onAdjust: () => void;
}): JSX.Element {
  const quota = stats?.quota;
  const rejecting = quota?.action === 'reject';
  return (
    <div className="rdc-quota-banner" role="status">
      <strong>已触发调用上限</strong>
      <span>
        最近 {quota?.windowMinutes} 分钟的已出站调用达到 {quota?.maxCalls} 次，当前动作是「
        {rejecting ? '拒绝调用' : '仅告警'}」。
        {rejecting ? '新的调用会被直接拒绝，不会请求上游。' : '调用仍会正常发出，此处仅作提醒。'}
      </span>
      {writable ? (
        <Button type="button" className="button button-link" onClick={onAdjust}>
          调整上限
        </Button>
      ) : null}
    </div>
  );
}
