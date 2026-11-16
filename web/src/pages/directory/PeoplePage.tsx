/**
 * 人员列表（基础资料 → 人员管理）。
 * 职责：只调人员列表接口，关联的团队/项目名字由该接口直接带回。
 * 输入：URL 的 page / pageSize；输出：服务端分页的 DataTable + 「新增人员」入口。
 * 边界：不拉团队或项目列表（那属于对应页面自己的事）、不内嵌表单。
 */
import { Link, useNavigate } from 'react-router-dom';
import type { ColumnsType } from 'antd/es/table';
import type { Person } from '../../api/people';
import { DataTable } from '../../components/DataTable';
import { Empty, ErrorState } from '../../components/States';
import { usePageState } from '../../hooks/usePageState';
import { usePeoplePage } from '../../hooks/useDirectory';

export function PeoplePage(): JSX.Element {
  const navigate = useNavigate();
  const { page, pageSize, change: changePage } = usePageState();
  const { items: people, total, loading, error, reload } = usePeoplePage(page, pageSize);

  const columns: ColumnsType<Person> = [
    {
      title: '姓名',
      key: 'name',
      render: (_, person) => <span className="cell-name">{person.name}</span>,
    },
    {
      title: '登录名',
      key: 'username',
      render: (_, person) => <span className="cell-sub">{person.username || '—'}</span>,
    },
    {
      title: '岗位',
      key: 'title',
      render: (_, person) => person.title || '—',
    },
    {
      title: '联系方式',
      key: 'contact',
      render: (_, person) => (
        <span className="cell-sub">{[person.email, person.phone].filter(Boolean).join(' · ') || '—'}</span>
      ),
    },
    {
      title: '关联团队',
      key: 'teams',
      render: (_, person) =>
        person.teams.length === 0 ? (
          <span className="cell-sub">—</span>
        ) : (
          <div className="chip-row">
            {person.teams.map((team) => (
              <span className="chip" key={team.id}>
                {team.name}
              </span>
            ))}
          </div>
        ),
    },
    {
      title: '关联项目',
      key: 'projects',
      render: (_, person) =>
        person.projects.length === 0 ? (
          <span className="cell-sub">—</span>
        ) : (
          <div className="chip-row">
            {person.projects.map((project) => (
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
      render: (_, person) => (
        <div className="row-actions">
          <button
            className="button button-ghost button-small"
            type="button"
            onClick={() => navigate(`/directory/people/${person.id}/edit`)}
          >
            编辑
          </button>
        </div>
      ),
    },
  ];

  return (
    <section className="page">
      <div className="page-head">
        <div>
          <h1>人员管理</h1>
          <p className="sub">共 {total} 人 · 可关联多个团队与项目</p>
        </div>
        <Link className="button button-primary" to="/directory/people/new">
          新增人员
        </Link>
      </div>

      <div className="card">
        {error ? (
          <ErrorState message={error} onRetry={reload} />
        ) : (
          <DataTable<Person>
            columns={columns}
            dataSource={people}
            rowKey={(person) => person.id}
            loading={loading}
            serverPagination={{ page, pageSize, total, onChange: changePage }}
            empty={
              <Empty
                title="还没有人员"
                description="新增人员后，可以在团队与项目中关联他。"
                action={
                  <Link className="button button-primary" to="/directory/people/new">
                    新增第一个人员
                  </Link>
                }
              />
            }
          />
        )}
      </div>
    </section>
  );
}
