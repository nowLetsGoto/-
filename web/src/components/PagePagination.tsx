/**
 * 职责：为卡片列表提供统一组件库分页与总数展示。
 * 输入：受控页码、每页条数、总数及切页回调；输出：分页导航。
 * 边界：不请求数据、不切片、不管理 URL；空列表由页面空态负责。
 */
import { Pagination } from '@seakoi/console-kit';
import { PAGE_SIZE_OPTIONS } from '../hooks/usePageState';

interface PagePaginationProps {
  page: number;
  pageSize: number;
  total: number;
  onChange: (page: number, pageSize: number) => void;
}

export function PagePagination({
  page,
  pageSize,
  total,
  onChange,
}: PagePaginationProps): JSX.Element | null {
  if (total === 0) return null;
  return (
    <nav className="page-pagination" aria-label="版本分页">
      <span>共 {total} 个版本</span>
      <Pagination
        current={page}
        pageSize={pageSize}
        total={total}
        onChange={onChange}
        showSizeChanger
        pageSizeOptions={PAGE_SIZE_OPTIONS}
        hideOnSinglePage={false}
      />
    </nav>
  );
}
