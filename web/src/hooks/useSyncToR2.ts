/**
 * 「同步到 R2」的交互状态。
 * 职责：管理「受理 → 轮询进度 → 成功提示 / 失败提示」，并按包保存进度快照。
 * 输入：同步成功后刷新列表的回调；页面加载时给出的待检查包 id。
 * 输出：progress（每个包的进度）、sync（发起同步）、restore（从服务端恢复进度）。
 * 边界：不认识包的业务字段，只处理状态流转；请求语义留给 api/builds。
 *
 * 为什么单独成 hook：同步按行独立进行，需要记住「哪一行在转圈、到了百分之几」，
 * 这个状态与页面的筛选、分页、编辑抽屉都无关。混进主组件会让它继续膨胀。
 *
 * 为什么需要 restore：切菜单会卸载列表页，组件内存状态随之消失。
 * 服务端 tracker 保留进度 10 分钟，因此页面回来时按包 id 问一下服务端，
 * 就能把「正在同步」的按钮禁用与进度条恢复出来，而不是退回可点击态。
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
  restore: (ids: string[]) => void;
} {
  const [progress, setProgress] = useState<Record<string, SyncProgress>>({});
  const progressRef = useRef<Record<string, SyncProgress>>({});
  // 用 ref 记住正在轮询的包：回调依赖 state 会让同一次点击被重复受理。
  const runningRef = useRef(new Set<string>());

  const update = useCallback((id: string, value: SyncProgress): void => {
    progressRef.current = { ...progressRef.current, [id]: value };
    setProgress(progressRef.current);
  }, []);

  // pollAndReport 假定 id 已在 runningRef 中，结束（含失败）时负责释放。
  const pollAndReport = useCallback(
    (id: string): void => {
      pollProgress(id, update)
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
          runningRef.current.delete(id);
        });
    },
    [onResult, update],
  );

  // watch 用于「服务端已在同步、前端只是恢复现场」的场景，不再发 POST。
  const watch = useCallback(
    (id: string): void => {
      if (runningRef.current.has(id)) return;
      runningRef.current.add(id);
      pollAndReport(id);
    },
    [pollAndReport],
  );

  const sync = useCallback(
    (id: string): void => {
      if (runningRef.current.size > 0) return; // 同一时刻只跑一个：后端也按包去重。
      runningRef.current.add(id);
      update(id, { state: 'running', bytes: 0, total: 0, error: '' });

      syncBuildToR2(id)
        .then(() => pollAndReport(id))
        .catch((err: unknown) => {
          const message = errorMessage(err);
          update(id, { state: 'failed', bytes: 0, total: 0, error: message });
          onResult(message);
          runningRef.current.delete(id);
        });
    },
    [onResult, pollAndReport, update],
  );

  // restore 从服务端 tracker 恢复进度：已记录的跳过，idle 的不写入，running 的继续轮询。
  const restore = useCallback(
    (ids: string[]): void => {
      ids.forEach((id) => {
        if (progressRef.current[id] || runningRef.current.has(id)) return;
        getSyncProgress(id)
          .then((current) => {
            if (current.state === 'idle') return;
            update(id, current);
            if (current.state === 'running') watch(id);
          })
          .catch(() => {
            // 恢复是旁路：查询失败不该让列表报错，用户点同步时会重新走完整流程。
          });
      });
    },
    [update, watch],
  );

  return { progress, sync, restore };
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
