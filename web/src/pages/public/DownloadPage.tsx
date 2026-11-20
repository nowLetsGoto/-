/**
 * 独立下载页（对外扫码页）。
 * 职责：按 id 取详情，完整呈现应用身份、版本信息、下载入口、安装引导、更新说明与常见问题。
 * 输入：URL 参数 id；输出：getBuild 请求与三态页面。
 * 边界：二维码、复制、下载一律使用服务端给的绝对字段，绝不拼 window.location.origin；
 *       地址仅本机可达时给中文提示且不渲染扫不开的码；
 *       截图是随页面发布的静态素材，尚未接入上传流程；介绍 / 开发商无字段时不编造内容；
 *       页面不套后台骨架，扫码的访客看不到内部导航。
 */
import { useEffect, useState } from 'react';
import type { MouseEvent } from 'react';
import { Link, useParams } from 'react-router-dom';
import { QRCode } from '@seakoi/console-kit';
import type { Build, Platform, PublicBuildVersion } from '../../api/builds';
import { getBuild, isShareableUrl, listBuildVersions } from '../../api/builds';
import { ApiError, errorMessage } from '../../api/client';
import { Button } from '../../components/Controls';
import { Empty, ErrorState, Loading } from '../../components/States';
import { environmentLabel, formatBytes, formatDate, platformLabel } from '../../utils/format';
import workbenchShot from '../../assets/screenshots/workbench.png';
import storesShot from '../../assets/screenshots/stores.png';
import customerShot from '../../assets/screenshots/customer-detail.png';
import assetShot from '../../assets/screenshots/asset-sign.png';

interface PageState {
  loading: boolean;
  data: Build | null;
  error: string;
  missing: boolean;
}

const DOWNLOAD_COOLDOWN_MS = 3000;
const VERSION_PREVIEW_LIMIT = 10;

const APP_INTRO =
  '景云销APP是一款全方位销售和客户关系管理工具。它整合了待办事项、拜访管理、客户管理、渠道分销、订单管理、活动管理、资产管理、流程管理和合同管理等重要功能，为企业提供一站式销售管理解决方案。景云销利用先进的云计算技术，帮助销售团队实现工作流程的优化，提升销售效率和客户服务质量，从而推动企业的持续发展和增长。';

/** 构建人目前写在更新说明里（形如「构建人:linanan」），没有独立接口字段，取不到就留空。 */
function builderFrom(changelog: string): string {
  return /构建人[:：]\s*([^\s,，;；]+)/.exec(changelog)?.[1] ?? '上传方未提供';
}

const SCREENSHOTS = [
  { src: workbenchShot, label: '工作台' },
  { src: storesShot, label: '终端门店' },
  { src: customerShot, label: '客户详情' },
  { src: assetShot, label: '资产扫码签收' },
] as const;

const FEATURES = [
  { title: '扫码即达', desc: '二维码直接打开此页，不用额外转发安装包。' },
  { title: '环境隔离', desc: '测试、紧急修复、预发、生产包分开分发，避免装错版本。' },
  { title: '版本可追溯', desc: '版本号、构建号、上传时间与下载次数一并留存。' },
  { title: '原始安装包', desc: '下载文件与上传时完全一致，不做二次签名或改写。' },
] as const;

function faqsFor(platform: Platform): ReadonlyArray<{ q: string; a: string }> {
  const common = [
    {
      q: '下载后打不开安装包？',
      a: '先确认文件大小与页面显示一致；如果下载中断，请删除残包后重新下载。仍无法打开时，换用系统自带浏览器再试一次。',
    },
    {
      q: '这个包是谁发布的？',
      a: '本页由 Dandelion 分发平台生成。发布方信息由上传方维护，当前接口未提供开发商字段，安装前请向分享链接的人确认来源。',
    },
    {
      q: '下载次数是怎么计算的？',
      a: '只有安装包完整下载成功后才会计入次数，点击按钮本身不会增加计数。',
    },
  ];
  const specific: Record<Platform, { q: string; a: string }> = {
    android: {
      q: '为什么提示「禁止安装未知应用」？',
      a: '这是 Android 的安全策略，不是安装包损坏。在系统设置里给浏览器或文件管理器开启「安装未知应用」权限后，再点击安装即可。',
    },
    ios: {
      q: '为什么 iOS 不能直接点按钮安装？',
      a: 'iOS 不允许浏览器直接安装 IPA。需要发布方提供企业签名或 TestFlight 入口；本页的 IPA 作为原始包保留，便于归档与签名后分发。',
    },
    harmony: {
      q: '为什么提示应用来源未知？',
      a: '鸿蒙对非应用市场安装的应用会做风险提示。确认来源可信后，在系统安全设置中允许本次安装即可。',
    },
  };
  return [specific[platform], ...common];
}

export function DownloadPage(): JSX.Element {
  const { id } = useParams<{ id: string }>();
  const [state, setState] = useState<PageState>({
    loading: true,
    data: null,
    error: '',
    missing: false,
  });
  const [reloadKey, setReloadKey] = useState(0);
  const [downloadDisabled, setDownloadDisabled] = useState(false);
  const [copied, setCopied] = useState(false);
  const [notice, setNotice] = useState('');
  const [versions, setVersions] = useState<PublicBuildVersion[]>([]);

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

  // 其它版本是增强信息：取不到就隐藏板块，不能拖累主下载流程。
  useEffect(() => {
    if (!id) {
      return;
    }
    let cancelled = false;
    setVersions([]);
    listBuildVersions(id)
      .then((items) => {
        if (!cancelled) {
          setVersions(items);
        }
      })
      .catch(() => undefined);
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
      <section className="download-page download-state">
        <Loading text="正在加载包信息…" />
      </section>
    );
  }
  if (state.missing) {
    return (
      <section className="download-page download-state">
        <Empty
          title="包不存在"
          description="链接可能已失效，或这个包已被移除。请向分享链接的人获取最新地址。"
        />
      </section>
    );
  }
  if (state.error || !state.data) {
    return (
      <section className="download-page download-state">
        <ErrorState
          message={state.error || '加载失败'}
          onRetry={() => setReloadKey((value) => value + 1)}
        />
      </section>
    );
  }

  const build = state.data;
  const shareable = isShareableUrl(build.downloadPageUrl);
  const platform = platformLabel(build.platform);
  const versionText = build.buildNumber ? `${build.version} (${build.buildNumber})` : build.version;
  const faqs = faqsFor(build.platform);
  const visibleVersions = versions.slice(0, VERSION_PREVIEW_LIMIT);
  const hasMoreVersions = versions.length > VERSION_PREVIEW_LIMIT;

  return (
    <section className="download-page">
      <article className="download-shell">
        <header className="download-topbar">
          <span className="download-brand">Dandelion</span>
          <span className="download-topbar-right">
            <span className="download-secure">安全分发</span>
            {/* 环境是区别于普通应用商店的关键信息，必须常显 */}
            <span className="badge badge-env">{environmentLabel(build.environment)}</span>
            <Button
              className="button button-ghost button-small"
              type="button"
              onClick={() => void copyText(build.downloadPageUrl)}
            >
              {copied ? '已复制' : '分享'}
            </Button>
          </span>
        </header>

        <div className="download-hero">
          <div className="download-hero-main">
            {build.projectIconUrl ? (
              <img className="app-icon app-icon-img" src={build.projectIconUrl} alt="" />
            ) : (
              <div className="app-icon" aria-hidden="true">
                {build.appName.slice(0, 1).toUpperCase()}
              </div>
            )}
            <p className="download-eyebrow">
              {platform} 安装包 · {environmentLabel(build.environment)}环境
            </p>
            <h1 className="download-title">{build.appName}</h1>
            {build.packageId && build.packageId !== build.appName ? (
              <p className="download-pkg">{build.packageId}</p>
            ) : null}
            <div className="badge-row download-badges">
              <span className={`badge badge-${build.platform}`}>{platform}</span>
              <span className="badge badge-plain">版本 {versionText}</span>
            </div>
            <p className="download-lead">
              {formatBytes(build.fileSize)} · 更新于 {formatDate(build.createdAt)} · 已被下载{' '}
              {build.downloadCount} 次
            </p>
            <div className="download-cta">
              <a
                className={`button button-primary button-block download-action${downloadDisabled ? ' button-disabled' : ''}`}
                href={build.downloadFileUrl}
                onClick={onDownloadClick}
                aria-disabled={downloadDisabled}
              >
                下载 / 安装 · {formatBytes(build.fileSize)}
              </a>
            </div>
            <p className="muted download-hint">
              {build.platform === 'ios'
                ? 'iOS 提供原始 IPA 文件，需通过签名工具或 TestFlight 安装'
                : '下载得到与上传时完全一致的原始安装包'}
            </p>
            {notice ? <p className="field-error">{notice}</p> : null}
          </div>

          <aside className="download-qr" aria-label="扫码打开本页">
            {shareable ? (
              <>
                <QRCode
                  value={build.downloadPageUrl}
                  size={168}
                  errorLevel="M"
                  color="#0f2c36"
                  bgColor="#ffffff"
                  bordered={false}
                />
                <strong>扫码在手机上打开</strong>
                <span className="muted">也可转发此页面给同事</span>
              </>
            ) : (
              <>
                <p className="notice">当前对外基址不可被手机访问，请配置 PUBLIC_BASE_URL</p>
                <code className="share-text">{build.downloadPageUrl}</code>
              </>
            )}
          </aside>
        </div>

        <dl className="download-metrics">
          <div>
            <dt>当前版本</dt>
            <dd>{build.version}</dd>
            <span>构建号 {build.buildNumber || '—'}</span>
          </div>
          <div>
            <dt>安装包大小</dt>
            <dd>{formatBytes(build.fileSize)}</dd>
            <span>{build.fileExt.toUpperCase()} 文件</span>
          </div>
          <div>
            <dt>更新时间</dt>
            <dd>{formatDate(build.createdAt)}</dd>
            <span>按上传时间倒序</span>
          </div>
          <div>
            <dt>下载次数</dt>
            <dd>{build.downloadCount}</dd>
            <span>完整下载后计数</span>
          </div>
        </dl>

        <div className="download-grid">
          <div className="download-main">
            <section className="download-section">
              <h2>{build.appName}的介绍</h2>
              <p className="app-intro">{APP_INTRO}</p>
            </section>

            <section className="download-section">
              <h2>关于{build.appName}</h2>
              <dl className="info-list info-list-grid">
                <div>
                  <dt>大小</dt>
                  <dd>{formatBytes(build.fileSize)}</dd>
                </div>
                <div>
                  <dt>更新时间</dt>
                  <dd>{formatDate(build.createdAt)}</dd>
                </div>
                <div>
                  <dt>版本</dt>
                  <dd>{versionText}</dd>
                </div>
                <div>
                  <dt>构建人</dt>
                  <dd>{builderFrom(build.changelog)}</dd>
                </div>
                <div>
                  <dt>Bundle ID / 包名</dt>
                  <dd className="info-ellipsis">{build.packageId || '—'}</dd>
                </div>
                <div>
                  <dt>适用平台</dt>
                  <dd>{platform}</dd>
                </div>
              </dl>
            </section>

            <section className="download-section">
              <h2>更新说明</h2>
              {build.changelog ? (
                <p className="changelog">{build.changelog}</p>
              ) : (
                <p className="muted download-placeholder">
                  上传方本次未填写更新说明。需要了解改动内容，请联系分享链接的人。
                </p>
              )}
            </section>

            <section className="download-section">
              <h2>{build.appName}的其它版本</h2>
              {versions.length > 0 ? (
                <>
                  <div className="version-list">
                    {visibleVersions.map((version) => (
                      <Link className="version-item" to={version.downloadPagePath} key={version.id}>
                        <span className="version-main">
                          <strong>{version.version}</strong>
                          {version.buildNumber ? (
                            <span className="muted"> (build {version.buildNumber})</span>
                          ) : null}
                        </span>
                        <span className="version-sub">
                          {environmentLabel(version.environment)} ·{' '}
                          {platformLabel(version.platform)} · {formatDate(version.createdAt)}
                        </span>
                        <span className="version-go" aria-hidden="true">
                          →
                        </span>
                      </Link>
                    ))}
                  </div>
                  {hasMoreVersions ? (
                    <Link
                      className="version-more"
                      to={`/p/${versions[0]?.projectKey ?? ''}/packages`}
                    >
                      查看更多
                    </Link>
                  ) : null}
                </>
              ) : (
                <p className="muted download-placeholder">当前没有其它版本。</p>
              )}
            </section>

            <section className="download-section">
              <h2>应用截图</h2>
              <div className="shot-grid">
                {SCREENSHOTS.map((shot) => (
                  <a
                    className="shot-item"
                    href={shot.src}
                    target="_blank"
                    rel="noreferrer"
                    key={shot.label}
                  >
                    <img src={shot.src} alt={`${shot.label}截图`} loading="lazy" />
                    <span>{shot.label}</span>
                  </a>
                ))}
              </div>
            </section>

            <section className="download-section">
              <h2>分发特性</h2>
              <div className="feature-grid">
                {FEATURES.map((feature) => (
                  <div className="feature-item" key={feature.title}>
                    <strong>{feature.title}</strong>
                    <p>{feature.desc}</p>
                  </div>
                ))}
              </div>
            </section>

            <section className="download-section">
              <h2>常见问题</h2>
              <div className="faq-list">
                {faqs.map((item) => (
                  <details key={item.q}>
                    <summary>{item.q}</summary>
                    <p>{item.a}</p>
                  </details>
                ))}
              </div>
            </section>
          </div>
        </div>

        <footer className="download-foot">
          <span>由 Dandelion 分发 · 仅限内部使用</span>
          <span>安装包版本 {versionText}</span>
        </footer>
      </article>
    </section>
  );
}
