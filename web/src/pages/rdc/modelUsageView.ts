/**
 * 调用统计页的纯常量与小工具（非组件模块，与页面同级）。
 * 职责：分组维度白名单与 URL 读取、时间范围快捷项与半开区间换算。
 * 输入：URLSearchParams、天数。
 * 输出：分组维度数组、时间区间的 ISO 字符串对。
 * 边界：**不含任何 JSX 与 React 导入**（保持 .ts 后缀，避免 react-refresh 的混合导出告警）；
 *       不发请求、不做统计口径计算（成功/失败/平均耗时/token 过滤全由后端 SQL 的 FILTER 决定）。
 */

/** 分组维度白名单；顺序与后端 AIUsageGroupBy 一致（前端只做展示顺序，不参与统计）。 */
export const GROUP_OPTIONS = ['model', 'protocol', 'scene', 'day'] as const;
export type GroupDimension = (typeof GROUP_OPTIONS)[number];

/** 时间范围快捷项：默认最近 7 天由后端补，这里只提供"改区间"的便捷入口。 */
export const RANGE_OPTIONS = [
  { value: '7', label: '最近 7 天' },
  { value: '14', label: '最近 14 天' },
  { value: '30', label: '最近 30 天' },
] as const;

/** 把 days 换算成半开区间 [start, end)：end 取当前整点，与后端缺省口径一致。 */
export function rangeFromDays(days: number): { start: string; end: string } {
  const end = new Date();
  end.setMinutes(0, 0, 0);
  const start = new Date(end.getTime() - days * 24 * 60 * 60 * 1000);
  return { start: start.toISOString(), end: end.toISOString() };
}

/** 从 URL 读取分组维度，非法值与空值一律回落 ['model']，保证表格至少有一列维度。 */
export function readGroupBy(params: URLSearchParams): GroupDimension[] {
  const picked = (params.get('groupBy') ?? 'model')
    .split(',')
    .filter((item): item is GroupDimension => (GROUP_OPTIONS as readonly string[]).includes(item));
  return picked.length > 0 ? picked : ['model'];
}
