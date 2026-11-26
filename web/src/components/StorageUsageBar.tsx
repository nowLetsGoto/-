/**
 * 存储容量条：显示当前租户的 R2 占用与配额余量。
 *
 * 职责：把 StorageUsage 渲染成一条进度条 + 一行摘要。
 * 输入：fetchStorageUsage 的结果；输出：进度条与提示文字。
 * 边界：不计算任何配额——余量、可上传数量全部由服务端算好，
 *       前端重复算一遍必然与服务端判定漂移。
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
    </div>
  );
}
