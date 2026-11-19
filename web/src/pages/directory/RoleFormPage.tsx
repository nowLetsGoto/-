/**
 * 角色新增 / 编辑页（/directory/roles/new 与 /directory/roles/:id/edit）。
 * 职责：维护角色名称、标识、描述与「菜单 + 操作权限」授权树；提交后回列表。
 * 输入：URL 参数 id（无则新增）、菜单目录（自带查看/操作权限）、角色本身。
 * 输出：createRole / updateRole 请求。
 * 边界：目录来自服务端，前端不硬编码任何 key；查看权限由勾选菜单自动获得、
 *       不单独配置（后端也会推导一次）；非编辑者只读。
 */
import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { errorMessage } from '../../api/client';
import type { Menu, RoleInput } from '../../api/roles';
import { createRole, getRole, listMenus, updateRole } from '../../api/roles';
import { Input } from '../../components/Controls';
import { FormPage } from '../../components/FormPage';
import { RolePermissionTree } from '../../components/RolePermissionTree';
import { ErrorState, Loading } from '../../components/States';
import { useMe } from '../../hooks/useMe';
import { atLeastLevel } from '../../utils/accountLevel';

// menuKeys 留空数组而不是 undefined：新建时"一个菜单都不勾"是管理员的明确选择，
// 与"旧客户端没传这个字段"要区分开（后端对 nil 会默认全量）。
const EMPTY: RoleInput = { key: '', name: '', description: '', permissions: [], menuKeys: [] };

const LIST_PATH = '/directory/roles';

export function RoleFormPage(): JSX.Element {
  // 用 id 而非 key 定位：key 只保证公司内唯一，超管视角下会改错公司的同名角色。
  const { id } = useParams<{ id: string }>();
  const editing = Boolean(id);
  const navigate = useNavigate();
  const { me } = useMe();

  const [form, setForm] = useState<RoleInput>(EMPTY);
  const [menus, setMenus] = useState<Menu[]>([]);
  // 当前编辑的角色是不是平台内置（tenant_id 为空的那批）。
  // 内置角色可以改（每家公司各持一份副本，改它只影响本公司），只是不能删。
  // 这里保留 isBuiltin 是为了在标题下方说明它的特殊身份，不再用于置灰。
  const [isBuiltin, setIsBuiltin] = useState(false);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState('');

  // 只有一种只读情况：层级不足（路由已拦，这里是兜底）。
  // 内置角色不再置灰——公司管理员维护自己公司的角色定义是正当操作。
  const readOnly = !atLeastLevel(me, 'platform_admin');

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    // 菜单目录决定授权树长什么样（它自带查看/操作权限），角色决定勾了哪些。
    Promise.all([listMenus(), id ? getRole(id) : Promise.resolve(null)])
      .then(([menuCatalog, role]) => {
        if (cancelled) {
          return;
        }
        setMenus(menuCatalog.items);
        setIsBuiltin(role?.isBuiltin === true);
        setForm(
          role
            ? {
                key: role.key,
                name: role.name,
                description: role.description,
                permissions: role.permissions,
                menuKeys: role.menuKeys,
              }
            : EMPTY,
        );
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setLoadError(errorMessage(err));
          setLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [id]);

  const submit = async (): Promise<void> => {
    setSaving(true);
    setNotice('');
    try {
      if (id) {
        await updateRole(id, form);
      } else {
        await createRole(form);
      }
      navigate(LIST_PATH, { replace: true });
    } catch (err) {
      setNotice(errorMessage(err));
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <section className="page">
        <Loading text="正在加载角色与权限目录…" />
      </section>
    );
  }
  if (loadError) {
    return (
      <section className="page">
        <div className="page-head">
          <div>
            <h1>角色</h1>
            <p className="sub">加载失败</p>
          </div>
        </div>
        <ErrorState message={loadError} />
      </section>
    );
  }

  const title = editing
    ? readOnly
      ? `查看角色：${form.name}`
      : `编辑角色：${form.name}`
    : '新建角色';

  return (
    <FormPage
      title={title}
      subtitle={
        isBuiltin
          ? '内置角色：本公司预置，可以修改权限但不能删除；改动只影响本公司，不影响其他公司'
          : '勾选的权限点决定该角色「能做什么」；管理能力（管人、改配置）由账号的管理身份决定，不在这里勾选'
      }
      backTo={LIST_PATH}
      saving={saving}
      notice={notice}
      readOnly={readOnly}
      onSubmit={() => void submit()}
    >
      <div className="field-grid">
        <label className="field">
          <span>角色名称 *</span>
          <Input
            className="text-input"
            maxLength={40}
            value={form.name}
            disabled={readOnly}
            onChange={(event) => setForm({ ...form, name: event.target.value })}
          />
        </label>
        <label className="field">
          <span>角色标识 *</span>
          <Input
            className="text-input"
            maxLength={32}
            placeholder="如 devops，本公司内不可重名，创建后不可改"
            value={form.key}
            readOnly={editing}
            disabled={readOnly}
            onChange={(event) => setForm({ ...form, key: event.target.value })}
          />
        </label>
        <label className="field">
          <span>描述</span>
          <Input
            className="text-input"
            maxLength={200}
            value={form.description}
            disabled={readOnly}
            onChange={(event) => setForm({ ...form, description: event.target.value })}
          />
        </label>
      </div>

      <div className="field-block">
        <span className="field-label">授权范围</span>
        <p className="cell-sub">
          勾选菜单即拥有该页面的查看权限；展开菜单可进一步勾选「新增、编辑、删除」等操作权限。
          未勾选的菜单不会出现在该角色的侧栏中。
        </p>
        <RolePermissionTree
          menus={menus}
          menuKeys={form.menuKeys ?? []}
          permissions={form.permissions}
          disabled={readOnly}
          onChange={({ menuKeys, permissions }) =>
            setForm((prev) => ({ ...prev, menuKeys, permissions }))
          }
        />
      </div>
    </FormPage>
  );
}
