/**
 * 三个通用状态组件：加载 / 空 / 错误。
 * 职责：三态渲染；空状态给出「为什么空 + 下一步」，而不是一句「暂无数据」。
 * 输入：文案、说明、可选动作与重试回调；输出：状态区块 JSX。
 * 边界：不发请求、不持有跨页状态、不含任何板块专属文案。
 */
import type { ReactNode } from 'react';
import { Button } from './Controls';

export function Loading({ text = '加载中…' }: { text?: string }): JSX.Element {
  return (
    <div className="state" role="status">
      <div className="spinner" aria-hidden="true" />
      <p>{text}</p>
    </div>
  );
}

/** 空状态插画：蒲公英线稿，与登录页同一意象，纯装饰。 */
function EmptyArt(): JSX.Element {
  return (
    <svg
      className="empty-art"
      width="72"
      height="72"
      viewBox="0 0 72 72"
      fill="none"
      aria-hidden="true"
    >
      <path d="M36 44v18" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
      <path
        d="M36 44c-7-3-11-9-11-16M36 44c7-3 11-9 11-16M36 44c0-8 3-14 8-19M36 44c0-8-3-14-8-19"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
      />
      <circle cx="36" cy="20" r="2.4" fill="currentColor" />
      <circle cx="25" cy="27" r="1.8" fill="currentColor" />
      <circle cx="47" cy="27" r="1.8" fill="currentColor" />
      <circle cx="44" cy="24" r="1.4" fill="currentColor" />
    </svg>
  );
}

export function Empty({
  title,
  description,
  action,
}: {
  title: string;
  description?: string;
  action?: ReactNode;
}): JSX.Element {
  return (
    <div className="empty">
      <EmptyArt />
      <p className="empty-title">{title}</p>
      {description ? <p className="empty-desc">{description}</p> : null}
      {action ? <div className="empty-actions">{action}</div> : null}
    </div>
  );
}

export function ErrorState({
  message,
  onRetry,
}: {
  message: string;
  onRetry?: () => void;
}): JSX.Element {
  return (
    <div className="state state-error" role="alert">
      <p className="state-title">{message}</p>
      {onRetry ? (
        <div className="state-action">
          <Button className="button button-ghost" type="button" onClick={onRetry}>
            重试
          </Button>
        </div>
      ) : null}
    </div>
  );
}
