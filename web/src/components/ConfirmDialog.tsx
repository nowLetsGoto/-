/**
 * 确认弹窗（Ant Design Modal 的唯一封装）。
 * 职责：危险操作的二次确认，统一标题、影响面文案与按钮语义。
 * 输入：open / title / description / confirmText / danger / onConfirm / onCancel。
 * 输出：居中弹窗。
 * 边界：**业务页面不得直接 import antd 的 Modal**——与 DataTable、DetailDrawer 同理。
 */
import { Modal } from 'antd';
import type { ReactNode } from 'react';

interface ConfirmDialogProps {
  open: boolean;
  title: string;
  /** 影响面说明：写清楚"会发生什么"，不要只说"确定吗" */
  description: ReactNode;
  confirmText?: string;
  /** 高危操作用红色主按钮 */
  danger?: boolean;
  loading?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

export function ConfirmDialog({
  open,
  title,
  description,
  confirmText = '确认',
  danger = false,
  loading = false,
  onConfirm,
  onCancel,
}: ConfirmDialogProps): JSX.Element {
  return (
    <Modal
      open={open}
      title={title}
      okText={confirmText}
      cancelText="取消"
      okButtonProps={{ danger, loading }}
      onOk={onConfirm}
      onCancel={onCancel}
      destroyOnHidden
      maskClosable={false}
    >
      <div className="confirm-body">{description}</div>
    </Modal>
  );
}
