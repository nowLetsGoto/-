/**
 * 职责：按设计稿展示版本卡片、筛选、编辑抽屉与删除确认。
 * 输入：项目路径、环境/平台/搜索/排序/分页 URL 与当前权限；输出：卡片列表及操作反馈。
 * 边界：请求经 usePackageBuilds；环境经 useEnvironment；不修改安装文件、不拼 API 地址。
 */
import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import type { Build, Environment, Platform, UpdateBuildRequest } from '../../api/builds';
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
  const [editing, setEditing] = useState<Build | null>(null);
  const [notice, setNotice] = useState('');
  const removal = useDeleteAction<Build & { label: string }>(builds.remove, () => {
    setNotice('应用包已删除，原下载页与分享链接已失效。');
    builds.reload();
  });

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

function AppMark({ build }: { build: Build }): JSX.Element {
  return build.projectIconUrl ? (
    <img className="package-mark" src={build.projectIconUrl} alt="" />
  ) : (
    <span className="package-mark" aria-hidden="true">
      {build.appName.slice(0, 1).toUpperCase()}
    </span>
  );
}

function PackageCard({
  build,
  latest,
  onEdit,
  onDelete,
}: {
  build: Build;
  latest: boolean;
  onEdit?: () => void;
  onDelete?: () => void;
}): JSX.Element {
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
        <Link className="button package-download" to={build.downloadPagePath}>
          下载页
          <Icon name="external" size={14} />
        </Link>
        <div className="package-actions">
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
