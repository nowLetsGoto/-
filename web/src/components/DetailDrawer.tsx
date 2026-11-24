/**
 * 抽屉容器（使用 console-kit 的 Drawer 命名导出）。
 * 职责：统一抽屉宽度、内边距、关闭行为、标题区样式及 Safari 背景滚动锁。
 * 输入：open / title / onClose / children 及可选位置、尺寸、底部操作区；输出：默认右侧或底部抽屉。
 * 边界：页面通过本组件统一使用抽屉；容器不请求业务数据，也不决定详情内容。
 */
import { Drawer } from '@seakoi/console-kit';
import type { DrawerProps } from '@seakoi/console-kit';
import type { ReactNode } from 'react';
import { useDrawerScrollLock } from '../hooks/useDrawerScrollLock';

interface DetailDrawerProps {
  open: boolean;
  title: ReactNode;
  onClose: () => void;
  children: ReactNode;
  /** 默认 560，详情类信息够排两列 */
  width?: number;
  placement?: DrawerProps['placement'];
  height?: DrawerProps['height'];
  className?: string;
  footer?: ReactNode;
}

export function DetailDrawer({
  open,
  title,
  onClose,
  children,
  width = 560,
  placement = 'right',
  height,
  className = '',
  footer,
}: DetailDrawerProps): JSX.Element {
  const scrollLock = useDrawerScrollLock(open);
  return (
    <Drawer
      rootClassName={`detail-drawer ${scrollLock.rootClassName} ${className}`.trim()}
      afterOpenChange={scrollLock.afterOpenChange}
      open={open}
      title={title}
      onClose={onClose}
      width={width}
      placement={placement}
      height={height}
      footer={footer}
      destroyOnHidden
      maskClosable
    >
      {children}
    </Drawer>
  );
}
