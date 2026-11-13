/**
 * 包列表页（P-02）。
 * 职责：四环境一级切换 + 平台筛选（写入 URL searchParams）、按时间倒序渲染列表与下载次数列。
 * 输入：URL 查询参数；输出：listBuilds 请求与三态列表。
 * 边界：不直接 fetch（走 api/builds）、不拼 API 路径；列表页无二维码入口；
 *       行操作「下载页」用服务端给的相对 downloadPagePath 做 react-router 内部跳转。
 */
import { useEffect, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import type { Environment, ListBuildsResult, Platform } from '../api/builds';
import { listBuilds } from '../api/builds';
import { errorMessage } from '../api/client';
import { Empty, ErrorState, Loading } from '../components/States';
import { ENVIRONMENTS, PLATFORMS, environmentLabel, formatBytes, formatDate, platformLabel, shortText } from '../utils/format';

interface PageState {
  loading: boolean;
  data: ListBuildsResult | null;
  error: string;
}

export function ListPage(): JSX.Element {
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();

  const envParam = params.get('environment');
  const environment: Environment = ENVIRONMENTS.some((item) => item.value === envParam)
    ? (envParam as Environment)
    : 'test';
  const platformParam = params.get('platform');
  const platform: Platform | 'all' = platformParam === 'android' || platformParam === 'ios' ? platformParam : 'all';

  const [state, setState] = useState<PageState>({ loading: true, data: null, error: '' });
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setState({ loading: true, data: null, error: '' });
    listBuilds({ environment, platform })
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
  }, [environment, platform, reloadKey]);

  // 筛选条件写进 URL：复制转发、刷新、返回列表都不丢条件。
  const updateFilter = (nextEnv: Environment, nextPlatform: Platform | 'all'): void => {
    const next = new URLSearchParams();
    next.set('environment', nextEnv);
    if (nextPlatform !== 'all') {
      next.set('platform', nextPlatform);
    }
    setParams(next);
  };

  return (
    <section className="page">
      <div className="page-head">
        <h1>App 包列表</h1>
        <Link className="button button-primary" to="/upload">
          + 上传包
        </Link>
      </div>

      <div className="filters">
        <div className="filter-row">
          <span className="filter-label">环境</span>
          <div className="segmented">
            {ENVIRONMENTS.map((item) => (
              <button
                key={item.value}
                type="button"
                className={environment === item.value ? 'segment segment-active' : 'segment'}
                onClick={() => updateFilter(item.value, platform)}
              >
                {item.label}
                <small>{item.value}</small>
              </button>
            ))}
          </div>
        </div>
        <div className="filter-row">
          <span className="filter-label">平台</span>
          <div className="segmented">
            <button
              type="button"
              className={platform === 'all' ? 'segment segment-active' : 'segment'}
              onClick={() => updateFilter(environment, 'all')}
            >
              全部
            </button>
            {PLATFORMS.map((item) => (
              <button
                key={item.value}
                type="button"
                className={platform === item.value ? 'segment segment-active' : 'segment'}
                onClick={() => updateFilter(environment, item.value)}
              >
                {item.label}
              </button>
            ))}
          </div>
        </div>
      </div>

      {state.loading ? <Loading text="正在加载包列表…" /> : null}
      {!state.loading && state.error ? (
        <ErrorState message={state.error} onRetry={() => setReloadKey((value) => value + 1)} />
      ) : null}
      {!state.loading && !state.error && state.data && state.data.items.length === 0 ? (
        <Empty
          text={`${environmentLabel(environment)} 环境暂无包`}
          action={
            <Link className="button button-primary" to="/upload">
              去上传
            </Link>
          }
        />
      ) : null}
      {!state.loading && !state.error && state.data && state.data.items.length > 0 ? (
        <div className="card table-card">
          <p className="muted">共 {state.data.total} 个包，按上传时间倒序</p>
          <div className="table-scroll">
            <table className="table">
              <thead>
                <tr>
                  <th>平台</th>
                  <th>应用名</th>
                  <th>版本（构建号）</th>
                  <th>大小</th>
                  <th>上传时间</th>
                  <th>更新说明</th>
                  <th>下载次数</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {state.data.items.map((build) => (
                  <tr key={build.id}>
                    <td>
                      <span className={`badge badge-${build.platform}`}>{platformLabel(build.platform)}</span>
                    </td>
                    <td className="cell-name">{build.appName}</td>
                    <td>
                      {build.version}
                      {build.buildNumber ? ` (${build.buildNumber})` : ''}
                    </td>
                    <td>{formatBytes(build.fileSize)}</td>
                    <td>{formatDate(build.createdAt)}</td>
                    <td className="muted">{build.changelog ? shortText(build.changelog) : '—'}</td>
                    <td>{build.downloadCount}</td>
                    <td>
                      <button className="button button-ghost button-small" type="button" onClick={() => navigate(build.downloadPagePath)}>
                        下载页
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ) : null}
    </section>
  );
}
