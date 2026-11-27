/**
 * 「重试下载到本机」的交互状态。
 *
 * 职责：记录哪一行正在重试，发起请求，成功后刷新列表。
 * 输入：重试结果回调（通常是显示提示 + 刷新列表）。
 * 输出：retryingID（哪一行在转圈）、retry（发起重试）。
 * 边界：不认识包的字段，只处理「一个包同时只重试一次」这件事。
 *
 * 为什么比 useSyncToR2 简单得多：那个要轮询服务端进度（重推是后台任务，
 * 进度只在 tracker 里）；这里进度直接来自列表的 localPulledBytes，
 * 列表刷新后自然会动，所以只需管住按钮本身。
 */
import { useCallback, useState } from 'react';
import { errorMessage } from '../api/client';
import { retryLocalPull } from '../api/builds';

export function useRetryLocalPull(onResult: (error: string) => void): {
  retryingID: string;
  retry: (id: string) => void;
} {
  // 用 id 而非布尔：同屏可能有多行同时处于失败态，
  // 布尔会让所有重试按钮一起转圈，用户分不清点的是哪一个。
  const [retryingID, setRetryingID] = useState('');

  const retry = useCallback(
    (id: string): void => {
      if (retryingID) return; // 一次只受理一个：后端也按包去重，这里避免连点。
      setRetryingID(id);
      retryLocalPull(id)
        .then(() => onResult(''))
        .catch((err: unknown) => onResult(errorMessage(err)))
        .finally(() => setRetryingID(''));
    },
    [retryingID, onResult],
  );

  return { retryingID, retry };
}
