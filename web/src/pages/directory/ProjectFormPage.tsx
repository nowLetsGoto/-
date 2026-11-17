/**
 * 项目新增 / 编辑页（/directory/projects/new 与 /directory/projects/:key/edit）。
 * 职责：无 key 走新增；有 key 先取详情回填再走编辑；提交成功后回列表。
 * 输入：URL 参数 key、useProjects。
 * 输出：FormPage 包裹的字段集合——团队模式有「项目角色」，单人模式没有。
 * 边界：项目标识是 URL 段，创建后只读；列表与表单是两个独立路由。
 */
import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import type { ProjectInput } from '../../api/projects';
import { createProject, getProject, updateProject } from '../../api/projects';
import type { Role } from '../../api/roles';
import { listRoles } from '../../api/roles';
import { errorMessage } from '../../api/client';
import { readSession } from '../../api/session';
import { Input } from '../../components/Controls';
import { FormPage } from '../../components/FormPage';
import { Select } from '../../components/Select';
import { ErrorState, Loading } from '../../components/States';
import { invalidateProjects } from '../../hooks/useProjects';

const EMPTY_FORM: ProjectInput = { key: '', name: '', description: '', repoUrl: '', roleKey: '' };

const LIST_PATH = '/directory/projects';

export function ProjectFormPage(): JSX.Element {
  const { key } = useParams<{ key: string }>();
  const editing = Boolean(key);
  const navigate = useNavigate();
  // 单人模式没有角色区分：不展示角色字段，也不请求角色列表（单人无 role.read 权限）。
  const solo = (readSession()?.mode ?? 'solo') === 'solo';

  const [form, setForm] = useState<ProjectInput>(EMPTY_FORM);
  const [roles, setRoles] = useState<Role[]>([]);
  const [loading, setLoading] = useState(editing);
  const [loadError, setLoadError] = useState('');
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState('');

  useEffect(() => {
    if (solo) {
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
  }, [solo]);

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
        {solo ? null : (
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
        )}
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

      {solo ? (
        <p className="settings-hint">
          项目创建后即可上传包；单人模式不区分角色，所有项目都由你自己管理。
        </p>
      ) : (
        <p className="settings-hint">
          项目角色优先级高于人员角色：设置后，所有人访问本项目都按此角色的权限判定（超级管理员除外）。
        </p>
      )}
    </FormPage>
  );
}
