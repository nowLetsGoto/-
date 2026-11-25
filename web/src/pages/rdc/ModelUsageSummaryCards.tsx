/**
 * 调用统计的摘要卡区（AI模型 → 调用统计页的组成部分）。
 * 职责：把一次统计响应渲染成 8 张数字卡 + 「未上报 token」说明条。
 * 输入：AIModelCallStats（已由页面请求好）、includeLocal（决定是否展示「本地拒绝」卡）。
 * 输出：纯展示的卡片网格；不请求、不改 URL、不持有状态。
 * 边界：不做任何聚合与口径计算（所有数字来自后端 SQL 的 FILTER 结果）；
 *       可空值为 null 时渲染「—」而不是 0（与后端 *int64 语义一一对应）。
 */
import type { AIModelCallStats } from '../../api/rdcModel';
import { numberOrDash } from '../../utils/format';

interface ModelUsageSummaryCardsProps {
  stats: AIModelCallStats;
  /** true 时才展示「本地拒绝」卡：该值默认口径下恒为 0，展示出来只会误导。 */
  includeLocal: boolean;
}

/** 单张卡片；抽出局部组件避免 8 段重复的 span/strong 结构。 */
function SummaryCard({ label, value }: { label: string; value: string }): JSX.Element {
  return (
    <div className="rdc-model-summary-card">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

export function ModelUsageSummaryCards({
  stats,
  includeLocal,
}: ModelUsageSummaryCardsProps): JSX.Element {
  const averageLatency =
    stats.avgSuccessLatencyMs == null ? '—' : `${stats.avgSuccessLatencyMs} ms`;
  return (
    <>
      <div className="rdc-model-summary">
        <SummaryCard label="总调用" value={String(stats.totalCalls)} />
        <SummaryCard label="成功" value={String(stats.successCalls)} />
        <SummaryCard label="失败" value={String(stats.failedCalls)} />
        {includeLocal ? <SummaryCard label="本地拒绝" value={String(stats.localRejected)} /> : null}
        <SummaryCard label="prompt tokens" value={numberOrDash(stats.promptTokens)} />
        <SummaryCard label="completion tokens" value={numberOrDash(stats.completionTokens)} />
        <SummaryCard label="平均耗时" value={averageLatency} />
        <SummaryCard label="未上报 token 的调用" value={String(stats.tokensUnreported)} />
      </div>

      {stats.tokensUnreported > 0 ? (
        // 这句是「不知道 ≠ 零」的用户侧解释：不解释的话，用户会以为合计算错了。
        <p className="settings-hint">
          有 {stats.tokensUnreported} 次调用上游未返回 usage，这些调用的 token 记为「未知」而不是
          0， 不计入上方合计。
        </p>
      ) : null}
    </>
  );
}
