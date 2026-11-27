/**
 * 平台集成页：按公司范围与 settings.read/write 权限读取、维护通知和上传凭证。
 * 职责：配置 Webhook（开关 / 地址 / 签名密钥 / 推送字段）并测试投递；展示外部上传接口、轮换 API Key。
 * 输入：GET /api/v1/settings、当前项目 key（仅用于外部接口示例的占位值）；
 *       输出：PUT 保存、POST 测试、POST 轮换 Key、复制 API Key 与调用示例。
 * 边界：不做字段合法性判断（服务端校验）、不缓存设置副本；
 *       切换 Tab 不重复取数，只有保存后以服务端返回的值为准；
 *       配置归属由登录身份决定，超管维护平台兜底；当前项目只用于调用示例。
 */
import { useEffect, useState } from 'react';
import type { Settings, WebhookConfig, WebhookField, WebhookTestResult } from '../../api/settings';
import type { WebhookProvider } from '../../api/settings';
import { getSettings, rotateApiKey, testWebhook, updateWebhook } from '../../api/settings';
import { errorMessage } from '../../api/client';
import { useCurrentProject } from '../../hooks/useProjects';
import { useCopyText } from '../../hooks/useCopyText';
import { useMe } from '../../hooks/useMe';
import { can } from '../../utils/permission';
import { WEBHOOK_PROVIDERS } from '../../utils/format';
import { Button, Checkbox, Input, Segmented } from '../../components/Controls';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { ErrorState, Loading } from '../../components/States';

type Tab = 'webhook' | 'api';

export function SettingsPage(): JSX.Element {
  const copyText = useCopyText();
  const { me } = useMe();
  const canWrite = can(me, 'settings.write');
  // 示例里的 project 必须取**真实解析出来的项目**：useCurrentProject().key 可能来自 URL 或
  // localStorage 里残留的失效值（此时 key 有值但 project 为 null），那样照抄示例就会 400。
  const { project } = useCurrentProject();
  const [tab, setTab] = useState<Tab>('webhook');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [data, setData] = useState<Settings | null>(null);
  const [form, setForm] = useState<WebhookConfig | null>(null);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [notice, setNotice] = useState('');
  const [testResult, setTestResult] = useState<WebhookTestResult | null>(null);

  useEffect(() => {
    let cancelled = false;
    getSettings()
      .then((settings) => {
        if (!cancelled) {
          setData(settings);
          setForm(settings.webhook);
          setLoading(false);
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setError(errorMessage(err));
          setLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  /** 用服务端返回的权威值覆盖本地副本，避免保存后界面与库里不一致。 */
  const applySettings = (settings: Settings): void => {
    setData(settings);
    setForm(settings.webhook);
  };

  const patchForm = (patch: Partial<WebhookConfig>): void => {
    setForm((previous) => (previous ? { ...previous, ...patch } : previous));
    setNotice('');
    setTestResult(null);
  };

  const toggleField = (field: WebhookField): void => {
    if (!form) {
      return;
    }
    const fields = form.fields.includes(field)
      ? form.fields.filter((item) => item !== field)
      : [...form.fields, field];
    patchForm({ fields });
  };

  const save = async (): Promise<void> => {
    if (!form || saving || !canWrite) {
      return;
    }
    setSaving(true);
    setNotice('');
    try {
      applySettings(await updateWebhook(form));
      setTestResult(null);
      setNotice('已保存');
    } catch (err) {
      setNotice(errorMessage(err));
    } finally {
      setSaving(false);
    }
  };

  const runTest = async (): Promise<void> => {
    if (testing || !canWrite) {
      return;
    }
    setTesting(true);
    setTestResult(null);
    try {
      setTestResult(await testWebhook());
    } catch (err) {
      setNotice(errorMessage(err));
    } finally {
      setTesting(false);
    }
  };

  const copy = async (text: string, message: string): Promise<void> => {
    setNotice((await copyText(text)) ? message : '浏览器未允许自动复制，请手动选中复制');
  };

  if (loading) {
    return (
      <section className="page">
        <div className="page-head">
          <div>
            <h1>平台集成</h1>
            <p className="sub">Webhook 推送与外部上传接口</p>
          </div>
        </div>
        <Loading text="正在读取设置…" />
      </section>
    );
  }

  if (error || !data || !form) {
    return (
      <section className="page">
        <div className="page-head">
          <div>
            <h1>平台集成</h1>
            <p className="sub">Webhook 推送与外部上传接口</p>
          </div>
        </div>
        <ErrorState message={error || '设置加载失败'} onRetry={() => window.location.reload()} />
      </section>
    );
  }

  return (
    <section className="page">
      <div className="page-head">
        <div>
          <h1>平台集成</h1>
          <p className="sub">Webhook 推送与外部上传接口</p>
          {me ? (
            <p className="settings-hint">
              配置归属：
              {me.isSuperAdmin || me.accountLevel === 'super_admin'
                ? '平台兜底配置'
                : me.tenantName || me.tenantId}
              {me.isSuperAdmin || me.accountLevel === 'super_admin'
                ? '。CI 上传请使用公司管理员生成的 API Key。'
                : '。本页 API Key 自动归属于该公司，无需额外绑定。'}
            </p>
          ) : null}
        </div>
      </div>

      <div className="settings-tabs">
        <Segmented<Tab>
          aria-label="平台集成模块"
          value={tab}
          options={[
            { value: 'webhook', label: 'Webhook' },
            { value: 'api', label: '外部 API' },
          ]}
          onChange={setTab}
        />
      </div>

      {tab === 'webhook' ? (
        <WebhookPanel
          form={form}
          data={data}
          saving={saving}
          testing={testing}
          notice={notice}
          testResult={testResult}
          canWrite={canWrite}
          onPatch={patchForm}
          onToggleField={toggleField}
          onSave={() => void save()}
          onTest={() => void runTest()}
        />
      ) : (
        <ApiPanel
          configured={data.apiKeyConfigured}
          projectKey={project?.key ?? 'default'}
          canWrite={canWrite}
          onCopy={(text, message) => void copy(text, message)}
        />
      )}
    </section>
  );
}

interface WebhookPanelProps {
  form: WebhookConfig;
  data: Settings;
  saving: boolean;
  testing: boolean;
  notice: string;
  testResult: WebhookTestResult | null;
  canWrite: boolean;
  onPatch: (patch: Partial<WebhookConfig>) => void;
  onToggleField: (field: WebhookField) => void;
  onSave: () => void;
  onTest: () => void;
}

/** Webhook 配置面板：只负责渲染与回调，状态由页面持有。 */
function WebhookPanel(props: WebhookPanelProps): JSX.Element {
  const { form, data, saving, testing, notice, testResult, canWrite } = props;
  const feishu = form.provider === 'feishu';
  const dirty = JSON.stringify(form) !== JSON.stringify(data.webhook);

  return (
    <div className="card settings-card">
      <div className="settings-row">
        <div>
          <div className="settings-label">启用 Webhook</div>
          <p className="settings-hint">发布成功后，把下面勾选的包信息推送到接收地址。</p>
        </div>
        {canWrite ? (
          <Button
            type="button"
            role="switch"
            aria-checked={form.enabled}
            aria-label="启用 Webhook"
            className={form.enabled ? 'switch switch-on' : 'switch'}
            onClick={() => props.onPatch({ enabled: !form.enabled })}
          >
            <span className="switch-knob" aria-hidden="true" />
          </Button>
        ) : null}
      </div>

      <div className="settings-field">
        <span>通知类型</span>
        <Segmented<WebhookProvider>
          aria-label="通知类型"
          options={[...WEBHOOK_PROVIDERS]}
          value={form.provider ?? 'generic'}
          disabled={!canWrite || saving || testing}
          onChange={(provider) =>
            props.onPatch({
              provider,
              fields:
                provider === 'feishu' && !form.fields.includes('downloadPageUrl')
                  ? [...form.fields, 'downloadPageUrl']
                  : form.fields,
            })
          }
        />
        <p className="settings-hint">
          {feishu
            ? '发布成功后发送飞书卡片，点击“打开下载页”即可查看、下载本次版本，无需登录。'
            : '向接收服务发送通用 JSON，由接收服务自行处理。'}
        </p>
      </div>

      <label className="settings-field">
        <span>{feishu ? '飞书机器人 Webhook 地址' : '接收地址'}</span>
        <Input
          className="text-input"
          type="url"
          inputMode="url"
          placeholder={
            feishu
              ? 'https://open.feishu.cn/open-apis/bot/v2/hook/…'
              : 'https://example.com/hooks/dandelion'
          }
          value={form.url}
          disabled={!canWrite || saving || testing}
          onChange={(event) => props.onPatch({ url: event.target.value })}
        />
      </label>

      <label className="settings-field">
        <span>{feishu ? '飞书机器人签名密钥（启用签名校验时填写）' : '签名密钥（可选）'}</span>
        <Input
          className="text-input"
          type="password"
          autoComplete="new-password"
          placeholder={
            feishu
              ? '粘贴飞书机器人“签名校验”中提供的密钥'
              : '填写后请求头会带 X-Dandelion-Signature'
          }
          value={form.secret}
          disabled={!canWrite || saving || testing}
          onChange={(event) => props.onPatch({ secret: event.target.value })}
        />
        <em className="settings-hint">
          {feishu
            ? '此处填写飞书签名密钥，与 CI 上传的 API Key 不同；如配置了机器人关键词，可填写“应用更新提醒”。'
            : '接收端用同一密钥做 HMAC-SHA256，即可确认消息来自本平台。'}
        </em>
      </label>

      <div className="settings-field">
        <span>推送字段</span>
        <div className="field-picker">
          {data.availableFields.map((option) => (
            <Checkbox
              key={option.key}
              className={
                form.fields.includes(option.key) ? 'field-check field-check-on' : 'field-check'
              }
              checked={form.fields.includes(option.key)}
              disabled={
                !canWrite || saving || testing || (feishu && option.key === 'downloadPageUrl')
              }
              onChange={() => props.onToggleField(option.key)}
            >
              {option.label}
            </Checkbox>
          ))}
        </div>
        <em className="settings-hint">
          {feishu
            ? '下载页地址为必选项，完整分享凭证会自动放入卡片按钮；其它信息按勾选显示。'
            : '只推送勾选的字段；勾上“下载页地址”接收端就能直接转发扫码链接。'}
        </em>
      </div>

      <div className="settings-actions">
        {canWrite ? (
          <>
            <Button
              className="button button-primary"
              type="button"
              onClick={props.onSave}
              disabled={saving || testing}
            >
              {saving ? '保存中…' : '保存'}
            </Button>
            <Button
              className="button"
              type="button"
              onClick={props.onTest}
              disabled={testing || saving || !form.enabled || dirty}
            >
              {testing ? '发送中…' : '发送测试消息'}
            </Button>
          </>
        ) : null}
        {notice ? <span className="settings-notice">{notice}</span> : null}
      </div>

      <p className="settings-hint">
        {dirty
          ? '配置已修改，请先保存，再发送测试消息。'
          : '测试消息只验证已保存的通知配置；上传真实安装包可验证下载按钮。'}
        {feishu ? ' 使用手机打开下载页时，手机需要能访问分发地址。' : ''}
      </p>

      {testResult ? (
        <p className={testResult.delivered ? 'notice notice-ok' : 'notice'}>
          {testResult.message}
          {testResult.statusCode > 0 ? `（HTTP ${testResult.statusCode}）` : ''}
        </p>
      ) : null}
    </div>
  );
}

/** 外部 API 面板：把接口、Key 与可复制的 curl 放在一页，免去翻文档。 */
function ApiPanel({
  configured,
  projectKey,
  onCopy,
  canWrite,
}: {
  configured: boolean;
  projectKey: string;
  onCopy: (text: string, message: string) => void;
  canWrite: boolean;
}): JSX.Element {
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [rotating, setRotating] = useState(false);
  const [rotateError, setRotateError] = useState('');
  // 明文只存在于「刚轮换完」这一份组件状态里：库中只有摘要，刷新页面就再也看不到。
  // 这跟一次性密码是同一套产品约定。
  const [plainKey, setPlainKey] = useState('');

  const handleRotate = async (): Promise<void> => {
    if (!canWrite) return;
    setRotating(true);
    setRotateError('');
    try {
      const result = await rotateApiKey();
      setPlainKey(result.apiKey);
      setConfirmOpen(false);
    } catch (err) {
      setRotateError(errorMessage(err));
    } finally {
      setRotating(false);
    }
  };

  // 仅用于生成给人复制的示例；真正的下载地址由服务端按 PUBLIC_BASE_URL 生成。
  const origin = window.location.origin;
  const key = plainKey || '<你的 API Key>';
  // 大包推荐三步直传：字节不经过 Tunnel，也不会受 100 MB 请求体上限影响。
  // 票据里的 header 必须原样带上，示例只写占位符，避免前端硬编码签名头。
  const curlDirect = [
    '# 1. 取票据',
    `curl -X POST ${origin}/api/v1/open/builds/upload-ticket \\`,
    `  -H "X-API-Key: ${key}" \\`,
    '  -H "Content-Type: application/json" \\',
    `  -d '{"project":"${projectKey}","fileName":"app-release.apk","size":124927223,"contentType":"application/vnd.android.package-archive"}'`,
    '',
    '# 2. 直传 R2（把票据返回的 putUrl 与 header 原样代入）',
    'curl -X PUT "$PUT_URL" \\',
    '  -H "Content-Type: <票据 header.Content-Type>" \\',
    '  -H "Cache-Control: <票据 header.Cache-Control>" \\',
    '  -H "Content-Disposition: <票据 header.Content-Disposition>" \\',
    '  --data-binary @./app-release.apk',
    '',
    '# 3. 提交元信息，返回 201 + Build',
    `curl -X POST ${origin}/api/v1/open/builds/stage-from-r2 \\`,
    `  -H "X-API-Key: ${key}" \\`,
    '  -H "Content-Type: application/json" \\',
    `  -d '{"uploadToken":"<票据 uploadToken>","project":"${projectKey}","environment":"test","platform":"android","changelog":"修复登录态过期"}'`,
  ].join('\n');
  const curl = [
    '# 小包兼容：一步式 multipart',
    `curl -X POST ${origin}/api/v1/open/builds \\`,
    `  -H "X-API-Key: ${key}" \\`,
    `  -F "project=${projectKey}" \\`,
    '  -F "file=@./app-release.apk" \\',
    '  -F "environment=test" \\',
    '  -F "platform=android" \\',
    '  -F "changelog=修复登录态过期"',
  ].join('\n');

  return (
    <div className="card settings-card">
      <div className="settings-block">
        <h2>调用方式</h2>
        <p className="settings-hint">
          外部系统（CI / 脚本）用 API Key 上传，不需要管理员登录态。 Key
          与公司绑定：用它上传的包只会落在本公司的项目里。
        </p>
        <dl className="kv-list">
          <div>
            <dt>接口</dt>
            <dd>
              <code>POST /api/v1/open/builds/upload-ticket</code> → PUT R2 →{' '}
              <code>POST /api/v1/open/builds/stage-from-r2</code>
              <br />
              <span className="settings-hint">小包仍可用一步式 POST /api/v1/open/builds</span>
            </dd>
          </div>
          <div>
            <dt>鉴权</dt>
            <dd>
              <code>X-API-Key: &lt;key&gt;</code>
            </dd>
          </div>
          <div>
            <dt>格式</dt>
            <dd>multipart/form-data，单包上限 300 MB</dd>
          </div>
          <div>
            <dt>返回</dt>
            <dd>201 + 完整 Build（含下载页与下载文件绝对地址）</dd>
          </div>
        </dl>
      </div>

      <div className="settings-block">
        <h2>API Key</h2>
        <div className="copy-row">
          <Input
            className="text-input mono"
            readOnly
            value={plainKey || (configured ? '（已配置 · 明文不再展示）' : '（尚未生成）')}
            aria-label="API Key"
          />
          <Button
            className="button button-ghost"
            type="button"
            disabled={!plainKey}
            onClick={() => onCopy(plainKey, 'API Key 已复制')}
          >
            复制
          </Button>
          {canWrite ? (
            <Button
              className="button button-ghost"
              type="button"
              onClick={() => setConfirmOpen(true)}
            >
              轮换
            </Button>
          ) : null}
        </div>
        <p className="settings-hint">
          服务端只保存 Key 的 SHA-256 摘要，明文仅在轮换成功时显示这一次，之后无法再取回。
          确认没有保存的话，重新轮换一把即可。
        </p>
        <p className="settings-hint">
          轮换后旧 Key <strong>立即失效</strong>，请同步更新所有外部系统。
        </p>
        {plainKey ? (
          <p className="temp-password-hint">
            新 Key 已生成：请立即复制并写入外部系统，离开本页后无法再次查看。
          </p>
        ) : null}
        {rotateError ? <p className="field-error">{rotateError}</p> : null}

        <ConfirmDialog
          open={confirmOpen}
          title="轮换 API Key？"
          description={
            <>
              <p>
                旧 Key 会<strong>立即失效</strong>，所有使用它的 CI / 脚本都会开始返回
                401，直到换成新 Key。
              </p>
              <p className="settings-hint">新 Key 只显示一次，请立即复制到外部系统。</p>
            </>
          }
          confirmText="轮换"
          danger
          loading={rotating}
          onConfirm={() => void handleRotate()}
          onCancel={() => setConfirmOpen(false)}
        />
      </div>

      <div className="settings-block">
        <h2>字段说明</h2>
        <div className="table-scroll">
          <table className="table table-compact">
            <thead>
              <tr>
                <th>字段</th>
                <th>必填</th>
                <th>说明</th>
              </tr>
            </thead>
            <tbody>
              <tr>
                <td>
                  <code>project</code>
                </td>
                <td>是</td>
                <td>项目 key，在左上角项目切换器里可见</td>
              </tr>
              <tr>
                <td>
                  <code>file</code>
                </td>
                <td>是</td>
                <td>.apk / .ipa / .hap 文件本身</td>
              </tr>
              <tr>
                <td>
                  <code>environment</code>
                </td>
                <td>是</td>
                <td>test / hotfix / pre / prd</td>
              </tr>
              <tr>
                <td>
                  <code>platform</code>
                </td>
                <td>是</td>
                <td>android / ios / harmony，需与文件扩展名一致</td>
              </tr>
              <tr>
                <td>
                  <code>appName</code>、<code>version</code> 等
                </td>
                <td>否</td>
                <td>省略时用包内解析出的信息补齐，再缺才用文件名兜底</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <div className="settings-block">
        <h2>调用示例</h2>
        <p className="settings-hint">
          大包走三步直传：字节直接到 R2，发布后后台异步拉回本机，进度与页面上传共用同一套。 票据里的
          header 必须原样带上。
        </p>
        <pre className="code-block">{curlDirect}</pre>
        <div className="settings-actions">
          <Button
            className="button button-ghost"
            type="button"
            onClick={() => onCopy(curlDirect, '直传示例已复制')}
          >
            复制直传示例
          </Button>
        </div>
        <pre className="code-block">{curl}</pre>
        <div className="settings-actions">
          <Button
            className="button button-ghost"
            type="button"
            onClick={() => onCopy(curl, '一步式示例已复制')}
          >
            复制一步式示例
          </Button>
        </div>
        <p className="settings-hint">完整说明见仓库 docs/09-OPEN-API.md。</p>
      </div>
    </div>
  );
}
