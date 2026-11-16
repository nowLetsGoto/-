/**
 * 应用包列表页（原 ListPage）。
 * 职责：按「全局环境 + 页面内平台筛选」查列表，渲染工具栏与表格；空结果给引导而不是一句「暂无」。
 * 输入：URL 的 env（全局）与 platform（本页）；输出：listBuilds 请求与三态。
 * 边界：不直接 fetch（走 api/builds）、不拼 API 路径、环境不在这里独立存一份；
 *       环境来自顶栏，页面只消费，避免同一维度两处真相。
 */
import { useEffect, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import type { Build, ListBuildsResult, Platform } from '../api/builds';
import { listBuilds } from '../api/builds';
import { errorMessage } from '../api/client';
import { Empty, ErrorState, Loading } from '../components/States';
import { useEnvironment } from '../hooks/useEnvironment';
import { PLATFORMS, environmentLabel, formatBytes, formatDate, platformLabel } from '../utils/format';

interface PageState {
  loading: boolean;
  data: ListBuildsResult | null;
  error: string;
}

const ALL_PLATFORMS = 'all';

/** 平台筛选写进 URL：和旧链接一致，复制出去仍带着筛选条件。 */
function readPlatform(value: string | null): Platform | typeof ALL_PLATFORMS {
  return value === 'android' || value === 'ios' ? value : ALL_PLATFORMS;
}

export function PackagesPage(): JSX.Element {
  const [environment] = useEnvironment();
  const [params, setParams] = useSearchParams();
  const platform = readPlatform(params.get('platform'));

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
  // 空结果且没有任何筛选时，工具栏没有意义，一并隐藏。
  const showToolbar = state.loading || items.length > 0 || platform !== ALL_PLATFORMS;

  return (
    <section className="page">
      <div className="page-head">
        <div>
          <h1>应用包</h1>
          <p className="sub">
            {environmentLabel(environment)}环境 · {state.data ? `共 ${state.data.total} 个包` : '加载中…'} · 按上传时间倒序
          </p>
        </div>
      </div>

      <div className="card">
        {showToolbar ? (
          <div className="toolbar">
            <div className="segmented" role="group" aria-label="平台">
              <button
                type="button"
                className={platform === ALL_PLATFORMS ? 'segment segment-active' : 'segment'}
                onClick={() => setPlatform(ALL_PLATFORMS)}
              >
                全部
              </button>
              {PLATFORMS.map((item) => (
                <button
                  key={item.value}
                  type="button"
                  className={platform === item.value ? 'segment segment-active' : 'segment'}
                  onClick={() => setPlatform(item.value)}
                >
                  {item.label}
                </button>
              ))}
            </div>
          </div>
        ) : null}

        {state.loading ? <Loading text="正在加载包列表…" /> : null}
        {!state.loading && state.error ? (
          <ErrorState message={state.error} onRetry={() => setReloadKey((value) => value + 1)} />
        ) : null}
        {!state.loading && !state.error && items.length === 0 ? (
          platform === ALL_PLATFORMS ? (
            <Empty
              title={`${environmentLabel(environment)}环境还没有包`}
              description="上传第一个 APK / IPA 后，这里会显示版本、构建号与下载次数，并自动生成可扫码的下载页。解析失败不会阻塞发布，表单仍可手动填写。"
              action={
                <Link className="button button-primary" to="/upload">
                  上传第一个包
                </Link>
              }
            />
          ) : (
            <Empty
              title={`${environmentLabel(environment)}环境暂无 ${platformLabel(platform)} 包`}
              description="换一个平台，或回到全部平台看看其它包。"
              action={
                <button className="button button-ghost" type="button" onClick={() => setPlatform(ALL_PLATFORMS)}>
                  查看全部平台
                </button>
              }
            />
          )
        ) : null}

        {!state.loading && !state.error && items.length > 0 ? (
          <div className="table-scroll">
            <table className="table">
              <thead>
                <tr>
                  <th>应用</th>
                  <th>版本（构建号）</th>
                  <th>平台</th>
                  <th>大小</th>
                  <th>上传时间</th>
                  <th>下载</th>
                  <th aria-label="操作" />
                </tr>
              </thead>
              <tbody>
                {items.map((build) => (
                  <BuildRow key={build.id} build={build} />
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
      </div>
    </section>
  );
}

/** 单行不取数、不带状态，只做展示与一次跳转。 */
function BuildRow({ build }: { build: Build }): JSX.Element {
  const navigate = useNavigate();
  return (
    <tr>
      <td>
        <div className="cell-app">
          <span className="app-mark" aria-hidden="true">
            {build.appName.slice(0, 1).toUpperCase()}
          </span>
          <div>
            <div className="cell-name">{build.appName}</div>
            <div className="cell-sub">{build.packageId || '—'}</div>
          </div>
        </div>
      </td>
      <td>
        <span className="ver">{build.version}</span>
        {build.buildNumber ? <span className="cell-sub"> ({build.buildNumber})</span> : null}
      </td>
      <td>
        <span className={`badge badge-${build.platform}`}>{platformLabel(build.platform)}</span>
      </td>
      <td>{formatBytes(build.fileSize)}</td>
      <td className="cell-sub">{formatDate(build.createdAt)}</td>
      <td>{build.downloadCount}</td>
      <td>
        {/* downloadPagePath 的唯一用途：react-router 内部跳转 */}
        <button className="button button-ghost button-small" type="button" onClick={() => navigate(build.downloadPagePath)}>
          下载页
        </button>
      </td>
    </tr>
  );
}
