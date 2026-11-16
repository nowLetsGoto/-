/**
 * 项目上下文守卫：链接里的项目 key 失效时，用一句人话替代红色报错。
 * 职责：在 /p/:key/* 下拦截「项目不存在」，给出「回到最近项目」与「去项目管理」两个出口；
 *       其余路径（含平台级 /settings、/directory/*）原样放行，不参与判断。
 * 输入：children、当前路径。
 * 输出：children，或一张「项目不存在」空态卡片。
 * 边界：**只有在项目内路径下才挂载真正的判定组件**，
 *       平台级页面不会为了这个守卫去拉项目列表。
 */
import type { ReactNode } from 'react';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import { recentProjectKey, useCurrentProject, useProjects } from '../hooks/useProjects';
import { Empty } from './States';

export function ProjectGate({ children }: { children: ReactNode }): JSX.Element {
  const { pathname } = useLocation();

  // 平台级页面没有项目上下文，直接放行（此处不调用任何取数 hook）。
  const projectScoped = pathname === '/p' || pathname.startsWith('/p/');
  if (!projectScoped) {
    return <>{children}</>;
  }
  return <ProjectScopedGate>{children}</ProjectScopedGate>;
}

/** 只会在 /p/* 下挂载，因此这里的取数是必要的。 */
function ProjectScopedGate({ children }: { children: ReactNode }): JSX.Element {
  const navigate = useNavigate();
  const { projects } = useProjects();
  const { key, notFound } = useCurrentProject();

  if (!notFound) {
    return <>{children}</>;
  }

  // notFound 成立时列表非空，所以这里一定拿得到回退目标：最近访问过的项目优先。
  const fallback = projects.find((item) => item.key === recentProjectKey()) ?? projects[0];
  const backTarget = fallback ? `/p/${fallback.key}/overview` : '/';

  return (
    <Empty
      title={`项目「${key}」不存在`}
      description="链接里的项目可能已被删除或改名，也可能只是地址拼错了。"
      action={
        <>
          <button className="button button-primary" type="button" onClick={() => navigate(backTarget)}>
            回到最近项目
          </button>
          <Link className="button button-ghost" to="/directory/projects">
            去项目管理
          </Link>
        </>
      }
    />
  );
}
