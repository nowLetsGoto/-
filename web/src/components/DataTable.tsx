/**
 * 全站唯一的表格封装（基于 Ant Design Table）。
 * 职责：统一列配置、加载态、空态、分页与视觉，业务页面只描述「有哪些列」。
 * 输入：columns / dataSource / rowKey / loading / empty / pageSize / serverPagination。
 * 输出：带分页的表格。
 * 边界：**业务页面不得直接 import antd 的 Table**——antd 只出现在本文件与 theme/antd.ts，
 *       将来换组件库或调整表格外观，只改这两处。
 */
import { Table } from '@seakoi/console-kit';
import type { TableColumnsType } from '@seakoi/console-kit';
import type { ReactNode } from 'react';
import { Empty } from './States';

const PAGE_SIZE_OPTIONS = [10, 20, 50];

interface ServerPagination {
  page: number;
  pageSize: number;
  /** 由接口返回的总条数 */
  total: number;
  onChange: (page: number, pageSize: number) => void;
}

interface DataTableProps<T> {
  columns: TableColumnsType<T>;
  dataSource: T[];
  /** 用记录 id 做 key，避免 React 列表复用错行 */
  rowKey: (row: T) => string;
  loading?: boolean;
  /** 空态内容，默认给一句通用的「暂无数据」 */
  empty?: ReactNode;
  /** 前端分页的每页条数；传了 serverPagination 时以那边为准 */
  pageSize?: number;
  /**
   * 服务端分页：传了就是受控分页（表格里已经是某一页的数据，总数由接口给）；
   * 不传则退化为前端分页（一次拿全量、由表格自己切页）。
   */
  serverPagination?: ServerPagination;
}

export function DataTable<T extends object>({
  columns,
  dataSource,
  rowKey,
  loading = false,
  empty,
  pageSize = 10,
  serverPagination,
}: DataTableProps<T>): JSX.Element {
  return (
    <Table<T>
      className="data-table"
      columns={columns}
      dataSource={dataSource}
      rowKey={rowKey}
      loading={loading}
      scroll={{ x: 'max-content' }}
      locale={{ emptyText: empty ?? <Empty title="暂无数据" /> }}
      pagination={buildPagination(dataSource.length, pageSize, serverPagination)}
    />
  );
}

/**
 * 分页配置：
 * - 服务端分页 → 受控（current / pageSize / total 全部来自外部）
 * - 前端分页 → 只要列表非空就常驻分页条，避免"数据少就没有分页"让人误以为不支持分页
 * - 空列表 → 不挂分页，交给空态卡片
 */
function buildPagination(
  rowCount: number,
  pageSize: number,
  server?: ServerPagination,
): false | Record<string, unknown> {
  const common = {
    showSizeChanger: true,
    pageSizeOptions: PAGE_SIZE_OPTIONS,
    showTotal: (total: number) => `共 ${total} 条`,
  };

  if (server) {
    return {
      ...common,
      current: server.page,
      pageSize: server.pageSize,
      total: server.total,
      onChange: (nextPage: number, nextSize: number) => server.onChange(nextPage, nextSize),
    };
  }
  if (rowCount === 0) {
    return false;
  }
  return { ...common, pageSize, hideOnSinglePage: false };
}
