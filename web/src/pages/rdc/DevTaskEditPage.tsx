/**
 * 编辑研发任务页（/rdc/dev/:id/edit）。
 * 职责：修改任务名、换绑本公司需求与两条子任务的备注，提交后回列表。
 * 输入：路由 id、详情接口回填值、本公司需求下拉与用户输入。
 * 输出：updateDevTask 请求与跳回列表；页面明确提示状态要去详情抽屉变更。
 * 边界：**不渲染任何 status 控件、不提交 status 字段**；SKIPPED 在本页不可见也不可恢复；
 *       详情加载失败或越界由 ErrorState 呈现，不放空表单让人盲填。
 */
import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { errorMessage } from '../../api/client';
import type { Requirement } from '../../api/rdc';
import { listRequirements } from '../../api/rdc';
import type { DevSubtaskNoteUpdate, DevTaskDetail } from '../../api/rdcDev';
import { getDevTask, updateDevTask } from '../../api/rdcDev';
import { Input, TextArea } from '../../components/Controls';
import { FormPage } from '../../components/FormPage';
import { Select } from '../../components/Select';
import { ErrorState, Loading } from '../../components/States';
import { devKindLabel } from '../../utils/format';

const LIST_PATH = '/rdc/dev';

interface EditForm {
  title: string;
  requirementID: string;
  notes: Record<string, string>;
}

const EMPTY_FORM: EditForm = { title: '', requirementID: '', notes: {} };

export function DevTaskEditPage(): JSX.Element {
  const { id = '' } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [detail, setDetail] = useState<DevTaskDetail | null>(null);
  const [form, setForm] = useState<EditForm>(EMPTY_FORM);
  const [requirements, setRequirements] = useState<Requirement[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState('');

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setLoadError('');
    Promise.all([getDevTask(id), listRequirements({ pageSize: 0 })])
      .then(([task, requirementPage]) => {
        if (cancelled) {
          return;
        }
        const notes: Record<string, string> = {};
        task.subtasks.forEach((subtask) => {
          notes[subtask.id] = subtask.note;
        });
        setDetail(task);
        setForm({ title: task.title, requirementID: task.requirementId, notes });
        setRequirements(requirementPage.items);
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
    const title = form.title.trim();
    if (!title) {
      setNotice('请填写任务名称');
      return;
    }
    if (title.length > 100) {
      setNotice('任务名称不能超过 100 字');
      return;
    }
    if (!form.requirementID) {
      setNotice('请选择来源需求');
      return;
    }
    const subtasks: DevSubtaskNoteUpdate[] = (detail?.subtasks ?? []).map((subtask) => ({
      id: subtask.id,
      note: form.notes[subtask.id] ?? '',
    }));
    if (subtasks.some((item) => item.note.length > 500)) {
      setNotice('子任务备注不能超过 500 字');
      return;
    }
    setSaving(true);
    setNotice('');
    try {
      await updateDevTask(id, {
        title,
        requirementId: form.requirementID,
        subtasks,
      });
      navigate(LIST_PATH, { replace: true });
    } catch (err) {
      setNotice(errorMessage(err));
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <section className="page">
        <Loading text="正在读取任务资料…" />
      </section>
    );
  }
  if (loadError || !detail) {
    return (
      <section className="page">
        <ErrorState
          message={loadError || '任务不存在或已被移除'}
          onRetry={() => navigate(LIST_PATH)}
        />
      </section>
    );
  }

  return (
    <FormPage
      title="编辑研发任务"
      subtitle="只修改资料：任务名、绑定需求与子任务备注"
      backTo={LIST_PATH}
      saving={saving}
      notice={notice}
      onSubmit={() => void submit()}
    >
      <div className="field-grid">
        <label className="field field-block">
          <span className="field-label">任务名称 *</span>
          <Input
            className="text-input"
            maxLength={100}
            value={form.title}
            onChange={(event) => setForm({ ...form, title: event.target.value })}
          />
        </label>
        <label className="field field-block">
          <span className="field-label">来源需求 *</span>
          <Select
            value={form.requirementID}
            searchable
            placeholder="选择本公司需求"
            options={requirements.map((item) => ({
              value: item.id,
              label: `${item.reqKey} · ${item.title}`,
            }))}
            onChange={(next) => setForm({ ...form, requirementID: next })}
          />
        </label>
        {detail.subtasks.map((subtask) => (
          <label className="field field-block" key={subtask.id}>
            <span className="field-label">{devKindLabel(subtask.kind)}子任务备注</span>
            <TextArea
              className="text-input"
              rows={3}
              maxLength={500}
              value={form.notes[subtask.id] ?? ''}
              onChange={(event) =>
                setForm({
                  ...form,
                  notes: { ...form.notes, [subtask.id]: event.target.value },
                })
              }
            />
          </label>
        ))}
        <p className="cell-sub field-block rdc-dev-hint">
          状态请到详情抽屉用「推进状态到 …」按钮变更；本页不提供状态控件，已跳过的子任务也无法恢复。
        </p>
      </div>
    </FormPage>
  );
}
