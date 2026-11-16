/**
 * 基础资料的两个列表：人员与团队。
 * 职责：提供「分页列表」与「全量选项」两种用法，两者走同一个接口、不同 pageSize。
 * 输入：分页用 (page, pageSize)；选项用无参。
 * 输出：items / total / 三态 / reload。
 * 边界：不读项目（项目在 hooks/useProjects）、不发写请求；
 *       关联名字由接口直接带回，页面不必为了显示名字再拉对方列表。
 */
import { useCallback, useEffect, useState } from 'react';
import type { Person } from '../api/people';
import { listPeople } from '../api/people';
import type { Team } from '../api/teams';
import { listTeams } from '../api/teams';
import { errorMessage } from '../api/client';

/** 分页列表的返回形状。 */
interface PagedState<T> {
  items: T[];
  total: number;
  loading: boolean;
  error: string;
}

/** 全量选项的返回形状（选项不需要 total）。 */
interface AllState<T> {
  items: T[];
  loading: boolean;
  error: string;
}

const EMPTY_PAGED = { items: [], total: 0, loading: true, error: '' };

/**
 * 分页列表的通用加载逻辑：页码或页大小变化就重新取。
 * 刻意不做模块级缓存——分页结果天然属于「某一页」，缓存它只会让翻页看到旧数据。
 */
function usePaged<T>(
  load: (page: number, pageSize: number) => Promise<{ items: T[]; total: number }>,
  page: number,
  pageSize: number,
): PagedState<T> & { reload: () => void } {
  const [state, setState] = useState<PagedState<T>>(EMPTY_PAGED);
  const [version, setVersion] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setState((prev) => ({ ...prev, loading: true, error: '' }));
    load(page, pageSize)
      .then((result) => {
        if (!cancelled) {
          setState({ items: result.items, total: result.total, loading: false, error: '' });
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setState({ items: [], total: 0, loading: false, error: errorMessage(err) });
        }
      });
    return () => {
      cancelled = true;
    };
    // load 是模块级函数，引用稳定；这里只跟页码、页大小与手动刷新走。
  }, [load, page, pageSize, version]);

  return { ...state, reload: () => setVersion((value) => value + 1) };
}

/** 全量选项的通用加载逻辑：只取一次，写操作后由 invalidateDirectory 清缓存。 */
function useAll<T>(load: () => Promise<{ items: T[] }>, invalidate: () => void): AllState<T> & { reload: () => void } {
  const [state, setState] = useState<AllState<T>>({ items: [], loading: true, error: '' });
  const [version, setVersion] = useState(0);

  useEffect(() => {
    let cancelled = false;
    load()
      .then((result) => {
        if (!cancelled) {
          setState({ items: result.items, loading: false, error: '' });
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setState({ items: [], loading: false, error: errorMessage(err) });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [load, version]);

  const reload = useCallback(() => {
    invalidate();
    setVersion((value) => value + 1);
  }, [invalidate]);

  return { ...state, reload };
}

// ---- 全量选项缓存（只给表单的多选下拉用）----
let peopleAllCache: Person[] | null = null;
let peopleAllInflight: Promise<{ items: Person[] }> | null = null;

function loadAllPeople(): Promise<{ items: Person[] }> {
  if (peopleAllCache) {
    return Promise.resolve({ items: peopleAllCache });
  }
  if (!peopleAllInflight) {
    peopleAllInflight = listPeople({ pageSize: 0 })
      .then((result) => {
        peopleAllCache = result.items;
        return { items: result.items };
      })
      .finally(() => {
        peopleAllInflight = null;
      });
  }
  return peopleAllInflight;
}

let teamsAllCache: Team[] | null = null;
let teamsAllInflight: Promise<{ items: Team[] }> | null = null;

function loadAllTeams(): Promise<{ items: Team[] }> {
  if (teamsAllCache) {
    return Promise.resolve({ items: teamsAllCache });
  }
  if (!teamsAllInflight) {
    teamsAllInflight = listTeams({ pageSize: 0 })
      .then((result) => {
        teamsAllCache = result.items;
        return { items: result.items };
      })
      .finally(() => {
        teamsAllInflight = null;
      });
  }
  return teamsAllInflight;
}

/**
 * 两个全量缓存一起失效：改人员会改变团队的成员列表，改团队也会改变人员的所属团队，
 * 写操作后统一清掉，避免出现「这边刷新了、那边还是旧的」。
 */
export function invalidateDirectory(): void {
  peopleAllCache = null;
  teamsAllCache = null;
}

function loadPeoplePage(page: number, pageSize: number): Promise<{ items: Person[]; total: number }> {
  return listPeople({ page, pageSize });
}

function loadTeamsPage(page: number, pageSize: number): Promise<{ items: Team[]; total: number }> {
  return listTeams({ page, pageSize });
}

/** 人员列表（分页）。 */
export function usePeoplePage(page: number, pageSize: number): PagedState<Person> & { reload: () => void } {
  return usePaged(loadPeoplePage, page, pageSize);
}

/** 团队列表（分页）。 */
export function useTeamsPage(page: number, pageSize: number): PagedState<Team> & { reload: () => void } {
  return usePaged(loadTeamsPage, page, pageSize);
}

/** 人员全量（表单多选选项）。 */
export function useAllPeople(): AllState<Person> & { reload: () => void } {
  return useAll(loadAllPeople, invalidateDirectory);
}

/** 团队全量（表单多选选项）。 */
export function useAllTeams(): AllState<Team> & { reload: () => void } {
  return useAll(loadAllTeams, invalidateDirectory);
}
