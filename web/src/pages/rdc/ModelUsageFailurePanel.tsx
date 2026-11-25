/**
 * 调用统计的失败分布区（AI模型 → 调用统计页的组成部分）。
 * 职责：把失败类别计数渲染成一份可扫读的列表，空时给一句明确说明。
 * 输入：AICallCategoryCount 列表（已由页面请求好）。
 * 输出：纯展示的列表；不请求、不改 URL、不持有状态、不排序（顺序由后端给定）。
 * 边界：不做分类聚合与文案翻译（中文来自 format.ts 的 aiTestCategoryLabel 唯一入口）；
 *       空列表渲染的是"没有失败调用"这个事实，不是通用空态插图。
 */
import type { AIModelCallStats } from '../../api/rdcModel';
import { aiTestCategoryLabel } from '../../utils/format';

interface ModelUsageFailurePanelProps {
  /** 直接接收统计响应的失败分布字段：本组件只消费它，不关心统计的其它部分。 */
  failureCategories: AIModelCallStats['failureCategories'];
}

export function ModelUsageFailurePanel({
  failureCategories,
}: ModelUsageFailurePanelProps): JSX.Element {
  return (
    <>
      <h3 className="rdc-model-section-title">失败分布</h3>
      {failureCategories.length === 0 ? (
        <p className="settings-hint">所选区间内没有失败调用。</p>
      ) : (
        <ul className="rdc-model-failure-list">
          {failureCategories.map((item) => (
            <li key={item.category}>
              <span>{aiTestCategoryLabel(item.category)}</span>
              <span className="rdc-model-failure-count">{item.count} 次</span>
            </li>
          ))}
        </ul>
      )}
    </>
  );
}
