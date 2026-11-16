/**
 * 应用页：按「应用」聚合包，回答「平台上有哪些应用、各自最新到哪一版」。
 * 职责：从四环境包列表推导应用摘要，渲染卡片网格。
 * 输入：useAllBuilds 的四环境列表；输出：应用卡片或引导式空态。
 * 边界：不新增后端接口——应用的稳定实体（apps 表）属于下一阶段，
 *       这里是从包列表派生的只读视图，字段全部来自 Build DTO。
 */
import { Link } from 'react-router-dom';
import type { Build } from '../../api/builds';
import { ProjectSelect } from '../../components/ProjectSelect';
import { Empty, ErrorState, Loading } from '../../components/States';
import { useAllBuilds } from '../../hooks/useAllBuilds';
import { useCurrentProject, useProjects } from '../../hooks/useProjects';
import { formatDate, platformLabel } from '../../utils/format';

interface AppSummary {
  name: string;
  packageId: string;
  latestAt: string;
  prodVersion: string;
  platforms: string[];
  count: number;
}

export function AppsPage(): JSX.Element {
  const { key } = useCurrentProject();
  const { projects } = useProjects();
  const { loading, error, groups, reload } = useAllBuilds(key);
  const apps = loading || error ? [] : summarize(groups.flatMap((group) => group.items));

  return (
    <section className="page">
      <div className="page-head">
        <div>
          <h1>应用</h1>
          <p className="sub">按应用聚合 · 共 {apps.length} 个 · 数据来自包列表</p>
        </div>
        <div className="page-head-actions">
          <ProjectSelect projects={projects} currentKey={key} />
          <button className="button button-ghost" type="button" onClick={reload}>
            刷新
          </button>
        </div>
      </div>

      {loading ? <Loading text="正在汇总应用…" /> : null}
      {!loading && error ? <ErrorState message={error} onRetry={reload} /> : null}
      {!loading && !error && apps.length === 0 ? (
        <Empty
          title="还没有可聚合的应用"
          description="应用由上传的包自动归集：同一个应用名下的包会合并成一张卡片。"
          action={
            <Link className="button button-primary" to={`/p/${key}/upload`}>
              上传第一个包
            </Link>
          }
        />
      ) : null}
      {!loading && !error && apps.length > 0 ? (
        <div className="apps-grid">
          {apps.map((app) => (
            <AppCard key={app.name} app={app} />
          ))}
        </div>
      ) : null}
    </section>
  );
}

function AppCard({ app }: { app: AppSummary }): JSX.Element {
  return (
    <div className="card app-card">
      <div className="app-card-head">
        <span className="app-avatar" aria-hidden="true">
          {app.name.slice(0, 1).toUpperCase()}
        </span>
        <div>
          <div className="app-name">{app.name}</div>
          <div className="app-pkg">{app.packageId || '—'}</div>
        </div>
      </div>
      <div className="app-rows">
        <div className="app-row">
          最新生产版本 <b>{app.prodVersion}</b>
        </div>
        <div className="app-row">
          累计包数量 <b>{app.count}</b>
        </div>
        <div className="app-row">
          最近更新 <b>{formatDate(app.latestAt)}</b>
        </div>
      </div>
      <div className="app-tags">
        {app.platforms.map((platform) => (
          <span key={platform} className={`badge badge-${platform}`}>
            {platformLabel(platform)}
          </span>
        ))}
      </div>
    </div>
  );
}

/** 把四环境的包按应用名归并；items 已按时间倒序，所以首个命中即最新。 */
function summarize(builds: Build[]): AppSummary[] {
  const byApp = new Map<string, Build[]>();
  for (const build of builds) {
    const list = byApp.get(build.appName);
    if (list) {
      list.push(build);
    } else {
      byApp.set(build.appName, [build]);
    }
  }

  return Array.from(byApp, ([name, items]) => {
    const latest = items.reduce((newest, build) => (build.createdAt > newest.createdAt ? build : newest));
    const prod = items.find((build) => build.environment === 'prd');
    return {
      name,
      packageId: latest.packageId,
      latestAt: latest.createdAt,
      prodVersion: prod?.version ?? '—',
      platforms: Array.from(new Set(items.map((build) => build.platform))),
      count: items.length,
    };
  }).sort((left, right) => right.latestAt.localeCompare(left.latestAt));
}
