/**
 * 加载骨架。
 * 职责：给列表、卡片与页面级异步态提供统一的占位块，避免加载时只剩一块空白。
 * 输入：可选的类名 / 行数 / 列数；输出：纯展示的骨架 JSX。
 * 边界：不发请求、不携带文案（无障碍标签由调用方给），不参与业务布局计算。
 */
export function Skeleton({ className = '' }: { className?: string }): JSX.Element {
  return <span className={`skeleton ${className}`.trim()} aria-hidden="true" />;
}

export function TableSkeleton({
  rows = 6,
  columns = 5,
}: {
  rows?: number;
  columns?: number;
}): JSX.Element {
  return (
    <div className="table-skeleton" role="status" aria-label="正在加载列表">
      <div className="table-skeleton-row table-skeleton-row-head">
        {Array.from({ length: columns }, (_, index) => (
          <Skeleton className="skeleton-cell skeleton-cell-head" key={`head-${index}`} />
        ))}
      </div>
      {Array.from({ length: rows }, (_, row) => (
        <div className="table-skeleton-row" key={`row-${row}`}>
          {Array.from({ length: columns }, (_, column) => (
            <Skeleton
              className={column === 0 ? 'skeleton-cell skeleton-cell-wide' : 'skeleton-cell'}
              key={`cell-${row}-${column}`}
            />
          ))}
        </div>
      ))}
    </div>
  );
}
