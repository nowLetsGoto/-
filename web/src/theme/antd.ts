/**
 * Ant Design 主题：把 antd 的默认外观对齐到本项目的青色设计系统。
 * 职责：只声明 token 映射，不改任何组件行为。
 * 输入：无；输出：可直接传给 ConfigProvider 的 ThemeConfig。
 * 边界：颜色值必须与 styles.scss 的 CSS 变量保持一致（antd 在 CSS-in-JS 里拿不到 var()，
 *       所以这里是一份显式镜像）；改主色时两处一起改。
 */
import type { ThemeConfig } from '@seakoi/console-kit';

/** 与 styles.scss :root 一一对应的色板（antd 需要字面量，不能用 CSS 变量）。 */
const palette = {
  bg: '#f4f7f9',
  primary: '#0369a1',
  primaryHover: '#075985',
  primaryActive: '#064e73',
  text: '#102b36',
  textSecondary: '#35525f',
  faint: '#8a9ea7',
  border: '#e2ebef',
  borderSoft: '#edf3f6',
  surface: '#ffffff',
  surface2: '#f8fafb',
  danger: '#b42318',
  success: '#127a5c',
  fontSans:
    '-apple-system, BlinkMacSystemFont, "PingFang SC", "Microsoft YaHei", "Segoe UI", sans-serif',
} as const;

export const antdTheme: ThemeConfig = {
  token: {
    colorPrimary: palette.primary,
    colorPrimaryHover: palette.primaryHover,
    colorPrimaryActive: palette.primaryActive,
    colorInfo: palette.primary,
    colorLink: palette.primary,
    colorError: palette.danger,
    colorSuccess: palette.success,
    colorText: palette.text,
    colorTextSecondary: palette.textSecondary,
    colorTextTertiary: palette.faint,
    colorBorder: palette.border,
    colorBorderSecondary: palette.borderSoft,
    colorBgContainer: palette.surface,
    colorBgElevated: palette.surface,
    colorBgLayout: palette.bg,
    // primary 按钮的文字色。组件库在 hover/active 时会把它派生到 colorPrimaryHover，
    // 与这里拿来做背景的深蓝撞色，必须显式锁白，否则文字直接融进背景。
    colorTextLightSolid: '#ffffff',
    borderRadius: 8,
    borderRadiusLG: 12,
    fontSize: 14,
    fontFamily: palette.fontSans,
    // 与 .button / .text-input 的 34px 高度对齐，混排时不会一高一矮
    controlHeight: 34,
  },
  components: {
    // 适配 console-kit 基础控件，保持原有轻量按钮与输入框的密度。
    Button: {
      defaultShadow: 'none',
      primaryShadow: 'none',
      dangerShadow: 'none',
      // 实心按钮三种状态都用同一个前景色；hover/active 只换背景，文字保持白色可读。
      primaryColor: '#ffffff',
    },
    Table: {
      headerBg: palette.surface2,
      headerColor: palette.textSecondary,
      headerSplitColor: 'transparent',
      borderColor: palette.borderSoft,
      rowHoverBg: palette.surface2,
      cellFontSize: 12.5,
      cellPaddingBlock: 10,
      headerBorderRadius: 0,
    },
    Pagination: {
      itemSize: 28,
      colorPrimary: palette.primary,
    },
  },
};
