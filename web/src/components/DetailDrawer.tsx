/**
 * 抽屉容器（Ant Design Drawer 的唯一封装）。
 * 职责：统一抽屉宽度、内边距、关闭行为与标题区样式。
 * 输入：open / title / onClose / children；输出：右侧抽屉。
 * 边界：**业务页面不得直接 import antd 的 Drawer**——与 DataTable 同理，
 *       antd 只出现在 components 下的封装里，换库时只改这里。
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

export function DetailDrawer({ open, title, onClose, children, width = 560 }: DetailDrawerProps): JSX.Element {
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
