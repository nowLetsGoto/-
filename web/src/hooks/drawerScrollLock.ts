/**
 * 职责：固定抽屉背后的页面，并拦截 Safari 在遮罩和滚动边界上的触摸回弹。
 * 输入：抽屉根节点类名与浏览器文档；输出：可重复调用的解锁函数。
 * 边界：仅管理页面滚动和触摸事件；按栈支持多个抽屉，最后一个关闭后恢复原位置及内联样式。
 */
const locks: symbol[] = [];
const roots = new Map<symbol, string>();
let restorePage: (() => void) | undefined;

function canScroll(element: HTMLElement, distance: number, horizontal: boolean): boolean {
  const style = getComputedStyle(element);
  if (!/(auto|scroll|overlay)/.test(horizontal ? style.overflowX : style.overflowY)) return false;
  const position = horizontal ? element.scrollLeft : element.scrollTop;
  const extent = horizontal
    ? element.scrollWidth - element.clientWidth
    : element.scrollHeight - element.clientHeight;
  return extent > 1 && (distance > 0 ? position > 0 : position < extent - 1);
}

export function acquireDrawerScrollLock(
  rootClassName: string,
  doc: Document = document,
  view: Window = window,
): () => void {
  const token = Symbol('drawer-scroll');
  roots.set(token, rootClassName);
  locks.push(token);

  if (locks.length === 1) {
    const body = doc.body;
    const root = doc.documentElement;
    const scrollX = view.scrollX;
    const scrollY = view.scrollY;
    const scrollbarWidth = Math.max(0, view.innerWidth - root.clientWidth);
    const properties = ['position', 'top', 'left', 'width'] as const;
    const previous = properties.map((name) => ({
      name,
      value: body.style.getPropertyValue(name),
      priority: body.style.getPropertyPriority(name),
    }));
    const alreadyLocked = root.classList.contains('page-scroll-locked');
    root.classList.add('page-scroll-locked');
    body.style.setProperty('position', 'fixed');
    body.style.setProperty('top', `${-scrollY}px`);
    body.style.setProperty('left', `${-scrollX}px`);
    body.style.setProperty('width', `calc(100% - ${scrollbarWidth}px)`);

    let lastX = 0;
    let lastY = 0;
    let scrollBody: HTMLElement | null = null;
    const onTouchStart = (event: TouchEvent): void => {
      const touch = event.touches[0];
      if (!touch) return;
      lastX = touch.clientX;
      lastY = touch.clientY;
      const target = event.target instanceof Element ? event.target : null;
      const topClassName = roots.get(locks[locks.length - 1]!);
      const drawer = topClassName ? doc.querySelector(`.${topClassName}`) : null;
      const candidate = target?.closest<HTMLElement>('.ant-drawer-body') ?? null;
      scrollBody = candidate && drawer?.contains(candidate) ? candidate : null;
    };
    const onTouchMove = (event: TouchEvent): void => {
      // 保留双指缩放；只有单指滚动需要做边界判断。
      if (event.touches.length !== 1) return;
      const touch = event.touches[0]!;
      const deltaX = touch.clientX - lastX;
      const deltaY = touch.clientY - lastY;
      lastX = touch.clientX;
      lastY = touch.clientY;
      const horizontal = Math.abs(deltaX) > Math.abs(deltaY);
      const distance = horizontal ? deltaX : deltaY;
      if (!distance) return;
      const target = event.target instanceof Element ? event.target : null;
      let element = target instanceof HTMLElement ? target : target?.parentElement;
      // 文本框、横向列表等内层容器可先消费手势；到抽屉正文边界仍不可滚动才拦截。
      while (element && scrollBody?.contains(element)) {
        if (canScroll(element, distance, horizontal)) return;
        if (element === scrollBody) break;
        element = element.parentElement;
      }
      if (event.cancelable) event.preventDefault();
    };
    doc.addEventListener('touchstart', onTouchStart, { passive: true, capture: true });
    doc.addEventListener('touchmove', onTouchMove, { passive: false, capture: true });

    restorePage = () => {
      doc.removeEventListener('touchstart', onTouchStart, true);
      doc.removeEventListener('touchmove', onTouchMove, true);
      for (const { name, value, priority } of previous) {
        if (value) body.style.setProperty(name, value, priority);
        else body.style.removeProperty(name);
      }
      if (!alreadyLocked) root.classList.remove('page-scroll-locked');
      view.scrollTo({ left: scrollX, top: scrollY, behavior: 'instant' });
    };
  }

  let released = false;
  return () => {
    if (released) return;
    released = true;
    roots.delete(token);
    locks.splice(locks.indexOf(token), 1);
    if (locks.length === 0) {
      restorePage?.();
      restorePage = undefined;
    }
  };
}
