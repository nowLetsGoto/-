/**
 * 项目详情（基础资料 → 项目管理 → 详情）。
 * 职责：展示项目信息（含可点击的项目库地址）与关联的人员、团队。
 * 输入：URL 参数 key；输出：getProject 的三态页面。
 * 边界：只读；编辑入口回到列表页，不在这里再放一套表单。
 */
import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import type { ProjectDetail } from '../../api/projects';
import { getProject } from '../../api/projects';
import { errorMessage } from '../../api/client';
import { ErrorState, Loading } from '../../components/States';
import { formatDate } from '../../utils/format';

interface PageState {
  loading: boolean;
  data: ProjectDetail | null;
  error: string;
}

export function ProjectDetailPage(): JSX.Element {
  const { key = '' } = useParams<{ key: string }>();
  const [state, setState] = useState<PageState>({ loading: true, data: null, error: '' });
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setState({ loading: true, data: null, error: '' });
    getProject(key)
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
  }, [key, reloadKey]);

  const backLink = (
    <Link className="button button-ghost" to="/directory/projects">
      返回项目列表
    </Link>
  );

  if (state.loading) {
    return (
      <section className="page">
        <Loading text="正在加载项目详情…" />
      </section>
    );
  }
  if (state.error || !state.data) {
    return (
      <section className="page">
        <div className="page-head">
          <div>
            <h1>项目详情</h1>
            <p className="sub">标识：{key}</p>
          </div>
          {backLink}
        </div>
        <ErrorState message={state.error || '项目不存在'} onRetry={() => setReloadKey((value) => value + 1)} />
      </section>
    );
  }

  const project = state.data;

  return (
    <section className="page">
      <div className="page-head">
        <div>
          <h1>{project.name}</h1>
          <p className="sub">标识：{project.key} · 创建于 {formatDate(project.createdAt)}</p>
        </div>
        <div className="page-head-actions">
          <Link className="button button-primary" to={`/directory/projects/${project.key}/edit`}>
            编辑项目
          </Link>
          {backLink}
        </div>
      </div>

      <div className="card settings-card">
        <h2>项目信息</h2>
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
        </dl>
      </div>

      <div className="card settings-card">
        <h2>关联人员</h2>
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
      </div>

      <div className="card settings-card">
        <h2>关联团队</h2>
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
      </div>
    </section>
  );
}
