/**
 * 职责：回归测试抽屉背景锁、嵌套锁、触摸边界及内部可滚动容器。
 * 输入：模拟文档与单指/双指触摸事件；输出：Node 测试断言。
 * 边界：直接测试滚动锁管理器，不启动浏览器、不请求接口；Safari 实机手势另行人工检查。
 */
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { acquireDrawerScrollLock } from '../src/hooks/drawerScrollLock.ts';

class ElementFixture {
  constructor(parentElement = null, drawerBody = false) {
    Object.assign(this, {
      parentElement,
      drawerBody,
      scrollTop: 0,
      scrollLeft: 0,
      clientHeight: 100,
      scrollHeight: 100,
      clientWidth: 100,
      scrollWidth: 100,
      overflowX: 'visible',
      overflowY: 'visible',
    });
  }
  closest() {
    return this.drawerBody ? this : (this.parentElement?.closest() ?? null);
  }
  contains(element) {
    return element === this || (!!element?.parentElement && this.contains(element.parentElement));
  }
}

function fixture(t) {
  const originals = {
    Element: globalThis.Element,
    HTMLElement: globalThis.HTMLElement,
    getComputedStyle: globalThis.getComputedStyle,
  };
  Object.assign(globalThis, {
    Element: ElementFixture,
    HTMLElement: ElementFixture,
    getComputedStyle: (element) => element,
  });
  const styles = new Map([
    ['position', 'relative'],
    ['width', '80%'],
  ]);
  const listeners = new Map();
  const classes = new Set();
  const drawer = new ElementFixture();
  const body = new ElementFixture(drawer, true);
  body.overflowY = 'auto';
  body.scrollHeight = 500;
  const mask = new ElementFixture(drawer);
  const elements = new Map([
    ['.first', drawer],
    ['.second', new ElementFixture()],
  ]);
  const doc = {
    body: {
      style: {
        getPropertyValue: (name) => styles.get(name) ?? '',
        getPropertyPriority: () => '',
        setProperty: (name, value) => styles.set(name, value),
        removeProperty: (name) => styles.delete(name),
      },
    },
    documentElement: {
      clientWidth: 375,
      classList: {
        contains: (name) => classes.has(name),
        add: (name) => classes.add(name),
        remove: (name) => classes.delete(name),
      },
    },
    addEventListener: (name, listener, options) => listeners.set(name, { listener, options }),
    removeEventListener: (name) => listeners.delete(name),
    querySelector: (selector) => elements.get(selector),
  };
  const restored = [];
  const view = {
    scrollX: 0,
    scrollY: 280,
    innerWidth: 390,
    scrollTo: (options) => restored.push(options),
  };
  const unlocks = [];
  const lock = (name) => {
    const unlock = acquireDrawerScrollLock(name, doc, view);
    unlocks.push(unlock);
    return unlock;
  };
  t.after(() => {
    unlocks.forEach((unlock) => unlock());
    Object.assign(globalThis, originals);
  });
  const touch = (name, target, x, y, count = 1) => {
    let prevented = false;
    listeners.get(name)?.listener({
      target,
      cancelable: true,
      touches: Array.from({ length: count }, () => ({ clientX: x, clientY: y })),
      preventDefault: () => {
        prevented = true;
      },
    });
    return prevented;
  };
  return { styles, listeners, classes, body, mask, restored, lock, touch };
}

test('固定页面并在最后一个抽屉关闭后恢复滚动位置、原样式', (t) => {
  const f = fixture(t);
  const first = f.lock('first');
  assert.equal(f.styles.get('position'), 'fixed');
  assert.equal(f.styles.get('top'), '-280px');
  assert.equal(f.styles.get('width'), 'calc(100% - 15px)');
  assert.equal(f.listeners.get('touchmove').options.passive, false);
  const second = f.lock('second');
  first();
  first();
  assert.equal(f.styles.get('position'), 'fixed');
  assert.equal(f.restored.length, 0);
  second();
  assert.equal(f.styles.get('position'), 'relative');
  assert.equal(f.styles.get('width'), '80%');
  assert.equal(f.styles.has('top'), false);
  assert.equal(f.classes.size, 0);
  assert.equal(f.listeners.size, 0);
  assert.deepEqual(f.restored, [{ left: 0, top: 280, behavior: 'instant' }]);
});

test('遮罩不消费滚动，正文在中间可滚动，顶部和底部不能把手势传给背景', (t) => {
  const f = fixture(t);
  f.lock('first');
  f.touch('touchstart', f.mask, 50, 100);
  assert.equal(f.touch('touchmove', f.mask, 50, 80), true);
  f.touch('touchstart', f.body, 50, 100);
  assert.equal(f.touch('touchmove', f.body, 50, 120), true);
  f.body.scrollTop = 200;
  f.touch('touchstart', f.body, 50, 100);
  assert.equal(f.touch('touchmove', f.body, 50, 80), false);
  f.body.scrollTop = 400;
  f.touch('touchstart', f.body, 50, 100);
  assert.equal(f.touch('touchmove', f.body, 50, 80), true);
  // 到达底部后反向滑动仍可回滚，不会把整段手势锁死。
  assert.equal(f.touch('touchmove', f.body, 50, 100), false);
});

test('内层横向区域能滚动；双指缩放保留；下层抽屉不能消费上层手势', (t) => {
  const f = fixture(t);
  f.lock('first');
  const inner = new ElementFixture(f.body);
  inner.overflowX = 'auto';
  inner.scrollWidth = 500;
  f.touch('touchstart', inner, 100, 100);
  assert.equal(f.touch('touchmove', inner, 80, 100), false);
  assert.equal(f.touch('touchmove', inner, 60, 100, 2), false);
  f.lock('second');
  f.body.scrollTop = 200;
  f.touch('touchstart', f.body, 100, 100);
  assert.equal(f.touch('touchmove', f.body, 100, 80), true);
});
