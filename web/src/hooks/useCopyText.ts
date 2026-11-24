/**
 * 职责：统一复制文本，兼容局域网 HTTP、Safari 及剪贴板权限拒绝的场景。
 * 输入：点击事件中传入的文本；输出：复制是否成功。
 * 边界：不展示提示、不生成链接；临时选区在同步手势中完成，复制后恢复焦点与原选区。
 */
import { useCallback } from 'react';

function copyWithSelection(text: string): boolean {
  const focused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const selection = window.getSelection();
  const ranges = selection
    ? Array.from({ length: selection.rangeCount }, (_, index) => selection.getRangeAt(index))
    : [];
  const field = document.createElement('textarea');
  field.value = text;
  field.readOnly = true;
  field.tabIndex = -1;
  field.setAttribute('aria-label', '复制地址');
  field.style.cssText = 'position:fixed;top:0;left:0;opacity:0;font-size:16px;pointer-events:none';
  // 放在当前弹层内，避免焦点陷阱把选区拉回按钮；readonly 避免唤起 iOS 键盘。
  const host = focused?.closest('.ant-drawer-content,.ant-modal-content') ?? document.body;
  host.appendChild(field);
  try {
    field.focus({ preventScroll: true });
    field.select();
    field.setSelectionRange(0, text.length);
    return document.execCommand('copy');
  } catch {
    return false;
  } finally {
    field.remove();
    focused?.focus({ preventScroll: true });
    if (selection) {
      selection.removeAllRanges();
      for (const range of ranges) selection.addRange(range);
    }
  }
}

export function useCopyText() {
  return useCallback(async (text: string): Promise<boolean> => {
    // HTTP 局域网下直接同步走兼容方式，保留 Safari 要求的用户点击手势。
    if (!window.isSecureContext || !navigator.clipboard?.writeText) return copyWithSelection(text);
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      return copyWithSelection(text);
    }
  }, []);
}
