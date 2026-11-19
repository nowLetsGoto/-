/**
 * 人员列表（基础资料 → 人员管理）。
 * 职责：只调人员列表接口，关联的团队/项目名字由该接口直接带回。
 * 输入：URL 的 page / pageSize；输出：服务端分页的 DataTable + 「新增人员」入口。
 * 边界：不拉团队或项目列表（那属于对应页面自己的事）、不内嵌表单；
 *       「新增 / 编辑」按 directory.write 隐藏，「删除」按 directory.delete 隐藏；
 *       真正能不能写由服务端判定——按钮隐藏只是体验，不是安全边界。
 */
import { Link, useNavigate } from 'react-router-dom';
import type { TableColumnsType } from '@seakoi/console-kit';
import type { Person } from '../../api/people';
import { deletePerson } from '../../api/people';
import { Button } from '../../components/Controls';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { DataTable } from '../../components/DataTable';
import { Empty, ErrorState } from '../../components/States';
import { useDeleteAction } from '../../hooks/useDeleteAction';
import { useMe } from '../../hooks/useMe';
import { usePageState } from '../../hooks/usePageState';
import { usePeoplePage } from '../../hooks/useDirectory';
import { can } from '../../utils/permission';

export function PeoplePage(): JSX.Element {
  const navigate = useNavigate();
  const { page, pageSize, change: changePage } = usePageState();
  const { items: people, total, loading, error, reload } = usePeoplePage(page, pageSize);
  const { me } = useMe();
  // directory.write 可由角色勾选，平台管理员另有固有下发（见 authz.managementPermissions）。
  // 前端零逻辑：can() 判的是服务端合成后的最终权限集，不关心它来自角色还是身份。
  const writable = can(me, 'directory.write');
  const deletable = can(me, 'directory.delete');
  // 删除的确认与错误展示交给统一 hook；成功后 reload 让列表回到服务端真相。
  const removal = useDeleteAction<{ id: string; label: string }>(
    (target) => deletePerson(target.id),
    reload,
  );

  const columns: TableColumnsType<Person> = [
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
      title: '角色',
      key: 'role',
      width: 130,
      render: (_, person) =>
        person.roleName ? (
          <span className="badge badge-role">{person.roleName}</span>
        ) : (
          <span className="cell-sub">未分配</span>
        ),
    },
    {
      title: '联系方式',
      key: 'contact',
      render: (_, person) => (
        <span className="cell-sub">
          {[person.email, person.phone].filter(Boolean).join(' · ') || '—'}
        </span>
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
      width: 150,
      render: (_, person) => {
        if (!writable && !deletable) {
          return null;
        }
        return (
          <div className="row-actions">
            {writable ? (
              <Button
                className="button button-ghost button-small"
                type="button"
                onClick={() => navigate(`/directory/people/${person.id}/edit`)}
              >
                编辑
              </Button>
            ) : null}
            {deletable ? (
              <Button
                className="button button-ghost button-small button-danger"
                type="button"
                onClick={() => removal.request({ id: person.id, label: person.name })}
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
          <h1>人员管理</h1>
          <p className="sub">共 {total} 人 · 可关联多个团队与项目</p>
        </div>
        {writable ? (
          <Link className="button button-primary" to="/directory/people/new">
            新增人员
          </Link>
        ) : null}
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
                description={
                  writable
                    ? '新增人员后，可以在团队与项目中关联他。'
                    : '暂无人员，请联系管理员添加。'
                }
                action={
                  writable ? (
                    <Link className="button button-primary" to="/directory/people/new">
                      新增第一个人员
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
        title={`删除人员「${removal.target?.label ?? ''}」？`}
        description={
          <>
            <p>删除后该人员从通讯录中移除，无法恢复。</p>
            <p className="settings-hint">
              若他仍属于团队、参与项目或被账号绑定，删除会被拒绝并提示还需解除哪些关联。
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
