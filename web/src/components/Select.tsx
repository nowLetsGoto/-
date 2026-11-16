/**
 * 单选下拉（Ant Design Select 的唯一封装）。
 * 职责：统一下拉的触发态、选项面板、键盘操作、禁用态与主题外观。
 * 输入：当前值、选项列表与变更回调；输出：单选下拉。
 * 边界：**业务页面不得直接 import antd 的 Select**——antd 只允许出现在本文件、
 *       DataTable、DetailDrawer、ConfirmDialog 与 theme/antd.ts；
 *       本组件不取数、不持有业务状态、不拼接口 URL。
 */
import { Select as AntSelect } from '@seakoi/console-kit';
import { useEffect, useState } from 'react';

export interface SelectOption {
  value: string;
  label: string;
}

interface SelectProps {
  value: string;
  options: ReadonlyArray<SelectOption>;
  onChange: (value: string) => void;
  /** 空值时的提示；表单里传 "" 作为未分配选项时不要给 placeholder */
  placeholder?: string;
  disabled?: boolean;
  /** 项目筛选这类紧凑场景给数值，表单里用 "100%" */
  width?: number | string;
  /** 选项多时才开启搜索；内置项目/角色列表默认关闭，避免多余输入框 */
  searchable?: boolean;
  className?: string;
}

/** 与 ant design 默认箭头区分：更细的线性 chevron，继承当前文字色。 */
function ChevronDown(): JSX.Element {
  return (
    <svg width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden="true">
      <path d="m2.5 4.5 3.5 3 3.5-3" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function Select({
  value,
  options,
  onChange,
  placeholder,
  disabled = false,
  width = '100%',
  searchable = false,
  className = '',
}: SelectProps): JSX.Element {
  // 显式管理弹层：Escape 由 Ant Design 自己处理之外，也允许外层状态统一收敛。
  const [open, setOpen] = useState(false);

  // 面板展开时监听文档级 Escape：焦点在触发器、搜索框或弹出层内都能统一关闭。
  useEffect(() => {
    if (!open) {
      return;
    }
    const close = (event: KeyboardEvent): void => {
      if (event.key === 'Escape') {
        setOpen(false);
      }
    };
    document.addEventListener('keydown', close);
    return () => document.removeEventListener('keydown', close);
  }, [open]);

  return (
    // 泛型顺序是「值类型, 选项类型」；值是字符串，选项才带 label。
    <AntSelect<string, SelectOption>
      className={`app-select ${className}`.trim()}
      classNames={{ popup: { root: 'app-select-popup' } }}
      open={open}
      onOpenChange={setOpen}
      variant="outlined"
      size="middle"
      style={{ width }}
      value={value}
      disabled={disabled}
      placeholder={placeholder}
      showSearch={searchable}
      optionFilterProp="label"
      options={[...options]}
      suffixIcon={<ChevronDown />}
      onChange={(next) => onChange(String(next ?? ''))}
    />
  );
}
