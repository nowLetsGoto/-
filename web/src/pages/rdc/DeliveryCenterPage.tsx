/**
 * 需求交付中心骨架页（/rdc/delivery）。
 * 职责：声明本菜单的标题、产品说明与 4 个子功能名称，交给共用外壳渲染。
 * 输入：无；输出：RdcPlaceholderPage。
 * 边界：只做静态展示；交付记录与归档能力均属后续迭代，本版不提供假按钮。
 */
import { RdcPlaceholderPage } from './RdcPlaceholderPage';

const FEATURES: ReadonlyArray<string> = ['Git 记录', '测试报告', '验收结果', '需求归档'];

export function DeliveryCenterPage(): JSX.Element {
  return (
    <RdcPlaceholderPage
      title="需求交付中心"
      description="留存交付证据并归档需求，形成从需求输入到交付闭环的最后一环。当前版本仅公开规划。"
      features={FEATURES}
    />
  );
}
