/**
 * 项目列表与「当前项目」的唯一入口。
 * 职责：一次取回项目列表并缓存；从 URL 的 /p/:key 解析当前项目；记住最近访问的项目；
 *       识别「链接里的项目不存在」供骨架层做友好降级。
 * 输入：react-router 的当前路径；输出：projects / current / loading / error / notFound / empty。
 * 边界：不发其他请求、不写业务状态；最近访问只存 key，存的是"上次看的项目"这件事本身；
 *       notFound / empty 只是「列表已就绪但仍然解析不出项目」的事实，不负责展示与跳转
 *       （那是 components/ProjectGate 的事）。
 */
import { useCallback, useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import type { Project } from '../api/projects';
import { listProjects } from '../api/projects';
import { errorMessage } from '../api/client';
import { readSession } from '../api/session';

const RECENT_KEY = 'dandelion.project';

/**
 * 项目列表的模块级缓存。
 *
 * **必须与登录身份绑定**：这是浏览器内存里的模块状态，不随登出清空。
 * 上一个账号（例如默认公司的成员）留下的列表，会被下一个账号（例如刚建好公司、
 * 一个项目都没有的平台管理员）直接读走——于是它看到的是别人的项目，
 * 点进去请求 400「项目不存在」，或首屏带着失效 key 去请求 /builds。
 *
 * 因此缓存里存下"这份列表属于谁"，读取时比对；身份变了就重新拉。
 * owner 由 readSession() 给出（用户名 + 模式）：同一用户换模式看到的数据范围不同，也算换身份。
 */
let cache: { owner: string; items: Project[] } | null = null;
let inflight: { owner: string; promise: Promise<Project[]> } | null = null;

/** 当前缓存归属；未登录时给空串（此时不该有任何列表请求）。 */
function cacheOwner(): string {
  const session = readSession();
  return session ? `${session.username}|${session.mode}` : '';
}

function loadProjects(): Promise<Project[]> {
  const owner = cacheOwner();
  if (cache && cache.owner === owner) {
    return Promise.resolve(cache.items);
  }
  // 身份变了：旧缓存立即失效，避免在等新请求期间被别处读到。
  if (cache && cache.owner !== owner) {
    cache = null;
  }
  if (!inflight || inflight.owner !== owner) {
    const promise = listProjects({ pageSize: 0 })
      .then((result) => {
        cache = { owner, items: result.items };
        return result.items;
      })
      .finally(() => {
        if (inflight?.owner === owner) {
          inflight = null;
        }
      });
    inflight = { owner, promise };
  }
  return inflight.promise;
}

/** 新增项目后调用，让下一次读取重新拉列表。 */
export function invalidateProjects(): void {
  cache = null;
  // 进行中的请求也一起丢弃：否则它 resolve 时会把旧列表写回缓存。
  inflight = null;
}

export function rememberProject(key: string): void {
  try {
    window.localStorage.setItem(RECENT_KEY, key);
  } catch {
    // localStorage 被禁用时不影响主流程，退化成"永远用第一个项目"。
  }
}

/** 最近访问过的项目 key；没有记录时返回空串。 */
export function recentProjectKey(): string {
  try {
    return window.localStorage.getItem(RECENT_KEY) ?? '';
  } catch {
    return '';
  }
}

interface ProjectsState {
  projects: Project[];
  loading: boolean;
  error: string;
}

export function useProjects(): ProjectsState & { reload: () => void } {
  const [state, setState] = useState<ProjectsState>({ projects: [], loading: true, error: '' });
  const [version, setVersion] = useState(0);

  useEffect(() => {
    let cancelled = false;
    loadProjects()
      .then((projects) => {
        if (!cancelled) {
          setState({ projects, loading: false, error: '' });
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setState({ projects: [], loading: false, error: errorMessage(err) });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [version]);

  // 新建 / 编辑项目后调用：清缓存并重拉，其他页面的下拉会在下次挂载时看到新数据。
  const reload = useCallback(() => {
    invalidateProjects();
    setState({ projects: [], loading: true, error: '' });
    setVersion((value) => value + 1);
  }, []);

  return { ...state, reload };
}

/**
 * 当前项目 key 的优先级：URL 段 → 最近访问。
 * **刻意不读 /me 与项目列表**：侧栏在每个页面都要拼链接，如果这里顺带取数，
 * 连「人员管理」这类与项目无关的页面也会多出两次请求。没有 key 时 navHref 会退回首页。
 */
export function useProjectKey(): string {
  const { key } = useParams<{ key: string }>();
  return key || recentProjectKey();
}

interface CurrentProject {
  key: string;
  project: Project | null;
  /** 项目列表是否仍在加载。ProjectGate 用它决定"先别渲染 children"，避免用未验证的 key 发请求。 */
  loading: boolean;
  /**
   * 列表已就绪、却按 key 解析不出项目。两种成因：
   * ① 一个项目都没有（首次使用，地址里的 key 必然是失效的）；
   * ② 有项目但都不叫这个 key（被删或改名）。
   * 二者对"该给什么引导"是不同的答案，所以下面用 empty 再区分一次。
   */
  notFound: boolean;
  /** notFound 的成因是「一个项目都没有」，此时应引导去新建项目而非回到某个项目。 */
  empty: boolean;
}

/** 当前项目的完整信息；侧栏切换器、顶栏面包屑与 ProjectGate 都用它。 */
export function useCurrentProject(): CurrentProject {
  const key = useProjectKey();
  const { projects, loading } = useProjects();

  const project = projects.find((item) => item.key === key) ?? null;
  // 首屏还在加载时列表为空，此时不能判定"不存在"，否则会先闪一下错误再跳回来。
  // 列表为空且已就绪同样算"解析不出项目"：地址里的 key 不可能是有效的，
  // 若放行就会带着废 key 去请求接口，后端只能回「项目不存在」。
  const notFound = !loading && project === null;
  const empty = notFound && projects.length === 0;

  // 进到某个项目就把"最近访问"记下来，下次访问 / 时回到它。
  // 只记**真实解析出来的项目**：首帧 loading=true 时 notFound 还是 false，
  // 若用 key（可能来自 URL 的失效值）或 !notFound 作条件，坏 key 会被抢先写进
  // localStorage 并永久污染「最近项目」（之后 / 会重定向到坏链接、设置页示例也会照抄出坏 key）。
  // 依赖用 project?.key 而不是 project 对象：项目列表刷新时对象引用会变，key 稳定才不空转。
  const resolvedKey = project?.key ?? '';
  useEffect(() => {
    if (resolvedKey) {
      rememberProject(resolvedKey);
    }
  }, [resolvedKey]);

  return { key, project, loading, notFound, empty };
}

/**
 * 项目列表（分页）：只给「项目管理」列表页用。
 * 与 useProjects（全量、带缓存）区分开——下拉选项与项目守卫需要完整列表，
 * 列表页需要的是「当前页」，两者混用会让一边拿到错的数据量。
 * 可见范围由登录模式决定（单人只有自己的项目），前端不需要也不应该传过滤条件。
 */
export function useProjectsPage(
  page: number,
  pageSize: number,
): { projects: Project[]; total: number; loading: boolean; error: string; reload: () => void } {
  const [state, setState] = useState<{
    projects: Project[];
    total: number;
    loading: boolean;
    error: string;
  }>({
    projects: [],
    total: 0,
    loading: true,
    error: '',
  });
  const [version, setVersion] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setState((prev) => ({ ...prev, loading: true, error: '' }));
    listProjects({ page, pageSize })
      .then((result) => {
        if (!cancelled) {
          setState({ projects: result.items, total: result.total, loading: false, error: '' });
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setState({ projects: [], total: 0, loading: false, error: errorMessage(err) });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [page, pageSize, version]);

  return { ...state, reload: () => setVersion((value) => value + 1) };
}
