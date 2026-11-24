/**
 * 职责：识别移动设备平台，并为公开下载页选出同环境的对应平台包。
 * 输入：浏览器设备描述、按发布时间倒序的公开版本、目标平台与环境、可选版本号。
 * 输出：设备平台或 null，以及匹配的公开版本或 undefined。
 * 边界：纯函数；不读浏览器全局、不请求接口、不生成 URL，不跨环境兜底。
 */
import type { Environment, Platform, PublicBuildVersion } from '../api/builds';

export function detectDevicePlatform(
  userAgent: string,
  browserPlatform: string,
  maxTouchPoints: number,
): Platform | null {
  if (/HarmonyOS/i.test(userAgent)) return 'harmony';
  if (
    /iPhone|iPad|iPod/i.test(userAgent) ||
    (browserPlatform === 'MacIntel' && maxTouchPoints > 1)
  ) {
    return 'ios';
  }
  return /Android/i.test(userAgent) ? 'android' : null;
}

export function findPlatformVersion(
  versions: readonly PublicBuildVersion[],
  platform: Platform,
  environment: Environment,
  version?: string,
): PublicBuildVersion | undefined {
  const candidates = versions.filter(
    (item) => item.platform === platform && item.environment === environment,
  );
  return candidates.find((item) => item.version === version) ?? candidates[0];
}
