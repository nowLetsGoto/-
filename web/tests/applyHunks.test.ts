/**
 * applyHunks 纯函数单测（Node 内置 node:test，零依赖）。
 * 职责：钉死 1-based 行号、累计偏移不漂移、增删文件语义、三态过滤、重叠冲突与末尾换行。
 * 输入：手写的最小 Hunk / decisions 与 base 文本；不依赖后端、不依赖浏览器。
 * 输出：断言 content / appliedHunkIds / conflicts。
 * 边界：测试文件放在 web/tests，不在 tsconfig.include 内，不影响 npm run build。
 */
import assert from 'node:assert/strict';
import test from 'node:test';
import type { components } from '../src/api/schema';
import { applyHunks } from '../src/utils/applyHunks.ts';

type Hunk = components['schemas']['Hunk'];
type Decision = 'pending' | 'accepted' | 'rejected';

/** 构造测试 hunk：只覆盖计算用到的字段，其余给合法默认值。 */
function makeHunk(partial: Partial<Hunk> & Pick<Hunk, 'hunkId' | 'oldStart' | 'oldLines'>): Hunk {
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

test('单 hunk 接受：替换原文指定行段', () => {
  const base = 'a\nb\nc';
  const hunks = [makeHunk({ hunkId: 'h1', oldStart: 2, oldLines: 1, newContent: ['B'] })];
  const result = applyHunks(base, hunks, { h1: 'accepted' });
  assert.equal(result.content, 'a\nB\nc');
  assert.deepEqual(result.appliedHunkIds, ['h1']);
  assert.deepEqual(result.conflicts, []);
});

test('多 hunk 按原始行号升序 + 累计偏移，不因前一个 hunk 增删而漂移', () => {
  const base = 'l1\nl2\nl3\nl4\nl5';
  const hunks = [
    makeHunk({ hunkId: 'h2', oldStart: 4, oldLines: 1, newContent: ['L4'] }),
    makeHunk({ hunkId: 'h1', oldStart: 2, oldLines: 1, newContent: ['L2a', 'L2b'] }),
  ];
  const result = applyHunks(base, hunks, { h1: 'accepted', h2: 'accepted' });
  // h1 把 1 行变 2 行，但 h2 仍按原始第 4 行定位（cursor 由原始 oldStart 推进）。
  assert.equal(result.content, 'l1\nL2a\nL2b\nl3\nL4\nl5');
  assert.deepEqual(result.appliedHunkIds, ['h1', 'h2']);
});

test('pending / rejected / 缺省 decisions 都不应用', () => {
  const base = 'a\nb';
  const hunks = [makeHunk({ hunkId: 'h1', oldStart: 1, oldLines: 1, newContent: ['X'] })];
  assert.equal(applyHunks(base, hunks, { h1: 'pending' }).content, base);
  assert.equal(applyHunks(base, hunks, { h1: 'rejected' }).content, base);
  assert.equal(applyHunks(base, hunks, {}).content, base);
  assert.equal(applyHunks(base, hunks).content, base);
});

test('added 文件：base 为空串，接受插入 hunk 后显示新增行', () => {
  const hunks = [makeHunk({ hunkId: 'add', oldStart: 1, oldLines: 0, newContent: ['line1', 'line2'] })];
  const result = applyHunks('', hunks, { add: 'accepted' });
  assert.equal(result.content, 'line1\nline2\n');
  assert.deepEqual(result.appliedHunkIds, ['add']);
});

test('deleted 文件：接受删除全部行后返回空串', () => {
  const base = 'a\nb\n';
  const hunks = [makeHunk({ hunkId: 'del', oldStart: 1, oldLines: base.split('\n').length, newContent: [] })];
  const result = applyHunks(base, hunks, { del: 'accepted' });
  assert.equal(result.content, '');
  assert.deepEqual(result.appliedHunkIds, ['del']);
});

test('重叠 hunk 进 conflicts 且跳过，不错位', () => {
  const base = 'a\nb\nc\nd';
  const hunks = [
    makeHunk({ hunkId: 'first', oldStart: 1, oldLines: 2, newContent: ['A', 'B'] }),
    makeHunk({ hunkId: 'overlap', oldStart: 2, oldLines: 1, newContent: ['OVERLAP'] }),
  ];
  const decisions: Record<string, Decision> = { first: 'accepted', overlap: 'accepted' };
  const result = applyHunks(base, hunks, decisions);
  assert.equal(result.content, 'A\nB\nc\nd');
  assert.deepEqual(result.appliedHunkIds, ['first']);
  assert.deepEqual(result.conflicts, ['overlap']);
});

test('末尾换行往返：base 以 \\n 结尾且不应用任何 hunk 时保持原样', () => {
  const base = 'a\nb\n';
  assert.equal(applyHunks(base, [], {}).content, base);
  const hunks = [makeHunk({ hunkId: 'h', oldStart: 2, oldLines: 1, newContent: ['B'] })];
  assert.equal(applyHunks(base, hunks, { h: 'accepted' }).content, 'a\nB\n');
});

test('上下文字段从不参与计算（仅中栏展示）', () => {
  const base = 'a\nb\nc';
  const hunks = [makeHunk({
    hunkId: 'h', oldStart: 2, oldLines: 1, newContent: ['B'],
    contextBefore: ['CONTEXT-BEFORE'], contextAfter: ['CONTEXT-AFTER'],
  })];
  assert.equal(applyHunks(base, hunks, { h: 'accepted' }).content, 'a\nB\nc');
});
