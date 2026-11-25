/**
 * 调用统计的分组明细表格（AI模型 → 调用统计页的组成部分）。
 * 职责：按已选分组维度拼出表格列并渲染 DataTable；
 *       维度列随 groupBy 增减，度量列固定（口径由后端 SQL 的 FILTER 决定）。
 * 输入：stats.groups（分组明细）、groupBy（已选维度）。
 * 输出：带空态的表格；不请求、不改 URL、不持有状态。
 * 边界：不做聚合与口径计算；可空值（token / 平均耗时）为 null 时渲染「—」而不是 0；
 *       维度顺序固定 model → protocol → scene → day，与后端 aiGroupColumns 同序。
 */
import type { TableColumnsType } from '@seakoi/console-kit';
import type { AIModelCallGroup } from '../../api/rdcModel';
import { DataTable } from '../../components/DataTable';
import { Empty } from '../../components/States';
import { aiProtocolLabel, numberOrDash } from '../../utils/format';
import type { GroupDimension } from './modelUsageView';

/** 与分组维度无关的度量列；空值一律显示破折号（对应后端 *int64 的"没有数据"语义）。 */
function metricColumns(): TableColumnsType<AIModelCallGroup> {
  return [
    { title: '总次数', dataIndex: 'totalCalls', key: 'totalCalls', width: 100 },
    { title: '成功', dataIndex: 'successCalls', key: 'successCalls', width: 90 },
    {
      title: '失败',
      key: 'failedCalls',
      width: 90,
      render: (_, row) =>
        row.failedCalls > 0 ? <span className="rdc-model-failed">{row.failedCalls}</span> : 0,
    },
    {
      title: 'prompt tokens',
      key: 'promptTokens',
      width: 130,
      render: (_, row) => numberOrDash(row.promptTokens),
    },
    {
      title: 'completion tokens',
      key: 'completionTokens',
      width: 150,
      render: (_, row) => numberOrDash(row.completionTokens),
    },
    {
      title: '平均耗时',
      key: 'avgSuccessLatencyMs',
      width: 120,
      render: (_, row) => (row.avgSuccessLatencyMs == null ? '—' : `${row.avgSuccessLatencyMs} ms`),
    },
  ];
}

/** 按已选维度拼出维度列；未选中的维度不出现（不填占位空列）。 */
function dimensionColumns(groupBy: GroupDimension[]): TableColumnsType<AIModelCallGroup> {
  const columns: TableColumnsType<AIModelCallGroup> = [];
  if (groupBy.includes('model')) {
    columns.push({
      title: '模型',
      key: 'modelName',
      // model_id 为 NULL 的记录是"本地拒绝"，没有具体模型，用可读文案代替空白。
      render: (_, row) => row.modelName || '（无匹配模型）',
    });
  }
  if (groupBy.includes('protocol')) {
    columns.push({
      title: '协议',
      key: 'protocol',
      width: 190,
      render: (_, row) => (row.protocol ? aiProtocolLabel(row.protocol) : '—'),
    });
  }
  if (groupBy.includes('scene')) {
    columns.push({
      title: '场景',
      key: 'scene',
      width: 130,
      render: (_, row) => row.scene || '—',
    });
  }
  if (groupBy.includes('day')) {
    columns.push({ title: '日期（UTC）', dataIndex: 'day', key: 'day', width: 130 });
  }
  return columns;
}

interface ModelUsageGroupTableProps {
  groups: AIModelCallGroup[];
  groupBy: GroupDimension[];
}

export function ModelUsageGroupTable({ groups, groupBy }: ModelUsageGroupTableProps): JSX.Element {
  return (
    <DataTable<AIModelCallGroup>
      columns={[...dimensionColumns(groupBy), ...metricColumns()]}
      dataSource={groups}
      // 复合键：同一模型在不同协议/场景/日期下是不同行，单用 modelId 会撞 key。
      rowKey={(row) => `${row.modelId}|${row.protocol}|${row.scene}|${row.day}|${row.modelName}`}
      empty={<Empty title="所选区间内没有调用记录" description="换个时间范围或先触发一次调用。" />}
    />
  );
}
