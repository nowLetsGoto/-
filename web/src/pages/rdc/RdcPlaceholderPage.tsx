/**
 * 研发中心骨架页的共用展示外壳。
 * 职责：渲染标题、产品说明、只读子功能清单与统一的"后续迭代开放"文案。
 * 输入：页面标题、说明文字、子功能名称列表（由各骨架页传入）。
 * 输出：一个真实的空态页面，不发起任何请求。
 * 边界：纯静态展示——不放操作按钮、不造假数据、不写占位实现；
 *       整页用 States.Empty 作诚实空态，子功能卡片只说明规划范围。
 */
import { Empty } from '../../components/States';

interface RdcPlaceholderPageProps {
  title: string;
  description: string;
  features: ReadonlyArray<string>;
}

export function RdcPlaceholderPage({
  title,
  description,
  features,
}: RdcPlaceholderPageProps): JSX.Element {
  return (
    <section className="page">
      <div className="page-head">
        <div>
          <h1>{title}</h1>
          <p className="sub">研发中心 · 当前版本为公开规划，暂不提供操作入口</p>
        </div>
      </div>

      <div className="card">
        <Empty title={`${title}将在后续迭代开放`} description={description} />

        <div className="rdc-feature-grid">
          {features.map((feature) => (
            <div className="rdc-feature-card" key={feature}>
              <span className="rdc-feature-name">{feature}</span>
              <span className="rdc-feature-tag">后续迭代开放</span>
            </div>
          ))}
        </div>

        <p className="settings-hint">
          以上子功能均处于规划阶段，当前版本不产生任何数据、也没有可点击的操作入口。
          需求中心的编号将作为后续研发、审核与交付环节的引用起点。
        </p>
      </div>
    </section>
  );
}
