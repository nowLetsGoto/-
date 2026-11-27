/**
 * 有包正在下载到本机时，定时刷新列表以更新进度条。
 *
 * 职责：判断「当前是否需要轮询」，需要就按固定间隔调用 refresh。
 * 输入：列表数据、刷新回调。
 * 输出：无。
 * 边界：只调度刷新节奏，不请求数据、不改状态；
 *       进度值本身来自服务端，前端不自己累加——两处各算一遍必然漂移。
 *
 * 为什么按需轮询而不是常开：
 * 一次列表请求要查两个分区，页面开着就每 5 秒打一次，
 * 绝大多数时间却没有任何包在下载。只在有 pulling 时才轮询，
 * 空闲时零额外请求。
 */
import { useEffect, useRef } from 'react';
import type { Build } from '../api/builds';

/** 轮询间隔：进度条 2% 一档，5 秒足够看出在动，也不至于给后端压力。 */
const POLL_INTERVAL_MS = 5000;

export function useLocalProgressPolling(builds: Build[], refresh: () => void): void {
  // 用 ref 持有最新回调：把它放进依赖数组会让 effect 每次渲染重建，
  // 定时器被反复清除重建，实际间隔变得不可预期。
  const refreshRef = useRef(refresh);
  refreshRef.current = refresh;

  // 只在「正在下载」时轮询。失败态不轮询：它不会自己变好，
  // 要等下一轮巡检重试，那时候重新进页面即可看到新状态。
  const pulling = builds.some((build) => build.localState === 'pulling');

  useEffect(() => {
    if (!pulling) {
      return;
    }
    const timer = window.setInterval(() => refreshRef.current(), POLL_INTERVAL_MS);
    return () => window.clearInterval(timer);
  }, [pulling]);
}
