/**
 * 顶栏：面包屑 + 应用包页的上传入口。
 * 职责：显示「项目 / 板块」，让用户在任意后台页面知道当前位置；
 *       应用包页且有项目时，把「上传包」放在右上角。
 * 输入：当前路径与当前登录人；输出：顶栏 JSX。
 * 边界：**只在项目内页面解析项目名**（`ProjectCrumb` / `TopbarUpload` 子组件），
 *       平台级页面（基础资料 / 设置）因此不会因为面包屑或上传入口多拉一次项目列表。
 */
import { Link, useLocation, useParams } from 'react-router-dom';
import { useMe } from '../hooks/useMe';
import { useCurrentProject } from '../hooks/useProjects';
import { can } from '../utils/permission';
import { Icon } from './icons';
import { boardLabel, isProjectScoped } from './navigation';

export function Topbar(): JSX.Element {
  const { pathname } = useLocation();
  const { key } = useParams<{ key: string }>();
  const { me } = useMe();
  const projectScoped = isProjectScoped(pathname);
  const isPackagesPage = /^\/p\/[^/]+\/packages\/?$/.test(pathname);
  const showUpload = isPackagesPage && Boolean(key) && can(me, 'build.write');

  return (
    <header className="topbar">
      <div className="crumbs">
        {projectScoped ? <ProjectCrumb /> : null}
        <span className="crumb-last">{boardLabel(pathname)}</span>
      </div>
      {showUpload ? <TopbarUpload to={`/p/${key}/upload`} /> : null}
    </header>
  );
}

/** 项目不存在时顶栏不该挂一个点了也进不去的上传入口。 */
function TopbarUpload({ to }: { to: string }): JSX.Element | null {
  const { project } = useCurrentProject();
  if (!project) {
    return null;
  }
  return (
    <div className="topbar-actions">
      <Link className="button button-primary" to={to}>
        <Icon name="plus" />
        上传包
      </Link>
    </div>
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
