/**
 * 概览页：登录后的落地页。
 * 职责：一屏回答「四个环境现在是什么状态」——本周发布、覆盖应用、下载总量、解析降级，
 *       加最近发布、包分布、应用 × 环境矩阵。
 * 输入：useAllBuilds 聚合的四环境列表；输出：统计条 + 三块内容。
 * 边界：不新增后端接口（用四次 listBuilds 聚合）、不写死假数据；
 *       聚合计算留在本页，hook 只负责取全四个环境。
 */
import { Link } from 'react-router-dom';
import type { Build, Environment } from '../../api/builds';
import { Button } from '../../components/Controls';
import { ProjectSelect } from '../../components/ProjectSelect';
import { Empty, ErrorState, Loading } from '../../components/States';
import type { EnvGroup } from '../../hooks/useAllBuilds';
import { useAllBuilds } from '../../hooks/useAllBuilds';
import { useCurrentProject, useProjects } from '../../hooks/useProjects';
import {
  ENVIRONMENTS,
  environmentLabel,
  formatBytes,
  formatDate,
  platformLabel,
} from '../../utils/format';

const WEEK_MS = 7 * 24 * 60 * 60 * 1000;

export function OverviewPage(): JSX.Element {
  const { key } = useCurrentProject();
  const { projects } = useProjects();
  const { loading, error, groups, reload } = useAllBuilds(key);
  const total = groups.reduce((sum, group) => sum + group.items.length, 0);

  return (
    <section className="page">
      <div className="page-head">
        <div>
          <h1>概览</h1>
          <p className="sub">当前项目四个环境的最新状态 · 最近 7 天</p>
        </div>
        <div className="page-head-actions">
          <ProjectSelect projects={projects} currentKey={key} />
          <Button className="button button-ghost" type="button" onClick={reload}>
            刷新
          </Button>
        </div>
      </div>

      {loading ? <Loading text="正在汇总四个环境…" /> : null}
      {!loading && error ? <ErrorState message={error} onRetry={reload} /> : null}
      {!loading && !error && total === 0 ? (
        <Empty
          title="这个项目还没有任何包"
          description="上传第一个 APK / IPA / HAP 后，这里会显示各环境的最新版本、发布趋势与下载量。"
          action={
            <Link className="button button-primary" to={`/p/${key}/upload`}>
              上传第一个包
            </Link>
          }
        />
      ) : null}
      {!loading && !error && total > 0 ? <OverviewDashboard groups={groups} /> : null}
    </section>
  );
}

/** 数据就绪后的仪表盘；与三态分离，页面只管状态、这里只管展示。 */
function OverviewDashboard({ groups }: { groups: EnvGroup[] }): JSX.Element {
  const all = groups.flatMap((group) => group.items);
  const weekAgo = Date.now() - WEEK_MS;

  const weekly = all.filter((build) => new Date(build.createdAt).getTime() >= weekAgo).length;
  const appCount = new Set(all.map((build) => build.appName)).size;
  const downloadTotal = all.reduce((sum, build) => sum + build.downloadCount, 0);
  const fallbackCount = all.filter((build) => build.metaSource === 'fallback').length;

  // 四个环境合到一起后按时间倒序，取最近 5 条。
  const recent = [...all]
    .sort((left, right) => right.createdAt.localeCompare(left.createdAt))
    .slice(0, 5);
  const maxTotal = Math.max(...groups.map((group) => group.total), 1);

  return (
    <>
      <div className="stat-strip">
        <Stat label="本周发布" value={weekly} note="四个环境合计" />
        <Stat label="覆盖应用" value={appCount} note="按应用名聚合" />
        <Stat label="下载总量" value={downloadTotal} note="仅统计成功下载" />
        <Stat label="解析降级" value={fallbackCount} note="靠人工 / 文件名兜底" />
      </div>

      <div className="overview-grid">
        <div className="card">
          <div className="card-head">
            <span className="card-title">最近发布</span>
            <Link className="card-link" to="/packages">
              查看全部 →
            </Link>
          </div>
          <ul className="feed">
            {recent.map((build) => (
              <li className="feed-row" key={build.id}>
                {build.projectIconUrl ? (
                  <img className="app-mark app-mark-img" src={build.projectIconUrl} alt="" />
                ) : (
                  <span className="app-mark" aria-hidden="true">
                    {build.appName.slice(0, 1).toUpperCase()}
                  </span>
                )}
                <div className="feed-main">
                  <div className="feed-name">
                    {build.appName} · {build.version}
                    {build.buildNumber ? ` (${build.buildNumber})` : ''}
                  </div>
                  <div className="feed-meta">
                    <span className={`badge badge-${build.platform}`}>
                      {platformLabel(build.platform)}
                    </span>
                    <span className="badge badge-env">{environmentLabel(build.environment)}</span>
                    {formatBytes(build.fileSize)}
                  </div>
                </div>
                <span className="feed-time">{formatDate(build.createdAt)}</span>
              </li>
            ))}
          </ul>
        </div>

        <div className="card">
          <div className="card-head">
            <span className="card-title">包分布</span>
            <span className="card-hint">按环境</span>
          </div>
          <div className="dist">
            {groups.map((group) => (
              <div className="dist-row" key={group.environment}>
                <span className="dist-name">{environmentLabel(group.environment)}</span>
                <span className="track">
                  <i style={{ width: `${Math.round((group.total / maxTotal) * 100)}%` }} />
                </span>
                <span className="dist-count">{group.total}</span>
              </div>
            ))}
          </div>
        </div>
      </div>

      <AppMatrix groups={groups} />
    </>
  );
}

/** 应用 × 环境矩阵：每格是该环境下的最新版本，一眼看出哪个环境落后。 */
function AppMatrix({ groups }: { groups: EnvGroup[] }): JSX.Element {
  // items 已按上传时间倒序，因此每个 (应用, 环境) 的首次命中就是最新一条。
  const latest = new Map<string, Build>();
  // 顺带记录每个应用最近一次上传时间，用于把最活跃的应用排在上面。
  const lastSeen = new Map<string, string>();
  for (const group of groups) {
    for (const build of group.items) {
      const key = matrixKey(build.appName, group.environment);
      if (!latest.has(key)) {
        latest.set(key, build);
      }
      const current = lastSeen.get(build.appName) ?? '';
      if (build.createdAt > current) {
        lastSeen.set(build.appName, build.createdAt);
      }
    }
  }

  const appNames = Array.from(lastSeen.keys()).sort((left, right) =>
    (lastSeen.get(right) ?? '').localeCompare(lastSeen.get(left) ?? ''),
  );

  return (
    <div className="card">
      <div className="card-head">
        <span className="card-title">应用 × 环境</span>
        <span className="card-hint">悬停版本号可看发布时间</span>
      </div>
      <div className="table-scroll">
        <table className="matrix">
          <thead>
            <tr>
              <th>应用</th>
              {ENVIRONMENTS.map((item) => (
                <th key={item.value}>{item.label}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {appNames.map((name) => (
              <tr key={name}>
                <td className="matrix-app">{name}</td>
                {ENVIRONMENTS.map((item) => {
                  const build = latest.get(matrixKey(name, item.value));
                  return (
                    <td key={item.value}>
                      {build ? (
                        <span className="ver" title={formatDate(build.createdAt)}>
                          {build.version}
                        </span>
                      ) : (
                        <span className="ver-empty">—</span>
                      )}
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function matrixKey(appName: string, environment: Environment): string {
  return `${appName}|${environment}`;
}

function Stat({ label, value, note }: { label: string; value: number; note: string }): JSX.Element {
  return (
    <div className="stat">
      <span className="stat-label">{label}</span>
      <span className="stat-value">{value}</span>
      <span className="stat-note">{note}</span>
    </div>
  );
}
