/**
 * 职责：在 Git 子进程隔离未验证前明确展示不可用状态。
 * 输入：详情页传入的需求 ID；输出：静态不可用说明。
 * 边界：不调用 Git API、不显示刷新按钮、不生成虚假的提交或状态。
 */
import type { FC } from 'react';

export const DeliveryGitPanel: FC<{ requirementID: string }> = () => {
  return (
    <section className="rdc-delivery-section">
      <h3>Git 记录</h3>
      <p>Git 安全隔离尚未就绪，仓库读取暂未开放。</p>
    </section>
  );
};
