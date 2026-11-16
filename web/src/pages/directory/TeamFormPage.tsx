/**
 * 团队新增 / 编辑页（/directory/teams/new 与 /directory/teams/:id/edit）。
 * 职责：无 id 走新增；有 id 先取详情回填再走编辑；提交成功后回列表。
 * 输入：URL 参数 id、usePeople（人员选项）、useProjects（项目选项）。
 * 输出：FormPage 包裹的字段与两个多选。
 * 边界：列表与表单是两个独立路由；字段校验全部交给服务端。
 */
import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import type { TeamInput } from '../../api/teams';
import { createTeam, getTeam, updateTeam } from '../../api/teams';
import { errorMessage } from '../../api/client';
import { Input } from '../../components/Controls';
import { FormPage } from '../../components/FormPage';
import { MultiSelect } from '../../components/MultiSelect';
import { ErrorState, Loading } from '../../components/States';
import { invalidateDirectory, useAllPeople } from '../../hooks/useDirectory';
import { useProjects } from '../../hooks/useProjects';

const EMPTY_FORM: TeamInput = { name: '', description: '', personIds: [], projectIds: [] };

const LIST_PATH = '/directory/teams';

export function TeamFormPage(): JSX.Element {
  const { id } = useParams<{ id: string }>();
  const editing = Boolean(id);
  const navigate = useNavigate();

  const {
    items: people,
    loading: peopleLoading,
    error: peopleError,
    reload: reloadPeople,
  } = useAllPeople();
  const { projects } = useProjects();

  const [form, setForm] = useState<TeamInput>(EMPTY_FORM);
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
    getTeam(id)
      .then((team) => {
        if (cancelled) {
          return;
        }
        setForm({
          name: team.name,
          description: team.description,
          personIds: team.persons.map((person) => person.id),
          projectIds: team.projects.map((project) => project.id),
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
        await updateTeam(id, form);
      } else {
        await createTeam(form);
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
        <Loading text="正在加载团队…" />
      </section>
    );
  }
  if (loadError) {
    return (
      <section className="page">
        <div className="page-head">
          <div>
            <h1>编辑团队</h1>
            <p className="sub">加载失败</p>
          </div>
        </div>
        <ErrorState message={loadError} onRetry={reloadPeople} />
      </section>
    );
  }
  if (peopleLoading || peopleError) {
    return (
      <section className="page">
        {peopleLoading ? <Loading text="正在加载人员选项…" /> : null}
        {peopleError ? <ErrorState message={peopleError} onRetry={reloadPeople} /> : null}
      </section>
    );
  }

  return (
    <FormPage
      title={editing ? '编辑团队' : '新建团队'}
      subtitle={editing ? '修改后保存即生效，关联为全量覆盖' : '填写团队信息，并勾选成员与项目'}
      backTo={LIST_PATH}
      saving={saving}
      notice={notice}
      onSubmit={() => void submit()}
    >
      <div className="field-grid">
        <label className="field">
          <span>团队名称 *</span>
          <Input
            className="text-input"
            maxLength={60}
            value={form.name}
            onChange={(event) => setForm({ ...form, name: event.target.value })}
          />
        </label>
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

      <MultiSelect
        label="关联人员（可多选）"
        options={people.map((person) => ({ id: person.id, label: person.name }))}
        selected={form.personIds}
        onChange={(personIds) => setForm({ ...form, personIds })}
        emptyText="还没有人员，先去「人员管理」加一个"
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
