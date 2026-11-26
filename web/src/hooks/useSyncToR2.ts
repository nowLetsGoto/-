/**
 * 「同步到 R2」的交互状态。
 * 职责：管理「点了同步 → 上传中 → 成功提示 / 失败提示」这一套流程，并记录进行中的包。
 * 输入：同步成功后刷新列表的回调。
 * 输出：syncingID（哪一行正在同步）、sync（发起同步）；结果通过 onResult 回传。
 * 边界：不认识包的业务字段，只处理状态流转；请求语义留给 api/builds。
 *
 * 为什么单独成 hook：同步按行独立进行，需要记住「哪一行在转圈」，
 * 这个状态与页面的筛选、分页、编辑抽屉都无关。混进主组件会让它继续膨胀，
 * 而列表页已经有筛选 / 分页 / 删除确认 / 编辑抽屉四套状态了。
 */
import { useCallback, useState } from 'react';
import { errorMessage } from '../api/client';
import { syncBuildToR2 } from '../api/builds';

export function useSyncToR2(onResult: (error: string) => void): {
  syncingID: string;
  sync: (id: string) => void;
} {
  // 用 id 而非布尔：同屏可能有多个可同步的包，布尔会让所有按钮一起转圈。
  const [syncingID, setSyncingID] = useState('');

  const sync = useCallback(
    (id: string): void => {
      if (syncingID) return; // 已有同步在进行：避免同一行或跨行重复提交。
      setSyncingID(id);
      syncBuildToR2(id)
        // 成功传空串、失败传原因：把两种结果交给调用方用同一处提示展示，
        // 页面因此不必为同步单独维护一套错误 UI。
        .then(() => onResult(''))
        .catch((err: unknown) => onResult(errorMessage(err)))
        .finally(() => setSyncingID(''));
    },
    [syncingID, onResult],
  );

  return { syncingID, sync };
}
