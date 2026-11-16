/**
 * 全站唯一的表格封装（基于 Ant Design Table）。
 * 职责：统一列配置、加载态、空态、分页与视觉，业务页面只描述「有哪些列」。
 * 输入：columns / dataSource / rowKey / loading / empty / pageSize。
 * 输出：带分页的表格。
 * 边界：**业务页面不得直接 import antd 的 Table**——antd 只出现在本文件与 theme/antd.ts，
 *       将来换组件库或调整表格外观，只改这两处。
 */
import { Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { ReactNode } from 'react';
import { Empty } from './States';

interface DataTableProps<T> {
  columns: ColumnsType<T>;
  dataSource: T[];
  /** 用记录 id 做 key，避免 React 列表复用错行 */
  rowKey: (row: T) => string;
  loading?: boolean;
  /** 空态内容，默认给一句通用的「暂无数据」 */
  empty?: ReactNode;
  pageSize?: number;
}

export function DataTable<T extends object>({
  columns,
  dataSource,
  rowKey,
  loading = false,
  empty,
  pageSize = 10,
}: DataTableProps<T>): JSX.Element {
  // 分页条常驻：只要列表非空就显示（哪怕只有 1 条），
  // 避免"数据少就没有分页"让人误以为不支持分页。空列表交给空态卡片，不挂分页。
  const showPagination = dataSource.length > 0;

  return (
    <Table<T>
      className="data-table"
      columns={columns}
      dataSource={dataSource}
      rowKey={rowKey}
      loading={loading}
      scroll={{ x: 'max-content' }}
      locale={{ emptyText: empty ?? <Empty title="暂无数据" /> }}
      pagination={
        showPagination
          ? {
              pageSize,
              showSizeChanger: true,
              pageSizeOptions: [10, 20, 50],
              showTotal: (total) => `共 ${total} 条`,
              // 只有一页时也保留分页条，保持列表页版式稳定
              hideOnSinglePage: false,
            }
          : false
      }
    />
  );
}
