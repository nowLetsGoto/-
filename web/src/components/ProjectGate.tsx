/**
 * 项目上下文守卫：链接里的项目 key 解析不出项目时，用一句人话替代红色报错。
 * 职责：在 /p/:key/* 下拦下「项目不存在」，并按成因给出对应出口：
 *       一个项目都没有 → 引导去新建（首次使用）；有项目但 key 失效 → 回到最近项目或去项目管理。
 *       列表请求失败时优先显示 ErrorState + 重试，不把「请求失败」误判成「还没有项目」。
 *       其余路径（含平台级 /settings、/directory/*）原样放行，不参与判断。
 * 输入：children、当前路径。
 * 输出：children，或一张空态卡片。
 * 边界：**只有在项目内路径下才挂载真正的判定组件**，
 *       平台级页面不会为了这个守卫去拉项目列表。
 */
import type { ReactNode } from 'react';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import { recentProjectKey, useCurrentProject, useProjects } from '../hooks/useProjects';
import { Button } from './Controls';
import { Empty, ErrorState, Loading } from './States';

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
  const { key, loading, error, reload, notFound, empty } = useCurrentProject();

  // 列表还没就绪时**不要渲染 children**。
  //
  // 原因：概览页/应用包页一挂载就拿 key 去请求 /builds，而此时的 key 可能来自
  // URL 或 localStorage 的失效值（换账号后残留上一个账号的项目）。若放行，
  // 用户会先看到一条「项目不存在」的红色错误，随后才被这里替换成友好空态——
  // 中间那一下闪错既没必要也让人以为系统坏了。等列表就绪再决定渲染什么，
  // 代价只是多一个 loading 帧，换来"错误提示只在真正需要时出现"。
  if (loading) {
    return <Loading text="正在读取项目…" />;
  }
  // 列表请求失败（端口错 / 服务挂）时列表为空，notFound 与 empty 都会为真，
  // 若落到下面就会把「请求失败」误导成「还没有项目」。先给错误态和同源的重试出口。
  if (error) {
    return <ErrorState message={error} onRetry={reload} />;
  }
  if (!notFound) {
    return <>{children}</>;
  }
  // 成因不同，引导就不同：没有项目可回，只能去新建。
  return empty ? <NoProjectYet /> : <ProjectMissing projectKey={key} />;
}

/**
 * 「一个项目都没有」：单人普通用户与管理员看到的都是这一张卡。
 * 项目是上传包的前置条件，所以这里唯一的动作就是去新建，不给「重试」这类无效出口。
 * 导出是为了让根路径 `/` 复用同一份文案——同一个事实不该有两种说法。
 */
export function NoProjectYet(): JSX.Element {
  return (
    <Empty
      title="还没有项目"
      description="项目是应用包与应用的一级归属。先建一个自己的项目，再回来上传第一个包。"
      action={
        <Link className="button button-primary" to="/directory/projects/new">
          新建项目
        </Link>
      }
    />
  );
}

/** 「有项目，但链接里的这个 key 不存在」：给两个恢复到可用状态的出口。 */
function ProjectMissing({ projectKey }: { projectKey: string }): JSX.Element {
  const navigate = useNavigate();
  const { projects } = useProjects();

  // notFound 且非 empty 时列表非空，所以这里一定拿得到回退目标：最近访问过的项目优先。
  const fallback = projects.find((item) => item.key === recentProjectKey()) ?? projects[0];

  // 兜底：列表若在判定与渲染之间被清空（例如别处刚删掉了最后一个项目），
  // 就降级成同一张"还没有项目"卡片，绝不渲染出指向空项目的按钮。
  if (!fallback) {
    return <NoProjectYet />;
  }

  return (
    <Empty
      title={`项目「${projectKey}」不存在`}
      description="链接里的项目可能已被删除或改名，也可能只是地址拼错了。"
      action={
        <>
          <Button
            className="button button-primary"
            type="button"
            onClick={() => navigate(`/p/${fallback.key}/overview`)}
          >
            回到最近项目
          </Button>
          <Link className="button button-ghost" to="/directory/projects">
            去项目管理
          </Link>
        </>
      }
    />
  );
}
