/**
 * 三个通用状态组件。
 * 职责：加载 / 空 / 错误三态的统一渲染，错误态带重试回调。
 * 输入：文案与可选动作；输出：状态区块 JSX。
 * 边界：不发请求、不持有跨页状态、不含任何板块文案。
 */
import type { ReactNode } from 'react';

export function Loading({ text = '加载中…' }: { text?: string }): JSX.Element {
  return (
    <div className="state" role="status">
      <div className="spinner" aria-hidden="true" />
      <p>{text}</p>
    </div>
  );
}

export function Empty({ text, action }: { text: string; action?: ReactNode }): JSX.Element {
  return (
    <div className="state">
      <p className="state-title">{text}</p>
      {action ? <div className="state-action">{action}</div> : null}
    </div>
  );
}

export function ErrorState({ message, onRetry }: { message: string; onRetry?: () => void }): JSX.Element {
  return (
    <div className="state state-error" role="alert">
      <p className="state-title">{message}</p>
      {onRetry ? (
        <div className="state-action">
          <button className="button button-ghost" type="button" onClick={onRetry}>
            重试
          </button>
        </div>
      ) : null}
    </div>
  );
}
