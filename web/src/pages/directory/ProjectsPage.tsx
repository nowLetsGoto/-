/**
 * 项目列表（基础资料 → 项目管理）。
 * 职责：只调项目列表接口；新增 / 编辑走独立路由。
 * 输入：useProjects；输出：DataTable + 「新增项目」入口。
 * 边界：不内嵌表单、不发写请求。
 */
import { Link, useNavigate } from 'react-router-dom';
import type { ColumnsType } from 'antd/es/table';
import type { Project } from '../../api/projects';
import { DataTable } from '../../components/DataTable';
import { Empty, ErrorState } from '../../components/States';
import { useProjects } from '../../hooks/useProjects';

export function ProjectsPage(): JSX.Element {
  const navigate = useNavigate();
  const { projects, loading, error, reload } = useProjects();

  const columns: ColumnsType<Project> = [
    {
      title: '项目名称',
      key: 'name',
      render: (_, project) => (
        <Link className="cell-name" to={`/directory/projects/${project.key}`}>
          {project.name}
        </Link>
      ),
    },
    {
      title: '标识',
      key: 'key',
      render: (_, project) => <span className="cell-sub">{project.key}</span>,
    },
    {
      title: '项目库',
      key: 'repoUrl',
      render: (_, project) =>
        project.repoUrl ? (
          <a className="repo-link" href={project.repoUrl} target="_blank" rel="noreferrer">
            仓库
          </a>
        ) : (
          <span className="cell-sub">—</span>
        ),
    },
    {
      title: '描述',
      key: 'description',
      render: (_, project) => <span className="cell-sub">{project.description || '—'}</span>,
    },
    {
      title: '',
      key: 'actions',
      width: 150,
      render: (_, project) => (
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
      ),
    },
  ];

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
        {error ? (
          <ErrorState message={error} onRetry={reload} />
        ) : (
          <DataTable<Project>
            columns={columns}
            dataSource={projects}
            rowKey={(project) => project.id}
            loading={loading}
            empty={
              <Empty
                title="还没有项目"
                description="项目是应用包与应用的一级归属，先建一个再上传包。"
                action={
                  <Link className="button button-primary" to="/directory/projects/new">
                    新增第一个项目
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
