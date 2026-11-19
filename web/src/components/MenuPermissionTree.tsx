/**
 * 菜单授权树。
 * 职责：把平台级菜单目录按 group 渲染成勾选树，支持分组全选/取消。
 * 输入：menus（来自 /menus）、value（已授权 key）、onChange、disabled。
 * 输出：只读展示或可编辑的勾选矩阵。
 * 边界：不判断"这个菜单能不能授权给当前角色"——授权只做减法，
 *       放行与否由后端守卫决定（见 contract.Menus 的设计说明）。
 *       本组件只负责收集管理员的意愿。
 */
import { useMemo } from 'react';
import type { Menu } from '../api/roles';
import { Checkbox } from './Controls';

interface MenuPermissionTreeProps {
  menus: Menu[];
  value: string[];
  onChange: (keys: string[]) => void;
  disabled?: boolean;
}

/** 按 group 聚合，保持后端给的顺序（后端顺序即产品期望的展示顺序）。 */
function groupMenus(menus: Menu[]): Array<[string, Menu[]]> {
  const map = new Map<string, Menu[]>();
  for (const menu of menus) {
    const list = map.get(menu.group) ?? [];
    list.push(menu);
    map.set(menu.group, list);
  }
  return [...map.entries()];
}

export function MenuPermissionTree({
  menus,
  value,
  onChange,
  disabled = false,
}: MenuPermissionTreeProps): JSX.Element {
  const groups = useMemo(() => groupMenus(menus), [menus]);
  const selected = useMemo(() => new Set(value), [value]);

  const toggle = (key: string): void => {
    const next = new Set(selected);
    if (next.has(key)) {
      next.delete(key);
    } else {
      next.add(key);
    }
    onChange([...next]);
  };

  /** 分组全选/取消：只用本组的 key，不动其他分组的选择。 */
  const toggleGroup = (items: Menu[]): void => {
    const allSelected = items.every((item) => selected.has(item.key));
    const next = new Set(selected);
    for (const item of items) {
      if (allSelected) {
        next.delete(item.key);
      } else {
        next.add(item.key);
      }
    }
    onChange([...next]);
  };

  return (
    <div className="menu-tree">
      {groups.map(([group, items]) => {
        const allSelected = items.every((item) => selected.has(item.key));
        return (
          <div className="field-block" key={group}>
            <span className="field-label">
              <Checkbox
                checked={allSelected}
                disabled={disabled}
                onChange={() => toggleGroup(items)}
              >
                {group}
              </Checkbox>
            </span>
            <div className="field-picker menu-tree-children">
              {items.map((menu) => (
                <Checkbox
                  key={menu.key}
                  className={selected.has(menu.key) ? 'field-check field-check-on' : 'field-check'}
                  title={menu.key}
                  checked={selected.has(menu.key)}
                  disabled={disabled}
                  onChange={() => toggle(menu.key)}
                >
                  {menu.label}
                </Checkbox>
              ))}
            </div>
          </div>
        );
      })}
    </div>
  );
}
