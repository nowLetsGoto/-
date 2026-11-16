/**
 * 项目列表与「当前项目」的唯一入口。
 * 职责：一次取回项目列表并缓存；从 URL 的 /p/:key 解析当前项目；记住最近访问的项目；
 *       识别「链接里的项目不存在」供骨架层做友好降级。
 * 输入：react-router 的当前路径；输出：projects / current / loading / error / notFound。
 * 边界：不发其他请求、不写业务状态；最近访问只存 key，存的是"上次看的项目"这件事本身；
 *       notFound 只是「列表已就绪但仍然找不到」的事实，不负责展示与跳转（那是 components/ProjectGate 的事）。
 */
import { useCallback, useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import type { Project } from '../api/projects';
import { listProjects } from '../api/projects';
import { errorMessage } from '../api/client';

const RECENT_KEY = 'dandelion.project';

/** 列表很少变，模块级缓存避免侧栏与顶栏各请求一次。 */
let cache: Project[] | null = null;
let inflight: Promise<Project[]> | null = null;

function loadProjects(): Promise<Project[]> {
  if (cache) {
    return Promise.resolve(cache);
  }
  if (!inflight) {
    inflight = listProjects({ pageSize: 0 })
      .then((result) => {
        cache = result.items;
        return cache;
      })
      .finally(() => {
        inflight = null;
      });
  }
  return inflight;
}

/** 新增项目后调用，让下一次读取重新拉列表。 */
export function invalidateProjects(): void {
  cache = null;
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
  loading: boolean;
  /** 列表已加载完成、列表非空、却按 key 找不到该项目（链接里的项目被删或改名）。 */
  notFound: boolean;
}

/** 当前项目的完整信息；侧栏切换器、顶栏面包屑与 ProjectGate 都用它。 */
export function useCurrentProject(): CurrentProject {
  const key = useProjectKey();
  const { projects, loading } = useProjects();

  const project = projects.find((item) => item.key === key) ?? null;
  // 首屏还在加载时列表为空，此时不能判定"不存在"，否则会先闪一下错误再跳回来。
  const notFound = !loading && projects.length > 0 && project === null;

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

  return { key, project, loading, notFound };
}

/**
 * 项目列表（分页）：只给「项目管理」列表页用。
 * 与 useProjects（全量、带缓存）区分开——下拉选项与项目守卫需要完整列表，
 * 列表页需要的是「当前页」，两者混用会让一边拿到错的数据量。
 */
export function useProjectsPage(
  page: number,
  pageSize: number,
): { projects: Project[]; total: number; loading: boolean; error: string; reload: () => void } {
  const [state, setState] = useState<{ projects: Project[]; total: number; loading: boolean; error: string }>({
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
