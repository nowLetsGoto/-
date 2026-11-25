/**
 * 研发详情里的 5 个后续能力说明块（只读）。
 * 职责：逐项说明 Git 工作区 / 开发环境 / 数据库 / Agent 调度 / 自动测试的规划定位。
 * 输入：无。输出：5 张纯文字卡片，每张标注"后续迭代开放"。
 * 边界：刻意不放按钮 / 链接 / 请求 / 路由，也不注册 handler 或契约接口；
 *       本版真实能力只有任务资料的编排与手动状态推进。
 */
const PLACEHOLDER_FEATURES: ReadonlyArray<{ name: string; description: string }> = [
  {
    name: 'Git 工作区',
    description: '后续用于关联代码仓库、分支与工作区状态；本版不提供克隆、切换或提交入口。',
  },
  {
    name: '开发环境',
    description: '后续用于开通与连接研发环境；本版不申请资源、不保存连接信息。',
  },
  {
    name: '数据库',
    description: '后续用于环境级数据库的开通与连接；本版不创建实例、不展示凭据。',
  },
  {
    name: 'Agent 调度',
    description: '后续用于把研发任务交给执行器；本版的状态推进纯属人工编排，不触发 Agent。',
  },
  {
    name: '自动测试',
    description: '后续用于执行测试并汇总报告；本版不运行测试、不采集结果。',
  },
];

export function DevPlaceholderFeatures(): JSX.Element {
  return (
    <section className="rdc-dev-placeholders">
      <h3>后续能力（仅规划说明）</h3>
      <p className="cell-sub">以下能力尚未开放；本页仅说明用途，不提供任何可执行入口。</p>
      <div className="rdc-placeholder-grid">
        {PLACEHOLDER_FEATURES.map((feature) => (
          <div className="rdc-placeholder-card" key={feature.name}>
            <div className="rdc-placeholder-name">{feature.name}</div>
            <p>{feature.description}</p>
            <span className="rdc-placeholder-tag">后续迭代开放</span>
          </div>
        ))}
      </div>
    </section>
  );
}
