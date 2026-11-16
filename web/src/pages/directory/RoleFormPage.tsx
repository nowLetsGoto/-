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
import { SUPER_ADMIN_ROLE_KEY, createRole, getRole, listPermissions, updateRole } from '../../api/roles';
import { FormPage } from '../../components/FormPage';
import { ErrorState, Loading } from '../../components/States';
import { useMe } from '../../hooks/useMe';

const EMPTY: RoleInput = { key: '', name: '', description: '', permissions: [] };

const LIST_PATH = '/directory/roles';

export function RoleFormPage(): JSX.Element {
  const { key } = useParams<{ key: string }>();
  const editing = Boolean(key);
  const navigate = useNavigate();
  const { me } = useMe();

  const [form, setForm] = useState<RoleInput>(EMPTY);
  const [permissions, setPermissions] = useState<Permission[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState('');

  // 超管角色固定全量、非超管没有写权限 —— 两种都走只读展示
  const readOnly = !me?.isSuperAdmin || key === SUPER_ADMIN_ROLE_KEY;

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

  const title = editing ? (readOnly && me?.isSuperAdmin ? `查看角色：${form.name}` : `编辑角色：${form.name}`) : '新建角色';

  return (
    <FormPage
      title={title}
      subtitle={
        key === SUPER_ADMIN_ROLE_KEY
          ? '超级管理员角色的权限固定为全量，不可修改'
          : '勾选的权限点决定该角色「能做什么」'
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
          <input
            className="text-input"
            maxLength={40}
            value={form.name}
            disabled={readOnly}
            onChange={(event) => setForm({ ...form, name: event.target.value })}
          />
        </label>
        <label className="field">
          <span>角色标识 *</span>
          <input
            className="text-input"
            maxLength={32}
            placeholder="如 release-manager，创建后不可改"
            value={form.key}
            readOnly={editing}
            disabled={readOnly}
            onChange={(event) => setForm({ ...form, key: event.target.value })}
          />
        </label>
        <label className="field">
          <span>描述</span>
          <input
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
                <label
                  key={permission.key}
                  className={checked ? 'field-check field-check-on' : 'field-check'}
                  title={permission.key}
                >
                  <input
                    type="checkbox"
                    checked={checked}
                    disabled={readOnly}
                    onChange={() => toggle(permission.key)}
                  />
                  {permission.label}
                </label>
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
