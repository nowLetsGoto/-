/**
 * 项目详情抽屉。
 * 职责：按 key 取项目详情（含关联人员/团队名字）并在抽屉里展示，库地址可点击跳转。
 * 输入：projectKey（null 表示关闭）；输出：DetailDrawer + 详情内容。
 * 边界：**纯展示、不放任何操作入口**（编辑在列表行的操作列里）；关闭时清空数据，避免下次打开闪旧内容。
 */
import { useEffect, useState } from 'react';
import type { ProjectDetail } from '../../api/projects';
import { getProject } from '../../api/projects';
import { errorMessage } from '../../api/client';
import { DetailDrawer } from '../../components/DetailDrawer';
import { ErrorState, Loading } from '../../components/States';
import { formatDate } from '../../utils/format';

interface ProjectDetailDrawerProps {
  /** null 表示抽屉关闭 */
  projectKey: string | null;
  onClose: () => void;
}

interface DrawerState {
  loading: boolean;
  data: ProjectDetail | null;
  error: string;
}

const EMPTY: DrawerState = { loading: false, data: null, error: '' };

export function ProjectDetailDrawer({ projectKey, onClose }: ProjectDetailDrawerProps): JSX.Element {
  const [state, setState] = useState<DrawerState>(EMPTY);

  useEffect(() => {
    if (!projectKey) {
      setState(EMPTY);
      return;
    }
    let cancelled = false;
    setState({ loading: true, data: null, error: '' });
    getProject(projectKey)
      .then((data) => {
        if (!cancelled) {
          setState({ loading: false, data, error: '' });
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setState({ loading: false, data: null, error: errorMessage(err) });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [projectKey]);

  const project = state.data;

  return (
    <DetailDrawer open={Boolean(projectKey)} title={project?.name ?? '项目详情'} onClose={onClose}>
      {state.loading ? <Loading text="正在加载项目详情…" /> : null}
      {!state.loading && state.error ? <ErrorState message={state.error} /> : null}

      {!state.loading && !state.error && project ? (
        <div className="drawer-body">
          <dl className="kv-list">
            <div>
              <dt>项目名称</dt>
              <dd>{project.name}</dd>
            </div>
            <div>
              <dt>项目标识</dt>
              <dd>
                <code>{project.key}</code>
              </dd>
            </div>
            <div>
              <dt>项目库</dt>
              <dd>
                {project.repoUrl ? (
                  <a className="repo-link" href={project.repoUrl} target="_blank" rel="noreferrer">
                    {project.repoUrl}
                  </a>
                ) : (
                  <span className="cell-sub">未填写</span>
                )}
              </dd>
            </div>
            <div>
              <dt>描述</dt>
              <dd>{project.description || '—'}</dd>
            </div>
            <div>
              <dt>创建时间</dt>
              <dd>{formatDate(project.createdAt)}</dd>
            </div>
          </dl>

          <section className="drawer-section">
            <h3>关联人员（{project.persons.length}）</h3>
            {project.persons.length === 0 ? (
              <p className="settings-hint">还没有关联人员，可在「人员管理」里给成员勾选本项目。</p>
            ) : (
              <div className="chip-row">
                {project.persons.map((person) => (
                  <span className="chip" key={person.id}>
                    {person.name}
                  </span>
                ))}
              </div>
            )}
          </section>

          <section className="drawer-section">
            <h3>关联团队（{project.teams.length}）</h3>
            {project.teams.length === 0 ? (
              <p className="settings-hint">还没有关联团队，可在「团队管理」里给团队勾选本项目。</p>
            ) : (
              <div className="chip-row">
                {project.teams.map((team) => (
                  <span className="chip" key={team.id}>
                    {team.name}
                  </span>
                ))}
              </div>
            )}
          </section>

        </div>
      ) : null}
    </DetailDrawer>
  );
}
