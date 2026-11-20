/**
 * 项目新增 / 编辑页（/directory/projects/new 与 /directory/projects/:key/edit）。
 * 职责：无 key 走新增；有 key 先取详情回填再走编辑；提交成功后回列表。
 * 输入：URL 参数 key、useProjects。
 * 输出：FormPage 包裹的字段集合——团队模式有「项目角色」，单人模式没有。
 * 边界：项目标识是 URL 段，创建后只读；列表与表单是两个独立路由。
 */
import { useEffect, useState } from 'react';
import { Upload } from '@seakoi/console-kit';
import { useNavigate, useParams } from 'react-router-dom';
import type { ProjectInput } from '../../api/projects';
import { createProject, getProject, updateProject } from '../../api/projects';
import type { Role } from '../../api/roles';
import { listRoles } from '../../api/roles';
import { errorMessage } from '../../api/client';
import { readSession } from '../../api/session';
import { Button, Input } from '../../components/Controls';
import { FormPage } from '../../components/FormPage';
import { Select } from '../../components/Select';
import { ErrorState, Loading } from '../../components/States';
import { useMe } from '../../hooks/useMe';
import { invalidateProjects } from '../../hooks/useProjects';

const EMPTY_FORM: ProjectInput = {
  key: '',
  name: '',
  description: '',
  repoUrl: '',
  iconUrl: '',
  roleKey: '',
};

const LIST_PATH = '/directory/projects';
const MAX_ICON_BYTES = 512 * 1024;
const ICON_TYPES = ['image/png', 'image/jpeg', 'image/webp'];

export function ProjectFormPage(): JSX.Element {
  const { key } = useParams<{ key: string }>();
  const editing = Boolean(key);
  const navigate = useNavigate();
  // 单人模式没有角色区分；团队模式里角色清单也只对超管开放（/roles 走 RequireSuperAdmin）。
  // 两种情况都不该展示「项目角色」，也不必发注定 403 的请求。
  const solo = (readSession()?.mode ?? 'solo') === 'solo';
  const { me } = useMe();
  const showRoleField = !solo && me?.isSuperAdmin === true;

  const [form, setForm] = useState<ProjectInput>(EMPTY_FORM);
  const [roles, setRoles] = useState<Role[]>([]);
  const [loading, setLoading] = useState(editing);
  const [loadError, setLoadError] = useState('');
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState('');
  const [iconError, setIconError] = useState('');

  useEffect(() => {
    // 只有超管能拿到角色清单；其他人不必发这个注定 403 的请求。
    if (!showRoleField) {
      return;
    }
    let cancelled = false;
    listRoles()
      .then((result) => {
        if (!cancelled) {
          setRoles(result.items);
        }
      })
      .catch(() => {
        // 角色取不到不该挡住编辑；服务端仍会校验 roleKey
        if (!cancelled) {
          setRoles([]);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [showRoleField]);

  useEffect(() => {
    if (!key) {
      setForm(EMPTY_FORM);
      setLoading(false);
      return;
    }
    let cancelled = false;
    setLoading(true);
    getProject(key)
      .then((project) => {
        if (cancelled) {
          return;
        }
        setForm({
          key: project.key,
          name: project.name,
          description: project.description,
          repoUrl: project.repoUrl,
          iconUrl: project.iconUrl ?? '',
          roleKey: project.roleKey ?? '',
        });
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

  const handleIcon = (file: File): false | typeof Upload.LIST_IGNORE => {
    if (!ICON_TYPES.includes(file.type)) {
      setIconError('请选择 PNG / JPG / WebP 图片');
      return Upload.LIST_IGNORE;
    }
    if (file.size > MAX_ICON_BYTES) {
      setIconError('项目图标不能超过 512 KB');
      return Upload.LIST_IGNORE;
    }
    const reader = new FileReader();
    reader.onload = () =>
      setForm((current) => ({ ...current, iconUrl: String(reader.result ?? '') }));
    reader.onerror = () => setIconError('读取图片失败，请重试');
    reader.readAsDataURL(file);
    setIconError('');
    return false;
  };

  const submit = async (): Promise<void> => {
    setSaving(true);
    setNotice('');
    try {
      if (key) {
        await updateProject(key, form);
      } else {
        await createProject(form);
      }
      // 列表读的是模块级缓存：这里先失效，回到列表才会重新拉取。
      invalidateProjects();
      navigate(LIST_PATH, { replace: true });
    } catch (err) {
      setNotice(errorMessage(err));
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <section className="page">
        <Loading text="正在加载项目…" />
      </section>
    );
  }
  if (loadError) {
    return (
      <section className="page">
        <div className="page-head">
          <div>
            <h1>编辑项目</h1>
            <p className="sub">加载失败</p>
          </div>
        </div>
        <ErrorState message={loadError} />
      </section>
    );
  }

  return (
    <FormPage
      title={editing ? '编辑项目' : '新增项目'}
      subtitle={editing ? '项目标识创建后不可修改' : '创建后即可上传包、关联人员与团队'}
      backTo={LIST_PATH}
      saving={saving}
      notice={notice}
      onSubmit={() => void submit()}
    >
      <div className="field-grid">
        <label className="field">
          <span>项目名称 *</span>
          <Input
            className="text-input"
            maxLength={60}
            value={form.name}
            onChange={(event) => setForm({ ...form, name: event.target.value })}
          />
        </label>
        <label className="field">
          <span>项目标识 *</span>
          <Input
            className="text-input"
            maxLength={32}
            placeholder="如 shop，用于地址栏"
            value={form.key}
            readOnly={editing}
            onChange={(event) => setForm({ ...form, key: event.target.value })}
          />
        </label>
        <div className="field field-full">
          <span className="field-label">项目图标</span>
          <Upload.Dragger
            className="icon-dropzone"
            accept=".png,.jpg,.jpeg,.webp"
            multiple={false}
            maxCount={1}
            showUploadList={false}
            beforeUpload={handleIcon}
            aria-label="选择项目图标"
          >
            <p className="dropzone-title">拖拽图片到此处，或点击选择</p>
            <p className="dropzone-hint">PNG / JPG / WebP，不超过 512 KB</p>
          </Upload.Dragger>
          {form.iconUrl ? (
            <div className="icon-preview">
              <img src={form.iconUrl} alt="项目图标预览" />
              <Button
                className="button button-ghost button-small"
                type="button"
                onClick={() => setForm((current) => ({ ...current, iconUrl: '' }))}
              >
                移除图标
              </Button>
            </div>
          ) : null}
          {iconError ? <p className="field-error">{iconError}</p> : null}
        </div>
        <label className="field">
          <span>项目库地址</span>
          <Input
            className="text-input"
            maxLength={300}
            placeholder="https://gitlab.example.com/group/repo"
            value={form.repoUrl}
            onChange={(event) => setForm({ ...form, repoUrl: event.target.value })}
          />
        </label>
        {showRoleField ? (
          <label className="field">
            <span>项目角色</span>
            <Select
              value={form.roleKey}
              options={[
                { value: '', label: '沿用人员角色' },
                ...roles.map((role) => ({ value: role.key, label: role.name })),
              ]}
              onChange={(roleKey) => setForm({ ...form, roleKey })}
            />
          </label>
        ) : null}
        <label className="field">
          <span>描述</span>
          <Input
            className="text-input"
            maxLength={200}
            value={form.description}
            onChange={(event) => setForm({ ...form, description: event.target.value })}
          />
        </label>
      </div>

      {showRoleField ? (
        <p className="settings-hint">
          项目角色优先级高于人员角色：设置后，所有人访问本项目都按此角色的权限判定（超级管理员除外）。
        </p>
      ) : (
        <p className="settings-hint">
          {solo
            ? '项目创建后即可上传包；单人模式不区分角色，所有项目都由你自己管理。'
            : '项目角色由超级管理员在编辑项目时设置，你这次保存不会影响它。'}
        </p>
      )}
    </FormPage>
  );
}
