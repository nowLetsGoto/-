/**
 * 独立下载页（对外扫码页）。
 * 职责：按 id 取详情，展示应用 / 版本 / 环境与下载次数，提供下载、复制地址与二维码。
 * 输入：URL 参数 id；输出：getBuild 请求与三态页面。
 * 边界：二维码、复制、下载一律使用服务端给的绝对字段，绝不拼 window.location.origin；
 *       地址仅本机可达时给中文提示且不渲染扫不开的码；
 *       页面不套后台骨架，扫码的访客看不到内部导航。
 */
import { useEffect, useState } from 'react';
import type { MouseEvent } from 'react';
import { Link, useParams } from 'react-router-dom';
import { QRCodeSVG } from 'qrcode.react';
import type { Build } from '../api/builds';
import { getBuild, isShareableUrl } from '../api/builds';
import { ApiError, errorMessage } from '../api/client';
import { Empty, ErrorState, Loading } from '../components/States';
import { environmentLabel, formatBytes, formatDate, platformLabel } from '../utils/format';

interface PageState {
  loading: boolean;
  data: Build | null;
  error: string;
  missing: boolean;
}

const DOWNLOAD_COOLDOWN_MS = 3000;

export function DownloadPage(): JSX.Element {
  const { id } = useParams<{ id: string }>();
  const [state, setState] = useState<PageState>({ loading: true, data: null, error: '', missing: false });
  const [reloadKey, setReloadKey] = useState(0);
  const [downloadDisabled, setDownloadDisabled] = useState(false);
  const [copied, setCopied] = useState(false);
  const [notice, setNotice] = useState('');

  useEffect(() => {
    if (!id) {
      setState({ loading: false, data: null, error: '', missing: true });
      return;
    }
    let cancelled = false;
    setState({ loading: true, data: null, error: '', missing: false });
    getBuild(id)
      .then((data) => {
        if (!cancelled) {
          setState({ loading: false, data, error: '', missing: false });
        }
      })
      .catch((err: unknown) => {
        if (cancelled) {
          return;
        }
        const missing = err instanceof ApiError && err.code === 404;
        setState({ loading: false, data: null, error: missing ? '' : errorMessage(err), missing });
      });
    return () => {
      cancelled = true;
    };
  }, [id, reloadKey]);

  const copyText = async (text: string): Promise<void> => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      setNotice('复制失败，请手动选中地址复制');
    }
  };

  // 按钮点击后禁用约 3 秒，抑制短时间重复点击导致的重复计数（强去重不在第一版范围）。
  const onDownloadClick = (event: MouseEvent<HTMLAnchorElement>): void => {
    if (downloadDisabled) {
      event.preventDefault();
      return;
    }
    setDownloadDisabled(true);
    window.setTimeout(() => setDownloadDisabled(false), DOWNLOAD_COOLDOWN_MS);
  };

  if (state.loading) {
    return (
      <section className="download-page">
        <Loading text="正在加载包信息…" />
      </section>
    );
  }
  if (state.missing) {
    return (
      <section className="download-page">
        <Empty
          title="包不存在"
          description="链接可能已失效，或这个包已被移除。"
          action={
            <Link className="button button-primary" to="/packages">
              返回包列表
            </Link>
          }
        />
      </section>
    );
  }
  if (state.error || !state.data) {
    return (
      <section className="download-page">
        <ErrorState message={state.error || '加载失败'} onRetry={() => setReloadKey((value) => value + 1)} />
      </section>
    );
  }

  const build = state.data;
  const shareable = isShareableUrl(build.downloadPageUrl);
  const versionText = build.buildNumber ? `${build.version} (${build.buildNumber})` : build.version;

  return (
    <section className="download-page">
      <article className="card download-card">
        <header className="download-head">
          <span className="download-brand">Dandelion</span>
          {/* 环境是本平台区别于其它分发工具的关键信息，必须常显 */}
          <span className="badge badge-env">{environmentLabel(build.environment)}</span>
        </header>

        <div className="download-hero">
          <div className="app-icon" aria-hidden="true">
            {build.appName.slice(0, 1).toUpperCase()}
          </div>
          <h1 className="download-title">{build.appName}</h1>
          {/* 包名兜底时会等于应用名，重复显示没有信息量，直接省略 */}
          {build.packageId && build.packageId !== build.appName ? (
            <p className="download-pkg">{build.packageId}</p>
          ) : null}
          <div className="badge-row">
            <span className={`badge badge-${build.platform}`}>{platformLabel(build.platform)}</span>
            <span className="badge badge-plain">{versionText}</span>
          </div>
        </div>

        <a
          className={`button button-primary button-block download-action${downloadDisabled ? ' button-disabled' : ''}`}
          href={build.downloadFileUrl}
          onClick={onDownloadClick}
          aria-disabled={downloadDisabled}
        >
          下载 / 安装 · {formatBytes(build.fileSize)}
        </a>
        <p className="muted download-hint">
          {build.platform === 'ios'
            ? 'iOS 仅提供 .ipa 文件下载，暂不支持浏览器直接安装'
            : '下载得到与上传一致的原始安装包'}
        </p>

        <dl className="info-list">
          <div>
            <dt>文件大小</dt>
            <dd>{formatBytes(build.fileSize)}</dd>
          </div>
          <div>
            <dt>上传时间</dt>
            <dd>{formatDate(build.createdAt)}</dd>
          </div>
          <div>
            <dt>下载次数</dt>
            <dd>{build.downloadCount}</dd>
          </div>
          {build.packageId && build.packageId !== build.appName ? (
            <div>
              <dt>包名 / Bundle ID</dt>
              <dd className="info-ellipsis">{build.packageId}</dd>
            </div>
          ) : null}
        </dl>

        {build.changelog ? (
          <div className="changelog">
            <strong>更新说明</strong>
            <p>{build.changelog}</p>
          </div>
        ) : null}

        <div className="qrcode-box">
          {shareable ? (
            <>
              <QRCodeSVG
                value={build.downloadPageUrl}
                size={208}
                level="M"
                marginSize={4}
                bgColor="#ffffff"
                fgColor="#0f2c36"
              />
              <p className="muted">扫码在手机上打开此页</p>
            </>
          ) : (
            <>
              <p className="notice">当前对外基址不可被手机访问，请配置 PUBLIC_BASE_URL</p>
              <p className="muted">可先复制下面的页面地址转发：</p>
              <code className="share-text">{build.downloadPageUrl}</code>
            </>
          )}
        </div>

        <div className="actions actions-center">
          <button className="button button-ghost" type="button" onClick={() => void copyText(build.downloadPageUrl)}>
            {copied ? '已复制' : '复制页面地址'}
          </button>
        </div>
        {notice ? <p className="field-error">{notice}</p> : null}

        <p className="download-foot">由 Dandelion 分发 · 仅限内部使用</p>
      </article>
    </section>
  );
}
