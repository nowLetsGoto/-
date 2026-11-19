/**
 * 角色新增 / 编辑页（/directory/roles/new 与 /directory/roles/:key/edit）。
 * 职责：维护角色名称、标识、描述与权限点勾选矩阵；提交后回列表。
 * 输入：URL 参数 key（无则新增）、权限点目录、角色本身。
 * 输出：createRole / updateRole 请求。
 * 边界：权限点目录来自服务端，前端不硬编码任何 key；
 *       **超级管理员角色的权限只读**（固定全量，改了会导致没人能改角色）；
 *       非超管打开编辑页时整体只读（服务端同样 403，这里只是不给假入口）。
 */
import { useEffect, useMemo, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { errorMessage } from '../../api/client';
import type { Permission, RoleInput } from '../../api/roles';
import { createRole, getRole, listPermissions, updateRole } from '../../api/roles';
import { Checkbox, Input } from '../../components/Controls';
import { FormPage } from '../../components/FormPage';
import { ErrorState, Loading } from '../../components/States';
import { useMe } from '../../hooks/useMe';
import { atLeastLevel } from '../../utils/accountLevel';

const EMPTY: RoleInput = { key: '', name: '', description: '', permissions: [] };

const LIST_PATH = '/directory/roles';

export function RoleFormPage(): JSX.Element {
  const { key } = useParams<{ key: string }>();
  const editing = Boolean(key);
  const navigate = useNavigate();
  const { me } = useMe();

  const [form, setForm] = useState<RoleInput>(EMPTY);
  const [permissions, setPermissions] = useState<Permission[]>([]);
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
    // 权限目录与角色详情并行取：目录决定矩阵长什么样，角色决定勾了哪些。
    Promise.all([listPermissions(), key ? getRole(key) : Promise.resolve(null)])
      .then(([catalog, role]) => {
        if (cancelled) {
          return;
        }
        setPermissions(catalog.items);
        setIsBuiltin(role?.isBuiltin === true);
        setForm(
          role
            ? {
                key: role.key,
                name: role.name,
                description: role.description,
                permissions: role.permissions,
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
  }, [key]);

  // 按模块分组：后端给 group 字段，前端不在代码里维护第二份分组规则。
  const grouped = useMemo(() => {
    const map = new Map<string, Permission[]>();
    for (const permission of permissions) {
      const list = map.get(permission.group) ?? [];
      list.push(permission);
      map.set(permission.group, list);
    }
    return [...map.entries()];
  }, [permissions]);

  const toggle = (permissionKey: string): void => {
    setForm((prev) => ({
      ...prev,
      permissions: prev.permissions.includes(permissionKey)
        ? prev.permissions.filter((item) => item !== permissionKey)
        : [...prev.permissions, permissionKey],
    }));
  };

  const submit = async (): Promise<void> => {
    setSaving(true);
    setNotice('');
    try {
      if (key) {
        await updateRole(key, form);
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

      {grouped.map(([group, items]) => (
        <div className="field-block" key={group}>
          <span className="field-label">{group}</span>
          <div className="field-picker">
            {items.map((permission) => {
              const checked = form.permissions.includes(permission.key);
              return (
                <Checkbox
                  key={permission.key}
                  className={checked ? 'field-check field-check-on' : 'field-check'}
                  title={permission.key}
                  checked={checked}
                  disabled={readOnly}
                  onChange={() => toggle(permission.key)}
                >
                  {permission.label}
                </Checkbox>
              );
            })}
          </div>
        </div>
      ))}

      <p className="settings-hint">
        已选 {form.permissions.length} / {permissions.length} 个权限点
      </p>
    </FormPage>
  );
}
