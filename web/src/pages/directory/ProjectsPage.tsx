/**
 * 项目列表（基础资料 → 项目管理）。
 * 职责：服务端分页拉列表，**点项目名**打开详情抽屉；新增 / 编辑走独立路由。
 * 输入：URL 的 page / pageSize；输出：DataTable + 详情抽屉。
 * 边界：不内嵌表单、不发写请求；列表不留「详情」按钮——项目名本身就是唯一入口，避免两个入口抢注意力。
 *       单人模式与团队模式共用这一页，差异只有「角色列」——单人没有角色区分。
 *       「新增 / 编辑」按 project.write 隐藏：单人模式的该权限来自服务端基线，前端不必分模式。
 */
import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import type { TableColumnsType } from '@seakoi/console-kit';
import type { Project } from '../../api/projects';
import { deleteProject } from '../../api/projects';
import { readSession } from '../../api/session';
import { Button } from '../../components/Controls';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { DataTable } from '../../components/DataTable';
import { Empty, ErrorState } from '../../components/States';
import { useDeleteAction } from '../../hooks/useDeleteAction';
import { useMe } from '../../hooks/useMe';
import { usePageState } from '../../hooks/usePageState';
import { invalidateProjects, useProjectsPage } from '../../hooks/useProjects';
import { can } from '../../utils/permission';
import { ProjectDetailDrawer } from './ProjectDetailDrawer';

export function ProjectsPage(): JSX.Element {
  const navigate = useNavigate();
  const { page, pageSize, change: changePage } = usePageState();
  // 单人模式没有角色区分：只裁掉角色列，新增与编辑照常可用（单人可自建项目）。
  const solo = (readSession()?.mode ?? 'solo') === 'solo';
  const { me } = useMe();
  const writable = can(me, 'project.write');
  const deletable = can(me, 'project.delete');

  const { projects, total, loading, error, reload } = useProjectsPage(page, pageSize);
  // 详情改用抽屉：null 表示关闭，字符串表示正在看哪个项目。
  const [detailKey, setDetailKey] = useState<string | null>(null);
  // 删除成功后除了刷新本页列表，还要清掉项目列表的模块级缓存：
  // 侧栏切换器与设置页的示例都读那份缓存，不清会继续指向已删掉的项目。
  const removal = useDeleteAction<{ id: string; label: string }>(
    (target) => deleteProject(target.id),
    () => {
      invalidateProjects();
      reload();
    },
  );

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
      width: 150,
      render: (_, project) => {
        if (!writable && !deletable) {
          return null;
        }
        return (
          <div className="row-actions">
            {writable ? (
              <Button
                className="button button-ghost button-small"
                type="button"
                onClick={() => navigate(`/directory/projects/${project.key}/edit`)}
              >
                编辑
              </Button>
            ) : null}
            {deletable ? (
              <Button
                className="button button-ghost button-small button-danger"
                type="button"
                onClick={() => removal.request({ id: project.key, label: project.name })}
              >
                删除
              </Button>
            ) : null}
          </div>
        );
      },
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
        {writable ? (
          <Link className="button button-primary" to="/directory/projects/new">
            新增项目
          </Link>
        ) : null}
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
                description={
                  writable
                    ? '项目是应用包与应用的一级归属，先建一个再上传包。'
                    : '暂无项目，请联系管理员创建。'
                }
                action={
                  writable ? (
                    <Link className="button button-primary" to="/directory/projects/new">
                      新增第一个项目
                    </Link>
                  ) : undefined
                }
              />
            }
          />
        )}
      </div>

      <ProjectDetailDrawer projectKey={detailKey} solo={solo} onClose={() => setDetailKey(null)} />

      <ConfirmDialog
        open={removal.target !== null}
        title={`删除项目「${removal.target?.label ?? ''}」？`}
        description={
          <>
            <p>删除后该项目的配置从列表中移除，无法恢复。</p>
            <p className="settings-hint">
              若项目下还有应用包、还有成员、或被团队关联，删除会被拒绝并提示剩余数量。
              应用包是不可恢复的资产，不会被级联删除。
            </p>
            {removal.error ? <p className="field-error">{removal.error}</p> : null}
          </>
        }
        confirmText="删除"
        danger
        loading={removal.loading}
        onConfirm={removal.confirm}
        onCancel={removal.cancel}
      />
    </section>
  );
}
