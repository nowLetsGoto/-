/**
 * 角色授权树：以菜单为骨架，操作权限内嵌在对应菜单节点下。
 *
 * 设计（为什么不再分成"权限点矩阵 + 菜单树"两块）：
 *   - 勾选菜单 = 自动获得该菜单的查看权限（viewPermission），查看权限不单独勾选；
 *   - 每个菜单展开后显示它自己的操作权限（写/删等）；
 *   - 后端会再推导一次，确保"有菜单才有查看权、操作权限必须挂在已勾菜单下"，
 *     前端这里只负责把用户的意图表达清楚。
 *
 * 这样从界面上就消除了原先"勾了角色管理菜单却没勾 role.read，菜单死活不显示"的困惑。
 */
import { useMemo } from 'react';
import type { Menu } from '../api/roles';
import { Checkbox } from './Controls';

interface RolePermissionTreeProps {
  menus: Menu[];
  menuKeys: string[];
  permissions: string[];
  onChange: (next: { menuKeys: string[]; permissions: string[] }) => void;
  disabled?: boolean;
}

/** 按 group 聚合，保持后端顺序（后端顺序即产品期望的展示顺序）。 */
function groupMenus(menus: Menu[]): Array<[string, Menu[]]> {
  const map = new Map<string, Menu[]>();
  for (const menu of menus) {
    const list = map.get(menu.group) ?? [];
    list.push(menu);
    map.set(menu.group, list);
  }
  return [...map.entries()];
}

export function RolePermissionTree({
  menus,
  menuKeys,
  permissions,
  onChange,
  disabled = false,
}: RolePermissionTreeProps): JSX.Element {
  const groups = useMemo(() => groupMenus(menus), [menus]);
  const selectedMenus = useMemo(() => new Set(menuKeys), [menuKeys]);
  const selectedPermissions = useMemo(() => new Set(permissions), [permissions]);

  const toggleMenu = (key: string): void => {
    const next = new Set(selectedMenus);
    const nextPermissions = new Set(selectedPermissions);
    if (next.has(key)) {
      // 取消菜单：连它名下的操作权限一起清掉，避免留下"看不到却有权"的孤儿权限。
      next.delete(key);
      const menu = menus.find((item) => item.key === key);
      for (const action of menu?.actions ?? []) {
        nextPermissions.delete(action.key);
      }
    } else {
      next.add(key);
      // 查看权限由后端自动推导（viewPermission），前端不写进 permissions。
    }
    onChange({ menuKeys: [...next], permissions: [...nextPermissions] });
  };

  const toggleAction = (permissionKey: string): void => {
    const next = new Set(selectedPermissions);
    if (next.has(permissionKey)) {
      next.delete(permissionKey);
    } else {
      next.add(permissionKey);
    }
    onChange({ menuKeys: [...selectedMenus], permissions: [...next] });
  };

  /** 分组全选/取消，只动本组菜单及其操作权限。 */
  const toggleGroup = (items: Menu[]): void => {
    const allSelected = items.every((item) => selectedMenus.has(item.key));
    const nextMenus = new Set(selectedMenus);
    const nextPermissions = new Set(selectedPermissions);
    for (const menu of items) {
      if (allSelected) {
        nextMenus.delete(menu.key);
        for (const action of menu.actions) {
          nextPermissions.delete(action.key);
        }
      } else {
        nextMenus.add(menu.key);
      }
    }
    onChange({ menuKeys: [...nextMenus], permissions: [...nextPermissions] });
  };

  return (
    <div className="role-tree">
      {groups.map(([group, items]) => {
        const allSelected = items.every((item) => selectedMenus.has(item.key));
        return (
          <section className="role-tree-group" key={group}>
            <div className="role-tree-group-head">
              <Checkbox
                checked={allSelected}
                disabled={disabled}
                onChange={() => toggleGroup(items)}
              >
                <span className="role-tree-group-title">{group}</span>
              </Checkbox>
            </div>
            <div className="role-tree-items">
              {items.map((menu) => {
                const menuChecked = selectedMenus.has(menu.key);
                return (
                  <div
                    className={menuChecked ? 'role-tree-node role-tree-node-on' : 'role-tree-node'}
                    key={menu.key}
                  >
                    <div className="role-tree-node-head">
                      <Checkbox
                        checked={menuChecked}
                        disabled={disabled}
                        onChange={() => toggleMenu(menu.key)}
                      >
                        <span className="role-tree-node-label">{menu.label}</span>
                      </Checkbox>
                      <span className="role-tree-node-hint" title={menu.key}>
                        {menu.actions.length > 0 ? `${menu.actions.length} 项操作` : '仅查看'}
                      </span>
                    </div>
                    {menuChecked && menu.actions.length > 0 ? (
                      <div className="role-tree-actions">
                        {menu.actions.map((action) => (
                          <Checkbox
                            key={action.key}
                            className={
                              selectedPermissions.has(action.key)
                                ? 'field-check field-check-on'
                                : 'field-check'
                            }
                            title={action.key}
                            checked={selectedPermissions.has(action.key)}
                            disabled={disabled}
                            onChange={() => toggleAction(action.key)}
                          >
                            {action.label}
                          </Checkbox>
                        ))}
                      </div>
                    ) : null}
                  </div>
                );
              })}
            </div>
          </section>
        );
      })}
    </div>
  );
}
