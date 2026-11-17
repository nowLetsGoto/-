/**
 * 团队列表（基础资料 → 团队管理）。
 * 职责：只调团队列表接口，关联的人员/项目名字由该接口直接带回。
 * 输入：URL 的 page / pageSize；输出：服务端分页的 DataTable + 「新建团队」入口。
 * 边界：不拉人员或项目列表（那属于对应页面自己的事）、不内嵌表单；
 *       「新建 / 编辑」按 directory.write 隐藏，真正能不能写由服务端判定。
 */
import { Link, useNavigate } from 'react-router-dom';
import type { TableColumnsType } from '@seakoi/console-kit';
import type { Team } from '../../api/teams';
import { Button } from '../../components/Controls';
import { DataTable } from '../../components/DataTable';
import { Empty, ErrorState } from '../../components/States';
import { useMe } from '../../hooks/useMe';
import { usePageState } from '../../hooks/usePageState';
import { useTeamsPage } from '../../hooks/useDirectory';
import { can } from '../../utils/permission';

export function TeamsPage(): JSX.Element {
  const navigate = useNavigate();
  const { page, pageSize, change: changePage } = usePageState();
  const { items: teams, total, loading, error, reload } = useTeamsPage(page, pageSize);
  const { me } = useMe();
  const writable = can(me, 'directory.write');

  const columns: TableColumnsType<Team> = [
    {
      title: '团队名称',
      key: 'name',
      render: (_, team) => <span className="cell-name">{team.name}</span>,
    },
    {
      title: '描述',
      key: 'description',
      render: (_, team) => <span className="cell-sub">{team.description || '—'}</span>,
    },
    {
      title: '成员',
      key: 'persons',
      render: (_, team) =>
        team.persons.length === 0 ? (
          <span className="cell-sub">—</span>
        ) : (
          <div className="chip-row">
            {team.persons.map((person) => (
              <span className="chip" key={person.id}>
                {person.name}
              </span>
            ))}
          </div>
        ),
    },
    {
      title: '关联项目',
      key: 'projects',
      render: (_, team) =>
        team.projects.length === 0 ? (
          <span className="cell-sub">—</span>
        ) : (
          <div className="chip-row">
            {team.projects.map((project) => (
              <span className="chip" key={project.id}>
                {project.name}
              </span>
            ))}
          </div>
        ),
    },
    {
      title: '',
      key: 'actions',
      width: 90,
      render: (_, team) =>
        writable ? (
          <div className="row-actions">
            <Button
              className="button button-ghost button-small"
              type="button"
              onClick={() => navigate(`/directory/teams/${team.id}/edit`)}
            >
              编辑
            </Button>
          </div>
        ) : null,
    },
  ];

  return (
    <section className="page">
      <div className="page-head">
        <div>
          <h1>团队管理</h1>
          <p className="sub">共 {total} 个团队 · 可关联多名人员与多个项目</p>
        </div>
        {writable ? (
          <Link className="button button-primary" to="/directory/teams/new">
            新建团队
          </Link>
        ) : null}
      </div>

      <div className="card">
        {error ? (
          <ErrorState message={error} onRetry={reload} />
        ) : (
          <DataTable<Team>
            columns={columns}
            dataSource={teams}
            rowKey={(team) => team.id}
            loading={loading}
            serverPagination={{ page, pageSize, total, onChange: changePage }}
            empty={
              <Empty
                title="还没有团队"
                description={
                  writable
                    ? '新建团队后，可以把人员与项目关联进来。'
                    : '暂无团队，请联系管理员创建。'
                }
                action={
                  writable ? (
                    <Link className="button button-primary" to="/directory/teams/new">
                      新建第一个团队
                    </Link>
                  ) : undefined
                }
              />
            }
          />
        )}
      </div>
    </section>
  );
}
