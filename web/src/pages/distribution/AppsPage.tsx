/**
 * 应用页：按「应用」聚合包，回答「平台上有哪些应用、各自最新到哪一版」。
 * 职责：从四环境包列表推导应用摘要，渲染卡片网格。
 * 输入：useAllBuilds 的四环境列表；输出：应用卡片或引导式空态。
 * 边界：不新增后端接口——应用的稳定实体（apps 表）属于下一阶段，
 *       这里是从包列表派生的只读视图，字段全部来自 Build DTO。
 */
import { Link } from 'react-router-dom';
import type { Build } from '../../api/builds';
import { Button } from '../../components/Controls';
import { ProjectSelect } from '../../components/ProjectSelect';
import { Empty, ErrorState, Loading } from '../../components/States';
import { useAllBuilds } from '../../hooks/useAllBuilds';
import { useCurrentProject, useProjects } from '../../hooks/useProjects';
import { formatDate, platformLabel } from '../../utils/format';

interface AppSummary {
  name: string;
  packageId: string;
  iconUrl: string;
  latestAt: string;
  latestVersion: string;
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
          <Button className="button button-ghost" type="button" onClick={reload}>
            刷新
          </Button>
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
            <AppCard key={app.name} app={app} projectKey={key} />
          ))}
        </div>
      ) : null}
    </section>
  );
}

function AppCard({ app, projectKey }: { app: AppSummary; projectKey: string }): JSX.Element {
  return (
    <article className="card app-card">
      <div className="app-card-head">
        {app.iconUrl ? (
          <img className="app-avatar app-avatar-img" src={app.iconUrl} alt="" />
        ) : (
          <span className="app-avatar" aria-hidden="true">
            {app.name.slice(0, 1).toUpperCase()}
          </span>
        )}
        <div className="app-card-title">
          <h3 className="app-name">{app.name}</h3>
          <p className="app-pkg">{app.packageId || '未填写包名'}</p>
        </div>
        <div className="app-card-platforms">
          {app.platforms.map((platform) => (
            <span key={platform} className={`badge badge-${platform}`}>
              {platformLabel(platform)}
            </span>
          ))}
        </div>
      </div>

      <div className="app-card-stats">
        <div>
          <span>最新版本</span>
          <strong>{app.latestVersion}</strong>
        </div>
        <div>
          <span>累计包</span>
          <strong>{app.count}</strong>
        </div>
        <div>
          <span>最近更新</span>
          <strong>{formatDate(app.latestAt)}</strong>
        </div>
      </div>

      <div className="app-card-foot">
        <span>由包列表自动归集</span>
        <Link to={`/p/${projectKey}/packages`}>查看包列表 →</Link>
      </div>
    </article>
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
    const latest = items.reduce((newest, build) =>
      build.createdAt > newest.createdAt ? build : newest,
    );
    return {
      name,
      packageId: latest.packageId,
      iconUrl: latest.projectIconUrl,
      latestAt: latest.createdAt,
      latestVersion: latest.version,
      platforms: Array.from(new Set(items.map((build) => build.platform))),
      count: items.length,
    };
  }).sort((left, right) => right.latestAt.localeCompare(left.latestAt));
}
