/**
 * 团队列表（基础资料 → 团队管理）。
 * 职责：只调团队列表接口，关联的人员/项目名字由该接口直接带回。
 * 输入：useTeams；输出：表格 + 「新建团队」入口。
 * 边界：不拉人员或项目列表（那属于对应页面自己的事）、不内嵌表单。
 */
import { Link, useNavigate } from 'react-router-dom';
import { Empty, ErrorState, Loading } from '../../components/States';
import { useTeams } from '../../hooks/useDirectory';

export function TeamsPage(): JSX.Element {
  const navigate = useNavigate();
  const { teams, loading, error, reload } = useTeams();

  return (
    <section className="page">
      <div className="page-head">
        <div>
          <h1>团队管理</h1>
          <p className="sub">共 {teams.length} 个团队 · 可关联多名人员与多个项目</p>
        </div>
        <Link className="button button-primary" to="/directory/teams/new">
          新建团队
        </Link>
      </div>

      <div className="card">
        {loading ? <Loading text="正在加载团队…" /> : null}
        {!loading && error ? <ErrorState message={error} onRetry={reload} /> : null}
        {!loading && !error && teams.length === 0 ? (
          <Empty
            title="还没有团队"
            description="新建团队后，可以把人员与项目关联进来。"
            action={
              <Link className="button button-primary" to="/directory/teams/new">
                新建第一个团队
              </Link>
            }
          />
        ) : null}
        {!loading && !error && teams.length > 0 ? (
          <div className="table-scroll">
            <table className="table">
              <thead>
                <tr>
                  <th>团队名称</th>
                  <th>描述</th>
                  <th>成员</th>
                  <th>关联项目</th>
                  <th aria-label="操作" />
                </tr>
              </thead>
              <tbody>
                {teams.map((team) => (
                  <tr key={team.id}>
                    <td className="cell-name">{team.name}</td>
                    <td className="cell-sub">{team.description || '—'}</td>
                    <td>
                      <div className="chip-row">
                        {team.persons.length === 0 ? (
                          <span className="cell-sub">—</span>
                        ) : (
                          team.persons.map((person) => (
                            <span className="chip" key={person.id}>
                              {person.name}
                            </span>
                          ))
                        )}
                      </div>
                    </td>
                    <td>
                      <div className="chip-row">
                        {team.projects.length === 0 ? (
                          <span className="cell-sub">—</span>
                        ) : (
                          team.projects.map((project) => (
                            <span className="chip" key={project.id}>
                              {project.name}
                            </span>
                          ))
                        )}
                      </div>
                    </td>
                    <td>
                      <div className="row-actions">
                        <button
                          className="button button-ghost button-small"
                          type="button"
                          onClick={() => navigate(`/directory/teams/${team.id}/edit`)}
                        >
                          编辑
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
      </div>
    </section>
  );
}
