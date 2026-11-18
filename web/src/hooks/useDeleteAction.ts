/**
 * 删除确认的统一交互状态。
 * 职责：管理「点了删除 → 弹确认 → 提交 → 失败时把服务端原因显示出来」这一套流程。
 * 输入：执行删除的异步函数、删除成功后的回调（通常是刷新列表）。
 * 输出：target（待删对象）、request（发起删除）、cancel（关闭确认）、dialogProps（交给 ConfirmDialog）。
 * 边界：不认识任何业务实体，只处理状态流转；标题/影响面文案由调用方给出。
 *       不发请求本身——删除动作的语义留给各 API 模块。
 *
 * 为什么抽出来：人员 / 团队 / 项目 / 角色四个页面的删除交互完全同构
 * （按权限显示按钮 → 确认 → 请求 → 400 显示原因 → 成功后刷新）。
 * 各写一遍会出现四份不一致的实现，改一处忘三处是必然的。
 */
import { useCallback, useState } from 'react';
import { errorMessage } from '../api/client';

interface DeleteTarget {
  /** 用于 React key 与调试，不参与请求。 */
  id: string;
  /** 展示在确认框标题里，让用户确认删的是哪一个。 */
  label: string;
}

interface DeleteState<T extends DeleteTarget> {
  /** 当前待删对象；null 表示确认框关闭。 */
  target: T | null;
  /** 请求进行中，用于禁用确认按钮避免二次提交。 */
  loading: boolean;
  /** 失败原因；成功提交后清空，在确认框内展示。 */
  error: string;
  /** 打开确认框。 */
  request: (target: T) => void;
  /** 关闭确认框并清掉错误（取消时不该留着上次的红字）。 */
  cancel: () => void;
  /** 执行删除；成功后关闭并回调。 */
  confirm: () => void;
}

/**
 * useDeleteAction 提供删除确认的完整状态机。
 *
 * remove 由调用方给出（绑定到具体 API），这样同一个 hook 能服务四种实体，
 * 而"删什么"的语义仍留在各自的模块里——hook 只负责"怎么删"的一致体验。
 */
export function useDeleteAction<T extends DeleteTarget>(
  remove: (target: T) => Promise<void>,
  onDeleted: () => void,
): DeleteState<T> {
  const [target, setTarget] = useState<T | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const request = useCallback((next: T) => {
    setTarget(next);
    setError('');
  }, []);

  const cancel = useCallback(() => {
    // 请求进行中不允许关闭：否则用户点了取消但请求还在飞，成功后列表刷新会更加困惑。
    setLoading((busy) => {
      if (!busy) {
        setTarget(null);
        setError('');
      }
      return busy;
    });
  }, []);

  const confirm = useCallback(() => {
    if (!target) {
      return;
    }
    setLoading(true);
    setError('');
    remove(target)
      .then(() => {
        setTarget(null);
        onDeleted();
      })
      .catch((err: unknown) => {
        // 服务端的 400 文案已说明"还剩哪些关联"，原样展示最有用——
        // 在这里改写成"删除失败"会丢掉用户唯一能据以行动的信息。
        setError(errorMessage(err));
      })
      .finally(() => {
        setLoading(false);
      });
  }, [target, remove, onDeleted]);

  return { target, loading, error, request, cancel, confirm };
}
