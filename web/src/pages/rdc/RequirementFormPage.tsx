/**
 * 新建需求页（研发中心 → 需求中心 → /new）。
 * 职责：采集标题/描述/状态/验收标准/所属项目，选择多张图片，提交后回列表并提示需求编号。
 * 输入：用户填写的 5 个契约文本字段、本公司项目下拉与 Upload.Dragger 选择的图片。
 * 输出：createRequirement 的 multipart 请求与跳回列表；图片区显示缩略图并可移除。
 * 边界：编号/创建人/归属公司由服务端生成；项目必选、图片格式/体积/数量前端先拦一道，
 *       后端仍会复校；beforeUpload 返回 false 禁止组件自动上传；不提供编辑/删除。
 */
import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Upload } from '@seakoi/console-kit';
import type { RcFile, UploadFile, UploadProps } from '@seakoi/console-kit';
import type { RequirementInput } from '../../api/rdc';
import { createRequirement } from '../../api/rdc';
import { errorMessage } from '../../api/client';
import { Input, TextArea } from '../../components/Controls';
import { FormPage } from '../../components/FormPage';
import { Select } from '../../components/Select';
import { useProjects } from '../../hooks/useProjects';
import {
  RDC_IMAGE_ERRORS,
  REQUIREMENT_STATUS_OPTIONS,
  requirementStatusLabel,
} from '../../utils/format';
import { validateImageFiles } from '../../utils/rdcUpload';

const LIST_PATH = '/rdc/requirements';

const EMPTY_FORM: RequirementInput = {
  title: '',
  description: '',
  status: 'pending_review',
  acceptanceCriteria: '',
  projectId: '',
};

export function RequirementFormPage(): JSX.Element {
  const navigate = useNavigate();
  const [form, setForm] = useState<RequirementInput>(EMPTY_FORM);
  const [fileList, setFileList] = useState<UploadFile[]>([]);
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState('');
  const { projects } = useProjects();

  const projectOptions = projects.map((project) => ({ value: project.id, label: project.name }));

  /**
   * 只拦不传：返回 false 让 antd 把文件留在受控 fileList 里但不发请求；
   * 非法文件返回 LIST_IGNORE，不进入后续 FormData。
   */
  const beforeUpload = (file: RcFile, batch: RcFile[]): boolean | string => {
    const error = validateImageFiles(batch, fileList.length);
    if (error) {
      setNotice(error);
      return Upload.LIST_IGNORE;
    }
    // file 参数由 antd 保证属于 batch；这里显式引用避免 lint 报未使用并保持语义清晰。
    void file;
    setNotice('');
    return false;
  };

  const onUploadChange: UploadProps['onChange'] = ({ fileList: next }) => {
    setFileList(next);
  };

  const submit = async (): Promise<void> => {
    const title = form.title.trim();
    const description = form.description.trim();
    if (!form.projectId) {
      setNotice('请选择所属项目');
      return;
    }
    if (!title) {
      setNotice('请填写标题');
      return;
    }
    if (title.length > 100) {
      setNotice('标题不能超过 100 字');
      return;
    }
    if (!description) {
      setNotice('请填写描述');
      return;
    }
    if (description.length > 4000) {
      setNotice('描述不能超过 4000 字');
      return;
    }
    if (form.acceptanceCriteria && form.acceptanceCriteria.trim().length > 2000) {
      setNotice('验收标准不能超过 2000 字');
      return;
    }
    const images = fileList
      .map((item) => item.originFileObj)
      .filter((item): item is RcFile => Boolean(item));
    const imageError = validateImageFiles(images);
    if (imageError) {
      setNotice(imageError);
      return;
    }

    setSaving(true);
    setNotice('');
    try {
      const created = await createRequirement(
        {
          title,
          description,
          status: form.status,
          acceptanceCriteria: form.acceptanceCriteria?.trim() ?? '',
          projectId: form.projectId,
        },
        images,
      );
      // 编号由服务端生成，通过路由 state 带回列表页展示，避免再发一次详情请求。
      navigate(LIST_PATH, { replace: true, state: { createdKey: created.reqKey } });
    } catch (err) {
      setNotice(errorMessage(err));
      setSaving(false);
    }
  };

  /** 下拉值来自契约选项表，用它做一次回查，避免把任意字符串塞进状态字段。 */
  const changeStatus = (value: string): void => {
    const matched = REQUIREMENT_STATUS_OPTIONS.find((option) => option.value === value);
    setForm((current) => ({
      ...current,
      status: matched?.value ?? 'pending_review',
    }));
  };

  return (
    <FormPage
      title="新建需求"
      subtitle="需求编号由服务端生成，创建后不可修改；每条需求必须归属一个本公司项目"
      backTo={LIST_PATH}
      saving={saving}
      notice={notice}
      onSubmit={() => void submit()}
    >
      <div className="field-grid">
        <label className="field">
          <span>所属项目 *</span>
          <Select
            value={form.projectId ?? ''}
            options={projectOptions}
            placeholder="选择本公司项目"
            onChange={(value) => setForm({ ...form, projectId: value })}
          />
        </label>
        <label className="field">
          <span>状态</span>
          <Select
            value={form.status ?? 'pending_review'}
            options={REQUIREMENT_STATUS_OPTIONS.map((option) => ({
              value: option.value,
              label: requirementStatusLabel(option.value),
            }))}
            onChange={changeStatus}
          />
        </label>
        <label className="field field-block">
          <span className="field-label">标题 *</span>
          <Input
            className="text-input"
            maxLength={100}
            placeholder="一句话说清要做什么"
            value={form.title}
            onChange={(event) => setForm({ ...form, title: event.target.value })}
          />
        </label>
        <label className="field field-block">
          <span className="field-label">描述 *</span>
          <TextArea
            className="text-input"
            rows={6}
            maxLength={4000}
            placeholder="背景、目标与范围"
            value={form.description}
            onChange={(event) => setForm({ ...form, description: event.target.value })}
          />
        </label>
        <label className="field field-block">
          <span className="field-label">验收标准（选填）</span>
          <TextArea
            className="text-input"
            rows={4}
            maxLength={2000}
            placeholder="怎样算做完"
            value={form.acceptanceCriteria}
            onChange={(event) => setForm({ ...form, acceptanceCriteria: event.target.value })}
          />
        </label>
        <div className="field field-block">
          <span className="field-label">图片附件（选填，最多 9 张）</span>
          <Upload.Dragger
            className="rdc-upload-dragger"
            accept="image/png,image/jpeg,image/webp,image/gif"
            multiple
            fileList={fileList}
            beforeUpload={beforeUpload}
            onChange={onUploadChange}
            showUploadList={{ showPreviewIcon: false, showDownloadIcon: false }}
          >
            <p>点击或拖拽图片到这里，可一次选择多张</p>
            <p className="cell-sub">
              {RDC_IMAGE_ERRORS.format}；{RDC_IMAGE_ERRORS.size}；{RDC_IMAGE_ERRORS.count}
            </p>
          </Upload.Dragger>
        </div>
      </div>
    </FormPage>
  );
}
