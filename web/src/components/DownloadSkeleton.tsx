/**
 * 公开下载页的骨架布局。
 * 职责：让路由资源加载、首次取数和版本切换共用与成品布局一致的占位。
 * 输入：可选环境名称和验证提示开关；输出：整页、安装包和扫码区的纯展示骨架。
 * 边界：不读取路由、不请求数据、不持有业务状态；骨架中不提供下载或分享入口。
 */
import { Skeleton } from './Skeleton';
import brandIcon from '../assets/download/brand.svg';
import clockIcon from '../assets/download/clock.svg';

export function DownloadReleaseSkeleton({
  environmentLabel = '',
  showWarning = true,
}: {
  environmentLabel?: string;
  showWarning?: boolean;
}): JSX.Element {
  return (
    <div
      className="download-release-skeleton"
      role="status"
      aria-label={`正在加载${environmentLabel ? `${environmentLabel}环境` : ''}安装包`}
    >
      <div aria-hidden="true">
        <div className="download-release">
          <div className="download-release-top">
            <Skeleton className="download-skeleton-badge" />
            <Skeleton className="download-skeleton-label" />
          </div>
          <div className="download-version-line">
            <Skeleton className="download-skeleton-version" />
            <Skeleton className="download-skeleton-switch" />
          </div>
          <Skeleton className="download-skeleton-caption" />
          <dl className="download-package-metrics download-skeleton-metrics">
            {[0, 1, 2, 3].map((item) => (
              <div className={item === 3 ? 'download-package-id' : undefined} key={item}>
                <dt>
                  <Skeleton className="download-skeleton-label" />
                </dt>
                <dd>
                  <Skeleton className="download-skeleton-value" />
                </dd>
              </div>
            ))}
          </dl>
          <div className="download-release-bottom">
            <Skeleton className="download-skeleton-caption" />
            <Skeleton className="download-skeleton-label" />
          </div>
        </div>
        <div className="download-skeleton-qr">
          <Skeleton className="download-mobile-qr-thumbnail" />
          <div>
            <Skeleton className="download-skeleton-value" />
            <Skeleton className="download-skeleton-caption" />
            <Skeleton className="download-skeleton-caption" />
          </div>
        </div>
        <div className="download-changelog download-skeleton-changelog">
          <Skeleton className="download-skeleton-badge" />
          <Skeleton className="skeleton-line" />
        </div>
        {showWarning ? (
          <div className="download-environment-notice">
            <Skeleton className="skeleton-line" />
          </div>
        ) : null}
      </div>
    </div>
  );
}

export function DownloadInstallSkeleton(): JSX.Element {
  return (
    <div role="status" aria-label="正在加载所选版本二维码与下载入口">
      <Skeleton className="download-skeleton-scan" />
      <Skeleton className="download-skeleton-scan-version" />
      <Skeleton className="download-skeleton-scan-caption" />
      <Skeleton className="download-skeleton-install" />
    </div>
  );
}

export function DownloadPageSkeleton(): JSX.Element {
  return (
    <article className="download-page" aria-busy="true">
      <header className="download-topbar">
        <div>
          <span className="download-brand">
            <img src={brandIcon} alt="" />
            Dandelion
          </span>
          <span className="download-nav-label">应用分发</span>
        </div>
      </header>
      <div className="download-container">
        <header className="download-hero" aria-hidden="true">
          <div className="download-identity">
            <Skeleton className="download-app-icon" />
            <div className="download-identity-skeleton">
              <Skeleton className="download-skeleton-title" />
              <Skeleton className="download-skeleton-label" />
            </div>
            <Skeleton className="download-tagline download-skeleton-caption" />
          </div>
        </header>
        <div className="download-layout">
          <main className="download-main">
            <section className="download-release-panel" aria-label="环境与安装包">
              <div className="download-selection-bar">
                <div className="download-environments">
                  <div className="download-section-heading">
                    <h2>选择环境</h2>
                    <span>不同环境的安装包独立发布</span>
                  </div>
                  <Skeleton className="download-skeleton-environments" />
                </div>
                <div className="download-platform-picker" aria-hidden="true">
                  <h2>安装平台</h2>
                  <Skeleton className="download-skeleton-platforms" />
                </div>
              </div>
              <DownloadReleaseSkeleton />
              <div className="download-version-placeholder" aria-hidden="true">
                <span className="download-version-heading">
                  <img src={clockIcon} alt="" />
                  <strong>版本记录</strong>
                  <Skeleton className="download-version-count" />
                </span>
                <Skeleton className="download-skeleton-label" />
              </div>
            </section>
            <section className="download-preview" aria-hidden="true">
              <div className="download-section-heading">
                <h2>应用预览</h2>
                <span>左右滑动查看</span>
              </div>
              <div className="download-preview-content">
                <div className="download-shots">
                  {[0, 1, 2].map((item) => (
                    <Skeleton className="download-shot-loading" key={item} />
                  ))}
                </div>
                <div className="download-preview-description download-preview-skeleton">
                  <Skeleton className="download-skeleton-label" />
                  <Skeleton className="download-skeleton-caption" />
                  <Skeleton className="skeleton-line" />
                  <Skeleton className="skeleton-line" />
                </div>
              </div>
            </section>
          </main>
          <aside className="download-sidebar">
            <section className="download-install-card">
              <span className="download-eyebrow">SCAN TO INSTALL</span>
              <h2>在手机上安装</h2>
              <DownloadInstallSkeleton />
              <div className="download-skeleton-help" aria-hidden="true">
                <Skeleton className="download-skeleton-caption" />
              </div>
            </section>
          </aside>
        </div>
      </div>
      <footer className="download-dock">
        <div className="download-dock-context" aria-hidden="true">
          <Skeleton className="download-skeleton-dock-version" />
        </div>
        <div role="status" aria-label="正在准备下载入口">
          <Skeleton className="download-skeleton-install" />
        </div>
        <p>原始安装包 · 仅供内部使用</p>
      </footer>
    </article>
  );
}
