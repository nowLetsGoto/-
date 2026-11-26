/**
 * 上传页（P-01）。
 * 职责：选文件触发 stage、按 touched 优先级带出解析值、表单始终可编辑、一次点击完成发布、成功卡片。
 * 输入：用户选中的 .apk / .ipa / .hap 与表单值；输出：stage / commit 两个请求与成功后的绝对下载地址。
 * 边界：touched 只是前端本地 UI 状态，不进入 API 契约；commit 只发 8 字段 JSON，不携带文件与 metaSource；
 *       stage 对用户透明，不是两步发布，用户全程只点一次「上传并发布」；不提供上传后编辑。
 */
import { useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import type {
  Build,
  CommitBuildRequest,
  Environment,
  Platform,
  StageResult,
} from '../../api/builds';
import {
  abortUpload,
  commitBuild,
  isShareableUrl,
  putToR2,
  requestUploadTicket,
  stageBuild,
  stageFromR2,
} from '../../api/builds';
import { ApiError, errorMessage } from '../../api/client';
import { Button, Input, Segmented, TextArea } from '../../components/Controls';
import { FileDropzone } from '../../components/FileDropzone';
import { useEnvironment } from '../../hooks/useEnvironment';
import { useCopyText } from '../../hooks/useCopyText';
import { useCurrentProject } from '../../hooks/useProjects';
import { ENVIRONMENTS, PLATFORMS, platformFromFileName } from '../../utils/format';

type ParsedField = 'appName' | 'packageId' | 'version' | 'buildNumber';
type TextField = ParsedField | 'changelog';
const PARSED_FIELDS: ReadonlyArray<ParsedField> = [
  'appName',
  'packageId',
  'version',
  'buildNumber',
];

/**
 * 判断是否属于"对象存储未启用"，只有这种情况才静默回落到 multipart 上传。
 * 服务端此时返回 400 且文案固定；其余错误（网络、大小不符、签名过期）必须让用户看到。
 */
function isDirectUploadUnavailable(err: unknown): boolean {
  return err instanceof ApiError && err.code === 400 && err.message.includes('对象存储');
}

interface FormState {
  environment: Environment;
  platform: Platform;
  appName: string;
  packageId: string;
  version: string;
  buildNumber: string;
  changelog: string;
}

/** 表单初始值：环境跟随顶栏的全局环境，避免「看着生产、传到测试」的错位。 */
const initialForm = (environment: Environment): FormState => ({
  environment,
  platform: 'android',
  appName: '',
  packageId: '',
  version: '',
  buildNumber: '',
  changelog: '',
});

export function UploadPage(): JSX.Element {
  const copy = useCopyText();
  const navigate = useNavigate();
  const { key } = useCurrentProject();
  const [globalEnvironment] = useEnvironment();
  const [form, setForm] = useState<FormState>(() => initialForm(globalEnvironment));
  // touched 是前端局部状态，仅用于 stage 异步返回时不覆盖用户已输入字段；不进 API 契约、服务端不做合并。
  const touchedRef = useRef<Set<TextField>>(new Set());
  const [file, setFile] = useState<File | null>(null);
  const [stageToken, setStageToken] = useState('');
  const [parsing, setParsing] = useState(false);
  // 直传进度（0-100）。-1 表示未在直传；走 multipart 通道时保持 -1。
  const [uploadPercent, setUploadPercent] = useState(-1);
  // 记住本次直传的凭证，用于改选文件时清理 R2 上的临时对象。
  const uploadTokenRef = useRef('');
  const [submitting, setSubmitting] = useState(false);
  const [warnings, setWarnings] = useState<ReadonlyArray<string>>([]);
  const [error, setError] = useState('');
  const [created, setCreated] = useState<Build | null>(null);
  const [copied, setCopied] = useState(false);

  const setField = (key: keyof FormState, value: string): void => {
    if (key !== 'environment' && key !== 'platform') {
      touchedRef.current.add(key as TextField);
    }
    setForm((previous) => ({ ...previous, [key]: value }));
  };

  const applyStageResult = (result: StageResult): void => {
    setStageToken(result.stageToken);
    setWarnings(result.warnings);
    // 仅未 touched 的字段填入解析值；用户改过的字段即便重新选文件也不被覆盖。
    setForm((previous) => {
      const next = { ...previous };
      for (const key of PARSED_FIELDS) {
        if (!touchedRef.current.has(key)) {
          next[key] = result.parsed[key];
        }
      }
      return next;
    });
  };

  /** 改选文件时先放弃上一次直传，避免 R2 上留下孤立对象。 */
  const discardPendingUpload = async (): Promise<void> => {
    const token = uploadTokenRef.current;
    uploadTokenRef.current = '';
    if (!token) return;
    try {
      await abortUpload(token);
    } catch {
      // 清理失败不影响用户：服务端有过期兜底。
    }
  };

  const handleSelect = async (selected: File, hint: Platform | null): Promise<void> => {
    await discardPendingUpload();
    setFile(selected);
    setCreated(null);
    setError('');
    setWarnings([]);
    setStageToken('');
    setUploadPercent(-1);
    setParsing(true);
    if (hint) {
      setForm((previous) => ({ ...previous, platform: hint }));
    }
    try {
      // 优先直传 R2：字节不经过本机与 Tunnel，因此不受 100 MB 请求体上限约束。
      // 未启用对象存储时服务端会在取票据阶段拒绝，此处自动回落到 multipart。
      let result: StageResult | null = null;
      try {
        // project 一并传入：服务端据此把"租户/项目"编进 R2 对象 key。
        const ticket = await requestUploadTicket(selected, key);
        uploadTokenRef.current = ticket.uploadToken;
        setUploadPercent(0);
        await putToR2(ticket, selected, setUploadPercent);
        result = await stageFromR2(ticket.uploadToken);
        uploadTokenRef.current = '';
      } catch (directErr) {
        // 只在"未启用对象存储"时静默回落；其余错误（网络、大小不符）要提示用户。
        if (!isDirectUploadUnavailable(directErr)) {
          throw directErr;
        }
        result = await stageBuild(selected);
      }
      applyStageResult(result);
    } catch (err) {
      setError(errorMessage(err));
      setFile(null);
    } finally {
      setUploadPercent(-1);
      setParsing(false);
    }
  };

  const handleRemove = (): void => {
    void discardPendingUpload();
    setFile(null);
    setStageToken('');
    setWarnings([]);
    setError('');
    setUploadPercent(-1);
    setCreated(null);
    setParsing(false);
  };

  const handleSubmit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault();
    if (!file || !stageToken) {
      setError('请先选择受支持的安装包（.apk / .ipa / .hap）');
      return;
    }
    if (parsing) {
      setError('正在识别包信息，请稍候');
      return;
    }
    const hint = platformFromFileName(file.name);
    if (hint && hint !== form.platform) {
      setError('所选平台与文件类型不一致');
      return;
    }

    setSubmitting(true);
    setError('');
    // 恰好 9 个字段：项目 + token + 表单最终值。不含文件、不含 metaSource（由服务端派生）。
    const payload: CommitBuildRequest = {
      project: key,
      stageToken,
      environment: form.environment,
      platform: form.platform,
      appName: form.appName,
      packageId: form.packageId,
      version: form.version,
      buildNumber: form.buildNumber,
      changelog: form.changelog,
    };
    try {
      // 表单即最终真值：这里不发文件，服务端也不会用解析值覆盖任何字段。
      const build = await commitBuild(payload);
      setCreated(build);
    } catch (err) {
      const message = errorMessage(err);
      if (err instanceof ApiError && err.code === 400 && message.includes('暂存')) {
        // token 失效：回到选文件步骤，让用户重新选择一次文件。
        setFile(null);
        setStageToken('');
        setError(`${message}，请重新选择文件`);
      } else {
        setError(message);
      }
    } finally {
      setSubmitting(false);
    }
  };

  const copyText = async (text: string): Promise<void> => {
    if (await copy(text)) {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } else {
      setError('复制失败，请手动选中地址复制');
    }
  };

  const resetAll = (): void => {
    setForm(initialForm(globalEnvironment));
    touchedRef.current = new Set();
    handleRemove();
    setCopied(false);
  };

  if (created) {
    return (
      <section className="page page-narrow">
        <div className="card success-card">
          <h1>上传成功</h1>
          <p className="muted">下载页绝对地址，可直接转发给同事：</p>
          <div className="copy-row">
            <Input
              className="text-input"
              readOnly
              value={created.downloadPageUrl}
              aria-label="下载页地址"
            />
            <Button
              className="button button-primary"
              type="button"
              onClick={() => void copyText(created.downloadPageUrl)}
            >
              {copied ? '已复制' : '复制地址'}
            </Button>
          </div>
          {!isShareableUrl(created.downloadPageUrl) ? (
            <p className="notice">当前对外基址不可被手机访问，请配置 PUBLIC_BASE_URL 后再扫码</p>
          ) : null}
          <div className="actions">
            {/* 优先跳分享页（免登录）；无有效分享时退回内部预览（需登录）。 */}
            <Button
              className="button button-primary"
              type="button"
              onClick={() => navigate(created.downloadSharePath || created.downloadPagePath)}
            >
              去下载页
            </Button>
            <Link className="button button-ghost" to={`/p/${key}/packages`}>
              返回列表
            </Link>
            <Button className="button button-ghost" type="button" onClick={resetAll}>
              再上传一个
            </Button>
          </div>
        </div>
      </section>
    );
  }

  return (
    <section className="page page-narrow">
      <div className="page-head">
        <h1>上传 App 包</h1>
        <Link className="button button-ghost" to={`/p/${key}/packages`}>
          包列表
        </Link>
      </div>

      <form className="card form-card" onSubmit={(event) => void handleSubmit(event)}>
        <div className="form-section">
          <h2>1. 选择安装包</h2>
          <FileDropzone
            file={file}
            disabled={parsing || submitting}
            onSelect={(f, p) => void handleSelect(f, p)}
            onRemove={handleRemove}
          />
          {parsing ? (
            <p className="notice">
              {uploadPercent >= 0 && uploadPercent < 100
                ? `正在上传到对象存储… ${uploadPercent}%（大包请耐心等待，可后台进行）`
                : '解析中… 正在识别应用名、包名与版本号'}
            </p>
          ) : null}
          {warnings.length > 0 ? <p className="notice">部分信息未自动识别，可手动修正</p> : null}
        </div>

        <div className="form-section">
          <h2>2. 必填信息</h2>
          <label className="field-label">环境</label>
          <Segmented<Environment>
            aria-label="环境"
            value={form.environment}
            disabled={submitting}
            options={ENVIRONMENTS.map((item) => ({
              value: item.value,
              // 环境名本身就是英文标识，用小字并排显示，比单独一行说明更省空间。
              label: (
                <span className="segment-label">
                  {item.label}
                  <small>{item.value}</small>
                </span>
              ),
            }))}
            onChange={(value) => setField('environment', value)}
          />

          <label className="field-label">平台</label>
          <Segmented<Platform>
            aria-label="平台"
            value={form.platform}
            disabled={submitting}
            options={PLATFORMS.map((item) => ({ value: item.value, label: item.label }))}
            onChange={(value) => setField('platform', value)}
          />
        </div>

        <div className="form-section">
          <h2>3. 选填信息</h2>
          <p className="muted">自动识别的字段始终可以修改；留空由服务端用文件名 / 占位值兜底。</p>
          <div className="field-grid">
            <label className="field">
              <span>应用名</span>
              <Input
                className="text-input"
                maxLength={100}
                value={form.appName}
                onChange={(e) => setField('appName', e.target.value)}
              />
            </label>
            <label className="field">
              <span>包名 / Bundle ID</span>
              <Input
                className="text-input"
                maxLength={100}
                value={form.packageId}
                onChange={(e) => setField('packageId', e.target.value)}
              />
            </label>
            <label className="field">
              <span>版本号</span>
              <Input
                className="text-input"
                maxLength={50}
                value={form.version}
                onChange={(e) => setField('version', e.target.value)}
              />
            </label>
            <label className="field">
              <span>构建号</span>
              <Input
                className="text-input"
                maxLength={50}
                value={form.buildNumber}
                onChange={(e) => setField('buildNumber', e.target.value)}
              />
            </label>
          </div>
          <label className="field">
            <span>更新说明</span>
            <TextArea
              className="text-input"
              rows={3}
              maxLength={2000}
              value={form.changelog}
              onChange={(e) => setField('changelog', e.target.value)}
            />
          </label>
        </div>

        {error ? (
          <p className="field-error" role="alert">
            {error}
          </p>
        ) : null}
        <Button
          className="button button-primary button-block"
          type="submit"
          disabled={parsing || submitting || !file || !stageToken}
        >
          {submitting ? '正在发布…' : '上传并发布'}
        </Button>
      </form>
    </section>
  );
}
