/**
 * 人员新增 / 编辑页（/directory/people/new 与 /directory/people/:id/edit）。
 * 职责：无 id 走新增；有 id 先取详情回填再走编辑；提交成功后回列表。
 * 输入：URL 参数 id、useTeams（团队选项）、useProjects（项目选项）。
 * 输出：FormPage 包裹的字段与两个多选。
 * 边界：列表与表单是两个独立路由（列表页不再内嵌表单）；字段校验全部交给服务端。
 */
import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import type { PersonInput } from '../../api/people';
import { createPerson, getPerson, updatePerson } from '../../api/people';
import { errorMessage } from '../../api/client';
import { FormPage } from '../../components/FormPage';
import { MultiSelect } from '../../components/MultiSelect';
import { ErrorState, Loading } from '../../components/States';
import { invalidateDirectory, useTeams } from '../../hooks/useDirectory';
import { useProjects } from '../../hooks/useProjects';

const EMPTY_FORM: PersonInput = {
  name: '',
  username: '',
  email: '',
  phone: '',
  title: '',
  teamIds: [],
  projectIds: [],
};

const LIST_PATH = '/directory/people';

export function PersonFormPage(): JSX.Element {
  const { id } = useParams<{ id: string }>();
  const editing = Boolean(id);
  const navigate = useNavigate();

  const { teams, loading: teamsLoading, error: teamsError, reload: reloadTeams } = useTeams();
  const { projects } = useProjects();

  const [form, setForm] = useState<PersonInput>(EMPTY_FORM);
  const [loading, setLoading] = useState(editing);
  const [loadError, setLoadError] = useState('');
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState('');

  useEffect(() => {
    if (!id) {
      setForm(EMPTY_FORM);
      setLoading(false);
      return;
    }
    let cancelled = false;
    setLoading(true);
    getPerson(id)
      .then((person) => {
        if (cancelled) {
          return;
        }
        setForm({
          name: person.name,
          username: person.username ?? '',
          email: person.email,
          phone: person.phone,
          title: person.title,
          teamIds: person.teams.map((team) => team.id),
          projectIds: person.projects.map((project) => project.id),
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
  }, [id]);

  const submit = async (): Promise<void> => {
    setSaving(true);
    setNotice('');
    try {
      if (id) {
        await updatePerson(id, form);
      } else {
        await createPerson(form);
      }
      // 列表读的是模块级缓存：这里先失效，回到列表才会重新拉取。
      invalidateDirectory();
      navigate(LIST_PATH, { replace: true });
    } catch (err) {
      setNotice(errorMessage(err));
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <section className="page">
        <Loading text="正在加载人员…" />
      </section>
    );
  }
  if (loadError) {
    return (
      <section className="page">
        <div className="page-head">
          <div>
            <h1>编辑人员</h1>
            <p className="sub">加载失败</p>
          </div>
        </div>
        <ErrorState message={loadError} onRetry={reloadTeams} />
      </section>
    );
  }
  if (teamsLoading || teamsError) {
    return (
      <section className="page">
        {teamsLoading ? <Loading text="正在加载团队选项…" /> : null}
        {teamsError ? <ErrorState message={teamsError} onRetry={reloadTeams} /> : null}
      </section>
    );
  }

  return (
    <FormPage
      title={editing ? '编辑人员' : '新增人员'}
      subtitle={editing ? '修改后保存即生效，关联为全量覆盖' : '填写基本信息，并勾选所属团队与项目'}
      backTo={LIST_PATH}
      saving={saving}
      notice={notice}
      onSubmit={() => void submit()}
    >
      <div className="field-grid">
        <label className="field">
          <span>姓名 *</span>
          <input
            className="text-input"
            maxLength={40}
            value={form.name}
            onChange={(event) => setForm({ ...form, name: event.target.value })}
          />
        </label>
        <label className="field">
          <span>登录名</span>
          <input
            className="text-input"
            maxLength={64}
            placeholder="填了才能与登录账号对应"
            value={form.username}
            onChange={(event) => setForm({ ...form, username: event.target.value })}
          />
        </label>
        <label className="field">
          <span>邮箱</span>
          <input
            className="text-input"
            maxLength={120}
            value={form.email}
            onChange={(event) => setForm({ ...form, email: event.target.value })}
          />
        </label>
        <label className="field">
          <span>手机号</span>
          <input
            className="text-input"
            maxLength={40}
            value={form.phone}
            onChange={(event) => setForm({ ...form, phone: event.target.value })}
          />
        </label>
        <label className="field">
          <span>岗位</span>
          <input
            className="text-input"
            maxLength={60}
            value={form.title}
            onChange={(event) => setForm({ ...form, title: event.target.value })}
          />
        </label>
      </div>

      <MultiSelect
        label="关联团队（可多选）"
        options={teams.map((team) => ({ id: team.id, label: team.name }))}
        selected={form.teamIds}
        onChange={(teamIds) => setForm({ ...form, teamIds })}
        emptyText="还没有团队，先去「团队管理」建一个"
      />
      <MultiSelect
        label="关联项目（可多选）"
        options={projects.map((project) => ({ id: project.id, label: project.name }))}
        selected={form.projectIds}
        onChange={(projectIds) => setForm({ ...form, projectIds })}
        emptyText="还没有项目，先去「项目管理」建一个"
      />
    </FormPage>
  );
}
