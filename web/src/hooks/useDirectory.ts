/**
 * 基础资料的两个列表：人员与团队。
 * 职责：各自一份模块级缓存与加载函数，页面只取自己需要的那个列表，互不牵连。
 * 输入：无；输出：items / 三态 / reload。
 * 边界：不读项目（项目由 hooks/useProjects 负责）、不发写请求；
 *       关联名字由接口直接带回，页面不必为了显示名字再拉对方列表。
 */
import { useCallback, useEffect, useState } from 'react';
import type { Person } from '../api/people';
import { listPeople } from '../api/people';
import type { Team } from '../api/teams';
import { listTeams } from '../api/teams';
import { errorMessage } from '../api/client';

interface ListState<T> {
  items: T[];
  loading: boolean;
  error: string;
}

/** 人员缓存：只被 usePeople 使用。 */
let peopleCache: Person[] | null = null;
let peopleInflight: Promise<Person[]> | null = null;

function loadPeople(): Promise<Person[]> {
  if (peopleCache) {
    return Promise.resolve(peopleCache);
  }
  if (!peopleInflight) {
    peopleInflight = listPeople()
      .then((result) => {
        peopleCache = result.items;
        return peopleCache;
      })
      .finally(() => {
        peopleInflight = null;
      });
  }
  return peopleInflight;
}

/** 团队缓存：只被 useTeams 使用。 */
let teamsCache: Team[] | null = null;
let teamsInflight: Promise<Team[]> | null = null;

function loadTeams(): Promise<Team[]> {
  if (teamsCache) {
    return Promise.resolve(teamsCache);
  }
  if (!teamsInflight) {
    teamsInflight = listTeams()
      .then((result) => {
        teamsCache = result.items;
        return teamsCache;
      })
      .finally(() => {
        teamsInflight = null;
      });
  }
  return teamsInflight;
}

/**
 * 两个缓存一起失效：改人员会改变团队的成员列表，改团队也会改变人员的所属团队，
 * 所以写操作后统一失效，避免出现「这边刷新了、那边还是旧的」。
 */
export function invalidateDirectory(): void {
  peopleCache = null;
  teamsCache = null;
}

/** 人员与团队共用的加载 / 三态 / 重载逻辑，避免两份几乎相同的 hook。 */
function useCachedList<T>(load: () => Promise<T[]>, invalidate: () => void): ListState<T> & { reload: () => void } {
  const [state, setState] = useState<ListState<T>>({ items: [], loading: true, error: '' });
  const [version, setVersion] = useState(0);

  useEffect(() => {
    let cancelled = false;
    load()
      .then((items) => {
        if (!cancelled) {
          setState({ items, loading: false, error: '' });
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
    setState({ items: [], loading: true, error: '' });
    setVersion((value) => value + 1);
  }, [invalidate]);

  return { ...state, reload };
}

export function usePeople(): { people: Person[]; loading: boolean; error: string; reload: () => void } {
  const { items, ...rest } = useCachedList(loadPeople, invalidateDirectory);
  return { people: items, ...rest };
}

export function useTeams(): { teams: Team[]; loading: boolean; error: string; reload: () => void } {
  const { items, ...rest } = useCachedList(loadTeams, invalidateDirectory);
  return { teams: items, ...rest };
}
