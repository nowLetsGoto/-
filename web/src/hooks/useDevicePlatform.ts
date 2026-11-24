/**
 * 职责：读取当前浏览器的设备平台，为下载页提供安装包匹配提示。
 * 输入：浏览器 UA、平台和触摸点；输出：移动设备平台，桌面或未知设备返回 null。
 * 边界：只做设备提示，不自动跳转、不发请求、不把视口宽度当作操作系统。
 */
import { useState } from 'react';
import { detectDevicePlatform } from '../utils/downloadPlatform';

export function useDevicePlatform() {
  const [platform] = useState(() =>
    typeof navigator === 'undefined'
      ? null
      : detectDevicePlatform(navigator.userAgent, navigator.platform, navigator.maxTouchPoints),
  );
  return platform;
}
