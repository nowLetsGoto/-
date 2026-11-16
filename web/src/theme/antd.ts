/**
 * Ant Design 主题：把 antd 的默认外观对齐到本项目的青色设计系统。
 * 职责：只声明 token 映射，不改任何组件行为。
 * 输入：无；输出：可直接传给 ConfigProvider 的 ThemeConfig。
 * 边界：颜色值必须与 styles.css 的 CSS 变量保持一致（antd 在 CSS-in-JS 里拿不到 var()，
 *       所以这里是一份显式镜像）；改主色时两处一起改。
 */
import type { ThemeConfig } from 'antd';

/** 与 styles.css :root 一一对应的色板（antd 需要字面量，不能用 CSS 变量）。 */
const palette = {
  primary: '#0369a1',
  primaryStrong: '#075985',
  text: '#0f2c36',
  textSecondary: '#5b7884',
  faint: '#7d97a2',
  border: '#e3eef2',
  borderSoft: '#edf5f8',
  surface: '#ffffff',
  surface2: '#fafdfe',
  danger: '#b42318',
  fontSans: '-apple-system, BlinkMacSystemFont, "PingFang SC", "Microsoft YaHei", "Segoe UI", sans-serif',
} as const;

export const antdTheme: ThemeConfig = {
  token: {
    colorPrimary: palette.primary,
    colorInfo: palette.primary,
    colorLink: palette.primary,
    colorError: palette.danger,
    colorText: palette.text,
    colorTextSecondary: palette.textSecondary,
    colorTextTertiary: palette.faint,
    colorBorder: palette.border,
    colorBorderSecondary: palette.borderSoft,
    colorBgContainer: palette.surface,
    colorBgElevated: palette.surface,
    borderRadius: 8,
    borderRadiusLG: 12,
    fontSize: 14,
    fontFamily: palette.fontSans,
    // 与 .button / .text-input 的 34px 高度对齐，混排时不会一高一矮
    controlHeight: 34,
  },
  components: {
    Table: {
      headerBg: palette.surface2,
      headerColor: palette.faint,
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
