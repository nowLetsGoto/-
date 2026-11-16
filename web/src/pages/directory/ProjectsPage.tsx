/**
 * 项目列表（基础资料 → 项目管理）。
 * 职责：只负责列出项目、跳转到详情或编辑；新增 / 编辑走独立路由。
 * 输入：useProjects；输出：表格 + 「新增项目」入口。
 * 边界：不内嵌表单、不发写请求；列表与表单是两个独立页面。
 */
import { Link, useNavigate } from 'react-router-dom';
import { Empty, ErrorState, Loading } from '../../components/States';
import { useProjects } from '../../hooks/useProjects';

export function ProjectsPage(): JSX.Element {
  const navigate = useNavigate();
  const { projects, loading, error, reload } = useProjects();

  return (
    <section className="page">
      <div className="page-head">
        <div>
          <h1>项目管理</h1>
          <p className="sub">共 {projects.length} 个项目 · 应用包与应用都挂在项目下</p>
        </div>
        <Link className="button button-primary" to="/directory/projects/new">
          新增项目
        </Link>
      </div>

      <div className="card">
        {loading ? <Loading text="正在加载项目…" /> : null}
        {!loading && error ? <ErrorState message={error} onRetry={reload} /> : null}
        {!loading && !error && projects.length === 0 ? (
          <Empty
            title="还没有项目"
            description="项目是应用包与应用的一级归属，先建一个再上传包。"
            action={
              <Link className="button button-primary" to="/directory/projects/new">
                新增第一个项目
              </Link>
            }
          />
        ) : null}
        {!loading && !error && projects.length > 0 ? (
          <div className="table-scroll">
            <table className="table">
              <thead>
                <tr>
                  <th>项目名称</th>
                  <th>标识</th>
                  <th>项目库</th>
                  <th>描述</th>
                  <th aria-label="操作" />
                </tr>
              </thead>
              <tbody>
                {projects.map((project) => (
                  <tr key={project.id}>
                    <td className="cell-name">
                      <Link to={`/directory/projects/${project.key}`}>{project.name}</Link>
                    </td>
                    <td className="cell-sub">{project.key}</td>
                    <td>
                      {project.repoUrl ? (
                        <a className="repo-link" href={project.repoUrl} target="_blank" rel="noreferrer">
                          仓库
                        </a>
                      ) : (
                        <span className="cell-sub">—</span>
                      )}
                    </td>
                    <td className="cell-sub">{project.description || '—'}</td>
                    <td>
                      <div className="row-actions">
                        <button
                          className="button button-ghost button-small"
                          type="button"
                          onClick={() => navigate(`/directory/projects/${project.key}`)}
                        >
                          详情
                        </button>
                        <button
                          className="button button-ghost button-small"
                          type="button"
                          onClick={() => navigate(`/directory/projects/${project.key}/edit`)}
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
