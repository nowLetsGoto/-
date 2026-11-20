/**
 * 关联对象标签组。
 * 职责：把团队 / 项目等关联列表收敛成一行 chip，超出 max 时用「+N」收尾。
 * 输入：items（id + name）与最多显示数量；输出：chip 行或占位横线。
 * 边界：不取数、不认识业务类型、不处理点击——只负责显示。
 */
interface ChipItem {
  id: string;
  name: string;
}

export function ChipList({ items, max = 2 }: { items: ChipItem[]; max?: number }): JSX.Element {
  if (items.length === 0) {
    return <span className="cell-sub">—</span>;
  }

  const visible = items.slice(0, max);
  const rest = items.length - visible.length;

  return (
    <div className="chip-row">
      {visible.map((item) => (
        <span className="chip" key={item.id}>
          {item.name}
        </span>
      ))}
      {rest > 0 ? <span className="chip chip-more">+{rest}</span> : null}
    </div>
  );
}
