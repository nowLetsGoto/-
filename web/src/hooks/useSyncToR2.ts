/**
 * 「同步到 R2」的交互状态。
 * 职责：管理「受理 → 轮询进度 → 成功提示 / 失败提示」，并按包保存进度快照。
 * 输入：同步成功后刷新列表的回调。
 * 输出：progress（每个包的进度）、sync（发起同步）；结果通过 onResult 回传。
 * 边界：不认识包的业务字段，只处理状态流转；请求语义留给 api/builds。
 *
 * 为什么单独成 hook：同步按行独立进行，需要记住「哪一行在转圈、到了百分之几」，
 * 这个状态与页面的筛选、分页、编辑抽屉都无关。混进主组件会让它继续膨胀。
 *
 * 为什么轮询：服务端立即返回 202、后台上传，避免撞上 Cloudflare 的 100 秒超时。
 * 前端因此不能把「请求成功」当「同步完成」，必须等进度进入 succeeded / failed。
 */
import { useCallback, useRef, useState } from 'react';
import { errorMessage } from '../api/client';
import { getSyncProgress, syncBuildToR2 } from '../api/builds';
import type { SyncProgress } from '../api/builds';

/** 轮询间隔：1 秒足够让进度条看起来连续，又不会给详情接口造成明显压力。 */
const POLL_INTERVAL_MS = 1000;
/** 最长等待 8 分钟：覆盖慢上行下的整包上传，超时交给用户稍后刷新。 */
const POLL_TIMEOUT_MS = 8 * 60 * 1000;

export function useSyncToR2(onResult: (error: string) => void): {
  progress: Record<string, SyncProgress>;
  sync: (id: string) => void;
} {
  const [progress, setProgress] = useState<Record<string, SyncProgress>>({});
  // 用 ref 记住正在同步的包：回调依赖它会导致同一次点击被重复受理。
  const runningRef = useRef('');

  const update = useCallback((id: string, value: SyncProgress): void => {
    setProgress((prev) => ({ ...prev, [id]: value }));
  }, []);

  const sync = useCallback(
    (id: string): void => {
      if (runningRef.current) return; // 同一时刻只跑一个：后端也按包去重。
      runningRef.current = id;
      update(id, { state: 'running', bytes: 0, total: 0, error: '' });

      syncBuildToR2(id)
        .then(() => pollProgress(id, update))
        .then((result) => {
          update(id, result);
          if (result.state === 'succeeded') {
            onResult('');
          } else {
            onResult(result.error || '同步失败，请稍后重试');
          }
        })
        .catch((err: unknown) => {
          const message = errorMessage(err);
          update(id, { state: 'failed', bytes: 0, total: 0, error: message });
          onResult(message);
        })
        .finally(() => {
          runningRef.current = '';
        });
    },
    [onResult, update],
  );

  return { progress, sync };
}

/** 轮询到终态为止；每次快照都回传，让进度条持续前进。 */
async function pollProgress(
  id: string,
  onTick: (id: string, value: SyncProgress) => void,
): Promise<SyncProgress> {
  const deadline = Date.now() + POLL_TIMEOUT_MS;
  while (Date.now() < deadline) {
    await delay(POLL_INTERVAL_MS);
    const current = await getSyncProgress(id);
    onTick(id, current);
    if (current.state !== 'running') return current;
  }
  return { state: 'failed', bytes: 0, total: 0, error: '同步仍在进行中，请稍后刷新页面查看状态' };
}

/** delay 把 setTimeout 包成 Promise，让轮询写成顺序的 async 流程。 */
function delay(ms: number): Promise<void> {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}
