/**
 * R2 对象存储配置面板：让管理员在页面上维护存储凭据并当场验证连通性。
 * 职责：渲染字段、收集输入、触发保存与连通性测试，并把三项测试结果分开呈现。
 * 输入：当前配置（GET /settings/r2）、保存与测试的回调。
 * 输出：保存后的权威配置、测试结果。
 * 边界：不做字段校验（服务端是唯一裁判）、不缓存配置副本、
 *       不展示密钥明文（服务端从不返回，页面也拿不到）。
 *
 * 为什么独立成文件而不是塞进 SettingsPage：那个文件已承载 Webhook 与外部 API
 * 两条完整流程（600 余行）。R2 是第三种配置，变化原因与前两者无关，
 * 混在一起会让每次改动都要在数百行里定位。
 */
import { useState } from 'react';
import type { R2Config, R2TestResult, R2UpdateRequest } from '../../api/settings';
import { Button, Input } from '../../components/Controls';

interface R2PanelProps {
  /** 服务端返回的权威配置，用于判断「是否修改过」与回显非敏感字段。 */
  data: R2Config;
  saving: boolean;
  testing: boolean;
  notice: string;
  testResult: R2TestResult | null;
  canWrite: boolean;
  onSave: (input: R2UpdateRequest) => void;
  onTest: () => void;
}

/** 可编辑的表单状态；密钥单独管理，因为它有「不改 / 替换 / 清除」三种意图。 */
interface R2Form {
  accountId: string;
  accessKeyId: string;
  bucket: string;
  publicBaseUrl: string;
}

export function R2Panel(props: R2PanelProps): JSX.Element {
  const { data, saving, testing, notice, testResult, canWrite } = props;
  const [form, setForm] = useState<R2Form>({
    accountId: data.accountId,
    accessKeyId: data.accessKeyId,
    bucket: data.bucket,
    publicBaseUrl: data.publicBaseUrl,
  });
  // replacing 为 true 表示用户点了「替换」，此时才显示密钥输入框。
  // 不点就一直是「已配置」的只读状态——密钥从不回显，也就无从误改。
  const [replacing, setReplacing] = useState(!data.secretConfigured);
  const [secret, setSecret] = useState('');

  const dirty =
    form.accountId !== data.accountId ||
    form.accessKeyId !== data.accessKeyId ||
    form.bucket !== data.bucket ||
    form.publicBaseUrl !== data.publicBaseUrl ||
    (replacing && secret !== '');

  /** 组装请求体。changeSecret 只在「用户确实动过密钥」时为 true。 */
  const buildRequest = (): R2UpdateRequest => ({
    accountId: form.accountId.trim(),
    accessKeyId: form.accessKeyId.trim(),
    bucket: form.bucket.trim(),
    publicBaseUrl: form.publicBaseUrl.trim(),
    secret: replacing ? secret.trim() : '',
    // 三种意图用一个布尔表达，避免让空串兼任「不改」与「删除」：
    //   false → 沿用库里的密钥；true + 非空 → 替换；true + 空 → 清除。
    changeSecret: replacing,
  });

  const patch = (key: keyof R2Form, value: string): void => {
    setForm((previous) => ({ ...previous, [key]: value }));
  };

  /** 全部清空等于关闭该公司的 R2：下载会降级走本机，需要明确提示。 */
  const disabling = !form.accountId.trim() && !form.bucket.trim();

  return (
    <div className="card settings-card">
      <div className="settings-block">
        <h2>存储通道</h2>
        <p className="settings-hint">
          配置 Cloudflare R2 后，安装包会镜像到云端，下载直连 CDN 而不经过本机。
          留空表示不使用对象存储，下载走本机磁盘（可用，但慢）。
        </p>
        <p className="settings-hint">
          保存后立即生效，无需重启服务。
          <strong>各公司互相独立</strong>：这里的配置只影响本公司，与其他公司互不影响。
        </p>
      </div>

      <label className="settings-field">
        <span>Account ID</span>
        <Input
          className="text-input mono"
          placeholder="Cloudflare 账号 ID（32 位十六进制）"
          value={form.accountId}
          disabled={!canWrite || saving || testing}
          onChange={(event) => patch('accountId', event.target.value)}
        />
        <em className="settings-hint">Cloudflare 控制台 → R2 → 概览页右侧可见。</em>
      </label>

      <label className="settings-field">
        <span>Access Key ID</span>
        <Input
          className="text-input mono"
          placeholder="R2 API 令牌的 Access Key ID"
          value={form.accessKeyId}
          disabled={!canWrite || saving || testing}
          onChange={(event) => patch('accessKeyId', event.target.value)}
        />
      </label>

      <div className="settings-field">
        <span>Secret Access Key</span>
        {replacing ? (
          <>
            <div className="copy-row">
              <Input
                className="text-input mono"
                type="password"
                autoComplete="new-password"
                placeholder={
                  data.secretConfigured ? '留空并保存将清除密钥' : '粘贴 Secret Access Key'
                }
                value={secret}
                disabled={!canWrite || saving || testing}
                onChange={(event) => setSecret(event.target.value)}
              />
              {data.secretConfigured ? (
                <Button
                  className="button button-ghost"
                  type="button"
                  disabled={saving || testing}
                  onClick={() => {
                    setReplacing(false);
                    setSecret('');
                  }}
                >
                  取消
                </Button>
              ) : null}
            </div>
            {data.secretConfigured ? (
              <em className="settings-hint">
                已在库中加密保存。这里留空并保存会<strong>清除密钥</strong>，之后需要重新填写。
              </em>
            ) : null}
          </>
        ) : (
          <div className="copy-row">
            <Input
              className="text-input mono"
              readOnly
              value="（已配置 · 明文不可回显）"
              aria-label="Secret Access Key"
            />
            <Button
              className="button button-ghost"
              type="button"
              disabled={!canWrite || saving || testing}
              onClick={() => setReplacing(true)}
            >
              替换
            </Button>
          </div>
        )}
        <em className="settings-hint">
          密钥加密后存库，读不回明文。这是签名请求所必需，与只做比对的 API Key 不同。
        </em>
      </div>

      <label className="settings-field">
        <span>Bucket</span>
        <Input
          className="text-input mono"
          placeholder="桶名称，例如 api-4"
          value={form.bucket}
          disabled={!canWrite || saving || testing}
          onChange={(event) => patch('bucket', event.target.value)}
        />
      </label>

      <label className="settings-field">
        <span>自定义域名（强烈建议）</span>
        <Input
          className="text-input mono"
          type="url"
          inputMode="url"
          placeholder="https://download.example.com"
          value={form.publicBaseUrl}
          disabled={!canWrite || saving || testing}
          onChange={(event) => patch('publicBaseUrl', event.target.value)}
        />
        <em className="settings-hint">
          填了下载直连 CDN，速度快 10–30 倍；不填则用 R2 的签名地址（国内较慢）。
          只填到域名，不要带查询参数。
        </em>
      </label>

      <div className="settings-actions">
        {canWrite ? (
          <>
            <Button
              className="button button-primary"
              type="button"
              disabled={saving || testing || (!dirty && !replacing)}
              onClick={() => props.onSave(buildRequest())}
            >
              {saving ? '保存中…' : '保存'}
            </Button>
            <Button
              className="button"
              type="button"
              // 测试的是已保存的配置：表单有改动时先保存，否则测的与存的对不上。
              disabled={testing || saving || dirty || !data.secretConfigured}
              onClick={props.onTest}
            >
              {testing ? '测试中…' : '测试连接'}
            </Button>
          </>
        ) : null}
        {notice ? <span className="settings-notice">{notice}</span> : null}
      </div>

      {disabling ? (
        <p className="notice">
          所有字段都为空：保存后将<strong>关闭本公司的对象存储</strong>，
          下载会改为经本机转发，大包会明显变慢。
        </p>
      ) : null}

      {testResult ? <R2TestReport result={testResult} hasPublicURL={!!data.publicBaseUrl} /> : null}
    </div>
  );
}

/**
 * 连通性测试的三项结果。
 *
 * 分开呈现而不是合成一个「连接失败」：三项对应三类完全不同的修复动作，
 * 合在一起等于告诉管理员「哪里不对，自己猜」。
 */
function R2TestReport({
  result,
  hasPublicURL,
}: {
  result: R2TestResult;
  hasPublicURL: boolean;
}): JSX.Element {
  const rows: Array<{ label: string; ok: boolean; hint: string; skip?: boolean }> = [
    {
      label: '凭据',
      ok: result.credentialsOk,
      hint: '密钥能否完成签名与鉴权。失败通常是 Access Key 与 Secret 不匹配。',
    },
    {
      label: '存储桶',
      ok: result.bucketOk,
      hint: '桶是否存在且可访问。失败通常是桶名拼错或令牌没有该桶的权限。',
    },
    {
      label: '自定义域名',
      ok: result.publicUrlOk,
      hint: '域名能否真的取到对象。失败通常是 DNS 未解析、未开启公共访问，或域名不是这个桶的。',
      // 未配置域名不是故障：此时下载走 R2 签名直链，功能完整。
      skip: !hasPublicURL,
    },
  ];

  return (
    <div className="settings-block">
      <h2>测试结果</h2>
      <dl className="kv-list">
        {rows.map((row) => (
          <div key={row.label}>
            <dt>{row.label}</dt>
            <dd>
              {row.skip ? (
                <span className="settings-hint">未配置自定义域名，跳过</span>
              ) : (
                <span className={row.ok ? 'status-dot status-online' : 'status-dot status-failed'}>
                  {row.ok ? '正常' : '失败'}
                </span>
              )}
              {!row.skip && !row.ok ? <em className="settings-hint"> {row.hint}</em> : null}
            </dd>
          </div>
        ))}
      </dl>
      {result.message ? <p className="settings-hint">{result.message}</p> : null}
    </div>
  );
}
