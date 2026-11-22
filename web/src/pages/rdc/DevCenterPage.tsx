/**
 * AI 研发中心骨架页（/rdc/dev）。
 * 职责：声明本菜单的标题、产品说明与 5 个子功能名称，交给共用外壳渲染。
 * 输入：无；输出：RdcPlaceholderPage。
 * 边界：只做静态展示；规划中的接口不注册路由、不建 handler、不在这里发请求。
 */
import { RdcPlaceholderPage } from './RdcPlaceholderPage';

const FEATURES: ReadonlyArray<string> = [
  'Git 工作区',
  '开发环境',
  '数据库',
  'Agent 调度',
  '自动测试',
];

export function DevCenterPage(): JSX.Element {
  return (
    <RdcPlaceholderPage
      title="AI研发中心"
      description="AI 自动执行研发任务的中枢：以需求编号为起点，串联工作区、环境、数据库、Agent 调度与自动测试。当前版本仅公开规划。"
      features={FEATURES}
    />
  );
}
