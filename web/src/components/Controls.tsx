/**
 * 基础控件适配：输入原生按钮/文本字段属性，输出组件库控件与原有事件。
 * 边界：只转换按钮提交语义、关闭输入裁剪、统一分段控件的类名；
 *       不参与校验、取数或业务状态。
 * Checkbox/Radio 自带 label，调用方以其包裹文案，避免嵌套 label 破坏点击与键盘操作。
 */
import {
  Button as KitButton,
  Input as KitInput,
  Checkbox as KitCheckbox,
  Radio as KitRadio,
  Segmented as KitSegmented,
} from '@seakoi/console-kit';
import type { SegmentedProps } from '@seakoi/console-kit';
import type { ButtonHTMLAttributes, ComponentProps } from 'react';

export function Button({
  type = 'submit',
  className = '',
  ...props
}: Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'color'>): JSX.Element {
  const primary = className
    .split(/\s+/)
    .some((name) => name === 'button-primary' || name === 'login-submit');
  return (
    <KitButton
      {...props}
      className={`app-control-button ${className}`}
      type={primary ? 'primary' : 'text'}
      htmlType={type}
    />
  );
}

export function Input(props: ComponentProps<typeof KitInput>): JSX.Element {
  return <KitInput {...props} trim={false} />;
}

export const TextArea = KitInput.TextArea;
export function Checkbox({
  className = '',
  ...props
}: ComponentProps<typeof KitCheckbox>): JSX.Element {
  return <KitCheckbox {...props} className={`app-control-check ${className}`} />;
}

export function Radio({ className = '', ...props }: ComponentProps<typeof KitRadio>): JSX.Element {
  return <KitRadio {...props} className={`app-control-radio ${className}`} />;
}

/**
 * 分段控件（互斥选项切换）。
 * 统一走组件库而不各页面用 button 拼：手拼的 tab 需要自己处理选中态、
 * 键盘可达性与基线对齐，历史上就出现过文字不垂直居中的问题。
 */
export function Segmented<T extends string>({
  className = '',
  ...props
}: SegmentedProps<T>): JSX.Element {
  return <KitSegmented<T> {...props} className={`app-segmented ${className}`.trim()} />;
}
