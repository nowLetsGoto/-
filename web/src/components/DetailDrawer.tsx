/**
 * 抽屉容器（使用 console-kit 的 Drawer 命名导出）。
 * 职责：统一抽屉宽度、内边距、关闭行为与标题区样式。
 * 输入：open / title / onClose / children；输出：右侧抽屉。
 * 边界：页面通过本组件统一使用抽屉；容器不请求业务数据，也不决定详情内容。
 */
import { Drawer } from '@seakoi/console-kit';
import type { ReactNode } from 'react';

interface DetailDrawerProps {
  open: boolean;
  title: ReactNode;
  onClose: () => void;
  children: ReactNode;
  /** 默认 560，详情类信息够排两列 */
  width?: number;
}

export function DetailDrawer({
  open,
  title,
  onClose,
  children,
  width = 560,
}: DetailDrawerProps): JSX.Element {
  return (
    <Drawer
      className="detail-drawer"
      open={open}
      title={title}
      onClose={onClose}
      width={width}
      destroyOnHidden
      maskClosable
    >
      {children}
    </Drawer>
  );
}
