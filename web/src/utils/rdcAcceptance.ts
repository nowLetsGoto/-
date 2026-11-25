/**
 * 职责：验收标准展示层拆条，与 Go 服务端同构。
 * 输入：需求中的标准原文；输出：连续编号的条目。
 * 边界：纯函数；服务端始终用数据库现值重新拆分与校验。
 */
export interface AcceptanceCriterion {
  index: number;
  text: string;
}
const marker = /(?:^|\s)(?:\d+[.、)]|[-•])\s*/g;
export function splitAcceptanceCriteria(text: string): AcceptanceCriterion[] {
  const result: AcceptanceCriterion[] = [];
  for (const raw of text.replace(/\r\n?|\n/g, '\n').split('\n')) {
    const line = raw.trim();
    if (!line) continue;
    const positions = [...line.matchAll(marker)];
    if (positions.length === 0) {
      result.push({ index: result.length, text: line });
      continue;
    }
    const first = positions[0];
    if (first && first.index > 0) {
      const prefix = line.slice(0, first.index).trim();
      if (prefix) result.push({ index: result.length, text: prefix });
    }
    positions.forEach((match, index) => {
      const next = positions[index + 1];
      const item = line
        .slice((match.index ?? 0) + match[0].length, next?.index ?? line.length)
        .trim();
      if (item) result.push({ index: result.length, text: item });
    });
  }
  return result;
}
