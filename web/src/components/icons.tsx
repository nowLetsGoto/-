/**
 * 内联线性图标。
 * 职责：把侧边栏与顶栏用到的图标收敛成一份 <path> 数据，避免各组件各写一份 svg。
 * 输入：ICONS 的键与可选尺寸；输出：继承 currentColor 的 16px 线性图标。
 * 边界：不承载业务含义、不写死颜色与线宽以外的样式。
 */

const ICONS = {
  overview: 'M4 5.5h7v7H4z M13 5.5h7v7h-7z M4 14.5h7v4H4z M13 14.5h7v4h-7z',
  packages: 'M12 3.5 20 7.5v9l-8 4-8-4v-9z M4 7.5l8 4 8-4 M12 11.5v9',
  apps: 'M12 3.5 3.5 8.5 12 13.5l8.5-5z M3.5 13.5 12 18.5l8.5-5',
  plus: 'M12 5.5v13 M5.5 12h13',
  settings:
    'M12 9.2a2.8 2.8 0 1 0 0 5.6a2.8 2.8 0 1 0 0-5.6 M12 3v2.4 M12 18.6V21 M3 12h2.4 M18.6 12H21 M5.7 5.7l1.7 1.7 M16.6 16.6l1.7 1.7 M18.3 5.7l-1.7 1.7 M7.4 16.6l-1.7 1.7',
  people:
    'M9.5 11.5a3 3 0 1 0 0-6a3 3 0 0 0 0 6 M4 20v-1.2a5.2 5.2 0 0 1 11 0V20 M16.5 6.2a3 3 0 0 1 0 5.6 M17.6 14.3a4.4 4.4 0 0 1 3.4 4.3V20',
  team: 'M7 9.5a2.2 2.2 0 1 0 0-4.4a2.2 2.2 0 0 0 0 4.4 M12 9.5a2.2 2.2 0 1 0 0-4.4a2.2 2.2 0 0 0 0 4.4 M17 9.5a2.2 2.2 0 1 0 0-4.4a2.2 2.2 0 0 0 0 4.4 M4 19v-1.5a8 8 0 0 1 16 0V19',
  project: 'M4 7.5h5.5l1.6 2H20V17a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2z',
} as const;

export type IconName = keyof typeof ICONS;

export function Icon({ name, size = 16 }: { name: IconName; size?: number }): JSX.Element {
  return (
    <svg
      className="icon"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d={ICONS[name]} />
    </svg>
  );
}
