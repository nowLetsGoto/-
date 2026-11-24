# 09 · 外部上传接口（Open API）

> 面向：CI / 发布脚本 / 其它内部系统
> 配套：接口实现在 `server/internal/handler/openapi/`，鉴权在 `server/internal/middleware/apikey/`
> 契约：`api/openapi.yaml` 的 `/open/builds`（唯一 SSOT）

## 1. 它解决什么

管理端上传是「选文件 → 暂存解析 → 确认表单 → 发布」两步；外部系统没有界面，需要**一次请求完成全部**。
所以 `/open/builds` 把 stage 与 create 合成一次调用，并用 API Key 鉴权，不依赖管理员登录态。

## 2. 鉴权

| 项 | 值 |
| --- | --- |
| 请求头 | `X-API-Key: <key>` |
| Key 来源 | 管理端「系统设置 → 外部 API」 |
| 存储 | `system_settings.api_key`，32 位随机串，建表时生成 |
| 隔离 | 只授权 `/open/builds`；用它访问 `/api/v1/builds` 等管理接口仍会 401 |

Key 为空或服务端未配置时，接口一律拒绝——宁可全拒，也不放行一个没有凭据的写入口。

## 3. 请求

`POST /api/v1/open/builds`，`Content-Type: multipart/form-data`。

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `project` | 是 | 项目 key（小写字母/数字/短横线），包归属到该项目；在管理端左上角项目切换器里可见 |
| `file` | 是 | `.apk` / `.ipa` / `.hap` 文件本身，单包上限 200 MB |
| `environment` | 是 | `test` / `hotfix` / `pre` / `prd` |
| `platform` | 是 | `android` / `ios` / `harmony`，必须与文件扩展名一致 |
| `appName` | 否 | 省略时用包内解析值，再缺才用文件名 |
| `packageId` | 否 | 同上 |
| `version` | 否 | 同上 |
| `buildNumber` | 否 | 同上 |
| `changelog` | 否 | 更新说明 |

**必填三项**：`project`、`environment`、`platform` 必须由调用方给出，服务端不做兜底——缺任意一项直接 400。
`project` 必须已存在（先用 `GET /api/v1/projects` 查，或在管理端新建），否则返回 400「项目不存在」。

**补齐规则**：`调用方传值 → 包内解析值 → 文件名兜底`，三级依次生效，**只对 `appName` / `packageId` / `version` / `buildNumber` 这四个展示字段生效**。
只要发生过兜底，服务端会把 `metaSource` 记为 `fallback`，可在列表里识别出「字段不完整」的包。
鸿蒙 `.hap` 暂不做解析，四个字段全部按上传时传入的值落库（缺省则用文件名兜底），不影响发布。

```bash
curl -X POST https://dandelion.example.com/api/v1/open/builds \
  -H "X-API-Key: <key>" \
  -F "project=default" \
  -F "file=@./app-release.apk" \
  -F "environment=test" \
  -F "platform=android" \
  -F "changelog=修复登录态过期"
```

## 4. 响应

成功 `201`，返回完整 Build（与管理端发布一致，含三个绝对地址）：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "b8cf8d2e-78ad-45c7-83f1-e7a235d60374",
    "environment": "test",
    "platform": "android",
    "appName": "商城 App",
    "packageId": "com.example.shop",
    "version": "1.4.2",
    "buildNumber": "142",
    "fileSize": 40058880,
    "metaSource": "parsed",
    "downloadCount": 0,
    "downloadPagePath": "/d/b8cf8d2e-78ad-45c7-83f1-e7a235d60374",
    "downloadPageUrl": "https://dandelion.example.com/d/b8cf8d2e-78ad-45c7-83f1-e7a235d60374?shareToken=<share-token>",
    "downloadFileUrl": "https://dandelion.example.com/api/v1/public/builds/b8cf8d2e-78ad-45c7-83f1-e7a235d60374/file?shareToken=<share-token>",
    "createdAt": "2026-11-16T06:12:31Z"
  }
}
```

`downloadPageUrl` 可以直接发给测试同学：手机扫码打开独立下载页，无需登录。必须完整保留 shareToken 查询参数；它只允许查看和下载本次发布的一个版本，公司 API Key 不能放进分享链接。

## 5. 错误

| HTTP | code | 场景 |
| --- | --- | --- |
| 400 | 400 | 缺文件 / 缺 `project` / 项目不存在 / 扩展名不符 / 环境或平台非法 / 字段超长 |
| 401 | 401 | Key 缺失、错误或服务端未配置 |
| 413 | 413 | 超过 200 MB |
| 500 | 500 | 写文件或写库失败 |

错误统一返回 `{code, data: null, message}`，`message` 是可直接上屏的中文。

## 6. 与 Webhook 的关系

上传成功（`201`）后会**异步**触发一次 Webhook，事件名 `build.created`。
两者互不阻塞：Webhook 目标不可达只记日志，不影响上传结果。

接收端若配置了签名密钥，可用同一密钥校验：

```bash
# 请求头 X-Dandelion-Signature: sha256=<hex>
printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SECRET" | awk '{print "sha256="$2}'
```

投递体只包含「系统设置里勾选」的字段，`build` 是对象、`event` 与 `sentAt` 固定存在：

```json
{
  "event": "build.created",
  "sentAt": "2026-11-16T06:12:31Z",
  "build": { "appName": "商城 App", "version": "1.4.2", "downloadPageUrl": "https://…/d/…" }
}
```

## 7. 限制与后续

- 不做并发限流、不做重试队列：失败重试由调用方（CI）自己决定。
- 不支持查询 / 删除 / 更新，写接口只有一个。
- Key 轮换目前走运维 SQL（`UPDATE system_settings SET api_key = ...`），后续可在设置页加「重新生成」。
