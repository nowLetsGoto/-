# Dandelion · 多环境 App 包分发平台

团队内部自建的 App 包分发工具：把 `.apk` / `.ipa` 按四个环境上传到指定位置，按「环境 + 平台」找得到，点开独立下载页能下载、能扫码转发。

前后端同仓：`api/openapi.yaml` 是唯一契约 SSOT，`web/` 是唯一 npm 包，`server/` 是 Go module，根 `Makefile` 是唯一编排入口。

> ⚠️ `docs/02-ARCHITECTURE.md` 与 `docs/03-TASKS.md` 是 v1（Node + SQLite）废弃设计，仅供历史追溯；实现一律以 `docs/02-ARCHITECTURE-v2.md` + `docs/03-TASKS-v2.md` 为准。

---

## 1. 第一版范围（10 条 P0）

| 能力 | 说明 |
| --- | --- |
| 上传包 | 拖拽 / 点击选 `.apk`、`.ipa`，必选环境与平台，选填应用名、包名、版本号、构建号、更新说明 |
| 按环境 + 平台查列表 | 四环境常驻切换 + 平台筛选（写进 URL，可转发），按上传时间倒序 |
| 独立下载页 | `/d/:id?shareToken=...` 免登录查看并切换同应用有效分享版本；裸路径为登录预览，直接打开 / 刷新由 SPA fallback 兜住 |
| 下载原始文件 | Android 返回 `application/vnd.android.package-archive`，iOS 返回 `application/octet-stream` |
| 下载页二维码 | 内容为该包下载页的**绝对地址**，手机默认相机可扫 |
| 自动解析 | APK/IPA 解析应用名、包名、版本号、构建号并自动带出表单，字段始终可改，失败降级不阻塞 |
| 下载次数 | 仅在完整流式下载成功后 +1，列表页与下载页展示同一口径 |
| 登录 | 账号登录（种子管理员默认 `admin` / `admin123456`）；登录时选单人/团队模式，单人默认；令牌 12 小时有效 |

**明确不做**：iOS 浏览器直接安装（plist / itms-services）、列表页二维码入口、推送 / 通知 / Webhook / 订阅 / CI 集成 / 开放 API Key、多成员与角色权限（第一版只有单管理员登录，没有成员体系）、密码或回答问题安装、有效期 / 定时发布 / 短链、渠道与统计报表、鸿蒙 / CDN / 多语言、包删除与上传后编辑、应用图标解析、后端二维码接口、`POST /builds/parse` 预览端点、提交接口携带文件、任何多板块功能。

---

## 2. 目录结构

```
api/openapi.yaml         唯一契约 SSOT（枚举 / DTO / 路径 / 错误码 / 常量）
web/                     唯一 npm 包（Vite + React + TS）
  src/api/schema.d.ts    openapi-typescript 生成物，已入库
  src/api/               client.ts（唯一 HTTP 出入口）、builds.ts（唯一 URL 消费处）
  src/components/        Layout（三段式 + 导航数组）、FileDropzone、States
  src/pages/             UploadPage / ListPage / DownloadPage
server/                  Go module
  cmd/server/main.go     装配层
  public/                前端产物（//go:embed 目标，构建时生成）
  internal/              config / contract / handler / service / repository（含 schema.sql，//go:embed）/ parser / storage / middleware / app
deploy/                  Dockerfile / docker-compose.yml / nginx.conf / dandelion.service
storage/                 运行时数据（tmp 暂存 + uploads 正式包，已 gitignore）
Makefile                 gen:api / dev / build / test / up / down
```

---

## 3. 快速开始（本地）

前置：Go 1.24+、Node 20+、PostgreSQL 16+（用 Docker 起，或用本机已装实例，见下）。

```bash
docker compose -f deploy/docker-compose.yml up -d db   # 必须先起数据库
cp .env.example .env                                   # 必改 PUBLIC_BASE_URL；生产还需改 AUTH_SECRET 与 ADMIN_PASSWORD
npm --prefix web install
make dev                                               # 同时起 :10088（Go）与 :5173（Vite）
```

- 开发时浏览器打开 `http://localhost:5173`；`/api` 由 Vite 代理到 :10088。
- 首次打开会先到登录页：默认 `admin` / `admin123456`（可用 `.env` 的 `ADMIN_USERNAME` / `ADMIN_PASSWORD` 改）。
- 手机扫码请访问 `PUBLIC_BASE_URL` 指向的**局域网地址**（Vite 已监听 `0.0.0.0`），并确保手机与开发机同网段、防火墙放行 5173。
- 契约变更后执行 `make gen:api` 重生成 `web/src/api/schema.d.ts` 并提交入库；生成物入库后，前端 dev 不需要 Go、后端 build 不需要 Node。
- 纯静态自检：`make test`（Go 契约一致性测试）、`npm --prefix web run build`（前端类型检查 + 构建）。

### 无 Docker：使用本机 PostgreSQL

本机已装 PostgreSQL 时不必用 Docker。**建表 DDL 已 `//go:embed` 进二进制、进程启动时幂等执行**，所以只需先建一个空库，**不需要手工导入 `schema.sql`**：

```bash
createdb dandelion                                     # 本机 PostgreSQL 已有实例时
export DATABASE_URL="postgres://$(whoami)@localhost:5432/dandelion?sslmode=disable"
export PUBLIC_BASE_URL="http://<你的局域网IP>:5173"
npm --prefix web install
make dev
```

- 若本机 PG 需要密码或使用非默认端口，把 `DATABASE_URL` 换成实际凭证与端口（如 `postgres://user:pass@localhost:5433/dandelion?sslmode=disable`）。
- `GO_ENV=development` 下 `PUBLIC_BASE_URL` 允许填本机地址，但下载页会提示「当前对外基址不可被手机访问」；**要让手机扫码，必须填开发机的局域网 IP**（见 §4 的三场景表）。
- 只想跑后端、不启 Vite：先 `npm --prefix web run build` 生成 `server/public/`，再 `cd server && go run ./cmd/server`（DDL 与静态资源都已内嵌，不依赖当前工作目录）；或直接 `make build && ./bin/dandelion`，用单进程跑全部。

生产/单机一键构建（顺序不可颠倒，Go 的 `//go:embed public/*` 需要前端产物先存在）：

```bash
make build          # 前端产物 → server/public/，再 CGO_ENABLED=0 go build → bin/dandelion
./bin/dandelion     # 单进程同时提供 API、静态资源与 SPA fallback
```

---

## 4. 环境变量

唯一读取处是 `server/internal/config/config.go`；`VITE_API_BASE` 由前端 Vite 读取。`Makefile` 会把 `.env` 导出给子进程。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `DATABASE_URL` | `postgres://dandelion:dandelion@localhost:5432/dandelion?sslmode=disable` | PostgreSQL 连接串；生产改 `sslmode=require` |
| `PUBLIC_BASE_URL` | `http://localhost:10088` | 对外访问入口基址，三个绝对 URL 的唯一来源；生产必填手机可达地址 |
| `PORT` | `10088` | Go 监听端口 |
| `HOST` | `0.0.0.0` | Go 监听地址 |
| `STORAGE_DIR` | `./storage` | 运行时数据根，相对进程工作目录（make 系列命令在仓库根运行） |
| `MAX_UPLOAD_MB` | `200` | 单包上限，不得超过契约的 200 MB |
| `GO_ENV` | `development` | `development` 放宽不可达基址；其它值按生产严格校验、非法即 fail-fast |
| `ADMIN_USERNAME` | `admin` | 唯一管理员账号，不落库 |
| `ADMIN_PASSWORD` | `admin123456` | 管理员口令；**生产必须改** |
| `AUTH_SECRET` | `dandelion-dev-only-secret-change-me` | 登录令牌及分享签名派生的根密钥；轮换会使已发登录令牌和分享签名失效，生产沿用默认值会 fail-fast（`openssl rand -hex 32`） |
| `VITE_API_BASE` | 空 | 前端 API 基址，留空表示同源 / 走代理 |

### `PUBLIC_BASE_URL` 三场景

| 场景 | 取值 | 谁提供 `/d/:id` | 手机可达前提 |
| --- | --- | --- | --- |
| 本地开发 | `http://<开发机局域网IP>:5173` | Vite dev server（history fallback） | 同 WiFi / 同网段、防火墙放行 5173；**不能填 :10088，dev 下 Go 不托管 SPA** |
| 单机 / 内网 | `http://<内网IP>:10088` 或 `http://dandelion.internal` | Go 单二进制 embed 或 Nginx | 同内网 / VPN / 内网 DNS |
| 公网 / 容器 | `https://dandelion.example.com` | Nginx 终止 TLS + 反代 | 公网 DNS + 有效证书 |

禁止 `localhost` / `127.0.0.1` / `0.0.0.0` / `::1` / 容器内部服务名。生产环境非法会 fail-fast；开发环境放宽，但前端二维码区会显示「当前对外基址不可被手机访问，请配置 PUBLIC_BASE_URL」，不会渲染扫不开的码。

服务端生成绝对地址**只用 `PUBLIC_BASE_URL`，不读请求 `Host` / `X-Forwarded-*`**，所以 Vite proxy 与 Nginx 反代下结果一致。

---

## 5. 接口速查（以下为常用接口，完整路径以 OpenAPI 为准）

统一响应 `{code, data, message}`：成功 `code=0`，失败 `code` = HTTP 状态码（只有 400 / 401 / 404 / 405 / 413 / 500）且 `data=null`。唯一例外是文件下载成功返回二进制流。

| # | 方法 | 路径 | 鉴权 | 说明 |
| --- | --- | --- | --- | --- |
| 1 | POST | `/api/v1/auth/login` | 公开 | JSON `{username,password}`，返回 `{token,username,expiresAt}`；凭据错误返回 400 |
| 2 | GET | `/api/v1/projects` | 需登录 | 项目列表（按创建时间正序）；`?page=&pageSize=`，下拉与守卫传 `pageSize=0` 取全量。返回范围由登录模式决定：团队模式全量，单人模式只有本人的项目 |
| 3 | POST | `/api/v1/projects` | 需登录 | JSON `{key,name,description}`，`key` 在所属分区内唯一、创建后不可改（它是 URL 段），返回 201。两种模式都可新建；单人模式的项目归属自己 |
| 4 | POST | `/api/v1/builds/stage` | 需登录 | multipart `file`，暂存 + 同步解析，恒 200（解析失败只写 `warnings`） |
| 5 | POST | `/api/v1/builds` | 需登录 | **application/json**，`CommitBuildRequest` **恰好 9 字段**（`project` + stageToken + 表单值），不含文件与 `metaSource`，返回 201 完整 Build |
| 6 | GET | `/api/v1/builds?project=&environment=&platform=` | 需登录 | 列表，`project` 必填、`platform` 省略或 `all` 表示不筛，空结果 `items: []` |
| 7 | GET | `/api/v1/builds/{id}` | 需登录＋项目读取权限 | 内部下载预览详情 |
| 8 | GET | `/api/v1/builds/{id}/file` | 需登录＋项目读取权限 | 原始包流 |
| 分享详情 | GET | `/api/v1/public/builds/{id}` | 分享 Token | 免登录，只返回授权版本的白名单详情 |
| 分享版本 | GET | `/api/v1/public/builds/{id}/versions` | 来源包分享 Token | 同应用的环境、平台及历史版本，各带目标包自己的分享地址 |
| 分享文件 | GET | `/api/v1/public/builds/{id}/file` | 分享 Token | 免登录原生下载；每次校验授权 |
| 内部历史 | GET | `/api/v1/builds/{id}/versions` | 需登录＋项目读取权限 | 内部预览的其他版本 |
| 9 | GET | `/healthz` | 公开 | 健康检查（独立于 `/api/v1`） |
| 10 | GET | `/api/v1/settings` | **超管** | Webhook 配置 + API Key + 可推送字段目录（普通账号 403） |
| 11 | PUT | `/api/v1/settings` | **超管** | 覆盖保存 Webhook 配置（含字段勾选） |
| 12 | POST | `/api/v1/settings/webhook/test` | **超管** | 发一条测试消息，返回 `delivered / statusCode` |
| 13 | POST | `/api/v1/open/builds` | **API Key** | 外部系统一步式上传（multipart，`project` 必填），详见 `docs/09-OPEN-API.md` |
| 14 | GET | `/api/v1/projects/{key}` | 需登录 | 项目详情（含关联人员 / 团队 ID） |
| 15 | PUT | `/api/v1/projects/{key}` | 需登录 | 编辑项目（名称 / 描述 / 库地址；`key` 不可改） |
| 16 | GET | `/api/v1/people` | 需登录 | 人员列表（含关联名字）；`?page=&pageSize=`，`pageSize=0` 取全量 |
| 17 | POST | `/api/v1/people` | 需登录 | 新增人员，`teamIds` / `projectIds` 为多选 |
| 18 | PUT | `/api/v1/people/{id}` | 需登录 | 编辑人员（全量覆盖字段与关联） |
| 19 | GET | `/api/v1/teams` | 需登录 | 团队列表（含关联名字）；分页同上 |
| 20 | POST | `/api/v1/teams` | 需登录 | 新增团队，`personIds` / `projectIds` 为多选 |
| 21 | PUT | `/api/v1/teams/{id}` | 需登录 | 编辑团队（全量覆盖字段与关联） |
| 22 | GET | `/api/v1/me` | 需登录 | 当前登录人 + 关联项目 key（列表页默认项目的来源） |
| 23 | GET | `/api/v1/people/{id}` | 需登录 | 人员详情（编辑页回填），id 非 UUID 或不存在均 404 |
| 24 | GET | `/api/v1/teams/{id}` | 需登录 | 团队详情（编辑页回填），同上 |
| 25 | GET | `/api/v1/admin/users` | **超管** | 账号列表（分页 + 关键字），非超管 403 |
| 26 | POST | `/api/v1/admin/users` | **超管** | 新建账号（账号名 + 初始密码 + 绑定人员 + 角色 + 是否超管），返回 201 |
| 27 | PUT | `/api/v1/admin/users/{id}` | **超管** | 改超管标记 / 绑定人员 / 角色（登录名不可改；非超管不能分配超出自己权限的角色） |
| 28 | POST | `/api/v1/admin/users/{id}/reset-password` | **超管** | 重置密码，返回**一次性临时密码**并让旧令牌失效 |
| 29 | POST | `/api/v1/admin/users/{id}/status` | **超管** | 启用 / 禁用；禁用会让该账号令牌立即失效 |
| 30 | GET | `/api/v1/admin/audit-logs` | **超管** | 审计日志（只读，可按 action / actor / result 筛选） |
| 31 | POST | `/api/v1/settings/api-key/rotate` | **超管** | 轮换外部接口 Key：旧 Key 立即失效，新 Key 只在响应里出现一次 |
| 32 | GET | `/api/v1/permissions` | 需登录 | 权限点目录（前端据此渲染勾选矩阵，不硬编码 key） |
| 33 | GET | `/api/v1/roles` | 需登录 | 角色列表（含各自权限点；非超管看不到 `super-admin`） |
| 34 | POST | `/api/v1/roles` | **超管** | 新建自定义角色 |
| 35 | GET | `/api/v1/roles/{key}` | 需登录 | 角色详情（编辑页回填） |
| 36 | PUT | `/api/v1/roles/{key}` | **超管** | 编辑角色名称 / 描述 / 权限；**超管角色权限不可改** |
| 37 | GET | `/`、`/upload`、`/d/{id}` | 公开 | 静态资源 + SPA fallback |

> 标「需登录」的接口要带 `Authorization: Bearer <token>`，缺失或过期返回 401；带了的接口也一律按 `project` 隔离，串项目的包互相看不到。

```bash
# 造两个自测用小包（内容任意、扩展名正确即可，不新增脚本文件）
printf 'fake apk for smoke test' > /tmp/demo.apk
printf 'fake ipa for smoke test' > /tmp/demo.ipa

# ⓪ 登录换令牌（默认 admin / admin123456）
TOKEN=$(curl -sS -X POST http://localhost:10088/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin123456"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["token"])')

# ⓪' 项目列表（存量数据都在 key=default 的「默认项目」里）
curl -sS -H "Authorization: Bearer $TOKEN" http://localhost:10088/api/v1/projects

# ① 暂存：选文件时由前端自动触发；返回 stageToken / parsed / warnings
curl -sS -H "Authorization: Bearer $TOKEN" \
  -F "file=@/tmp/demo.apk" http://localhost:10088/api/v1/builds/stage

# ② 提交发布 commit：只发 JSON（恰好 9 字段），不带文件、不带 metaSource；把 <TOKEN> 换成上一步的 stageToken
curl -sS -X POST http://localhost:10088/api/v1/builds \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"project":"default","stageToken":"<TOKEN>","environment":"test","platform":"android","appName":"商城 App","packageId":"com.example.shop","version":"1.2.0","buildNumber":"3","changelog":"修复登录态过期问题"}'

# ③ 列表（project 必填；platform 省略或 all 表示不筛）
curl -sS -H "Authorization: Bearer $TOKEN" \
  'http://localhost:10088/api/v1/builds?project=default&environment=test&platform=android'

# ④ 免登录分享详情（SHARE_TOKEN 从服务端返回的 downloadPageUrl 中取得）
curl -sS -H "X-Share-Token: $SHARE_TOKEN" http://localhost:10088/api/v1/public/builds/<ID>

# ⑤ 免登录原生下载（直接使用返回的 downloadFileUrl，完整保留查询参数）
curl -OJ "$DOWNLOAD_FILE_URL"

# ⑥ 健康检查
curl -sS http://localhost:10088/healthz

# ⑦ SPA fallback：直接访问下载页拿到 index.html
curl -sS http://localhost:10088/d/<ID> | head -5
```

自造的小包不是合法 APK / IPA，stage 会返回 `warnings`（解析失败），但**上传仍会成功** —— 这正好用来验证 P0-9 的失败降级。

返回的三个地址字段：

| 字段 | 形态 | 用途 |
| --- | --- | --- |
| `downloadPagePath` | 相对 `/d/{id}` | **仅**用于前端 react-router 内部跳转 |
| `downloadPageUrl` | 绝对 | 二维码内容、复制地址 |
| `downloadFileUrl` | 绝对 | 下载按钮 |

---

## 6. 关键设计（读代码前先看这十三条）

1. **单人 / 团队分表隔离**：业务数据分两个 PostgreSQL schema——`team` 与 `solo`，各持一套同构的 `projects` / `builds` / `audit_logs`。分表的目的是让「单人只看自己」成为**结构事实**而不是纪律要求：越权在表边界上就不可能发生，不需要每条 SQL 都记得拼 owner 条件。全局事实（`users` / `roles` / `people` / `teams` / `system_settings` 与 `project_members` / `team_projects` 等关联表）留在 `public`，因为它们跨模式共享，且登录发生在选模式之前。唯一性按分区各定：`team.projects(key)` 全平台唯一，`solo.projects(owner_user_id, key)` 只在本人空间内唯一（两个人各自拥有一个 `web` 互不冲突）。代码层的唯一投影是 `repository.Partitioned[T]`：装配层建两份同构仓储，上层用 `.For(mode)` 取，业务代码里不出现 `if team / else solo`；分享链路无账号登录态，但先验证独立分享凭证，再用 `.For(mode)` 定位绑定分区和版本。`schema.sql` 幂等并 `//go:embed` 进二进制，启动时执行；存量 `public` 表由 `ALTER TABLE ... SET SCHEMA team` 迁入（外键引用表 OID，不会断）。**部署约束**：DDL 必须由应用自己的数据库账号执行，属主决定访问权。
2. **stage 上传对用户透明，不是两步发布**：选文件那一刻自动上传一次到 `storage/tmp/{token}.{ext}` 并同步解析（5s 超时、manifest/plist ≤5MB、zip 条目上限），前端只把解析值填进**未被用户改动**的字段；用户全程只点一次「上传并发布」，commit 请求是纯 JSON（恰好 9 个字段，含 `project`，不含文件、不含 `metaSource`），服务端不再调用解析器、也不覆盖任何用户值，只 rename + 写库 + 生成地址；`metaSource` 由服务端按「是否发生空值兜底」派生（`fallback` / `parsed`）后入库，前端无法伪造。token 有效期 1 小时、用后即失效，启动时清空 tmp、每次 stage 前清理 1 小时孤儿。
3. **三个 URL 由 `service/build_service.go` 的 `buildURLs(id)` 唯一生成**，`Create` / `List` / `Get` 统一调用；repository 的 `rowToBuild` 把这三个字段留空，绝不入库（换域名不用刷数据）。前端零拼接。
4. **下载次数在 `io.Copy` 成功且写出字节 == `Content-Length` 之后**才 `UPDATE ... +1`；404 / 路径非法 / 文件缺失 / 客户端中断 / copy 出错 / 字节不足一律不计数；计数是 2s 短超时的旁路，失败只记日志、绝不阻断已完成的下载。第一版不做强去重（按钮点击后禁用 3s）、不做 Range / 断点续传。
5. **契约是单一来源**：环境 / 平台枚举只写在 `api/openapi.yaml`，前端用生成类型，后端用 `go test ./...` 的契约一致性测试比对（把 yaml 里的 `pre` 改成 `pre2` 测试必须失败）；数据库 `environment` 列只 `NOT NULL`、**没有 CHECK**，因为枚举的真相在契约里，不该有两份。
6. **账号登录与免登录分享分离**：账号登录令牌用于内部操作；`/d/{id}?shareToken=...` 用独立授权访问一个版本，接收者不需要账号。详情与文件每次都验证签名、分区、绑定包及授权状态；版本列表先验证来源分享，再限定同项目、同应用、同上传者，只返回已有有效分享，各目标使用自己的凭证。默认长期有效，过期、撤销和 revision 已在服务端校验，管理入口后续再做。实现与流程见 [下载分享 MVP](docs/14-SHARE-TOKEN-MVP.md)。
7. **Webhook 与外部接口都是旁路**：`BuildService.Create` 成功后只调一次 `BuildNotifier`（实现是 `service/webhook`，异步投递、失败只记日志），所以第三方挂了也不会让发布失败；`/open/builds` 用 `X-API-Key` 鉴权、与管理端登录态互不影响，两者都复用同一份 `handler.ReadUpload` 与 `handler.ValidateCommitInput`，不另写一套校验。平台集成支持通用 JSON 与飞书机器人；飞书卡片按钮完整保留单版本分享凭证，HTTP 成功且业务码为 0 才确认投递成功。配置与验收见 [飞书发布通知接入](docs/15-FEISHU-WEBHOOK.md)。
8. **项目是一级归属，不是筛选条件**：`builds.project_id` 必填 + 外键 + `(project_id, environment, platform, created_at DESC)` 索引，所有列表查询都先按项目收敛；前端路由把项目做成**路径前缀** `/p/:key/*`（环境仍留在查询参数），所以切项目只是换前缀、**不丢板块**（在「应用包」切项目仍留在「应用包」）；`/settings` 与 `/d/:id` 不带项目前缀，前者是平台级配置、后者靠全局唯一的 id 定位。存量数据整体归入 `key=default` 的「默认项目」，老链接靠前端重定向兜住。项目不存在（key 被删/改名/拼错）由 `components/ProjectGate.tsx` 在骨架层统一拦成空态卡片，不放任各页面弹红色 400。

9. **基础资料是纯主数据，不含权限**：人员 / 团队 / 项目三张主表 + 三张多对多关联表（`team_members` / `project_members` / `team_projects`），关联由两侧各自维护自己那一半（人员页改 `teamIds`、团队页改 `personIds`，落到同一张表）；编辑一律**全量覆盖**（事务内先清后写），前端每次提交完整多选结果。**列表接口直接返回关联的 `{id,name}`（`RelationRef`）**，因此人员管理只调 `/people`、团队管理只调 `/teams`、项目管理只调 `/projects`——页面不会为了把 ID 显示成名字再去拉别的列表（这是「一个页面一个列表接口」的硬约束）。`people.username` 关联登录账号，`GET /api/v1/me` 用它把「我关联的第一个项目」投影给前端做列表页默认值——**只做默认值，不做权限**；谁能看哪个项目仍由 P2 的角色体系决定。

10. **UI 组件统一使用 `@seakoi/console-kit` 根入口命名导出**：Table / Drawer / Modal / Select / Button / Input / TextArea / Checkbox / Radio / Upload / ConfigProvider / zhCN / QRCode 与表格、主题类型均已切换；业务代码不直接 import antd 或 qrcode.react。`0.5.16` 的 `./antd` 子入口缺 JS，禁止使用；根入口已由 Vite `manualChunks` 独立拆包。表格、抽屉、确认框、下拉集中封装，基础输入/按钮通过 `Controls.tsx` 适配；`Upload.Dragger` 禁止自动上传，仍由页面调用一次 stage。主题通过 `ConfigProvider` 的 token 对齐现有青色设计系统（主色 `#0369a1`、圆角 8/12、控件高 34px），表格外观再由 `.data-table` 的一段 CSS 覆盖到与手写表格一致。组件库独立缓存（具体体积以每次构建输出为准）；`vite.config.ts` 用 `manualChunks` 把 antd 与 console-kit 分别拆包，业务更新不会让它们重新下载。**列表分页是服务端分页**：人员 / 团队 / 项目三个列表支持 `?page=&pageSize=`（默认 10 条、上限 200，`pageSize=0` 表示不分页取全量），响应平铺 `{items, total, page, pageSize}`；`DataTable` 传 `serverPagination` 即切到受控分页，不传则退化为前端分页（如应用包列表）。项目详情用**抽屉**展示（`components/DetailDrawer.tsx`，统一使用 console-kit 的 Drawer），点项目名即可展开，不再单开详情页。

11. **账号在库里，令牌带版本号**：`users` 表是唯一真相（环境变量 `ADMIN_USERNAME/PASSWORD` 只在 users 为空时做**首次种子**，之后改环境变量不再影响已有账号）。密码存 PBKDF2-HMAC-SHA256（10 万次迭代，标准库 `crypto/pbkdf2`，无外部依赖）。令牌载荷是 `账号|版本|到期.签名`，校验时比对库里的 `token_version`——**改密码 / 禁用账号只需把版本 +1，该账号已发出的令牌立即失效**，不需要会话表。超级管理员是平台角色：`/me` 返回 `isSuperAdmin`，前端据此隐藏「超级管理员」整组菜单，但真正的边界是 `/api/v1/admin/*` 上的 `RequireSuperAdmin`（非超管一律 403）；三条自锁红线写在 `service/user`：不能禁用自己、不能取消自己的超管身份、不能弄丢最后一个超管。**审计是只追加表**（`audit_logs` 无 UPDATE/DELETE 接口）：账号变更与发布包会记录「谁 / 何时 / 对什么 / 结果 / IP / detail JSON」；`actor_name` 冗余存姓名，账号改名或删除后历史仍可读；写审计失败只记日志、绝不影响业务操作。**密钥与平台配置整体收回超管**：`/api/v1/settings` 三个接口（含轮换）都过 `RequireSuperAdmin`，普通账号一律 403；轮换接口用数据库 `gen_random_uuid()` 生成 32 位 Key，旧 Key 立即失效，新 Key 只在响应里出现一次。

12. **登录模式是会话属性，不是前端开关**：登录页默认单人模式，团队模式可选；模式写进 HMAC 令牌载荷（`账号|版本|到期|模式.签名`）并随 `/me` 的会话存储投影到菜单。单人模式没有角色区分，普通账号即可完整使用概览 / 应用包 / 应用（含上传发布）与项目管理（含自建项目）；但数据落在 `solo` 分区、只看到自己的；管理员额外保留账号管理、审计与平台集成；团队模式维持完整平台视图。服务端按模式路由到各自分区，前端隐藏菜单只是体验，不构成安全边界。外部 API 上传没有登录态，固定按团队匿名身份处理，永不进单人空间。
13. **权限点目录在代码里，角色只存"拥有哪些"**：`contract.Permissions` 是唯一目录（12 个点，按应用包 / 项目 / 基础资料 / 平台设置 / 超级管理分组），数据库 `role_permissions` 只存角色与权限点的连线——新增权限点不用改表，也不会出现"代码有、库里没有"的漂移。角色管理挂在「基础资料」下（读对全员开放、**写仅超管**）；5 个内置角色在启动时播种，**只在角色缺失时写默认权限**，页面上改过就不会被重启覆盖。`super-admin` 角色的权限恒为全量且不可改，否则会出现「没人能改角色」的死锁。**人员可以分配一个角色**（`people.role_key`，外键到 `roles(key)`，删角色时自动置空），列表与表单直接显示角色名——这是「角色定义」走向「角色生效」的第一步；真正的接口判定（`RequirePermission`）已落地：超管 → 项目角色 → 人员角色 → 默认读取基线。**默认读取能力**：所有已登录账号都拥有 `build.read` 与 `project.read`，因此概览、应用包、应用三个菜单始终可见可用；上传/发布仍需 `build.write`。项目角色只覆盖项目内判定，平台接口刻意忽略 `?project=`，防止借项目角色绕过平台权限；角色授予（人员/项目的角色字段）仅超管可改，避免普通编辑者自提权。

**多板块 / push 预留**：只体现在三段式 Layout + 一个导航数组（总览 / 分发 / 基础资料 / 平台四组）、`/d/:id` 路由命名空间永不改动、`/api/v1` 前缀、`BuildService.Create()` 这个唯一「上传完成」时点、装配层可注入。没有任何占位菜单、空页面、事件类型、outbox 表或 `TODO`。

---

## 7. 部署

### ① 本地开发
见第 3 节：`docker compose up -d db` → `cp .env.example .env` → `make dev`。

### ② 单机 / 内网（systemd）
```bash
make build
sudo mkdir -p /opt/dandelion
sudo cp -R bin deploy .env /opt/dandelion/
sudo cp deploy/dandelion.service /etc/systemd/system/dandelion.service
sudo systemctl daemon-reload && sudo systemctl enable --now dandelion
```
`WorkingDirectory=/opt/dandelion`，`EnvironmentFile` 必须包含 `PUBLIC_BASE_URL` 与 `DATABASE_URL`，服务在 `postgresql.service` 之后启动，崩溃自动拉起。Go 单二进制自带静态资源与 SPA fallback，只开 10088 即可。

### ③ 容器（三服务）
```bash
cp .env.example .env       # 必改 PUBLIC_BASE_URL 为对外 https 域名，并把 AUTH_SECRET 换成 openssl rand -hex 32
make up                    # = docker compose -f deploy/docker-compose.yml up -d --build
make down
```
`db` 用 `postgres:16-alpine` + `pgdata` 卷 + initdb 执行 `server/internal/repository/schema.sql`；`server` 等 db healthy 后启动，宿主机 `storage/` 挂到 `/app/storage`（删容器重建不丢包）；`nginx` 为可选 profile（`--profile with-nginx`），直出 `server/public/` 并做 `try_files $uri /index.html` fallback，`client_max_body_size 210m`、反代超时 300s。HTTPS 终止位在 `deploy/nginx.conf` 里留了注释。

**DDL 单一来源**：建表语句只有 `server/internal/repository/schema.sql` 一份，由 `//go:embed` 打进二进制，进程启动时幂等执行 —— **不依赖当前工作目录**，`cd server && go run ./cmd/server` 与仓库根直接运行效果一致；容器里同一份文件再挂给 PG initdb 作为双保险。

### 规模化（未来）
对象存储 + CDN、读副本与连接池；前提是文件与元数据都外置，第一版单进程带本地卷，不能直接多副本。

---

## 8. 备份与恢复

在同一个停写窗口内：

```bash
pg_dump -Fc -d "$DATABASE_URL" > backup_$(date +%F).dump
tar -czf uploads_$(date +%F).tar.gz -C storage uploads
# 恢复
pg_restore --clean --if-exists -d "$DATABASE_URL" backup_YYYY-MM-DD.dump
tar -xzf uploads_YYYY-MM-DD.tar.gz -C storage
```

`storage/tmp/` 是纯暂存态，不备份；容器部署时 `pgdata` 卷与 `storage/` 目录都要纳入备份计划。

---

## 9. iOS 第一版限制与 P1 前置条件

**第一版只提供 `.ipa` 文件下载，不能像蒲公英那样在手机浏览器点一下直接安装。** 下载页对 iOS 包会明确提示这一点。若后续勾选 P1-4（iOS 直接安装），需要先满足 6 项前置条件：

1. 公网可访问的 **HTTPS 域名 + 有效 CA 证书**（`itms-services://` 要求 https，内网自签证书手机不信任）。
2. 生成并托管符合 Apple 规范的 `manifest.plist`，其中包含IPA 地址、bundle id、版本号、应用名与图标地址。
3. 明确的签名方式：企业签名（Apple Developer Enterprise Program）或 Ad Hoc 描述文件，并保证设备 UDID 已登记。
4. IPA 的 `CFBundleIdentifier` / `CFBundleVersion` 与 plist 完全一致，否则安装会在校验阶段失败。
5. 下载页新增 iOS 专用安装入口与服务端 plist 生成/托管接口（第一版两者都没有，也不预留）。
6. 手机网络能解析并访问该域名（内网 DNS / 公网 DNS + 防火墙放行）；`PUBLIC_BASE_URL` 需一并切换成该 HTTPS 域名，已转发的旧二维码会失效。

---

## 10. 常见问题

| 现象 | 排查 |
| --- | --- |
| 上传大包返回 413 | 先看 Nginx `client_max_body_size 210m`，再看 `MAX_UPLOAD_MB`（不得超过 200）与契约 `x-constants.maxUploadMB` |
| 操作时提示「登录状态已失效」 | 令牌 12 小时过期，或服务端换了 `AUTH_SECRET`（换密钥会让全部旧令牌失效）；重新登录即可 |
| 手机扫码打不开 / 二维码区显示「基址不可被手机访问」 | 检查 `PUBLIC_BASE_URL` 是否是手机可达的绝对地址；本地开发要填局域网 IP + `:5173`，不能填 localhost 或 `:10088` |
| 重启后包不见了 | 检查 `storage/` 卷挂载与 `DATABASE_URL` 是否指向同一个 PostgreSQL；DB 记录与 uploads 文件必须成对存在 |
| 提交提示「暂存文件已失效，请重新选择文件」 | token 超过 1 小时、服务重启清空了 tmp、或已用过；重新选一次文件即可 |
| 改了后端代码，接口却返回「接口不存在」 | 10088 上还是上一个 `dev-server` 进程（Go 没有热重载，替换二进制不影响已运行的进程）。重启 `make dev` 即可 |
| 表单没有自动带出包名/版本 | 包可能损坏或是加固包，解析失败属预期降级，`warnings` 会提示，手填后照常上传 |
| `go test ./...` 失败且提示枚举不一致 | 有人改了 `api/openapi.yaml` 却没同步 Go 常量，或反之；契约测试就是用来卡住这种漂移的 |
| `/d/{id}` 刷新 404 | 单机检查 Go 的 SPA fallback，容器检查 Nginx `try_files $uri /index.html`；两处都要有 |

---

## 11. 自检命令速查

```bash
make gen:api                        # 契约 → web/src/api/schema.d.ts
make test                           # Go 契约一致性测试
make lint                           # 前端 ESLint + Stylelint 静态检查
npm --prefix web run build          # 前端类型检查 + 构建到 server/public/
make build                          # 前端 → 单二进制（顺序不可颠倒）
GOTOOLCHAIN=local CGO_ENABLED=0 go -C server build ./...   # 无 cgo 校验
```
