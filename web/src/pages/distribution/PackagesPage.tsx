/**
 * 职责：按设计稿展示版本卡片、筛选、编辑抽屉与删除确认。
 * 输入：项目路径、环境/平台/搜索/排序/分页 URL 与当前权限；输出：卡片列表及操作反馈。
 * 边界：请求经 usePackageBuilds；环境经 useEnvironment；不修改安装文件、不拼 API 地址。
 */
import { useCallback, useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import type {
  Build,
  Environment,
  Platform,
  SyncProgress,
  UpdateBuildRequest,
} from '../../api/builds';
import { errorMessage } from '../../api/client';
import { Button, Input, Segmented, TextArea } from '../../components/Controls';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { DetailDrawer } from '../../components/DetailDrawer';
import { PagePagination } from '../../components/PagePagination';
import { ProjectSelect } from '../../components/ProjectSelect';
import { Select } from '../../components/Select';
import { Skeleton } from '../../components/Skeleton';
import { Empty, ErrorState } from '../../components/States';
import { Icon } from '../../components/icons';
import { useDeleteAction } from '../../hooks/useDeleteAction';
import { useSyncToR2 } from '../../hooks/useSyncToR2';
import { useLocalProgressPolling } from '../../hooks/useLocalProgressPolling';
import { useRetryLocalPull } from '../../hooks/useRetryLocalPull';
import { useEnvironment } from '../../hooks/useEnvironment';
import { useMe } from '../../hooks/useMe';
import { usePackageBuilds } from '../../hooks/usePackageBuilds';
import { usePageState } from '../../hooks/usePageState';
import { useCurrentProject, useProjects } from '../../hooks/useProjects';
import {
  ENVIRONMENTS,
  PLATFORMS,
  environmentLabel,
  formatBytes,
  formatDate,
  platformLabel,
} from '../../utils/format';
import { can } from '../../utils/permission';

type PlatformFilter = Platform | 'all';

export function PackagesPage(): JSX.Element {
  const { key } = useCurrentProject();
  return <PackageList key={key} projectKey={key} />;
}

function PackageList({ projectKey }: { projectKey: string }): JSX.Element {
  const { projects } = useProjects();
  const { me } = useMe();
  const writable = can(me, 'build.write');
  const deletable = can(me, 'build.delete');
  const [params, setParams] = useSearchParams();
  const [environment, setEnvironment] = useEnvironment('all');
  const platform: PlatformFilter = PLATFORMS.some((item) => item.value === params.get('platform'))
    ? (params.get('platform') as Platform)
    : 'all';
  const search = params.get('q') ?? '';
  const ascending = params.get('sort') === 'oldest';
  const { page, pageSize, change } = usePageState();
  const builds = usePackageBuilds({ project: projectKey, environment, platform });
  const { reload: reloadBuilds } = builds;
  const [editing, setEditing] = useState<Build | null>(null);
  const [notice, setNotice] = useState('');
  const removal = useDeleteAction<Build & { label: string }>(builds.remove, () => {
    setNotice('应用包已删除，原下载页与分享链接已失效。');
    builds.reload();
  });
  // 同步结果统一走 notice 提示；成功时刷新列表，
  // 状态以服务端返回为准，前端不自己推测 r2State 怎么变。
  // useCallback 保持引用稳定，避免进度恢复 effect 随每次渲染重复触发。
  const onSyncResult = useCallback(
    (error: string) => {
      if (error) {
        setNotice(error);
        return;
      }
      setNotice('已同步到对象存储，下载将走 CDN 加速。');
      reloadBuilds();
    },
    [reloadBuilds],
  );
  const {
    progress: syncProgress,
    sync: startSync,
    restore: restoreSync,
  } = useSyncToR2(onSyncResult);

  // 重试下载到本机：结果同样走 notice，成功时刷新列表让进度条重新起步。
  const { retryingID, retry: retryPull } = useRetryLocalPull(
    useCallback(
      (error: string) => {
        setNotice(error || '已重新开始下载到本机。');
        if (!error) reloadBuilds();
      },
      [reloadBuilds],
    ),
  );

  // 切菜单回来会重新挂载列表页，内存中的进度状态随之消失。
  // 服务端仍保留 10 分钟进度，按包 id 恢复即可让按钮继续禁用、进度条继续显示。
  useEffect(() => {
    restoreSync(builds.items.map((build) => build.id));
  }, [builds.items, restoreSync]);

  // 有包正在下载到本机时定时刷新：进度存在数据库里，
  // 前端不自己累加，只按服务端的数字重画。
  // 用静默刷新：普通 reload 会清空列表并闪骨架屏，
  // 5 秒一次的话页面等于一直在闪烁。
  useLocalProgressPolling(builds.items, builds.reloadSilently);

  const setFilter = (name: string, value: string): void => {
    const next = new URLSearchParams(params);
    if (value && value !== 'all') next.set(name, value);
    else next.delete(name);
    next.delete('page');
    setParams(next, { replace: true });
  };
  const query = search.trim().toLocaleLowerCase();
  const filtered = builds.items
    .filter((build) =>
      [build.appName, build.version, build.buildNumber, build.packageId].some((value) =>
        value.toLocaleLowerCase().includes(query),
      ),
    )
    .sort((a, b) => {
      const order = a.createdAt.localeCompare(b.createdAt) || a.id.localeCompare(b.id);
      return ascending ? order : -order;
    });
  const lastPage = Math.max(1, Math.ceil(filtered.length / pageSize));
  const currentPage = Math.min(page, lastPage);
  const items = filtered.slice((currentPage - 1) * pageSize, currentPage * pageSize);
  const latestID = builds.items[0]?.id;

  useEffect(() => {
    if (!builds.loading && !builds.error && page > lastPage) change(lastPage, pageSize);
  }, [builds.loading, builds.error, page, lastPage, pageSize, change]);

  const empty = builds.items.length === 0 && environment === 'all' && platform === 'all' && !query;
  return (
    <section className="page packages-page">
      <div className="page-head packages-head">
        <div>
          <div className="packages-title">
            <h1>应用包</h1>
            <span className="packages-count">{builds.loading ? '…' : filtered.length}</span>
          </div>
          <p className="sub">管理应用版本与分发链接</p>
        </div>
        <div className="page-head-actions">
          <ProjectSelect projects={projects} currentKey={projectKey} />
          {writable ? (
            <Link className="button button-primary" to={`/p/${projectKey}/upload`}>
              <Icon name="plus" />
              上传包
            </Link>
          ) : null}
        </div>
      </div>

      <div className="package-filters">
        <div className="package-search-row">
          <Input
            className="package-search"
            aria-label="搜索应用名称、版本号"
            placeholder="搜索应用名称、版本号"
            prefix={<Icon name="search" />}
            value={search}
            allowClear
            onChange={(event) => setFilter('q', event.target.value)}
          />
          <Select
            className="package-sort"
            value={ascending ? 'oldest' : 'newest'}
            width={164}
            options={[
              { value: 'newest', label: '上传时间 ↓' },
              { value: 'oldest', label: '上传时间 ↑' },
            ]}
            onChange={(value) => setFilter('sort', value === 'newest' ? '' : value)}
          />
        </div>
        <div className="package-filter-row">
          <div className="filter-group">
            <span className="filter-label">环境</span>
            <Segmented<Environment | 'all'>
              aria-label="环境"
              value={environment}
              options={[{ value: 'all', label: '全部' }, ...ENVIRONMENTS]}
              onChange={setEnvironment}
            />
          </div>
          <div className="filter-group">
            <span className="filter-label">平台</span>
            <Segmented<PlatformFilter>
              aria-label="平台"
              value={platform}
              options={[{ value: 'all', label: '全部' }, ...PLATFORMS]}
              onChange={(value) => setFilter('platform', value)}
            />
          </div>
        </div>
      </div>

      {notice ? (
        <p className="package-notice" role="status">
          {notice}
        </p>
      ) : null}
      <div className="package-section-head">
        <h2>{environment === 'all' ? '全部版本' : `${environmentLabel(environment)}版本`}</h2>
        <span>
          {query
            ? `找到 ${filtered.length} 个匹配版本`
            : `按上传时间${ascending ? '正序' : '倒序'}`}
        </span>
      </div>
      {builds.error ? (
        <ErrorState message={builds.error} onRetry={builds.reload} />
      ) : builds.loading ? (
        <div className="package-grid" role="status" aria-label="正在加载应用包">
          {Array.from({ length: 6 }, (_, index) => (
            <div className="package-card package-card-skeleton" key={index}>
              <Skeleton className="skeleton-line skeleton-line-lg" />
              <Skeleton className="skeleton-line" />
              <Skeleton className="skeleton-line skeleton-line-short" />
            </div>
          ))}
        </div>
      ) : filtered.length === 0 ? (
        <div className="card">
          <Empty
            title={empty ? '还没有应用包' : '没有找到匹配的版本'}
            description={
              empty
                ? writable
                  ? '上传第一个 APK / IPA / HAP，开始管理版本与分发链接。'
                  : '项目还没有发布版本，请联系有发布权限的成员上传。'
                : '试试其他关键词，或调整环境与平台筛选。'
            }
            action={
              empty ? (
                writable ? (
                  <Link className="button button-primary" to={`/p/${projectKey}/upload`}>
                    上传第一个包
                  </Link>
                ) : undefined
              ) : (
                <Button
                  className="button button-ghost"
                  type="button"
                  onClick={() => {
                    const next = new URLSearchParams(params);
                    ['q', 'platform', 'env', 'environment', 'page'].forEach((name) =>
                      next.delete(name),
                    );
                    next.set('env', 'all');
                    setParams(next, { replace: true });
                  }}
                >
                  清除筛选
                </Button>
              )
            }
          />
        </div>
      ) : (
        <>
          <div className="package-grid">
            {items.map((build) => (
              <PackageCard
                key={build.id}
                build={build}
                latest={build.id === latestID}
                onEdit={writable ? () => setEditing(build) : undefined}
                onDelete={
                  deletable ? () => removal.request({ ...build, label: build.appName }) : undefined
                }
                onSync={writable ? () => startSync(build.id) : undefined}
                progress={syncProgress[build.id]}
                onRetryPull={writable ? () => retryPull(build.id) : undefined}
                retryingPull={retryingID === build.id}
              />
            ))}
          </div>
          <PagePagination
            page={currentPage}
            pageSize={pageSize}
            total={filtered.length}
            onChange={change}
          />
        </>
      )}

      {editing ? (
        <PackageEditor
          key={editing.id}
          build={editing}
          onClose={() => setEditing(null)}
          onSave={async (input) => {
            await builds.save(editing.id, input);
            setEditing(null);
            setNotice('应用包信息已保存，下载页已同步更新。');
          }}
        />
      ) : null}
      <ConfirmDialog
        open={removal.target !== null}
        title="删除此应用包？"
        confirmText="删除应用包"
        warningTone
        loading={removal.loading}
        onConfirm={removal.confirm}
        onCancel={removal.cancel}
        description={
          <>
            {removal.target ? (
              <div className="package-delete-summary">
                <AppMark build={removal.target} />
                <div>
                  <strong>{removal.target.appName}</strong>
                  <p>
                    {removal.target.version}{' '}
                    {removal.target.buildNumber ? `(${removal.target.buildNumber})` : ''} ·{' '}
                    {platformLabel(removal.target.platform)} ·{' '}
                    {environmentLabel(removal.target.environment)}
                  </p>
                </div>
              </div>
            ) : null}
            <p>删除后，安装包文件和对应下载页将不可用，已分享的链接也将失效。</p>
            <p className="package-delete-warning">
              <Icon name="warning" />
              此操作无法撤销，其他版本不受影响。
            </p>
            {removal.error ? (
              <p className="field-error" role="alert">
                {removal.error}
              </p>
            ) : null}
          </>
        }
      />
    </section>
  );
}

/**
 * 把「下载到本机」的服务端状态翻译成进度条数据；不该显示时返回 undefined。
 *
 * 为什么要翻译而不是直接用：服务端的 localState 是业务语义
 * （absent / pulling / present / failed），进度条只认
 * running / succeeded / failed 三态。这层映射让两边各说各的话，
 * 将来服务端加状态也不会把前端组件改乱。
 *
 * 三种不显示的情况，各有原因：
 *   - absent：还没开始下载，显示一条空进度条只会让人以为卡住了；
 *   - 没有 r2 对象（pending）：本机拉不到，进度条没有意义；
 *   - present 但没有下载记录（本地原本就有文件）：没发生过下载，
 *     不该凭空冒出一条「下载完成」。
 */
function localProgressOf(build: Build): SyncProgress | undefined {
  // 没有 R2 对象就没有「下载」这回事——字节都不在桶里。
  if (build.r2State === 'pending') {
    return undefined;
  }
  switch (build.localState) {
    case 'pulling':
      return {
        state: 'running',
        bytes: build.localPulledBytes,
        total: build.fileSize,
        error: '',
      };
    case 'present':
      // 只有真的下载过（字节数 > 0）才显示完成条，避免给存量包凭空加一条绿条。
      return build.localPulledBytes > 0
        ? { state: 'succeeded', bytes: build.fileSize, total: build.fileSize, error: '' }
        : undefined;
    case 'failed':
      return {
        state: 'failed',
        bytes: build.localPulledBytes,
        total: build.fileSize,
        error: build.localError,
      };
    default:
      return undefined;
  }
}

function AppMark({ build }: { build: Build }): JSX.Element {
  return build.projectIconUrl ? (
    <img className="package-mark" src={build.projectIconUrl} alt="" />
  ) : (
    <span className="package-mark" aria-hidden="true">
      {build.appName.slice(0, 1).toUpperCase()}
    </span>
  );
}

/**
 * 卡片上的进度条：进行中按字节推进，成功绿色、失败红色。
 *
 * 同时服务「上传到 R2」与「下载到本机」两个方向——它们交互完全同构，
 * 只是文案与数据来源不同。用一个组件 + 不同 label，比复制一份再各自演化更好维护。
 * label 从外部传入而非放进 SyncProgress：那是接口契约，
 * 不该为了展示而多一个只有前端才知道的字段。
 */
function SyncProgressBar({
  progress,
  label: verb,
  onRetry,
  retrying,
}: {
  progress: SyncProgress;
  label: string;
  /** 仅失败时使用：给出则可重试，不传则只显示失败原因。 */
  onRetry?: () => void;
  /** 重试请求进行中，用于禁用按钮避免连点。 */
  retrying?: boolean;
}): JSX.Element {
  // 成功固定 100%；进行中与失败都按真实字节算。
  //
  // 失败时不能画满：那条满格的红色进度条会让人以为「已经下完但出错了」，
  // 而实际上它是断在 22% 之类的位置。停在断点，用户才知道重试是从头再来、
  // 以及上次是在哪儿失败的。
  const percent =
    progress.state === 'succeeded'
      ? 100
      : progress.total > 0
        ? Math.min(100, Math.round((progress.bytes / progress.total) * 100))
        : 0;
  const label =
    progress.state === 'succeeded'
      ? `${verb}完成`
      : progress.state === 'failed'
        ? `${verb}失败：${progress.error || '请稍后重试'}`
        : progress.total > 0
          ? `${verb} ${formatBytes(progress.bytes)} / ${formatBytes(progress.total)}`
          : `${verb}，准备中…`;

  return (
    <div
      className={`package-sync package-sync-${progress.state}`}
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={percent}
    >
      <div className="package-sync-label">
        <span>{label}</span>
        {/* 右侧固定占位：运行中显示百分比，失败时换成重试按钮。
            两者不共存——失败后进度已停，再显示一个静止的百分比没有意义，
            而这个位置正是用户视线落点，放重试最容易被找到。 */}
        {/* 百分比只在「进行中」与「失败」两种状态下显示：
            成功时条已满，数字是多余的；失败时反而更需要它——
            用户据此知道上次断在哪儿。失败态的百分比与重试按钮并排，
            两者都在标签行右侧，视线一致。 */}
        {progress.state !== 'succeeded' ? <span>{percent}%</span> : null}
        {progress.state === 'failed' && onRetry ? (
          <button
            className="package-sync-retry"
            type="button"
            onClick={onRetry}
            disabled={retrying}
            aria-label={`重试${verb}`}
          >
            {retrying ? '重试中…' : '重试'}
          </button>
        ) : null}
      </div>
      <div className="package-sync-track">
        <div className="package-sync-fill" style={{ width: `${percent}%` }} />
      </div>
    </div>
  );
}

function PackageCard({
  build,
  latest,
  onEdit,
  onDelete,
  onSync,
  progress,
  onRetryPull,
  retryingPull,
}: {
  build: Build;
  latest: boolean;
  onEdit?: () => void;
  onDelete?: () => void;
  onSync?: () => void;
  progress?: SyncProgress;
  /** 下载失败时提供重试入口；无写权限时不传，卡片只显示失败原因。 */
  onRetryPull?: () => void;
  retryingPull?: boolean;
}): JSX.Element {
  // 下载到本机的进度全部由服务端字段派生，前端不维护副本：
  // 这样切菜单、换标签页、刷新页面看到的都是同一份事实。
  const localProgress = localProgressOf(build);
  const syncing = progress?.state === 'running';
  const justSynced = progress?.state === 'succeeded';
  return (
    <article
      className="package-card"
      aria-label={`${build.appName} ${build.version} ${platformLabel(build.platform)} ${environmentLabel(build.environment)}`}
    >
      <div className="package-card-head">
        <AppMark build={build} />
        <div className="package-identity">
          <div className="package-app-name">
            <h3 title={build.appName}>{build.appName}</h3>
            {latest ? <span className="package-latest">最新上传</span> : null}
          </div>
          <p title={build.packageId}>{build.packageId || '未填写包名'}</p>
        </div>
        <span className="package-platform">
          <Icon name="phone" />
          {platformLabel(build.platform)}
        </span>
      </div>
      {/* 上传到 R2 的进度：用户点「同步到 R2」后出现，终态保留约 10 分钟。 */}
      {progress && progress.state !== 'idle' ? (
        <SyncProgressBar progress={progress} label="同步到 R2" />
      ) : null}
      {/* 下载到本机的进度：数据来自数据库，切菜单回来仍在，终态永久保留。 */}
      {localProgress ? (
        <SyncProgressBar
          progress={localProgress}
          label="下载到本机"
          onRetry={onRetryPull}
          retrying={retryingPull}
        />
      ) : null}
      <div className="package-version-row">
        <div className="package-version">
          <strong title={build.version}>{build.version}</strong>
          {build.buildNumber ? (
            <span title={`Build ${build.buildNumber}`}>Build {build.buildNumber}</span>
          ) : null}
        </div>
        <span className={`package-env package-env-${build.environment}`}>
          {environmentLabel(build.environment)}
        </span>
      </div>
      <div className="package-meta">
        <span>
          <Icon name="file" />
          {formatBytes(build.fileSize)}
        </span>
        <span>
          <Icon name="download" />
          {build.downloadCount} 次下载
        </span>
      </div>
      <div className="package-time">
        <Icon name="clock" />
        <time dateTime={build.createdAt}>{formatDate(build.createdAt)}</time>
      </div>
      <div className="package-card-footer">
        {/* 优先打开分享页（免登录）；无有效分享时退回内部预览（需登录）。 */}
        <Link
          className="button package-download"
          to={build.downloadSharePath || build.downloadPagePath}
        >
          下载页
          <Icon name="external" size={14} />
        </Link>
        <div className="package-actions">
          {/* 只在「对象确实不在 R2」时出现：巡检淘汰为 evicted 才需要人工重推。
              列表可能有延迟：刚同步成功时后端状态已是 synced，但列表还没刷新，
              因此同时用进度终态挡住按钮，避免用户重复上传。 */}
          {onSync && build.r2State === 'evicted' && !justSynced ? (
            <Button
              className="button button-quiet"
              type="button"
              onClick={onSync}
              disabled={syncing}
              aria-label={`将 ${build.appName} ${build.version} 同步到对象存储`}
              title="对象已被容量淘汰，点击用本机副本重建并恢复 CDN 加速"
            >
              <Icon name="cloud" />
              {syncing ? '同步中…' : '同步到 R2'}
            </Button>
          ) : null}
          {onEdit ? (
            <Button
              className="button button-quiet"
              type="button"
              onClick={onEdit}
              aria-label={`编辑 ${build.appName} ${build.version}`}
            >
              <Icon name="edit" />
              编辑
            </Button>
          ) : null}
          {onDelete ? (
            <Button
              className="button button-quiet package-delete"
              type="button"
              onClick={onDelete}
              aria-label={`删除 ${build.appName} ${build.version}`}
            >
              <Icon name="trash" />
              删除
            </Button>
          ) : null}
        </div>
      </div>
    </article>
  );
}

function PackageEditor({
  build,
  onClose,
  onSave,
}: {
  build: Build;
  onClose: () => void;
  onSave: (input: UpdateBuildRequest) => Promise<void>;
}): JSX.Element {
  const [form, setForm] = useState<UpdateBuildRequest>({
    appName: build.appName,
    version: build.version,
    buildNumber: build.buildNumber,
    environment: build.environment,
    changelog: build.changelog,
  });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const close = (): void => {
    if (!busy) onClose();
  };
  const submit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    if (busy) return;
    if (!form.appName.trim() || !form.version.trim()) {
      setError('请填写应用名称与版本号');
      return;
    }
    setBusy(true);
    setError('');
    onSave(form).catch((err: unknown) => {
      setError(errorMessage(err));
      setBusy(false);
    });
  };
  return (
    <DetailDrawer
      open
      title={
        <div className="package-editor-title">
          编辑应用包
          <p>
            {build.appName} · {platformLabel(build.platform)} ·{' '}
            {environmentLabel(build.environment)}
          </p>
        </div>
      }
      width={600}
      className="package-editor"
      onClose={close}
      footer={
        <div className="package-editor-footer">
          <span>修改后同步更新下载页展示信息</span>
          <div>
            <Button className="button button-ghost" type="button" disabled={busy} onClick={close}>
              取消
            </Button>
            <Button
              className="button button-primary"
              type="submit"
              form="package-edit-form"
              disabled={busy}
            >
              {busy ? '保存中…' : '保存修改'}
            </Button>
          </div>
        </div>
      }
    >
      <div className="package-source-file">
        <div>
          <span className="package-mark">
            <Icon name="file" size={24} />
          </span>
          <div>
            <strong>{build.originalFileName}</strong>
            <p>
              {formatBytes(build.fileSize)} · 上传于 {formatDate(build.createdAt)}
            </p>
          </div>
        </div>
        <p>安装文件不可替换，更新安装包请重新上传</p>
      </div>
      <form id="package-edit-form" onSubmit={submit}>
        <fieldset disabled={busy} className="package-editor-fields">
          <label className="field">
            <span>
              应用名称 <span aria-hidden="true">*</span>
            </span>
            <Input
              id="package-app-name"
              autoFocus
              required
              maxLength={100}
              value={form.appName}
              onChange={(event) => setForm({ ...form, appName: event.target.value })}
            />
          </label>
          <div className="field-grid">
            <label className="field">
              <span>
                版本号 <span aria-hidden="true">*</span>
              </span>
              <Input
                required
                maxLength={50}
                value={form.version}
                onChange={(event) => setForm({ ...form, version: event.target.value })}
              />
            </label>
            <label className="field">
              <span>构建号</span>
              <Input
                maxLength={50}
                value={form.buildNumber}
                onChange={(event) => setForm({ ...form, buildNumber: event.target.value })}
              />
            </label>
          </div>
          <div className="field">
            <span id="package-edit-environment">环境</span>
            <Segmented<Environment>
              aria-labelledby="package-edit-environment"
              value={form.environment}
              options={[...ENVIRONMENTS]}
              onChange={(value) => setForm({ ...form, environment: value })}
              disabled={busy}
              block
            />
          </div>
          <label className="field">
            <span>更新说明</span>
            <TextArea
              rows={6}
              maxLength={2000}
              value={form.changelog}
              placeholder="填写本次版本的更新内容"
              onChange={(event) => setForm({ ...form, changelog: event.target.value })}
            />
          </label>
        </fieldset>
        {error ? (
          <p className="field-error" role="alert">
            {error}
          </p>
        ) : null}
      </form>
    </DetailDrawer>
  );
}
