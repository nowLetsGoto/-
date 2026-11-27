/**
 * 存储容量条：显示 R2 占用与配额余量，以及本机留底情况。
 *
 * 职责：把 StorageUsage 渲染成两段进度条 + 摘要文字。
 * 输入：fetchStorageUsage 的结果；输出：进度条与提示文字。
 * 边界：不计算任何配额——余量、可上传数量全部由服务端算好，
 *       前端重复算一遍必然与服务端判定漂移。
 *
 * 为什么分成两段而不是合成一条：
 * 两者上限性质不同。R2 是 9GB 配额，超额会触发淘汰，需要红色警示；
 * 本机上限取决于磁盘还剩多少，超额只会让留底失败，不会删数据。
 * 混在一条里会让「9GB 用了 100%」与「磁盘用了 5%」看起来像同一种危机。
 */
import { useEffect, useState } from 'react';
import { fetchStorageUsage, type StorageUsage } from '../api/builds';
import { formatBytes } from '../utils/format';

/** 用量超过这个比例就转成警示色，让管理员在触顶前有反应时间。 */
const WARN_RATIO = 0.8;

export function StorageUsageBar(): JSX.Element | null {
  const [usage, setUsage] = useState<StorageUsage | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let alive = true;
    fetchStorageUsage()
      .then((value) => {
        if (alive) {
          setUsage(value);
        }
      })
      .catch(() => {
        // 容量条是辅助信息：取不到就不显示，不能让它拖垮整个应用页。
        if (alive) {
          setFailed(true);
        }
      });
    return () => {
      alive = false;
    };
  }, []);

  if (failed || !usage || usage.quotaBytes <= 0) {
    return null;
  }

  const ratio = Math.min(usage.usedBytes / usage.quotaBytes, 1);
  const percent = Math.round(ratio * 1000) / 10;
  const level = ratio >= 1 ? 'danger' : ratio >= WARN_RATIO ? 'warn' : 'normal';

  return (
    <div className={`storage-usage storage-usage-${level}`} role="status">
      <div className="storage-usage-head">
        <span className="storage-usage-title">对象存储</span>
        <span className="storage-usage-figure">
          {formatBytes(usage.usedBytes)} / {formatBytes(usage.quotaBytes)}
          <span className="storage-usage-percent">{percent}%</span>
        </span>
      </div>
      <div
        className="storage-usage-track"
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={percent}
        aria-label="对象存储用量"
      >
        <div className="storage-usage-fill" style={{ width: `${percent}%` }} />
      </div>
      <p className="storage-usage-hint">
        {level === 'danger'
          ? '已超出配额，最早的版本将被移出对象存储'
          : `还可上传约 ${usage.estimatedUploads} 个包 · 版本 ${usage.r2BuildCount}/${usage.maxVersions}`}
        {usage.pendingCount > 0 ? ` · ${usage.pendingCount} 个待同步` : ''}
        {usage.failedCount > 0 ? ` · ${usage.failedCount} 个同步失败` : ''}
      </p>
      <LocalUsage usage={usage} />
    </div>
  );
}

/**
 * 本机留底那一段。
 *
 * 单独拆出来，是为了让主组件读起来只有「R2 一段 + 本机一段」，
 * 而不被「磁盘查不到怎么办」「没有包怎么办」这些分支淹没。
 */
function LocalUsage({ usage }: { usage: StorageUsage }): JSX.Element | null {
  // 磁盘可用查不到（服务端返回 0）时整段不显示：
  // 显示 0 会让人以为磁盘满了，比不显示更糟。
  if (usage.diskFreeBytes <= 0) {
    return null;
  }

  const total = usage.localBytes + usage.diskFreeBytes;
  const ratio = total > 0 ? usage.localBytes / total : 0;
  const percent = Math.round(ratio * 1000) / 10;

  return (
    <div className="storage-local">
      <div className="storage-usage-head">
        <span className="storage-usage-title">本机留底</span>
        <span className="storage-usage-figure">
          {formatBytes(usage.localBytes)}
          <span className="storage-usage-percent">{percent}%</span>
        </span>
      </div>
      <div
        className="storage-usage-track"
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={percent}
        aria-label="本机磁盘占用"
      >
        <div className="storage-usage-fill storage-local-fill" style={{ width: `${percent}%` }} />
      </div>
      <p className="storage-usage-hint">
        {usage.localBuilds} 个包已归档 · 磁盘剩余 {formatBytes(usage.diskFreeBytes)}
        {/* 缺副本的包单独提示：它们淘汰后无法下载，需等巡检拉回。 */}
        {usage.localMissing > 0 ? ` · ${usage.localMissing} 个待拉回` : ''}
      </p>
    </div>
  );
}
