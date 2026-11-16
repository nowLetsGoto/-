/**
 * 顶栏：面包屑 + 全局环境切换 + 板块主行动。
 * 职责：显示「项目 / 板块 / 环境」，切换环境（写进 URL），在需要的板块上提供「上传包」入口。
 * 输入：当前路径与环境；输出：顶栏 JSX。
 * 边界：**只在项目内页面解析项目名**（`ProjectCrumb` 子组件），
 *       平台级页面（基础资料 / 设置）因此不会因为面包屑而多拉一次项目列表。
 */
import { Link, useLocation } from 'react-router-dom';
import { useEnvironment } from '../hooks/useEnvironment';
import { useCurrentProject } from '../hooks/useProjects';
import { ENVIRONMENTS, environmentLabel } from '../utils/format';
import { Icon } from './icons';
import { boardLabel, canUpload, isEnvScoped, isProjectScoped, projectKeyFromPath } from './navigation';

export function Topbar(): JSX.Element {
  const { pathname } = useLocation();
  const [environment, setEnvironment] = useEnvironment();

  const projectScoped = isProjectScoped(pathname);
  const envScoped = isEnvScoped(pathname);
  const showUpload = canUpload(pathname);

  return (
    <header className="topbar">
      <div className="crumbs">
        {projectScoped ? <ProjectCrumb /> : null}
        <span className={envScoped ? undefined : 'crumb-last'}>{boardLabel(pathname)}</span>
        {envScoped ? (
          <>
            <span className="crumb-sep" aria-hidden="true">
              /
            </span>
            <span className="crumb-last">{environmentLabel(environment)}</span>
          </>
        ) : null}
      </div>

      {envScoped ? (
        <div className="env-switch" role="group" aria-label="环境">
          {ENVIRONMENTS.map((item) => (
            <button
              key={item.value}
              type="button"
              className={item.value === environment ? 'env-option env-option-active' : 'env-option'}
              aria-pressed={item.value === environment}
              onClick={() => setEnvironment(item.value)}
            >
              {item.label}
            </button>
          ))}
        </div>
      ) : null}

      {showUpload ? (
        <div className="topbar-actions">
          <Link className="button button-primary" to={`/p/${projectKeyFromPath(pathname)}/upload`}>
            <Icon name="plus" />
            上传包
          </Link>
        </div>
      ) : null}
    </header>
  );
}

/**
 * 项目名面包屑段。
 * 单独成组件是为了「按需取数」：只有项目内页面才会挂载它，
 * 平台级页面不会因此触发 /api/v1/me 与项目列表请求。
 */
function ProjectCrumb(): JSX.Element | null {
  const { project } = useCurrentProject();
  if (!project) {
    return null;
  }
  return (
    <>
      <span>{project.name}</span>
      <span className="crumb-sep" aria-hidden="true">
        /
      </span>
    </>
  );
}
