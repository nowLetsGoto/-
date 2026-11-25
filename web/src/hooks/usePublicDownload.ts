/**
 * 公开下载页的数据加载与版本投影。
 * 职责：按包 id 读取详情和同应用版本，合并当前包并按发布时间排序，独立呈现版本列表错误。
 * 输入：路由包 id 与可选分享凭证；输出：包详情、完整版本列表、加载/错误/缺失状态与重试函数。
 * 边界：只通过 builds API 取数；分享模式仅加载后端授权的同应用版本；按当前平台收敛，跨平台包单独供切换；不生成地址、不选环境。
 */
import { useEffect, useMemo, useState } from 'react';
import type { DownloadBuild, PublicBuildVersion } from '../api/builds';
import {
  getBuild,
  getSharedBuild,
  listBuildVersions,
  listSharedBuildVersions,
} from '../api/builds';
import { readSession } from '../api/session';
import { ApiError, errorMessage } from '../api/client';

interface DownloadState {
  key: string;
  revision: number;
  build: DownloadBuild | null;
  versions: PublicBuildVersion[];
  versionsError: string;
  error: string;
  missing: boolean;
}

const INITIAL: DownloadState = {
  key: '',
  revision: -1,
  build: null,
  versions: [],
  versionsError: '',
  error: '',
  missing: false,
};

// RFC3339Nano 会省略末尾的零；补齐精度后才可按字符串比较同一秒内的版本。
function publishedAt(version: PublicBuildVersion): string {
  return version.createdAt.replace(
    /(?:\.(\d+))?Z$/,
    (_match, fraction: string = '') => `.${fraction.padEnd(9, '0')}Z`,
  );
}

export function usePublicDownload(id: string, shareToken: string | null) {
  const shared = shareToken !== null;
  const key = `${id}\0${shared ? `share:${shareToken}` : `session:${readSession()?.token ?? ''}`}`;
  const [state, setState] = useState<DownloadState>(INITIAL);
  const [revision, setRevision] = useState(0);

  useEffect(() => {
    if (!id) return;
    let cancelled = false;
    let commitTimer: ReturnType<typeof setTimeout>;
    const startedAt = performance.now();
    // 首次打开和切换统一保留 240ms 骨架反馈，避免快速接口让占位只闪一帧。
    const minimumLoading = 240;
    const commit = (next: DownloadState): void => {
      commitTimer = setTimeout(
        () => {
          if (cancelled) return;
          setState(next);
        },
        Math.max(0, minimumLoading - (performance.now() - startedAt)),
      );
    };
    const versionsRequest = (
      shared ? listSharedBuildVersions(id, shareToken) : listBuildVersions(id)
    ).then(
      (versions) => ({ versions, versionsError: '' }),
      (err: unknown) => ({ versions: [], versionsError: errorMessage(err) }),
    );
    Promise.all([shared ? getSharedBuild(id, shareToken) : getBuild(id), versionsRequest])
      .then(([build, result]) => {
        if (cancelled) return;
        const current: PublicBuildVersion = {
          id: build.id,
          version: build.version,
          buildNumber: build.buildNumber,
          fileSize: build.fileSize,
          changelog: build.changelog,
          environment: build.environment,
          platform: build.platform,
          createdAt: build.createdAt,
          downloadPagePath: build.downloadPagePath,
          downloadPageUrl: build.downloadPageUrl,
          projectKey: '',
        };
        const unique = new Map([current, ...result.versions].map((item) => [item.id, item]));
        const versions = [...unique.values()].sort(
          (a, b) => publishedAt(b).localeCompare(publishedAt(a)) || b.id.localeCompare(a.id),
        );
        commit({
          key,
          revision,
          build,
          versions,
          versionsError: result.versionsError,
          error: '',
          missing: false,
        });
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        commit({
          ...INITIAL,
          key,
          revision,
          missing: err instanceof ApiError && err.code === 404,
          error: errorMessage(err),
        });
      });
    return () => {
      cancelled = true;
      clearTimeout(commitTimer);
    };
  }, [id, key, revision, shared, shareToken]);

  const current = state.key === key && state.revision === revision;
  const activePlatform =
    state.versions.find((item) => item.id === id)?.platform ?? state.build?.platform;
  const versions = useMemo(
    () => state.versions.filter((item) => item.platform === activePlatform),
    [state.versions, activePlatform],
  );
  return {
    build: current ? state.build : null,
    versions: current ? versions : [],
    platformVersions: current ? state.versions : [],
    activePlatform,
    loading: !!id && !current,
    missing: !id || (current && state.missing),
    error: current ? state.error : '',
    versionsError: current ? state.versionsError : '',
    reload: () => setRevision((value) => value + 1),
  };
}
