/**
 * 独立下载页（P-03）。
 * 职责：按 id 取详情、展示信息（环境标签必显）与下载次数、渲染二维码、提供下载/复制/返回。
 * 输入：URL 参数 id；输出：getBuild 请求与三态页面。
 * 边界：二维码 value、复制地址、下载按钮一律直接使用服务端给的绝对字段；
 *       绝不用 downloadPagePath 生成对外地址、绝不拼 window.location.origin；
 *       地址仅本机可达时显示中文提示且不渲染扫不开的码；列表页不放二维码入口。
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
      <section className="page page-narrow">
        <Loading text="正在加载包信息…" />
      </section>
    );
  }
  if (state.missing) {
    return (
      <section className="page page-narrow">
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
      <section className="page page-narrow">
        <ErrorState message={state.error || '加载失败'} onRetry={() => setReloadKey((value) => value + 1)} />
      </section>
    );
  }

  const build = state.data;
  const shareable = isShareableUrl(build.downloadPageUrl);

  return (
    <section className="page page-narrow download-page">
      <div className="card download-card">
        <div className="download-brand">Dandelion</div>
        <div className="app-icon" aria-hidden="true">
          {build.appName.slice(0, 1).toUpperCase()}
        </div>
        <h1 className="download-title">{build.appName}</h1>

        <div className="badge-row">
          {/* 环境标签是本平台下载页区别于蒲公英的必显字段 */}
          <span className="badge badge-env">{environmentLabel(build.environment)}</span>
          <span className={`badge badge-${build.platform}`}>{platformLabel(build.platform)}</span>
          <span className="badge badge-plain">
            {build.version}
            {build.buildNumber ? ` (${build.buildNumber})` : ''}
          </span>
        </div>

        <dl className="info-list">
          <div>
            <dt>包名 / Bundle ID</dt>
            <dd>{build.packageId || '—'}</dd>
          </div>
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
            <dd>已被下载 {build.downloadCount} 次</dd>
          </div>
        </dl>

        {build.changelog ? (
          <div className="changelog">
            <strong>更新说明</strong>
            <p>{build.changelog}</p>
          </div>
        ) : null}

        <a
          className={`button button-primary button-block${downloadDisabled ? ' button-disabled' : ''}`}
          href={build.downloadFileUrl}
          onClick={onDownloadClick}
          aria-disabled={downloadDisabled}
        >
          下载 / 安装 App
        </a>
        <p className="muted download-hint">
          {build.platform === 'ios' ? 'iOS 第一版仅提供 .ipa 文件下载，暂不支持浏览器直接安装' : '下载得到与上传一致的原始安装包'}
        </p>

        <div className="qrcode-box">
          {shareable ? (
            <>
              <QRCodeSVG value={build.downloadPageUrl} size={224} level="M" marginSize={4} bgColor="#ffffff" fgColor="#000000" />
              <p className="muted">扫码在手机上打开此下载页</p>
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
          <Link className="button button-ghost" to="/packages">
            返回包列表
          </Link>
        </div>
        {notice ? <p className="field-error">{notice}</p> : null}
      </div>
    </section>
  );
}
