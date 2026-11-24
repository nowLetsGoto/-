/**
 * 职责：验证扫码设备识别及跨平台切换时的环境、版本约束。
 * 输入：iPhone、iPad 桌面 UA、Android 与桌面设备，以及跨环境的版本样本。
 * 输出：平台识别和目标包选择的 Node 测试断言。
 * 边界：不模拟安装、不请求接口；浏览器页面交互另行验证。
 */
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { detectDevicePlatform, findPlatformVersion } from '../src/utils/downloadPlatform.ts';

test('识别 iPhone、桌面模式 iPad 和 Android；Mac 桌面不推荐 iOS 包', () => {
  assert.equal(
    detectDevicePlatform('Mozilla/5.0 (iPhone; CPU iPhone OS 18_0)', 'iPhone', 5),
    'ios',
  );
  assert.equal(
    detectDevicePlatform('Mozilla/5.0 (Macintosh; Intel Mac OS X)', 'MacIntel', 5),
    'ios',
  );
  assert.equal(
    detectDevicePlatform('Mozilla/5.0 (Linux; Android 15)', 'Linux armv8l', 5),
    'android',
  );
  assert.equal(
    detectDevicePlatform('Mozilla/5.0 (Macintosh; Intel Mac OS X)', 'MacIntel', 0),
    null,
  );
  assert.equal(detectDevicePlatform('unknown', '', 0), null);
});

const versions = [
  { id: 'ios-prod', platform: 'ios', environment: 'prd', version: '3.0' },
  { id: 'ios-new', platform: 'ios', environment: 'test', version: '2.0' },
  { id: 'android', platform: 'android', environment: 'test', version: '1.0' },
  { id: 'ios-matching', platform: 'ios', environment: 'test', version: '1.0' },
];

test('跨平台优先保持同环境同版本；没有同版本时选择该环境最新包', () => {
  assert.equal(findPlatformVersion(versions, 'ios', 'test', '1.0')?.id, 'ios-matching');
  assert.equal(findPlatformVersion(versions, 'ios', 'test', '0.9')?.id, 'ios-new');
  assert.equal(findPlatformVersion(versions, 'android', 'test', '1.0')?.id, 'android');
});

test('目标环境未发布时不回退到其它环境', () => {
  assert.equal(findPlatformVersion(versions, 'ios', 'pre', '1.0'), undefined);
  assert.equal(findPlatformVersion(versions, 'android', 'prd', '1.0'), undefined);
  assert.equal(findPlatformVersion([], 'ios', 'test'), undefined);
});
