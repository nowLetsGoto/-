/**
 * Diff 右栏预览的纯函数。
 * 职责：从原始完整内容出发，只应用 accepted 的 hunk，逐段重建「应用后」完整文件文本。
 * 输入：base（原始完整内容，added 文件传 ''）、hunks（1-based 行号）、decisions（hunkId → 三态）。
 * 输出：ApplyResult（完整文本 + 已应用 id + 冲突 id）。
 * 边界：零 UI / 零网络 / 零文件 IO，仅 type import；不改真实文件、不渲染运行效果；
 *       contextBefore/After 是独立展示字段，从不参与计算；重叠 hunk 进 conflicts 并跳过。
 */
import type { components } from '../api/schema';

type Hunk = components['schemas']['Hunk'];
export type Decision = components['schemas']['ReviewDecision'];

export interface ApplyResult {
  /** 应用后的完整文件文本（按 \n join，天然保持末尾换行）。 */
  content: string;
  /** 实际应用的 hunk id，按原始行号升序。 */
  appliedHunkIds: string[];
  /** 重叠 / 非法的 hunk id：跳过而不是静默错位。 */
  conflicts: string[];
}

/**
 * applyHunks 逐段重建应用后的文件。
 *
 * 口径（与后端 contract / Group B 执行器一致）：
 * - oldStart / newStart 均 1-based；oldLines / newLines 是行数组长度；
 * - 按 oldStart 升序处理，start = oldStart - 1；
 * - cursor 指向 base 中尚未处理的下标；先拷贝 [cursor, start) 原文，再插入 newContent；
 * - 若 start < cursor（重叠 / 乱序）则记入 conflicts 并跳过，保证不产生错位结果；
 * - pending / rejected 不应用，缺省 decisions 的 hunk 视为 pending。
 */
export function applyHunks(
  base: string,
  hunks: Hunk[],
  decisions: Record<string, Decision> = {},
): ApplyResult {
  // 保留 split 产生的末尾空元素，join 时才能还原末尾换行。
  const baseLines: string[] = base.split('\n');
  const accepted = hunks
    .filter((hunk) => decisions[hunk.hunkId] === 'accepted')
    .sort((left, right) => left.oldStart - right.oldStart);

  const result: string[] = [];
  const appliedHunkIds: string[] = [];
  const conflicts: string[] = [];
  let cursor = 0;

  for (const hunk of accepted) {
    const start = hunk.oldStart - 1;
    // 上界也必须校验：start 超过 base 末行时 slice 会静默返回空数组，
    // 让越界 hunk 看起来"应用成功"并追加到文件末尾。added 的 base='' 有 1 个空行、
    // deleted 的 oldLines 等于 split 后的总行数，两者都在合法上界内。
    const invalidRange =
      !Number.isInteger(hunk.oldStart) ||
      !Number.isInteger(hunk.oldLines) ||
      hunk.oldStart < 1 ||
      hunk.oldLines < 0 ||
      start > baseLines.length ||
      start + hunk.oldLines > baseLines.length ||
      !Array.isArray(hunk.newContent);
    if (invalidRange || start < cursor) {
      conflicts.push(hunk.hunkId);
      continue;
    }
    result.push(...baseLines.slice(cursor, start));
    result.push(...hunk.newContent);
    cursor = start + hunk.oldLines;
    appliedHunkIds.push(hunk.hunkId);
  }

  result.push(...baseLines.slice(cursor));
  return { content: result.join('\n'), appliedHunkIds, conflicts };
}
