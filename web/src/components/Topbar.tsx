/**
 * 顶栏：面包屑。
 * 职责：显示「项目 / 板块」，让用户在任意后台页面知道当前位置。
 * 输入：当前路径；输出：顶栏 JSX。
 * 边界：**只在项目内页面解析项目名**（`ProjectCrumb` 子组件），
 *       平台级页面（基础资料 / 设置）因此不会因为面包屑而多拉一次项目列表。
 *       环境切换与上传入口属于应用包页面，不放在这里。
 */
import { useLocation } from 'react-router-dom';
import { useCurrentProject } from '../hooks/useProjects';
import { boardLabel, isProjectScoped } from './navigation';

export function Topbar(): JSX.Element {
  const { pathname } = useLocation();
  const projectScoped = isProjectScoped(pathname);

  return (
    <header className="topbar">
      <div className="crumbs">
        {projectScoped ? <ProjectCrumb /> : null}
        <span className="crumb-last">{boardLabel(pathname)}</span>
      </div>
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
