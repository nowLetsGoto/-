/**
 * 新建研发任务页（/rdc/dev/new）。
 * 职责：选择本公司来源需求并填写任务名，提交后由服务端自动拆出前端 / 后端两条待开发子任务。
 * 输入：用户填写的任务名、来源需求下拉（listRequirements pageSize=0）。
 * 输出：createDevTask 请求与回列表提示；不提供子任务类型 / 初始状态入口。
 * 边界：需求必选且只列本公司；创建人、归属公司与整体状态全部由服务端推导；
 *       本页不写 status，也不触发任何真实研发动作。
 */
import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { errorMessage } from '../../api/client';
import type { Requirement } from '../../api/rdc';
import { listRequirements } from '../../api/rdc';
import { createDevTask } from '../../api/rdcDev';
import { Input } from '../../components/Controls';
import { FormPage } from '../../components/FormPage';
import { Select } from '../../components/Select';

const LIST_PATH = '/rdc/dev';

export function DevTaskFormPage(): JSX.Element {
  const navigate = useNavigate();
  const [title, setTitle] = useState('');
  const [requirementID, setRequirementID] = useState('');
  const [requirements, setRequirements] = useState<Requirement[]>([]);
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState('');

  useEffect(() => {
    let cancelled = false;
    listRequirements({ pageSize: 0 })
      .then((data) => {
        if (!cancelled) {
          setRequirements(data.items);
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setNotice(errorMessage(err));
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const submit = async (): Promise<void> => {
    const trimmed = title.trim();
    if (!requirementID) {
      setNotice('请选择来源需求');
      return;
    }
    if (!trimmed) {
      setNotice('请填写任务名称');
      return;
    }
    if (trimmed.length > 100) {
      setNotice('任务名称不能超过 100 字');
      return;
    }
    setSaving(true);
    setNotice('');
    try {
      const created = await createDevTask({ title: trimmed, requirementId: requirementID });
      navigate(LIST_PATH, { replace: true, state: { createdTask: created.title } });
    } catch (err) {
      setNotice(errorMessage(err));
      setSaving(false);
    }
  };

  return (
    <FormPage
      title="新建研发任务"
      subtitle="任务必须绑定一条本公司需求；创建后自动拆出前端 / 后端两条待开发子任务"
      backTo={LIST_PATH}
      saving={saving}
      notice={notice}
      onSubmit={() => void submit()}
    >
      <div className="field-grid">
        <label className="field field-block">
          <span className="field-label">来源需求 *</span>
          <Select
            value={requirementID}
            placeholder="选择本公司需求"
            searchable
            options={requirements.map((item) => ({
              value: item.id,
              label: `${item.reqKey} · ${item.title}`,
            }))}
            onChange={(next) => setRequirementID(next)}
          />
        </label>
        <label className="field field-block">
          <span className="field-label">任务名称 *</span>
          <Input
            className="text-input"
            maxLength={100}
            placeholder="例如：前端重构与接口联调"
            value={title}
            onChange={(event) => setTitle(event.target.value)}
          />
        </label>
        <p className="cell-sub field-block">
          创建后可在详情抽屉里手动推进状态；系统不会触发 Agent、Git 或测试动作。
        </p>
      </div>
    </FormPage>
  );
}
