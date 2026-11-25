/**
 * applyHunks QA 独立边界测试（node:test，零依赖）。
 * 职责：在实现者单测之外补测重叠等距、1-based 累计偏移（含删除后插入）、
 *       added base 空的多 hunk、deleted 接受后的末尾换行、非法行号与零副作用。
 * 输入：手写 Hunk 与 base；不读真实文件、不发请求。
 * 输出：断言 content / appliedHunkIds / conflicts；静态断言源码无 IO / 网络调用。
 * 边界：只测纯函数；不渲染 UI、不改工作区文件。
 */
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import type { components } from '../src/api/schema';
import { applyHunks } from '../src/utils/applyHunks.ts';

type Hunk = components['schemas']['Hunk'];
type Decision = 'pending' | 'accepted' | 'rejected';

function hunk(partial: Partial<Hunk> & Pick<Hunk, 'hunkId' | 'oldStart' | 'oldLines'>): Hunk {
  return {
    newStart: partial.oldStart,
    newLines: partial.newContent?.length ?? 0,
    oldContent: [],
    newContent: [],
    contextBefore: [],
    contextAfter: [],
    riskLabel: 'code_edit',
    ...partial,
  };
}

test('QA: 删除在前、插入在后，累计偏移不错位（1-based）', () => {
  const base = 'l1\nl2\nl3\nl4\nl5';
  const hunks = [
    hunk({ hunkId: 'del', oldStart: 2, oldLines: 2, newContent: [] }),
    hunk({ hunkId: 'ins', oldStart: 5, oldLines: 0, newContent: ['NEW'] }),
    hunk({ hunkId: 'rep', oldStart: 1, oldLines: 1, newContent: ['FIRST'] }),
  ];
  const decisions: Record<string, Decision> = { del: 'accepted', ins: 'accepted', rep: 'accepted' };
  const result = applyHunks(base, hunks, decisions);
  assert.equal(result.content, 'FIRST\nl4\nNEW\nl5');
  assert.deepEqual(result.appliedHunkIds, ['rep', 'del', 'ins']);
  assert.deepEqual(result.conflicts, []);
});

test('QA: added 文件 base 为空且两个插入 hunk，同一起始行按输入顺序稳定应用', () => {
  const hunks = [
    hunk({ hunkId: 'second', oldStart: 1, oldLines: 0, newContent: ['b', 'c'] }),
    hunk({ hunkId: 'first', oldStart: 1, oldLines: 0, newContent: ['a'] }),
  ];
  const result = applyHunks('', hunks, { first: 'accepted', second: 'accepted' });
  // oldStart 相同，稳定排序保留输入先后；两个 hunk 都被应用，不丢内容。
  assert.equal(result.content, 'b\nc\na\n');
  assert.deepEqual(result.appliedHunkIds, ['second', 'first']);
});

test('QA: deleted 文件接受删除且 base 末尾有换行，返回空串', () => {
  const base = 'a\nb\n';
  const hunks = [hunk({ hunkId: 'del', oldStart: 1, oldLines: 3, newContent: [] })];
  const result = applyHunks(base, hunks, { del: 'accepted' });
  assert.equal(result.content, '');
  assert.deepEqual(result.appliedHunkIds, ['del']);
});

test('QA: 相邻 hunk 不判冲突；完全重叠的第二个跳过且结果稳定', () => {
  const base = 'a\nb\nc\nd';
  const adjacent = [
    hunk({ hunkId: 'a1', oldStart: 1, oldLines: 1, newContent: ['A'] }),
    hunk({ hunkId: 'a2', oldStart: 2, oldLines: 1, newContent: ['B'] }),
  ];
  assert.equal(applyHunks(base, adjacent, { a1: 'accepted', a2: 'accepted' }).content, 'A\nB\nc\nd');

  const overlapped = [
    hunk({ hunkId: 'a1', oldStart: 1, oldLines: 2, newContent: ['A', 'B'] }),
    hunk({ hunkId: 'a2', oldStart: 2, oldLines: 1, newContent: ['X'] }),
  ];
  const result = applyHunks(base, overlapped, { a1: 'accepted', a2: 'accepted' });
  assert.equal(result.content, 'A\nB\nc\nd');
  assert.deepEqual(result.conflicts, ['a2']);
});

test('QA: 超出 base 行数的 hunk 应判为非法并跳过，不得静默追加到末尾', () => {
  const base = 'a\nb\nc';
  const hunks = [hunk({ hunkId: 'oob', oldStart: 10, oldLines: 1, newContent: ['X'] })];
  const result = applyHunks(base, hunks, { oob: 'accepted' });
  assert.equal(result.content, base);
  assert.deepEqual(result.appliedHunkIds, []);
  assert.deepEqual(result.conflicts, ['oob']);
});

test('QA: decisions 中的未知 hunk 不改变结果；传入的 hunks 数组不被排序副作用改写', () => {
  const base = 'a\nb\nc';
  const hunks = [
    hunk({ hunkId: 'z', oldStart: 3, oldLines: 1, newContent: ['C'] }),
    hunk({ hunkId: 'a', oldStart: 1, oldLines: 1, newContent: ['A'] }),
  ];
  const before = hunks.map((item) => item.hunkId);
  const result = applyHunks(base, hunks, { a: 'accepted', z: 'accepted', ghost: 'accepted' });
  assert.equal(result.content, 'A\nb\nC');
  assert.deepEqual(hunks.map((item) => item.hunkId), before);
  assert.deepEqual(result.appliedHunkIds, ['a', 'z']);
});

test('QA: 实现是纯函数，源码无文件 IO / 网络 / DOM 副作用', () => {
  const src = readFileSync(new URL('../src/utils/applyHunks.ts', import.meta.url), 'utf8');
  for (const forbidden of ['fetch(', 'XMLHttpRequest', 'writeFile', 'localStorage', 'document.', 'window.']) {
    assert.equal(src.includes(forbidden), false, `applyHunks 不应出现 ${forbidden}`);
  }
  assert.equal(/from\s+['"]\.\/schema['"]/.test(src) || src.includes("import type"), true);
});
