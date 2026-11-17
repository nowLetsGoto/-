/**
 * 项目列表（基础资料 → 项目管理）。
 * 职责：服务端分页拉列表，**点项目名**打开详情抽屉；新增 / 编辑走独立路由。
 * 输入：URL 的 page / pageSize；输出：DataTable + 详情抽屉。
 * 边界：不内嵌表单、不发写请求；列表不留「详情」按钮——项目名本身就是唯一入口，避免两个入口抢注意力。
 *       单人模式与团队模式共用这一页，差异只有「角色列」——单人没有角色区分。
 */
import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import type { TableColumnsType } from '@seakoi/console-kit';
import type { Project } from '../../api/projects';
import { readSession } from '../../api/session';
import { Button } from '../../components/Controls';
import { DataTable } from '../../components/DataTable';
import { Empty, ErrorState } from '../../components/States';
import { usePageState } from '../../hooks/usePageState';
import { useProjectsPage } from '../../hooks/useProjects';
import { ProjectDetailDrawer } from './ProjectDetailDrawer';

export function ProjectsPage(): JSX.Element {
  const navigate = useNavigate();
  const { page, pageSize, change: changePage } = usePageState();
  // 单人模式没有角色区分：只裁掉角色列，新增与编辑照常可用（单人可自建项目）。
  const solo = (readSession()?.mode ?? 'solo') === 'solo';

  const { projects, total, loading, error, reload } = useProjectsPage(page, pageSize);
  // 详情改用抽屉：null 表示关闭，字符串表示正在看哪个项目。
  const [detailKey, setDetailKey] = useState<string | null>(null);

  const columns: TableColumnsType<Project> = [
    {
      title: '项目名称',
      key: 'name',
      render: (_, project) => (
        <Button className="project-link" type="button" onClick={() => setDetailKey(project.key)}>
          {project.name}
        </Button>
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
    ...(solo
      ? []
      : [
          {
            title: '项目角色',
            key: 'role',
            width: 130,
            render: (_: unknown, project: Project) =>
              project.roleName ? (
                <span className="badge badge-role">{project.roleName}</span>
              ) : (
                <span className="cell-sub">沿用人员角色</span>
              ),
          },
        ]),
    {
      title: '描述',
      key: 'description',
      render: (_, project) => <span className="cell-sub">{project.description || '—'}</span>,
    },
    {
      title: '',
      key: 'actions',
      width: 90,
      render: (_, project) => (
        <div className="row-actions">
          <Button
            className="button button-ghost button-small"
            type="button"
            onClick={() => navigate(`/directory/projects/${project.key}/edit`)}
          >
            编辑
          </Button>
        </div>
      ),
    },
  ];

  return (
    <section className="page">
      <div className="page-head">
        <div>
          <h1>项目管理</h1>
          <p className="sub">
            {solo ? `共 ${total} 个你的项目` : `共 ${total} 个项目 · 应用包与应用都挂在项目下`}
          </p>
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
            serverPagination={{ page, pageSize, total, onChange: changePage }}
            empty={
              <Empty
                title={solo ? '还没有你的项目' : '还没有项目'}
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

      <ProjectDetailDrawer projectKey={detailKey} solo={solo} onClose={() => setDetailKey(null)} />
    </section>
  );
}
