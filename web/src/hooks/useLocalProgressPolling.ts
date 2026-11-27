/**
 * 有包在下载（或等待自动重试）时，定时刷新列表以更新进度条。
 *
 * 职责：判断「当前是否需要轮询」，需要就按固定间隔调用 refresh。
 * 输入：列表数据、刷新回调。
 * 输出：无。
 * 边界：只调度刷新节奏，不请求数据、不改状态；
 *       进度值本身来自服务端，前端不自己累加——两处各算一遍必然漂移。
 *
 * 为什么按需轮询而不是常开：
 * 一次列表请求要查两个分区，页面开着就每 5 秒打一次，
 * 绝大多数时间却没有任何包在动。只在有 pulling/failed 时才轮询，空闲时零请求。
 */
import { useEffect, useRef } from 'react';
import type { Build } from '../api/builds';

/** 轮询间隔：进度条 2% 一档，5 秒足够看出在动，也不至于给后端压力。 */
const POLL_INTERVAL_MS = 5000;

/**
 * 失败态的轮询上限（次数）。
 *
 * 失败会自己变好——补拉巡检每 5 分钟自动重试，所以失败态也要轮询，
 * 否则后端已经在重下了，卡片还停在红色的「下载失败」上。
 *
 * 但不能无限轮询：若某个包因为「对象不在桶里」这类不可恢复的原因永远失败，
 * 每 5 秒一次就成了永不停止的空转。给 30 次（约 2.5 分钟），
 * 足够跨过一次自动重试的时间窗；超过之后停止，用户手动刷新即可。
 */
const MAX_FAILED_POLLS = 30;

export function useLocalProgressPolling(builds: Build[], refresh: () => void): void {
  // 用 ref 持有最新回调：把它放进依赖数组会让 effect 每次渲染重建，
  // 定时器被反复清除重建，实际间隔变得不可预期。
  const refreshRef = useRef(refresh);
  refreshRef.current = refresh;

  // 失败态的已轮询次数。只统计「连续失败」——一旦出现 pulling 或恢复正常，
  // 说明自动重试起作用了，计数器应归零，以便下一次失败重新获得完整窗口。
  const failedPollsRef = useRef(0);

  // 只要还有「在下载」或「失败」的包就轮询。
  const pulling = builds.some((build) => build.localState === 'pulling');
  const failed = builds.some((build) => build.localState === 'failed');

  const shouldPoll = pulling || (failed && failedPollsRef.current < MAX_FAILED_POLLS);

  // 有 pulling 就清零计数：正在下载说明自动重试已生效，
  // 之后若再次失败应重新给满一轮观察窗口。
  // 放在 effect 里而不是 render 中：render 期间修改 ref 在并发模式下不安全。
  useEffect(() => {
    if (pulling) {
      failedPollsRef.current = 0;
    }
  }, [pulling]);

  useEffect(() => {
    if (!shouldPoll) {
      return;
    }
    const timer = window.setInterval(() => {
      // 仅失败态时累计次数；有 pulling 时不计，
      // 因为下载本身可能持续十几分钟，不应被这个上限截断。
      if (!pulling) {
        failedPollsRef.current += 1;
      }
      refreshRef.current();
    }, POLL_INTERVAL_MS);
    return () => window.clearInterval(timer);
  }, [shouldPoll, pulling]);
}
