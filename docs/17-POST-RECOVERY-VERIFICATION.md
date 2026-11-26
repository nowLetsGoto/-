# 服务恢复后的验证步骤

> 2026-11-26 · 面向 R2 存储治理（任务 1—7）上线后的首次验证
> 背景：验证期间排查端口问题时执行了 `pkill`，导致 `com.dandelion.server` 无法 bootstrap
> （报 `Bootstrap failed: 5: Input/output error`）。
> 本文从"恢复服务"开始，逐层验证到"容量条可见"。

## 0. 前置：恢复 launchd 服务

### 先判断服务是否已恢复

```bash
launchctl print gui/$(id -u)/com.dandelion.server | grep -E 'state|pid'
lsof -nP -iTCP:10088 -sTCP:LISTEN
```

期望看到 `state = running` 且有 pid，并且 10088 在监听。
两者都满足就跳到 §1。

### 一次到位：跑安装脚本

```bash
bash scripts/install-autostart.sh
```

**这是首选方案**，因为脚本里已经处理了一个关键坑：

```sh
# 脚本原文注释
# launchd 对刚卸载的 label 有短暂冷却期，立即 bootstrap 会返回 EX_CONFIG(78)。
sleep 3
```

进程被强杀后，launchd 对该 label 有一段冷却期。此时立即 `bootstrap` 会失败——
这正是本次 `Input/output error` 的成因之一。
脚本的做法是：先逐个 `bootout` 清理，**等 3 秒**，再 `bootstrap`，并带失败诊断。

脚本是幂等的（可重复执行不会重复注册），且只处理 Dandelion 的四个服务，
不会影响你其它的 LaunchAgent。

### 若脚本也失败：手动重试（注意等待）

```bash
launchctl bootout gui/$(id -u)/com.dandelion.server 2>/dev/null || true
sleep 3          # 关键：不要省略，冷却期未过会一直报错
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.dandelion.server.plist
```

若仍报 `Input/output error`，**重启 Mac**。重启是恢复 launchd 用户域最可靠的方式，
且服务会因 `RunAtLoad` 自动拉起，无需再做任何操作。

> 不要执行 `launchctl bootout gui/$(id -u)`——它会卸载该用户下**所有** LaunchAgent，
> 影响范围超出 Dandelion，可能连带停掉你其它的自启服务。

### 临时可用方案（不推荐长期使用）

如果只是想立刻把服务跑起来看效果，可以在终端前台运行：

```bash
cd ~/dandelion-runtime && ./dandelion
```

优点：立刻可用。缺点：**关掉终端就停、崩溃不会自动重启**——
只用于临时验证，验证完仍应把 launchd 修好。

## 1. 验证服务健康

```bash
curl -s --noproxy '*' http://127.0.0.1:10088/healthz
```

期望返回 `dbOk` 与 `storageWritable` 均为 `true`。

启动日志应包含这三行（`~/dandelion-runtime/logs/server.err.log`）：

```
对象存储镜像已启用 bucket=api-4 下载通道=公开CDN(https://download.dreamgarden.world)
Dandelion 就绪 0.0.0.0:10088 | 对外基址=https://dandelion.dreamgarden.world | ...
淘汰巡检完成：3 个租户 × 2 分区，淘汰 0 个对象，失败 0 次，耗时 Xms
```

**第三行是本次改造的关键证据**：它证明淘汰 Worker 已随服务启动并跑完第一轮。
若缺失，说明 `RunRetentionWorker` 未被调用，或未配置对象存储（日志会写「淘汰巡检未启动」）。

## 2. 验证数据库迁移

七个治理字段必须齐全（team 与 solo 两个分区都要有）：

```bash
psql "postgres://dandelion:dandelion@localhost:5432/dandelion?sslmode=disable" -c "
SELECT table_schema, count(*) AS 字段数
FROM information_schema.columns
WHERE table_name = 'builds'
  AND column_name IN ('r2_state','r2_synced_at','r2_bytes',
                      'pin_kind','pinned_at','pinned_by','last_accessed_at')
GROUP BY table_schema;"
```

期望两行，字段数都是 `7`。

再确认存量包的状态已收敛（否则容量约束会因字节全 0 而失效）：

```bash
psql "postgres://dandelion:dandelion@localhost:5432/dandelion?sslmode=disable" -c "
SELECT r2_state, count(*), pg_size_pretty(sum(r2_bytes)::bigint) AS 占用
FROM (SELECT r2_state, r2_bytes FROM team.builds
      UNION ALL SELECT r2_state, r2_bytes FROM solo.builds) t
GROUP BY r2_state ORDER BY 2 DESC;"
```

期望：有 `r2_key` 的包是 `synced` 且占用非 0；没有的为 `pending`、占用 0。

## 3. 验证上传走新 key 结构

本次改造把 R2 对象路径改为 `{租户}/{项目}/direct/{token}.{ext}`，
使对象自带归属信息（数据库丢失时仍能还原属于谁）。

**操作**：在后台任意项目下传一个小包。

**然后查数据库里的新 key**：

```bash
psql "postgres://dandelion:dandelion@localhost:5432/dandelion?sslmode=disable" -c "
SELECT r2_key, r2_state, pg_size_pretty(r2_bytes) AS 占用
FROM team.builds
WHERE r2_key LIKE '%/direct/%'
ORDER BY created_at DESC LIMIT 3;"
```

期望格式形如 `xxxxxxxx-xxxx-..../world2-1/direct/yyyy.apk`——
**租户 id 与项目 key 各占一段**。

> 历史对象仍是旧格式（`direct/xxx.apk`、`test/android/xxx.apk`），
> 数据库存的是完整 key，下载不受影响，无需迁移。

## 4. 验证用量接口

该接口需要登录态，未认证应返回 **401**：

```bash
curl -s --noproxy '*' -o /dev/null -w '%{http_code}\n' \
  http://127.0.0.1:10088/api/v1/storage/usage
```

期望 `401`。

要看真实数据，登录后带上令牌：

```bash
TOKEN=$(curl -s --noproxy '*' -X POST http://127.0.0.1:10088/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"你的账号","password":"你的密码"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["token"])')

curl -s --noproxy '*' -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:10088/api/v1/storage/usage | python3 -m json.tool
```

期望结构：

```json
{
  "usedBytes": 624636154,
  "quotaBytes": 9663676416,
  "freeBytes": 9039040262,
  "estimatedUploads": 86,
  "buildCount": 8,
  "r2BuildCount": 6,
  "maxVersions": 66,
  "remainingVersions": 60,
  "pendingCount": 2,
  "failedCount": 0,
  "groups": [
    { "projectKey": "world2-1", "environment": "test", "platform": "android",
      "buildCount": 4, "r2Bytes": 374781708 }
  ]
}
```

**核对三件事**：
- `quotaBytes` 应为 `9663676416`（9 GiB）
- `groups` 按 `r2Bytes` 从大到小排序
- `groups` 为空时应是 `[]` 而不是 `null`

## 5. 验证容量条可见

浏览器打开应用页（`/apps`），页面头部应出现容量条：

```
对象存储                         595 MB / 9 GB  6.6%
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
还可上传约 86 个包 · 版本 6/66
```

**三档视觉**（可临时改数据库验证，验证后改回）：

```bash
# 让用量看起来接近上限 → 应转警示橙色
# 注意：PostgreSQL 不支持 UPDATE ... LIMIT，必须用子查询圈定单行
psql "postgres://dandelion:dandelion@localhost:5432/dandelion?sslmode=disable" -c "
UPDATE team.builds SET r2_bytes = 8000000000
WHERE id IN (SELECT id FROM team.builds WHERE r2_key <> '' LIMIT 1);"
```

验证完把数据改回去（把 `r2_bytes` 恢复成 `file_size`）：

```bash
psql "postgres://dandelion:dandelion@localhost:5432/dandelion?sslmode=disable" -c "
UPDATE team.builds SET r2_bytes = file_size
WHERE r2_key <> '' AND r2_bytes = 8000000000;"
```

超过 100% 时应转危险红色，提示「已超出配额，最早的版本将被移出对象存储」。

**容量条不显示时的排查顺序**：
1. 浏览器控制台看 `/api/v1/storage/usage` 是否 401（未登录或令牌过期）
2. 强刷（Cmd+Shift+R）确认加载的是新前端产物
3. 确认 `server/public/assets/AppsPage-*.js` 里含 `storage-usage` 字样：
   ```bash
   grep -l "storage-usage" server/public/assets/*.js
   ```

> 按设计，取不到用量时容量条整条不渲染——这是为了避免辅助信息拖垮整个页面。
> 所以"看不到"不等于"出错"，要按上面三步确认。

## 6. 验证淘汰 Worker 在跑

看巡检日志：

```bash
grep "淘汰巡检" ~/dandelion-runtime/logs/server.err.log | tail -5
```

期望每 2 小时出现一行 `淘汰巡检完成：N 个租户 × 2 分区，淘汰 X 个对象，失败 Y 次`。

**服务重启会立即跑一轮**，这是有意设计的（重启往往意味着上次被中断）。

### 想立刻看到淘汰真的生效

不必等 2 小时，也不必真的塞满 9GB。用一条临时记录验证整条链路：

```bash
psql "postgres://dandelion:dandelion@localhost:5432/dandelion?sslmode=disable" -c "
-- 造一个超过配额的单条记录，巡检查到后必然淘汰它
INSERT INTO team.builds
  (project_id, environment, platform, app_name, original_file_name, file_size,
   file_ext, relative_path, r2_key, r2_state, r2_bytes)
SELECT id, 'test', 'android', 'evict-probe', 'probe.apk', 1,
       'apk', 'test/android/probe.apk', 'probe/nonexistent.apk', 'synced', 10000000000
FROM team.projects LIMIT 1;"
```

重启服务触发一轮巡检，然后确认该记录**仍在、但已标记淘汰**：

```bash
psql "postgres://dandelion:dandelion@localhost:5432/dandelion?sslmode=disable" -c "
SELECT app_name, r2_state, r2_key, r2_bytes
FROM team.builds WHERE app_name = 'evict-probe';"
```

期望：`r2_state = 'evicted'`、`r2_key` 为空串、`r2_bytes = 0`，而**这条记录还查得到**。

这正是「只删 R2、不删数据库」的核心契约。

验证完清理掉：

```bash
psql "postgres://dandelion:dandelion@localhost:5432/dandelion?sslmode=disable" -c "
DELETE FROM team.builds WHERE app_name = 'evict-probe';"
```

## 7. 验证上传前的项目校验

本次改造在取上传票据时就校验项目是否存在与可访问，避免传完 300MB 才发现项目无效。

**操作**：在浏览器开发者工具里手动发一个带错误项目的请求：

```bash
TOKEN=<登录令牌>
curl -s --noproxy '*' -X POST http://127.0.0.1:10088/api/v1/builds/upload-ticket \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"project":"不存在的项目","fileName":"a.apk","size":1024,"contentType":"application/vnd.android.package-archive"}'
```

期望返回项目不存在的 404 或 400（而非签发票据成功）。

## 7.5 验证访问时间被记录（淘汰排序的依据）

淘汰按「最久未被访问优先」排序，前提是 `last_accessed_at` 真的被写入。
此项一旦失效不会报错，只会在配额紧张时删错包，因此必须验证。

**操作**：在后台或分享页下载任意一个包。

**然后查该包的访问时间**：

```bash
psql "postgres://dandelion:dandelion@localhost:5432/dandelion?sslmode=disable" -c "
SELECT left(id::text,8) AS id, download_count, last_accessed_at
FROM team.builds WHERE r2_key <> ''
ORDER BY created_at DESC LIMIT 3;"
```

期望：刚下载过的那个包 `last_accessed_at` 非空，且 `download_count` 相应递增。

**判据**：如果出现「`download_count > 0` 但 `last_accessed_at` 为空」，
说明记录链路断了——淘汰排序会退化为纯按上传时间，需要立刻排查。

> 历史包在本次修复前下载过，其 `last_accessed_at` 仍为空。
> 它们会在下一次被下载时补上，无需回填。

## 8. 验收清单

| # | 检查项 | 期望 | 结果 |
| --- | --- | --- | --- |
| 1 | `launchctl print` 状态 | `state = running` 且有 pid | ☐ |
| 2 | `/healthz` | `dbOk` 与 `storageWritable` 为 true | ☐ |
| 3 | 启动日志含「淘汰巡检完成」 | 出现该行 | ☐ |
| 4 | 两分区各 7 个治理字段 | 两行，字段数 7 | ☐ |
| 5 | `synced` 包占用非 0 | 有 `r2_key` 的包 `r2_bytes > 0` | ☐ |
| 6 | 新上传的 key 含租户与项目 | `{租户}/{项目}/direct/xxx` | ☐ |
| 7 | 用量接口未认证 | 返回 401 | ☐ |
| 8 | 用量接口登录后 | `quotaBytes=9663676416`，`groups` 降序 | ☐ |
| 9 | 应用页头部 | 容量条可见、数值与接口一致 | ☐ |
| 10 | 淘汰实测 | 记录仍在、`r2_state=evicted`、`r2_key` 空 | ☐ |
| 10b | 下载后访问时间 | `last_accessed_at` 非空且计数递增 | ☐ |
| 11 | 上传票据传错项目 | 被拒，不签发票据 | ☐ |

## 9. 出问题时的日志位置

| 内容 | 路径 |
| --- | --- |
| 服务日志（含巡检） | `~/dandelion-runtime/logs/server.err.log` |
| launchd 状态 | `launchctl print gui/$(id -u)/com.dandelion.server` |
| plist | `~/Library/LaunchAgents/com.dandelion.server.plist` |
| 存储根 | `/Users/linanan/Desktop/dandelion/storage` |

**服务起不来时先看这三件事**，能排除掉绝大多数情况：

```bash
plutil -lint ~/Library/LaunchAgents/com.dandelion.server.plist   # plist 是否合法
ls -l ~/dandelion-runtime/dandelion                              # 二进制是否存在且可执行
tail -30 ~/dandelion-runtime/logs/server.err.log                 # 最近报什么错
```