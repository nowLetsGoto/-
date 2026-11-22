/**
 * 人工审核中心骨架页（/rdc/review）。
 * 职责：声明本菜单的标题、产品说明与 4 个子功能名称，交给共用外壳渲染。
 * 输入：无；输出：RdcPlaceholderPage。
 * 边界：只做静态展示；Diff 与提交能力均属后续迭代，本版不提供假按钮。
 */
import { RdcPlaceholderPage } from './RdcPlaceholderPage';

const FEATURES: ReadonlyArray<string> = ['前端 Diff', '后端 Diff', '编辑器打开', '人工提交'];

export function ReviewCenterPage(): JSX.Element {
  return (
    <RdcPlaceholderPage
      title="人工审核中心"
      description="AI 产出由人工最终把关后提交：逐项查看前后端差异、在编辑器中核对，再人工确认提交。当前版本仅公开规划。"
      features={FEATURES}
    />
  );
}
