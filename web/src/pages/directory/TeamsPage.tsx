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
import { deleteTeam } from '../../api/teams';
import { Button } from '../../components/Controls';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { DataTable } from '../../components/DataTable';
import { Empty, ErrorState } from '../../components/States';
import { useDeleteAction } from '../../hooks/useDeleteAction';
import { useMe } from '../../hooks/useMe';
import { usePageState } from '../../hooks/usePageState';
import { useTeamsPage } from '../../hooks/useDirectory';
import { atLeastLevel } from '../../utils/accountLevel';
import { can } from '../../utils/permission';

export function TeamsPage(): JSX.Element {
  const navigate = useNavigate();
  const { page, pageSize, change: changePage } = usePageState();
  const { items: teams, total, loading, error, reload } = useTeamsPage(page, pageSize);
  const { me } = useMe();
  const writable = can(me, 'directory.write');
  // 新建团队需要公司级视野：团队负责人只能维护自己已有的那几个团队，
  // 服务端 repository/teams.save 会拒绝 TeamScoped 身份新建。
  // 这里同步隐藏入口——不显示注定失败的按钮，比点了再吃 403 更好。
  const canCreate = atLeastLevel(me, 'platform_admin');
  const deletable = can(me, 'directory.delete');
  const removal = useDeleteAction<{ id: string; label: string }>(
    (target) => deleteTeam(target.id),
    reload,
  );

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
      width: 150,
      render: (_, team) => {
        if (!writable && !deletable) {
          return null;
        }
        return (
          <div className="row-actions">
            {writable ? (
              <Button
                className="button button-ghost button-small"
                type="button"
                onClick={() => navigate(`/directory/teams/${team.id}/edit`)}
              >
                编辑
              </Button>
            ) : null}
            {deletable ? (
              <Button
                className="button button-ghost button-small button-danger"
                type="button"
                onClick={() => removal.request({ id: team.id, label: team.name })}
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
          <h1>团队管理</h1>
          <p className="sub">共 {total} 个团队 · 可关联多名人员与多个项目</p>
        </div>
        {canCreate ? (
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
                  canCreate
                    ? '新建团队后，可以把人员与项目关联进来。'
                    : '暂无团队，请联系公司管理员创建。'
                }
                action={
                  canCreate ? (
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

      <ConfirmDialog
        open={removal.target !== null}
        title={`删除团队「${removal.target?.label ?? ''}」？`}
        description={
          <>
            <p>删除后该团队从组织架构中移除，无法恢复。</p>
            <p className="settings-hint">
              若团队还有成员、还关联项目、或还挂着管理员账号，删除会被拒绝并提示还剩哪些关系。
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
