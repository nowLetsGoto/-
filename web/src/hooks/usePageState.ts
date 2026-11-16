/**
 * 列表页的分页状态，落在 URL 上（?page=2&pageSize=20）。
 * 职责：读/写分页参数并做取值范围夹紧，供所有列表页共用。
 * 输入：react-router 的 search params；输出：{ page, pageSize, change }。
 * 边界：只管分页，不认识业务实体；placeholder 仍由各页面自己的查询串决定。
 */
import { useSearchParams } from 'react-router-dom';

/** 与 DataTable 的 pageSizeOptions 保持一致，换页大小只改这里。 */
export const PAGE_SIZE_OPTIONS = [10, 20, 50];

const DEFAULT_PAGE_SIZE = 10;

export function usePageState(): {
  page: number;
  pageSize: number;
  change: (page: number, pageSize: number) => void;
} {
  const [params, setParams] = useSearchParams();

  const rawPage = Number(params.get('page'));
  const rawSize = Number(params.get('pageSize'));
  const page = Number.isFinite(rawPage) && rawPage >= 1 ? Math.floor(rawPage) : 1;
  const pageSize = PAGE_SIZE_OPTIONS.includes(rawSize) ? rawSize : DEFAULT_PAGE_SIZE;

  const change = (nextPage: number, nextSize: number): void => {
    const updated = new URLSearchParams(params);
    updated.set('page', String(nextPage));
    updated.set('pageSize', String(nextSize));
    // 分页不属于"一次新的导航"，用 replace 避免后退键在页码之间反复跳
    setParams(updated, { replace: true });
  };

  return { page, pageSize, change };
}
