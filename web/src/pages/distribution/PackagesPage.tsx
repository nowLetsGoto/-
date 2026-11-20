/**
 * 应用包列表页。
 * 职责：按「页面内环境切换 + 平台筛选」查列表，渲染工具栏与表格；空结果给引导而不是「暂无」。
 * 输入：URL 的 env、project（路径）与 platform（本页）；输出：listBuilds 请求与表格。
 * 边界：不直接 fetch（走 api/builds）、不拼 API 路径、环境不在这里独立存一份；
 *       环境切换只在本页出现，上传入口收进顶栏右上角；
 *       表格统一走 components/DataTable，页面只声明「有哪些列」。
 */
import { useEffect, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import type { TableColumnsType } from '@seakoi/console-kit';
import type { Build, Environment, ListBuildsResult, Platform } from '../../api/builds';
import { listBuilds } from '../../api/builds';
import { errorMessage } from '../../api/client';
import { Button, Segmented } from '../../components/Controls';
import { DataTable } from '../../components/DataTable';
import { ProjectSelect } from '../../components/ProjectSelect';
import { Empty, ErrorState } from '../../components/States';
import { useCurrentProject, useProjects } from '../../hooks/useProjects';
import {
  ENVIRONMENTS,
  PLATFORMS,
  environmentLabel,
  formatBytes,
  formatDate,
  platformLabel,
} from '../../utils/format';

interface PageState {
  loading: boolean;
  data: ListBuildsResult | null;
  error: string;
}

const ALL_ENVIRONMENTS = 'all';
const ALL_PLATFORMS = 'all';

/** 环境筛选的取值集合：全部 + 契约里的每个环境。 */
type EnvironmentFilter = Environment | typeof ALL_ENVIRONMENTS;
/** 平台筛选的取值集合：全部 + 契约里的每个平台。 */
type PlatformFilter = Platform | typeof ALL_PLATFORMS;

function readEnvironment(value: string | null): EnvironmentFilter {
  if (value === ALL_ENVIRONMENTS) {
    return ALL_ENVIRONMENTS;
  }
  return ENVIRONMENTS.some((item) => item.value === value) ? (value as Environment) : 'test';
}

/**
 * 平台筛选写进 URL：与旧链接一致，复制出去仍带着筛选条件。
 * 判断依据是 PLATFORMS（契约投影），新增平台时这里自动支持，不会把 harmony 之类静默降级成「全部」。
 */
function readPlatform(value: string | null): PlatformFilter {
  return PLATFORMS.some((item) => item.value === value) ? (value as Platform) : ALL_PLATFORMS;
}

export function PackagesPage(): JSX.Element {
  const { key, project } = useCurrentProject();
  const { projects } = useProjects();
  const [params, setParams] = useSearchParams();
  // 环境筛选是本页维度：默认测试，?env=all 才看全部；写回 URL 便于分享当前视图。
  const environment = readEnvironment(params.get('env') ?? params.get('environment'));
  const platform = readPlatform(params.get('platform'));
  const navigate = useNavigate();

  const [state, setState] = useState<PageState>({ loading: true, data: null, error: '' });
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    if (!key) {
      return;
    }
    let cancelled = false;
    setState({ loading: true, data: null, error: '' });
    listBuilds({ project: key, environment, platform })
      .then((data) => {
        if (!cancelled) {
          setState({ loading: false, data, error: '' });
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setState({ loading: false, data: null, error: errorMessage(err) });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [key, environment, platform, reloadKey]);

  const setEnvironment = (next: EnvironmentFilter): void => {
    const updated = new URLSearchParams(params);
    updated.delete('environment');
    updated.set('env', next);
    setParams(updated, { replace: true });
  };

  const setPlatform = (next: Platform | typeof ALL_PLATFORMS): void => {
    const updated = new URLSearchParams(params);
    if (next === ALL_PLATFORMS) {
      updated.delete('platform');
    } else {
      updated.set('platform', next);
    }
    setParams(updated, { replace: true });
  };

  const items = state.data?.items ?? [];
  const environmentText = environment === ALL_ENVIRONMENTS ? '全部' : environmentLabel(environment);

  const columns: TableColumnsType<Build> = [
    {
      title: '应用',
      key: 'app',
      render: (_, build) => (
        <div className="cell-app">
          {project?.iconUrl ? (
            <img className="app-mark app-mark-img" src={project.iconUrl} alt="" />
          ) : (
            <span className="app-mark" aria-hidden="true">
              {build.appName.slice(0, 1).toUpperCase()}
            </span>
          )}
          <div>
            <div className="cell-name">{build.appName}</div>
            <div className="cell-sub">{build.packageId || '—'}</div>
          </div>
        </div>
      ),
    },
    {
      title: '版本（构建号）',
      key: 'version',
      render: (_, build) => (
        <>
          <span className="ver">{build.version}</span>
          {build.buildNumber ? <span className="cell-sub"> ({build.buildNumber})</span> : null}
        </>
      ),
    },
    {
      title: '平台',
      key: 'platform',
      render: (_, build) => (
        <span className={`badge badge-${build.platform}`}>{platformLabel(build.platform)}</span>
      ),
    },
    {
      title: '环境',
      key: 'environment',
      render: (_, build) => (
        <span className="badge badge-env">{environmentLabel(build.environment)}</span>
      ),
    },
    { title: '大小', key: 'size', render: (_, build) => formatBytes(build.fileSize) },
    {
      title: '上传时间',
      key: 'createdAt',
      render: (_, build) => <span className="cell-sub">{formatDate(build.createdAt)}</span>,
    },
    { title: '下载', key: 'downloadCount', render: (_, build) => build.downloadCount },
    {
      title: '',
      key: 'actions',
      width: 100,
      render: (_, build) => (
        <div className="row-actions">
          <Button
            className="button button-ghost button-small"
            type="button"
            onClick={() => navigate(build.downloadPagePath)}
          >
            下载页
          </Button>
        </div>
      ),
    },
  ];

  const emptyState =
    platform === ALL_PLATFORMS ? (
      <Empty
        title={`${environmentText}环境还没有包`}
        description="上传第一个 APK / IPA / HAP 后，这里会显示版本、构建号与下载次数，并自动生成可扫码的下载页。"
        action={
          <Link className="button button-primary" to={`/p/${key}/upload`}>
            上传第一个包
          </Link>
        }
      />
    ) : (
      <Empty
        title={`${environmentText}环境暂无 ${platformLabel(platform)} 包`}
        description="换一个平台，或回到全部平台看看其它包。"
        action={
          <Button
            className="button button-ghost"
            type="button"
            onClick={() => setPlatform(ALL_PLATFORMS)}
          >
            查看全部平台
          </Button>
        }
      />
    );

  return (
    <section className="page">
      <div className="page-head">
        <div>
          <h1>应用包</h1>
          <p className="sub">
            {environmentText}环境 · {state.data ? `共 ${state.data.total} 个包` : '加载中…'} ·
            按上传时间倒序
          </p>
        </div>
        <div className="page-head-actions">
          <ProjectSelect projects={projects} currentKey={key} />
        </div>
      </div>

      <div className="page-filters">
        <div className="filter-group">
          <span className="filter-label">按环境筛选</span>
          <Segmented<EnvironmentFilter>
            aria-label="环境"
            value={environment}
            options={[
              { value: ALL_ENVIRONMENTS, label: '全部' },
              ...ENVIRONMENTS.map((item) => ({ value: item.value, label: item.label })),
            ]}
            onChange={setEnvironment}
          />
        </div>
        <div className="filter-group">
          <span className="filter-label">按机型筛选</span>
          <Segmented<PlatformFilter>
            aria-label="平台"
            value={platform}
            options={[
              { value: ALL_PLATFORMS, label: '全部' },
              ...PLATFORMS.map((item) => ({ value: item.value, label: item.label })),
            ]}
            onChange={setPlatform}
          />
        </div>
      </div>

      <div className="card">
        {state.error ? (
          <ErrorState message={state.error} onRetry={() => setReloadKey((value) => value + 1)} />
        ) : (
          <DataTable<Build>
            columns={columns}
            dataSource={items}
            rowKey={(build) => build.id}
            loading={state.loading}
            empty={emptyState}
          />
        )}
      </div>
    </section>
  );
}
