/**
 * 公开应用分发页（移动端优先）。
 * 职责：展示应用身份、环境与版本、安装包及二维码；桌面内嵌版本记录，移动端用底部选择弹层。
 * 输入：路由包 id、useEnvironment 管理的 URL 环境、公开包详情和版本列表；输出：响应式下载页。
 * 边界：页面不请求接口、不拼下载 URL；路由保留指定版本，切环境选最新包；空环境绝不沿用旧包下载。
 */
import { useEffect, useRef, useState } from 'react';
import type { MouseEvent } from 'react';
import { useParams } from 'react-router-dom';
import { Collapse, QRCode } from '@seakoi/console-kit';
import type { Build, Environment, Platform, PublicBuildVersion } from '../../api/builds';
import { isShareableUrl } from '../../api/builds';
import { Button, Radio, Segmented } from '../../components/Controls';
import { DetailDrawer } from '../../components/DetailDrawer';
import {
  DownloadPageSkeleton,
  DownloadReleaseSkeleton,
  DownloadInstallSkeleton,
} from '../../components/DownloadSkeleton';
import { Skeleton } from '../../components/Skeleton';
import { Empty, ErrorState } from '../../components/States';
import { useCopyText } from '../../hooks/useCopyText';
import { useDevicePlatform } from '../../hooks/useDevicePlatform';
import { useEnvironment } from '../../hooks/useEnvironment';
import { useMediaQuery } from '../../hooks/useMediaQuery';
import { usePublicDownload } from '../../hooks/usePublicDownload';
import {
  ENVIRONMENTS,
  PLATFORMS,
  environmentLabel,
  formatBytes,
  formatDate,
  platformLabel,
} from '../../utils/format';
import { findPlatformVersion } from '../../utils/downloadPlatform';
import brandIcon from '../../assets/download/brand.svg';
import shareIcon from '../../assets/download/share.svg';
import androidIcon from '../../assets/download/android.svg';
import chevronIcon from '../../assets/download/chevron.svg';
import clockIcon from '../../assets/download/clock.svg';
import downloadIcon from '../../assets/download/download.svg';
import shieldIcon from '../../assets/download/shield.svg';
import infoIcon from '../../assets/download/info.svg';
import workbenchShot from '../../assets/screenshots/workbench.png';
import storesShot from '../../assets/screenshots/stores.png';
import customerShot from '../../assets/screenshots/customer-detail.png';
import assetShot from '../../assets/screenshots/asset-sign.png';

const SCREENSHOTS = [
  { src: workbenchShot, label: '工作台' },
  { src: storesShot, label: '终端门店' },
  { src: customerShot, label: '客户详情' },
  { src: assetShot, label: '资产扫码签收' },
];

function Brand(): JSX.Element {
  return (
    <span className="download-brand">
      <img src={brandIcon} alt="" />
      Dandelion
    </span>
  );
}

function EnvironmentBadge({ environment }: { environment: Environment }): JSX.Element {
  return (
    <span className={`download-env-badge download-env-${environment}`}>
      {environmentLabel(environment)}环境
    </span>
  );
}

interface DownloadVersionsProps {
  versions: PublicBuildVersion[];
  selectedId: string;
  complete: boolean;
  expanded: boolean;
  loading: boolean;
  onToggle: () => void;
  onChoose: (id: string) => void;
}

function DownloadVersions({
  versions,
  selectedId,
  complete,
  expanded,
  loading,
  onToggle,
  onChoose,
}: DownloadVersionsProps): JSX.Element {
  const content = (
    <section
      className="download-version-browser"
      id="download-version-list"
      aria-label="当前环境的版本记录"
      aria-busy={loading}
    >
      <div className="download-version-columns" aria-hidden="true">
        <span>版本 / 构建号</span>
        <span>发布时间</span>
        <span>安装包大小</span>
        <span>操作</span>
      </div>
      <ul className="download-version-list" aria-label="可选安装包版本">
        {loading
          ? Array.from({ length: Math.max(1, Math.min(versions.length, 5)) }, (_, index) => (
              <li
                className="download-version-row download-version-row-skeleton"
                key={index}
                aria-hidden="true"
              >
                <div className="download-version-name">
                  <Skeleton className="download-skeleton-value" />
                  <Skeleton className="download-skeleton-label" />
                </div>
                <Skeleton className="download-skeleton-value" />
                <Skeleton className="download-skeleton-value" />
                <Skeleton className="download-skeleton-value" />
              </li>
            ))
          : versions.map((item, index) => {
              const selected = item.id === selectedId;
              return (
                <li
                  className={`download-version-row${selected ? ' is-current' : ''}`}
                  key={item.id}
                  aria-current={selected ? 'true' : undefined}
                >
                  <div className="download-version-name">
                    <div>
                      <strong>{item.version}</strong>
                      {index === 0 && complete ? <span>最新</span> : null}
                    </div>
                    <small>Build {item.buildNumber || '—'}</small>
                  </div>
                  <time dateTime={item.createdAt}>{formatDate(item.createdAt)}</time>
                  <span className="download-version-size">{formatBytes(item.fileSize)}</span>
                  {selected ? (
                    <span className="download-version-current">
                      <svg
                        width="13"
                        height="13"
                        viewBox="0 0 16 16"
                        fill="none"
                        aria-hidden="true"
                      >
                        <path d="m3 8 3 3 7-7" stroke="currentColor" strokeWidth="1.6" />
                      </svg>
                      当前版本
                    </span>
                  ) : (
                    <Button
                      className="download-text-button download-version-use"
                      type="button"
                      aria-label={`使用版本 ${item.version}，构建号 ${item.buildNumber || '—'}`}
                      onClick={() => onChoose(item.id)}
                    >
                      使用此版本
                    </Button>
                  )}
                </li>
              );
            })}
      </ul>
      {versions.length === 1 && complete && !loading ? (
        <p className="download-version-only">当前环境仅发布了这一个版本。</p>
      ) : null}
    </section>
  );

  return (
    <Collapse
      className="download-version-accordion"
      bordered={false}
      activeKey={expanded ? ['versions'] : []}
      onChange={onToggle}
      expandIconPosition="end"
      expandIcon={({ isActive }) => (
        <img
          className={`download-version-chevron${isActive ? ' is-expanded' : ''}`}
          src={chevronIcon}
          alt=""
        />
      )}
      items={[
        {
          key: 'versions',
          label: (
            <span className="download-version-heading">
              <img src={clockIcon} alt="" />
              <strong>版本记录</strong>
              {loading ? (
                <Skeleton className="download-version-count" />
              ) : (
                <span className="download-version-count">{versions.length}</span>
              )}
            </span>
          ),
          extra: (
            <span className="download-version-disclosure">{expanded ? '收起' : '展开查看'}</span>
          ),
          children: content,
        },
      ]}
    />
  );
}

function DownloadLink({
  build,
  disabled,
  onClick,
}: {
  build: Build;
  disabled: boolean;
  onClick: (event: MouseEvent<HTMLAnchorElement>) => void;
}): JSX.Element {
  return (
    <a
      className={`download-install${disabled ? ' is-disabled' : ''}`}
      href={build.downloadFileUrl}
      onClick={onClick}
      aria-disabled={disabled}
    >
      <img src={downloadIcon} alt="" />
      <span>
        {disabled ? '正在准备下载…' : build.platform === 'ios' ? '下载 IPA 文件' : '下载安装'}
      </span>
      <small>{formatBytes(build.fileSize)}</small>
    </a>
  );
}

function DeviceDownloadAction({
  build,
  devicePlatform,
  target,
  disabled,
  onSwitch,
  onDownload,
}: {
  build: Build;
  devicePlatform: Platform | null;
  target: PublicBuildVersion | undefined;
  disabled: boolean;
  onSwitch: (platform: Platform) => void;
  onDownload: (event: MouseEvent<HTMLAnchorElement>) => void;
}): JSX.Element {
  if (!devicePlatform || devicePlatform === build.platform) {
    return <DownloadLink build={build} disabled={disabled} onClick={onDownload} />;
  }
  const label = platformLabel(devicePlatform);
  return (
    <div className="download-device-match">
      <p>
        {target
          ? `当前为 ${platformLabel(build.platform)} 包，建议使用 ${label} 包`
          : `${environmentLabel(build.environment)}环境暂未发布 ${label} 包`}
      </p>
      <Button
        className="download-install"
        type="button"
        disabled={!target}
        onClick={() => onSwitch(devicePlatform)}
      >
        {target ? `切换至 ${label} 包` : `暂无 ${label} 包`}
        {target ? <img src={chevronIcon} alt="" /> : null}
      </Button>
    </div>
  );
}

export function DownloadPage(): JSX.Element {
  const copyText = useCopyText();
  const mobile = useMediaQuery('(max-width: 760px)');
  const devicePlatform = useDevicePlatform();
  const { id = '' } = useParams<{ id: string }>();
  const {
    build,
    versions,
    platformVersions,
    activePlatform,
    loading,
    missing,
    error,
    versionsError,
    reload,
  } = usePublicDownload(id);
  const [environment, setEnvironment] = useEnvironment(build?.environment);
  const [sheet, setSheet] = useState<'versions' | 'help' | 'qr' | null>(null);
  const [pendingVersion, setPendingVersion] = useState('');
  const [versionsExpanded, setVersionsExpanded] = useState(false);
  const [preview, setPreview] = useState<(typeof SCREENSHOTS)[number] | null>(null);
  const [copied, setCopied] = useState(false);
  const [notice, setNotice] = useState<{ message: string; showLink?: boolean } | null>(null);
  const [downloadDisabled, setDownloadDisabled] = useState(false);
  const downloadTimer = useRef<ReturnType<typeof setTimeout>>();
  const copyTimer = useRef<ReturnType<typeof setTimeout>>();
  const releaseContent = useRef<HTMLDivElement>(null);
  const [releaseHeight, setReleaseHeight] = useState<number>();
  useEffect(
    () => () => {
      clearTimeout(downloadTimer.current);
      clearTimeout(copyTimer.current);
    },
    [],
  );
  // 兼容直接打开带环境参数的链接：URL 有对应包时定位过去，不误显示为空环境。
  useEffect(() => {
    if (loading || !build || build.environment === environment) return;
    const target = versions.find((item) => item.environment === environment);
    if (target) setEnvironment(environment, target.downloadPagePath);
  }, [build, environment, loading, versions, setEnvironment]);
  // 拖动桌面窗口跨过断点时，关闭旧的移动端版本弹层。
  useEffect(() => {
    if (!mobile && sheet === 'versions') setSheet(null);
  }, [mobile, sheet]);

  if (missing)
    return (
      <section className="download-state">
        <Empty
          title="安装包不存在"
          description="链接可能已失效，或安装包已被移除。请向分享链接的人获取最新地址。"
        />
      </section>
    );
  if (error)
    return (
      <section className="download-state">
        <ErrorState message={error} onRetry={reload} />
      </section>
    );
  if (!build) return <DownloadPageSkeleton />;

  const available = versions.filter((item) => item.environment === environment);
  const selectedBuild = !loading && build.environment === environment ? build : null;
  const latest = available[0];
  const historical = selectedBuild && latest && latest.id !== selectedBuild.id;
  const platform = platformLabel(build.platform);
  const fallback = versions.find((item) => item.environment === 'prd') ?? versions[0];
  const shareable = selectedBuild && isShareableUrl(selectedBuild.downloadPageUrl);
  const pending = available.find((item) => item.id === pendingVersion);
  const switching = loading || (!selectedBuild && available.length > 0);
  const currentPlatform = activePlatform ?? build.platform;
  const recommendedTarget = devicePlatform
    ? findPlatformVersion(platformVersions, devicePlatform, environment, selectedBuild?.version)
    : undefined;
  const platformOptions = PLATFORMS.filter(
    (item) =>
      item.value === 'android' ||
      item.value === 'ios' ||
      platformVersions.some((version) => version.platform === item.value),
  ).map((item) => {
    const published = !!findPlatformVersion(platformVersions, item.value, environment);
    return {
      value: item.value,
      label: item.label,
      title: published
        ? `${item.label} 安装包`
        : versionsError
          ? `${item.label} 包暂不可用，请重试加载`
          : `${environmentLabel(environment)}环境暂未发布 ${item.label} 包`,
      disabled: !published,
    };
  });

  const changePlatform = (next: Platform): void => {
    if (next === currentPlatform || loading) return;
    const target = findPlatformVersion(platformVersions, next, environment, selectedBuild?.version);
    if (!target) return;
    setReleaseHeight(releaseContent.current?.getBoundingClientRect().height);
    setEnvironment(environment, target.downloadPagePath);
    setVersionsExpanded(false);
    setSheet(null);
    setNotice(
      selectedBuild && target.version !== selectedBuild.version
        ? {
            message: `${platformLabel(next)} 暂无 v${selectedBuild.version}，已选择${environmentLabel(environment)}环境最新版本 v${target.version}。`,
          }
        : null,
    );
    setCopied(false);
    setDownloadDisabled(false);
  };

  const changeEnvironment = (next: Environment): void => {
    setReleaseHeight(releaseContent.current?.getBoundingClientRect().height);
    const target = versions.find((item) => item.environment === next);
    setEnvironment(next, target?.downloadPagePath);
    setVersionsExpanded(false);
    setSheet(null);
    setNotice(null);
    setCopied(false);
    setDownloadDisabled(false);
  };
  const chooseVersion = (versionId: string): void => {
    const target = available.find((item) => item.id === versionId);
    if (!target) return;
    setReleaseHeight(releaseContent.current?.getBoundingClientRect().height);
    setEnvironment(environment, target.downloadPagePath);
    setSheet(null);
    setNotice(null);
    setCopied(false);
    setDownloadDisabled(false);
  };
  const openVersions = (): void => {
    setPendingVersion(selectedBuild?.id ?? latest?.id ?? '');
    setSheet('versions');
  };
  const copyLink = async (): Promise<void> => {
    if (!selectedBuild) return;
    setNotice(null);
    if (await copyText(selectedBuild.downloadPageUrl)) {
      setCopied(true);
      clearTimeout(copyTimer.current);
      copyTimer.current = setTimeout(() => setCopied(false), 2000);
    } else {
      setNotice({ message: '浏览器未允许自动复制，请长按下方地址复制', showLink: true });
      if (shareable) setSheet('qr');
    }
  };
  const onDownload = (event: MouseEvent<HTMLAnchorElement>): void => {
    if (downloadDisabled) {
      event.preventDefault();
      return;
    }
    setDownloadDisabled(true);
    clearTimeout(downloadTimer.current);
    downloadTimer.current = setTimeout(() => setDownloadDisabled(false), 3000);
  };

  return (
    <article
      className={`download-page${devicePlatform && devicePlatform !== currentPlatform ? ' has-device-mismatch' : ''}`}
    >
      <header className="download-topbar">
        <div>
          <Brand />
          <span className="download-nav-label">应用分发</span>
        </div>
        <span className="download-secure">
          <img src={shieldIcon} alt="" />
          内部应用 · 原始安装包
        </span>
        <Button
          type="button"
          className="download-mobile-share"
          aria-label={copied ? '链接已复制' : '分享当前版本'}
          disabled={!selectedBuild}
          onClick={() => void copyLink()}
        >
          <img src={shareIcon} alt="" />
          {copied ? '已复制' : ''}
        </Button>
      </header>
      <div className="download-container">
        <header className="download-hero">
          <div className="download-identity">
            {build.projectIconUrl ? (
              <img className="download-app-icon" src={build.projectIconUrl} alt="" />
            ) : (
              <span className="download-app-icon download-icon-fallback" aria-hidden="true">
                {build.appName.slice(0, 1).toUpperCase()}
              </span>
            )}
            <div>
              <h1>{build.appName}</h1>
              <p className="download-platform">
                {build.platform === 'android' ? <img src={androidIcon} alt="" /> : null}
                {platform}
                <span>·</span>内部应用
              </p>
            </div>
            <p className="download-tagline">选择合适的环境与版本，开始安装。</p>
          </div>
          <Button
            className="download-share"
            type="button"
            disabled={!selectedBuild}
            onClick={() => void copyLink()}
          >
            <img src={shareIcon} alt="" />
            {copied ? '链接已复制' : '分享当前版本'}
          </Button>
        </header>
        {notice ? (
          <div className="download-notice" role="status">
            {notice.message}
            {notice.showLink && selectedBuild ? <code>{selectedBuild.downloadPageUrl}</code> : null}
          </div>
        ) : null}
        <div className="download-layout">
          <main className="download-main">
            <section
              className="download-release-panel"
              aria-label="环境与安装包"
              aria-busy={switching}
            >
              <div className="download-selection-bar">
                <div className="download-environments">
                  <div className="download-section-heading">
                    <h2>选择环境</h2>
                    <span>不同环境的安装包独立发布</span>
                  </div>
                  <Segmented<Environment>
                    block
                    className="download-env-tabs"
                    options={[...ENVIRONMENTS]}
                    value={environment}
                    onChange={changeEnvironment}
                    disabled={loading}
                    aria-label="选择安装环境"
                  />
                </div>
                {!mobile ? (
                  <div className="download-platform-picker">
                    <h2>安装平台</h2>
                    <Segmented<Platform>
                      className="download-platform-tabs"
                      options={platformOptions}
                      value={currentPlatform}
                      onChange={changePlatform}
                      disabled={loading}
                      aria-label="选择安装平台"
                    />
                  </div>
                ) : null}
              </div>
              <div
                ref={releaseContent}
                className="download-release-content"
                style={{ minHeight: switching ? releaseHeight : undefined }}
              >
                {switching ? (
                  <DownloadReleaseSkeleton
                    environmentLabel={environmentLabel(environment)}
                    showWarning={environment !== 'prd'}
                  />
                ) : (
                  <div className="download-release-body">
                    {selectedBuild ? (
                      <>
                        <section className="download-release" aria-label="当前安装包">
                          <div className="download-release-top">
                            <EnvironmentBadge environment={selectedBuild.environment} />
                            <span className="download-release-status">
                              {versionsError ? '当前版本' : historical ? '历史版本' : '最新发布'}
                            </span>
                          </div>
                          <div className="download-version-line">
                            <h2>
                              <small>v</small>
                              {selectedBuild.version}
                            </h2>
                            {mobile ? (
                              <Button
                                className="download-text-button download-version-trigger"
                                type="button"
                                disabled={switching}
                                onClick={openVersions}
                              >
                                切换版本
                                <img src={chevronIcon} alt="" />
                              </Button>
                            ) : null}
                          </div>
                          <p className="download-release-caption">
                            {formatDate(selectedBuild.createdAt)} 发布 ·{' '}
                            {versionsError
                              ? '当前所选版本'
                              : historical
                                ? '当前选择为历史版本'
                                : '当前环境最新版本'}
                          </p>
                          <dl className="download-package-metrics">
                            <div>
                              <dt>构建号</dt>
                              <dd>{selectedBuild.buildNumber || '—'}</dd>
                            </div>
                            <div>
                              <dt>安装包大小</dt>
                              <dd>{formatBytes(selectedBuild.fileSize)}</dd>
                            </div>
                            <div>
                              <dt>文件格式</dt>
                              <dd>{selectedBuild.fileExt.toUpperCase()}</dd>
                            </div>
                            <div className="download-package-id">
                              <dt>应用包名</dt>
                              <dd title={selectedBuild.packageId}>
                                {selectedBuild.packageId || '—'}
                              </dd>
                            </div>
                          </dl>
                          <div className="download-release-bottom">
                            <span>
                              <img src={clockIcon} alt="" />
                              {formatDate(selectedBuild.createdAt)} 更新
                            </span>
                            <span>{selectedBuild.downloadCount} 次下载</span>
                          </div>
                        </section>
                        {shareable ? (
                          <Button
                            className="download-mobile-qr"
                            type="button"
                            aria-label="查看当前版本安装二维码"
                            disabled={switching}
                            onClick={() => setSheet('qr')}
                          >
                            <span className="download-mobile-qr-thumbnail" aria-hidden="true">
                              <QRCode
                                value={selectedBuild.downloadPageUrl}
                                size={56}
                                errorLevel="M"
                                color="#152536"
                                bgColor="#ffffff"
                                bordered={false}
                              />
                            </span>
                            <span className="download-mobile-qr-content">
                              <strong>扫码安装</strong>
                              <span>
                                {environmentLabel(selectedBuild.environment)}环境 ·{' '}
                                {selectedBuild.version}
                              </span>
                              <small>在其他手机打开 · 点击放大</small>
                            </span>
                            <img src={chevronIcon} alt="" />
                          </Button>
                        ) : null}
                        <section className="download-changelog">
                          <div className="download-section-heading">
                            <h2>本次更新</h2>
                            <span>{selectedBuild.version}</span>
                          </div>
                          <p>{selectedBuild.changelog || '发布方暂未填写更新说明。'}</p>
                        </section>
                        {selectedBuild.environment !== 'prd' ? (
                          <p className="download-environment-notice">
                            <img src={infoIcon} alt="" />
                            {environmentLabel(selectedBuild.environment)}
                            包仅用于内部验证，请按发布方指引安装。
                          </p>
                        ) : null}
                      </>
                    ) : versionsError ? (
                      <ErrorState message={`环境版本暂不可用：${versionsError}`} onRetry={reload} />
                    ) : (
                      <div className="download-empty">
                        <Empty
                          title="这个环境还没有安装包"
                          description={`${environmentLabel(environment)}版本发布后会展示在这里。你也可以切换到其他环境查看。`}
                          action={
                            fallback ? (
                              <Button
                                className="download-text-button"
                                type="button"
                                onClick={() => changeEnvironment(fallback.environment)}
                              >
                                查看{environmentLabel(fallback.environment)}环境
                                <img src={chevronIcon} alt="" />
                              </Button>
                            ) : undefined
                          }
                        />
                        <p className="download-environment-notice">
                          生产包用于日常工作；其它环境请根据发布方指引选择。
                        </p>
                      </div>
                    )}
                    {versionsError && selectedBuild ? (
                      <div className="download-versions-error" role="alert">
                        <span>其它版本暂时加载失败，当前包仍可下载。</span>
                        <Button className="download-text-button" type="button" onClick={reload}>
                          重新加载
                        </Button>
                      </div>
                    ) : null}
                  </div>
                )}
                {!mobile && (switching || selectedBuild) ? (
                  <DownloadVersions
                    versions={available}
                    selectedId={selectedBuild?.id ?? ''}
                    complete={!versionsError}
                    expanded={versionsExpanded}
                    loading={switching}
                    onToggle={() => setVersionsExpanded((value) => !value)}
                    onChoose={chooseVersion}
                  />
                ) : null}
              </div>
            </section>
            <section className="download-preview">
              <div className="download-section-heading">
                <h2>应用预览</h2>
                <span>左右滑动查看</span>
              </div>
              <div className="download-preview-content">
                <div className="download-shots">
                  {SCREENSHOTS.map((shot) => (
                    <Button
                      type="button"
                      key={shot.label}
                      onClick={() => setPreview(shot)}
                      aria-label={`查看${shot.label}大图`}
                    >
                      <img src={shot.src} alt={`${shot.label}截图`} loading="lazy" />
                    </Button>
                  ))}
                </div>
                <div className="download-preview-description">
                  <span>MADE FOR YOUR WORKDAY</span>
                  <h2>让每一步业务，都有条不紊。</h2>
                  <p>从客户拜访到订单管理，从资产签收到团队协作，连接日常工作，让信息及时流转。</p>
                  <Button
                    className="download-text-button"
                    type="button"
                    onClick={() => setPreview(SCREENSHOTS[0] ?? null)}
                  >
                    查看全部应用截图
                    <img src={chevronIcon} alt="" />
                  </Button>
                </div>
              </div>
            </section>
            <section className="download-mobile-help">
              <Button
                className="download-text-button"
                type="button"
                onClick={() => setSheet('help')}
              >
                安装遇到问题？查看安装帮助
                <img src={chevronIcon} alt="" />
              </Button>
            </section>
          </main>
          <aside className="download-sidebar">
            <section className="download-install-card" aria-busy={switching}>
              <span className="download-eyebrow">SCAN TO INSTALL</span>
              <h2>在手机上安装</h2>
              {switching ? (
                <DownloadInstallSkeleton />
              ) : selectedBuild ? (
                <div className="download-install-content">
                  {shareable ? (
                    <div className="download-qr">
                      <QRCode
                        value={selectedBuild.downloadPageUrl}
                        size={174}
                        errorLevel="M"
                        color="#152536"
                        bgColor="#ffffff"
                        bordered={false}
                      />
                    </div>
                  ) : (
                    <p className="download-qr-unavailable">
                      当前分享地址仅本机可达，请联系发布方获取可在手机上打开的链接。
                    </p>
                  )}
                  <div className="download-scan-version">
                    <EnvironmentBadge environment={selectedBuild.environment} />
                    <span>
                      {selectedBuild.version} · Build {selectedBuild.buildNumber || '—'}
                    </span>
                  </div>
                  <p className="download-qr-caption">使用手机浏览器扫码打开</p>
                  <DeviceDownloadAction
                    build={selectedBuild}
                    devicePlatform={devicePlatform}
                    target={recommendedTarget}
                    disabled={downloadDisabled}
                    onSwitch={changePlatform}
                    onDownload={onDownload}
                  />
                </div>
              ) : (
                <div className="download-install-empty">
                  <p>当前环境暂无可下载版本</p>
                  <Button className="download-install" disabled type="button">
                    暂无可下载版本
                  </Button>
                </div>
              )}
              <Button
                className="download-install-help"
                type="button"
                onClick={() => setSheet('help')}
              >
                <img src={infoIcon} alt="" />
                安装遇到问题？查看安装帮助
                <img src={chevronIcon} alt="" />
              </Button>
            </section>
            <section className="download-file-note">
              <img src={shieldIcon} alt="" />
              <div>
                <h3>与你收到的版本保持一致</h3>
                <p>
                  二维码与分享链接均指向当前安装包。
                  <br />
                  安装前请核对环境和版本。
                </p>
              </div>
            </section>
          </aside>
        </div>
        <footer className="download-foot">
          <div>
            <Brand />
            <span>让每一次交付，清晰抵达。</span>
          </div>
          <span>仅限内部使用</span>
        </footer>
      </div>
      <footer className="download-dock">
        <div className="download-dock-context">
          {switching ? (
            <Skeleton className="download-skeleton-dock-version" />
          ) : (
            <span>
              {environmentLabel(environment)}环境
              {selectedBuild ? ` · ${selectedBuild.version}` : ''}
              {historical ? ' · 历史版本' : ''}
            </span>
          )}
        </div>
        {switching ? (
          <div role="status" aria-label="正在准备所选版本下载入口">
            <Skeleton className="download-skeleton-install" />
          </div>
        ) : selectedBuild ? (
          <DeviceDownloadAction
            build={selectedBuild}
            devicePlatform={devicePlatform}
            target={recommendedTarget}
            disabled={downloadDisabled}
            onSwitch={changePlatform}
            onDownload={onDownload}
          />
        ) : (
          <Button className="download-install" type="button" disabled>
            暂无可下载版本
          </Button>
        )}
        <p>
          {selectedBuild?.platform === 'ios'
            ? '原始 IPA 文件 · 需签名工具或 TestFlight 安装'
            : '原始安装包 · 仅供内部使用'}
        </p>
      </footer>
      <DetailDrawer
        open={mobile && sheet === 'versions'}
        title={
          <>
            <h2>选择版本</h2>
            <p>
              {environmentLabel(environment)}环境 · {platform} · 共 {available.length} 个版本
            </p>
          </>
        }
        onClose={() => setSheet(null)}
        placement="bottom"
        height="auto"
        className="download-version-sheet"
        footer={
          <Button
            className="button button-primary download-confirm"
            type="button"
            disabled={!pending}
            onClick={() => chooseVersion(pendingVersion)}
          >
            确认选择{pending ? ` · ${pending.version}` : ''}
          </Button>
        }
      >
        <div className="download-version-options">
          {available.map((item, index) => (
            <Radio
              key={item.id}
              name="download-version"
              value={item.id}
              checked={pendingVersion === item.id}
              onChange={() => setPendingVersion(item.id)}
              className={`download-version-option${pendingVersion === item.id ? ' is-selected' : ''}`}
            >
              <span className="download-option-title">
                <strong>{item.version}</strong>
                {index === 0 && !versionsError ? <small>最新</small> : null}
              </span>
              <span className="download-option-meta">
                Build {item.buildNumber || '—'} · {formatBytes(item.fileSize)} ·{' '}
                {formatDate(item.createdAt)}
              </span>
              <span className="download-option-description">
                {item.changelog || '发布方暂未填写更新说明'}
              </span>
            </Radio>
          ))}
        </div>
        {environment !== 'prd' ? (
          <p className="download-environment-notice">
            <img src={infoIcon} alt="" />
            {environmentLabel(environment)}包仅用于内部验证，请确认后再安装。
          </p>
        ) : null}
        {versionsError ? <ErrorState message={versionsError} onRetry={reload} /> : null}
      </DetailDrawer>
      <DetailDrawer
        open={sheet === 'qr' && !!selectedBuild && !!shareable}
        title={
          <>
            <h2>扫码安装</h2>
            <p>用另一台手机扫码，打开当前版本</p>
          </>
        }
        onClose={() => setSheet(null)}
        placement="bottom"
        height="auto"
        className="download-version-sheet download-qr-sheet"
        footer={
          <Button
            className="button button-primary download-confirm"
            type="button"
            disabled={!selectedBuild}
            onClick={() => void copyLink()}
          >
            {copied ? '链接已复制' : '复制当前版本链接'}
          </Button>
        }
      >
        {selectedBuild && shareable ? (
          <div className="download-qr-sheet-content">
            <h3>{selectedBuild.appName}</h3>
            <div className="download-qr-sheet-code">
              <QRCode
                value={selectedBuild.downloadPageUrl}
                size={208}
                errorLevel="M"
                color="#152536"
                bgColor="#ffffff"
                bordered={false}
              />
            </div>
            <div className="download-scan-version">
              <EnvironmentBadge environment={environment} />
              <span>
                {selectedBuild.version} · Build {selectedBuild.buildNumber || '—'}
              </span>
            </div>
            <p>二维码与链接均指向当前版本</p>
            {notice?.showLink ? (
              <div className="download-qr-copy-error" role="status">
                {notice.message}
                <code>{selectedBuild.downloadPageUrl}</code>
              </div>
            ) : null}
          </div>
        ) : null}
      </DetailDrawer>
      <DetailDrawer open={sheet === 'help'} title="安装帮助" onClose={() => setSheet(null)}>
        <div className="download-help-content">
          <h3>{platform} 安装说明</h3>
          <p>
            {build.platform === 'ios'
              ? '本页提供原始 IPA 文件，iOS 无法通过浏览器直接安装。请联系发布方获取企业签名、TestFlight 入口或使用签名工具安装。'
              : build.platform === 'android'
                ? '下载完成后打开 APK 文件。如系统提示禁止安装未知应用，请确认来源可信，再为当前浏览器或文件管理器开启安装权限。'
                : '下载完成后按系统指引安装。如提示应用来源未知，请先向发布方确认来源可信。'}
          </p>
          <h3>下载后打不开安装包？</h3>
          <p>
            请核对文件大小。如果下载中断，请删除未完成的文件后重新下载。仍无法打开时，可用系统自带浏览器重试。
          </p>
          <h3>如何确认安装的版本？</h3>
          <p>先核对页面上的环境、版本号与构建号。切换版本后，下载入口与分享链接会同步更新。</p>
          <h3>下载次数如何计算？</h3>
          <p>只有安装包完整下载成功才会计入次数，点击按钮本身不会增加计数。</p>
        </div>
      </DetailDrawer>
      <DetailDrawer
        open={preview !== null}
        title="应用截图"
        onClose={() => setPreview(null)}
        className="download-preview-drawer"
      >
        <div className="download-preview-thumbnails">
          {SCREENSHOTS.map((shot) => (
            <Button
              type="button"
              key={shot.label}
              className={preview?.src === shot.src ? 'is-active' : ''}
              onClick={() => setPreview(shot)}
            >
              {shot.label}
            </Button>
          ))}
        </div>
        {preview ? (
          <img className="download-preview-large" src={preview.src} alt={`${preview.label}截图`} />
        ) : null}
      </DetailDrawer>
    </article>
  );
}
