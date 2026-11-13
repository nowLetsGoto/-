# 07 · 独立验收用例清单与执行结果｜Dandelion 第一版

> 作者：严过关（QA）｜日期：2026-11-13｜被测版本：**冻结版**（三项修复已合入：非 UUID→404、MetaSource 两值、DDL embed）
> 依据：`04-DELTA-PRD.md`（D）、`02-ARCHITECTURE-v2.md`（A）、`03-TASKS-v2.md`（T）、`06-FOLDER-LAYOUT.md`（F）
> 纪律：本清单与结果均由 QA **独立构造**（curl 手工两步 + jq 断言 + sha256/cmp 比对 + psql 直查 + Chrome headless DOM 断言），不重跑工程师脚本。
> 执行环境：PostgreSQL 17.5（本机 127.0.0.1:5432）；`STORAGE_DIR=/tmp/dandelion-qa-storage`（**未写仓库 `storage/`**）；服务端口 18080/18081；Docker 未安装、无真机、无真实 APK 样本。

## 0. 结果总览

| 分组 | 用例数 | PASS | PASS*（替代验证/部分覆盖） | FAIL |
| --- | --- | --- | --- | --- |
| A · API 契约与上传主链路 | 11 | 10 | 1（A-11 含轻微偏差） | 0 |
| B · 三项新增功能 | 7 | 5 | 2（B-01、B-04/B-05 受环境限制） | 0 |
| C · 多环境/多平台与持久化 | 4 | 4 | 0 | 0 |
| D · 前端页面与状态 | 7 | 6 | 1（D-07 交互流程部分覆盖） | 0 |
| E · 结构与负面清单 | 6+1 | 7 | 0 | 0 |
| F · 目录规范自查（§9） | 5 | 5 | 0 | 0 |
| **合计** | **40 + D13** | **37** | **4** | **0（硬失败）** |

- **智能路由判定：`NoOne`（无阻塞性源码 Bug）**；另报 2 项**契约不符**（D-1 405 缺 `Allow`、D-2 未知字段未拒绝，team-lead 已裁决由工程师修复，进入 Round 2 定向回归）+ 3 项已记录偏差（D-3/D-4 已由规范承认，D-5 可接受）。
- 已知未覆盖：Android 正向解析（无合法 APK 样本）、真机扫码、Docker/Nginx、pg_dump/restore、`make dev` 前台模式。
- ⚠️ **本文件含 1 处已更正的历史误判（D-6 文档漂移），更正记录见 §11**；保留原文以免后人重复误判。

---

## 1. A 组 · API 契约与上传主链路（11）

| 编号 | 前置 | 操作 | 期望 | 依据 | 实机 | 结果 |
| --- | --- | --- | --- | --- | --- | --- |
| A-01 | 服务已起；自造合法 `.ipa` | `curl -F file=@Test.ipa /api/v1/builds/stage` | 200、code=0、stageToken 为 UUID v4、parsed 4 字段正确、warnings=[] | A §6.5① / D P0-9 | Y | **PASS**：`parsed={测试应用, com.demo.test, 1.2.3, 45}`、`warnings=[]` |
| A-02 | 同上 | 2KB 随机字节改名 `bad.apk` 走 stage | 恒 200、parsed 空或部分、warnings 为中文原因 | A §6.5① / T §4-6 | Y | **PASS**：200 + `["安装包结构无法识别，请手动填写应用名、包名与版本号"]` |
| A-03 | 同上 | ①缺 `file` 字段 ②`.txt`/`.zip` ③201MiB 包 | ①400 ②400 ③413「文件超过 200 MB 上限」，tmp 无残留 | A §6.1 | Y | **PASS**：400/400/400/413；失败请求 tmp 零残留 |
| A-04 | 已 stage | 提交 **恰好 8 字段**，`appName` 故意≠parsed | 201、appName=用户值、3 个 URL 同源绝对、metaSource=parsed、downloadCount=0、createdAt ISO8601 UTC | A §6.3/§6.4/§6.6 | Y | **PASS**：`appName=用户改名的测试包`；`pageUrl == PUBLIC_BASE_URL + pagePath`；`createdAt=2026-11-13T10:20:06.89854Z` |
| A-05 | 已 stage（损坏包） | 请求体额外塞 `file` / `metaSource:"parsed"` / `touchedFields`（共 11 字段） | 期望 400；底线：201 但三者完全不生效 | A §6.3 / T §6.1 | Y | **PASS（底线）**：201，`appName` 保持用户值、`metaSource=fallback`（客户端传值被忽略）、`fileSize=2048`（仍是 stage 那份）→ 见偏差 D-2 |
| A-06 | 已 stage | ①伪造 UUID token ②同 token 重放 ③ipa 配 `platform:android` ④`environment:dev` ⑤非 JSON body ⑥前三次失败后同 token 再提交 | 均 400 且文案精确；平台不一致后 token 仍可用 | A §6.5② | Y | **PASS**：四种 400 文案正确；⑥201（token 未被误消费） |
| A-07 | 有多环境数据 | `?environment=test/pre`、缺参、`environment=prod`、`platform=ios/all/bogus`、连传两包看顺序 | test 有 / pre `total:0, items:[]`；缺参与 prod 400；ios 排除 apk；all 不筛且 `platform=all`；bogus 400；createdAt 严格倒序 | A §6.5③ | Y | **PASS**：见 §5 证据 |
| A-08 | 已有包 | 详情正常 id、随机 UUID、**`abc`**、**`not-a-uuid-12345`**、**`abc/file`** | 200 / 404 / 三处非 UUID 均 404，**不得 500** | A §6.5④ + 本轮修复裁决 | Y | **PASS**：三入口全部 404「包不存在或已被移除」，无 500 |
| A-09 | 有中文名 ipa + apk | 下载并比对字节与 5 个响应头 | sha256 一致；Android `application/vnd.android.package-archive`、iOS `application/octet-stream`；CD 双重编码；CL/CC/XCTO 正确 | A §6.5⑤ / T §5-10 | Y | **PASS**：`filename="_____.ipa"; filename*=UTF-8''%E4%B8%AD%E6%96%87...`；`cmp` 字节一致 |
| A-10 | 服务已起 | `GET /healthz` | 200、code=0、4 个字段齐全 | A §6.5⑥ | Y | **PASS**：`{status:ok, uptime:8, dbOk:true, storageWritable:true}` |
| A-11 | 服务已起 | `PUT /api/v1/builds`、`GET /api/v1/nope`、`GET /d/{id}`、`/upload`、`/xyz` | 405+Allow、404 JSON（非 index.html）、SPA fallback 200 text/html | A §6.1 / T §4-9 | Y | **PASS（含偏差 D-1）**：405 文案正确但 **`Allow` 头为空**；`/api/v1/nope` 返回 404 JSON；三页均 200 text/html |

## 2. B 组 · 三项新增功能（7）

| 编号 | 前置 | 操作 | 期望 | 依据 | 实机 | 结果 |
| --- | --- | --- | --- | --- | --- | --- |
| B-01 | 自造合法 IPA（XML plist）；Android 无样本 | stage 后比对 4 字段 | iOS 4 字段逐字一致；Android 取 label/package/versionName/versionCode | D §2.2② / A §6.6 | Y* | **PASS（仅 iOS 正向）**：Android 正向**未覆盖**（本机及仓库无合法 `.apk`，不以损坏包冒充） |
| B-02 | 损坏包 | stage 得 warnings → 表单全留空 → commit | 上传成功，appName=文件名、packageId=appName、version/buildNumber=`-`、changelog=`""` | D §2.2② | Y | **PASS**：`兜底测试包 / 兜底测试包 / - / -`；UI 提示文案静态核对一致 |
| B-03 | 已有多条记录 | ①全字段填写 ②任一留空 ③请求带 `metaSource:"parsed"` 但 appName 空 | ①parsed ②fallback ③仍 fallback；全库只出现 parsed/fallback | A §4.2 / D §2.2 | Y | **PASS**：`select distinct meta_source` = `fallback,parsed`（无 manual） |
| B-04 | 有包 | 下载页 DOM 断言；再用 `PUBLIC_BASE_URL=http://localhost:18081` 起第二实例 | 渲染 1 个 QR svg 且无兜底提示；localhost 基址时**不渲染码**、显示中文提示 + 可复制地址 | D §2.2① / A §6.4、§9.4 | Y* | **PASS（内容正确性以代码单点+DOM 支撑；真机扫码不可得）**：正常实例 `svg=1` 且无提示；localhost 实例 `svg=0` 且提示「当前对外基址不可被手机访问…」 |
| B-05 | 有包 | 比较二维码 value / 复制按钮 / 下载 href 的数据来源 | 三者同源于 `downloadPageUrl` / `downloadFileUrl`，前端零拼接 | A §6.4 | Y | **PASS（剪贴板写入不可拦截）**：`QRCodeSVG value={build.downloadPageUrl}`、复制按钮同字段、下载 href 实测 `= http://192.168.6.25:18080/api/v1/builds/{id}/file` |
| B-06 | 新包 count=0 | 完整下载 1 次后查详情与列表 | 两处均为 1 | D P0-10 / A §6.7 | Y | **PASS**：详情=1、列表=1、DB=1；重启后再下载 → 2 |
| B-07 | 10MB 包 + 测试库 | ①`--limit-rate 100k --max-time 1` 中断 ②下载不存在 id ③行锁注入计数故障后完整下载 | ①计数不变 ②404 且行数/计数和不变 ③下载 200 且字节完整、计数不变 | A §6.7 / §5-11 | Y | **PASS**：①count=0 且日志「下载未完成，不计数 … socket is not connected」②前后 `5/2` 不变 ③行锁期内完整下载 10,485,760B、计数 0，日志「下载计数旁路失败（不影响本次下载）… context deadline exceeded」 |

## 3. C 组 · 多环境 / 多平台与持久化（4）

| 编号 | 前置 | 操作 | 期望 | 依据 | 实机 | 结果 |
| --- | --- | --- | --- | --- | --- | --- |
| C-01 | test/hotfix/pre/prd 各有数据 | 逐环境查 + 平台筛选 | test=3、hotfix=1、pre=0、prd=1；`hotfix+android`=0；`test+ios` 仅 ipa | D P0-3/P0-4 | Y | **PASS** |
| C-02 | 同 env+platform 已有包 | 同环境同平台再传一包 | 两行两条、两文件、新的在上，旧包不受影响 | T §5-6 | Y | **PASS**：`test/android` 行数=2、目录文件数=2 |
| C-03 | 已 stage 未 commit | 看 tmp → kill → 同库同目录重启 → 复测 | tmp 有 `{token}.apk|.ipa` + `{token}.name`；重启后 tmp 清空、包仍可查可下载 | T T03 / A §6.6 | Y | **PASS**：重启前 2 文件、重启后 0；行数 5 不变；详情 200；下载字节一致 |
| C-04 | 有可下载包 | 移走 uploads 下文件后下载 | 404 且计数不增；详情仍 200 | A §6.7 | Y | **PASS**：404「包不存在或已被移除」，count 0→0，详情 200（已恢复文件） |

## 4. D 组 · 前端页面与状态（7）

| 编号 | 前置 | 操作 | 期望 | 依据 | 实机 | 结果 |
| --- | --- | --- | --- | --- | --- | --- |
| D-01 | 前端已 embed | Chrome headless `--dump-dom` 访问 `/`、`/upload`、`/d/{id}`、`/xyz` | 三页渲染正常；`/d/{id}` 刷新不 404；未知路径回 `/` | A §9.1 | Y | **PASS**：列表「共 3 个包」、上传页表单齐全、下载页渲染应用名/包名/大小；`/d/{id}` 200 text/html |
| D-02 | 有数据 | 访问 `?environment=test&platform=ios` | 只显示 ipa 包，筛选态高亮可读，刷新保持 | A §9.1 | Y | **PASS**：仅「第二包」，active=[测试, iOS] |
| D-03 | ①空环境 ②接口 500 | 访问 `?environment=pre`；`dropdb --force` 后访问列表 | 空态「…暂无包 + 去上传」；错误态中文文案 + 重试，无空白页 | A §9.5 | Y | **PASS**：空态文案正确；500 时 DOM 显示「服务内部错误，请稍后重试 + 重试」，标题与筛选仍在 |
| D-04 | 前端 | DOM 断言主导航与列表页行操作 | 导航**仅 1 项**「应用包」；列表页无二维码元素；行操作只有「下载页」 | D §2.2① / T T04-4 | Y | **PASS**：`nav-link` 数量=1、文案=应用包；列表页 `svg=0`；7 列表头含「下载次数」 |
| D-05 | 前端 | 375px / 640px 视口 DOM 与 CSS 断点 | 单列可读、表格可横向滚动、二维码与按钮不裁切 | A §9.5 | Y | **PASS（静态+DOM；像素级视觉无眼睛通道，截图已留档人工确认）**：`@media (max-width:640px)` 1 处、`.table-scroll{overflow-x:auto}`、`.qrcode-box` 居中 |
| D-06 | 源码 | grep `window.location.origin` 与 `downloadPagePath` 用法 | 无 origin 拼接；`downloadPagePath` 仅用于 `navigate()` | T T04 / §6.3 | N | **PASS**：origin 仅出现在边界注释；`downloadPagePath` 仅 ListPage:162、UploadPage:185 两处 `navigate()` |
| D-07 | 后端可用 | 上传页交互（选文件自动 stage / 解析中 / 字段可编辑 / 提交 8 字段 / token 失效回到选文件） | 文件全程只传一次；手改值原样入库；失效有中文提示 | A §9.3 / T §4-6/§4-8 | Y | **PASS（部分覆盖）**：headless 无法驱动文件选择，未做端到端 UI 交互；以 A-04/A-05/A-06 接口行为 + 源码单点（`handleSelect`→`stageBuild` 一次、`payload` 恰好 8 字段、400+「暂存」分支清空文件）替代 |

## 5. E 组 · 结构与负面清单（6 + D13）

| 编号 | 操作（只读） | 期望 | 结果 |
| --- | --- | --- | --- |
| E-01 | `grep -rn 'SELECT \|INSERT INTO\|UPDATE builds\|DELETE FROM' server/` | 仅在 `repository/build_repository.go` | **PASS**：5 处命中全部在该文件（61/69/95/125/140 行） |
| E-02 | grep `storage/uploads`、`PUBLIC_BASE_URL`、`os.Getenv` | 路径只在 `storage/paths.go`；基址只在 config+service；唯一 `os.Getenv` 在 config | **PASS**：`server/internal` 内 `PUBLIC_BASE_URL` 仅 config.go（3 处）+ service 注释 1 处；`os.Getenv` 仅 config.go:48；路径拼装仅 paths.go |
| E-03 | 分层反查（handler SQL / service `net/http` / parser `os`·`net` / repository→service） | 全部 0 越界 | **PASS（含 2 处已记录偏差 D-3/D-4）**：handler 无 SQL、service 无 `net/http`、parser 无 `os`/`net`/repository、repository 不引 service；但 handler 直接 `os.OpenFile`/`os.Open`，health handler 直连 repository |
| E-04 | 新库执行 `server/internal/repository/schema.sql` 两次 + `\d builds` | 单表 + 1 索引、无 CHECK、重复执行不报错 | **PASS**：`CREATE TABLE/INDEX` 两次均成功（第二次 NOTICE skipping）、`TABLES=1`、`CHECK=0`、列与 §4.1 一致 |
| E-05 | grep Publisher/EventBus/EventHandler/`/builds/parse`/TODO + find 空目录 + go.mod | 均无 | **PASS**：仅注释中的负面表述；无 `/builds/parse`、无 TODO/FIXME、无空目录（`storage/tmp` 为运行时目录）；直接依赖 5 个（chi/pgx/androidbinary/yaml/plist） |
| E-06 | grep `hotfix`；`go build`/`go vet`/`go test`；契约反向演示（/tmp 整仓副本） | 4 处命中；全绿；改 `pre→pre2` 必须 FAIL、MetaSource 加回 `manual` 必须 FAIL | **PASS**：命中 `api/openapi.yaml`、`contract.go`、`format.ts`、`schema.d.ts`；`CGO_ENABLED=0 go build` 成功、`go vet` 干净、`go test ./...` pass；两处反向演示均 `--- FAIL`（`Environment.enum=[test hotfix pre2 prd]…`、`TestMetaSourceEnumMatchesContract`） |
| **D13** | ①`deploy/` 无 `schema.sql` ②compose initdb 指向 `../server/internal/repository/schema.sql` ③该文件存在且 `//go:embed` ④启动后 `\dt` 有 `builds`、重复启动不报错 | 四项同时成立 | **PASS**：①`ls deploy/schema.sql` 不存在 ②compose:17 指向新路径 ③`//go:embed schema.sql`（repository:27）+ `EnsureSchema(ctx)`（repository:50）④首次启动自动建表、重启 3 次均正常（`TABLES=1`、`ROWS=5` 不变） |

## 6. F 组 · 目录规范自查（对照 06 §9，5）

| 编号 | §9 条目 | 检查方法 | 结果 |
| --- | --- | --- | --- |
| F-01 | §9-1 职责不越界 | grep handler→repository/parser、service→`http.`、parser→`os.`/`net/` | **PASS（偏差见 D-3/D-4）**：handler 不调 repository/parser（health 例外）、service 无 http、parser 纯函数 |
| F-02 | §9-2/3/4 前端四条硬约束 | pages 互不 import、components 无 `fetch(`、页面无 `'/api/v1'` 字面量 | **PASS**：仅 App.tsx import 三页；`fetch(` 仅 `api/client.ts:29`；路径字面量仅 `api/builds.ts` ROUTES（偏差 D-5） |
| F-03 | §9-8 生成物未手改 | `npx openapi-typescript … -o /tmp/schema.check.d.ts` 后 diff | **PASS**：`diff -q` 无差异（各 467 行） |
| F-04 | §9-9 无空目录/空文件/TODO | `find -type d -empty`、`-type f -empty`、`.gitkeep` 范围 | **PASS**：空目录仅 `storage/tmp`（运行时）+ `.git/**`；无空文件；8 个 `.gitkeep` 全在 `storage/uploads/**` |
| F-05 | §9-7/10 归属与注释 | 规范外目录、文件头注释、`wc -l` | **PASS**：无 `pages/updates`/`event`/`store`/`contract/urls.go`；文件头「职责/输入/输出/边界」齐全；最大手写文件 379 行（service），生成物 `schema.d.ts` 467 行除外 |

---

## 7. 偏差与建议（均不阻塞第一版）

| 编号 | 类型 | 描述 | 期望 vs 实际 | 建议 |
| --- | --- | --- | --- | --- |
| **D-1** | **契约不符（已裁决修复）** | 自定义 `r.MethodNotAllowed` 覆盖了 chi 默认处理器，未回填 `Allow` 头 | 期望（A §6.1 / T T03）`405 + Allow`；Round 1 实测 `405`，`Allow` 为空 | team-lead 裁决：确认为契约不符，**工程师修复中**；Round 2 复测 `PUT /builds` → `Allow: GET, POST`、`DELETE /builds/{id}` → `Allow: GET` |
| D-2 | **契约不符（已裁决修复）** | `CommitBuildRequest.additionalProperties: false` 只是文档级约束，Go `json.Decoder` 未启用 `DisallowUnknownFields`，多传的 `file`/`metaSource`/`touchedFields` 被静默忽略（Round 1 实测 201） | 契约写「拒绝」，Round 1 实测「忽略但不生效」 | team-lead 裁决：**工程师改用 `DisallowUnknownFields` 修复**，修后多传未知字段应返回 **400**（中文「未知字段」类文案）；Round 2 复测，且正常 8 字段仍须 201 |
| D-3 | 分层差异（**规范已承认，非缺陷**） | `handler` 直接 `os.OpenFile`（写 tmp）与 `os.Open`（下载）；`health_handler` 直接依赖 `repository` | 06 §3 原文曾写「handler 不直接 `os.Open`、不调 repository」 | team-lead 裁决：**规范侧调整**（handler 对「storage 生成 + service 校验过」的路径做流式 I/O、health 直接探活属正常做法），架构师已写入 06 §3 有条件例外；代码不返工 |
| D-4 | 分层差异（**规范已承认，非缺陷**） | `repository` 暴露 `DB() *sql.DB` 供 health 探活 | 06 §3 原文曾写「仓储层不反向依赖」 | 同上，已作为 06 §3 有条件例外承认；无需改造 |
| D-5 | 契约路径字面量 | `app/router.go` 与 `web/src/api/builds.ts` 仍手写 `"/builds/stage"`、`"/api/v1/builds"` | A §6.2「禁止手写路径字面量」 | team-lead 已裁决「可接受实现」，仅备注，不返工 |
| ~~D-6~~ | ~~文档漂移（非代码）~~ | ~~`docs/02` §3、`docs/03` T02、`docs/06` §1/§5-8 仍写 `deploy/schema.sql`~~ | **已核实为误判**：复核 `grep -rn 'deploy/schema\.sql' docs/` 在 `02/03/06` **0 命中**（仅本文件 D13/D-6 命中）；架构师在我读取之后已同步三份文档（`06` §1 已写「不含 DDL（已移至 `server/internal/repository/schema.sql`）」、`03` 增 DB-6 变更行、`02` ADR-06 与 §4.1 已改新路径） | **无文档漂移，无需动作**。保留此条以说明误判成因：QA 读取时点早于文档同步 |

## 8. 已知限制与残留风险

| 项 | 原因 | 替代验证 | 残留风险 |
| --- | --- | --- | --- |
| Android 正向解析 | 本机与仓库均无合法 `.apk`（`find` 无样本） | 仅验证降级路径（A-02/B-02） | 真实 APK 的 `applicationLabel`（`@string/…` 解 `resources.arsc`）与 `applicationId` 一致性未验证 |
| 真机扫码 | 无手机 | 二维码 DOM 渲染 + localhost 兜底负向 + 内容单点来源 | 相机识别率、二维码尺寸/容错在真机的效果未验证 |
| Docker / Nginx | `docker` 未安装 | 静态核对：`client_max_body_size 210m`、`proxy_read_timeout 300s`、`try_files $uri /index.html`、db healthcheck + `depends_on: service_healthy`、uploads 卷、tmp 不持久化、`CGO_ENABLED=0` | 容器内 `GO_ENV=production` + 非本机基址的 fail-fast、initdb 挂载、Nginx 层 413 未实跑 |
| pg_dump / pg_restore | 未在本轮范围 | — | 备份恢复脚本未验证 |
| `make dev` 前台模式 | 需要交互式前台进程 | 单独运行二进制验证通过 | Makefile 一行式 `& … & trap … wait` 组合未验证 |
| 剪贴板写入 | headless 无法拦截 clipboard | 代码单点消费 `downloadPageUrl` | 「复制」按钮在无权限浏览器下的降级文案未验证 |
| 像素级视觉（640/375px） | 无视觉通道 | DOM + CSS 断点 + 截图留档 | 极端字长导致的换行/截断未目视确认 |

## 9. 执行证据（关键命令）

```bash
# 构建与静态
cd server && CGO_ENABLED=0 go build -o /tmp/dandelion ./cmd/server
cd server && go vet ./... && go test -count=1 ./...
cd web && npx openapi-typescript ../api/openapi.yaml -o /tmp/schema.check.d.ts && diff -q /tmp/schema.check.d.ts src/api/schema.d.ts

# 契约反向演示（整仓 /tmp 副本，仓库零改动）
cp -R api server /tmp/qa-contract2/ && sed -i '' 's/pre, prd/pre2, prd/' /tmp/qa-contract2/api/openapi.yaml
cd /tmp/qa-contract2/server && go test -count=1 ./internal/contract/   # 期望 FAIL

# 起服务（独立库 + /tmp 存储）
createdb dandelion_qa
DATABASE_URL='postgres://linanan@127.0.0.1:5432/dandelion_qa?sslmode=disable' \
PUBLIC_BASE_URL='http://192.168.6.25:18080' PORT=18080 HOST=127.0.0.1 \
STORAGE_DIR=/tmp/dandelion-qa-storage GO_ENV=development /tmp/dandelion

# 两步上传（文件只传一次）
curl -F "file=@/tmp/qa/Test.ipa" $B/builds/stage | jq '.data'
curl -H 'Content-Type: application/json' -d @/tmp/qa/c1.json $B/builds | jq '.data'

# 字节一致 / 计数 / 非 UUID
cmp -s /tmp/qa/中文测试包.ipa /tmp/qa/dl_ios.bin
psql -d dandelion_qa -At -c "select download_count from builds where id='$ID'"
curl -w '%{http_code}' $B/builds/abc          # 404

# 前端 DOM 断言（无 playwright，用本机 Chrome headless）
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless=new --disable-gpu \
  --no-sandbox --virtual-time-budget=6000 --dump-dom "http://127.0.0.1:18080/?environment=test"
```

## 10. 收尾状态

- 测试库：`dandelion_qa`、`dandelion_qa_11112`、`dandelion_qa_ddl` 均已在测试结束后 `dropdb`；未触碰 `dandelion_smoke` 与 `datahub_*`。
- 服务进程：18080 / 18081 均已停止并确认端口空闲。
- 仓库：`git status --porcelain` 为空（除本文件 `docs/07-QA-TEST-PLAN.md` 为 QA 交付物）；`storage/` 仍为 README + 8 个 `.gitkeep`，**零写入**。
- 临时产物：`/tmp/qa*`、`/tmp/dandelion*`、`/tmp/schema.check.d.ts` 已删除。

## 11. 更正记录（Round 1 → Round 2）

| 项 | Round 1 记录 | 更正后事实 | 触发 |
| --- | --- | --- | --- |
| **D-6（误判）** | 称 `docs/02/03/06` 仍写 `deploy/schema.sql`，存在文档漂移 | **误判**。复核 `grep -rn 'deploy/schema\.sql' docs/`：`02`/`03`/`06` **0 命中**，仅本文件命中。三份文档已由架构师同步为新路径（`06` §1「不含 DDL（已移至 `server/internal/repository/schema.sql`）」、`03` 新增 DB-6、`02` ADR-06 + §4.1） | QA 读取文档的时点早于架构师同步；team-lead 复核指出 |
| **A-05 期望升级** | 「400 或至少不生效」→ Round 1 实测 201 且不生效，判 PASS（底线） | Round 2 改为**严格期望 400**（`DisallowUnknownFields` 生效后），中文「未知字段」类文案；正常 8 字段必须仍 201 | team-lead 裁决 D-2 为契约不符并修复 |
| **A-11 期望升级** | 「405 + Allow」→ Round 1 实测 `Allow` 为空，判 PASS（含偏差 D-1） | Round 2 严格复测：`PUT /api/v1/builds` → `405 + Allow: GET, POST`；`DELETE /api/v1/builds/{id}` → `405 + Allow: GET`，且响应仍是统一 JSON 外壳 | team-lead 裁决 D-1 为契约不符并修复 |
| **D-3 / D-4 定性** | 记为「分层偏差 / 技术债 P2」 | 改为「**规范已承认的条件例外，非缺陷**」：`06 §3` 已写明 handler 可对 storage 生成、service 校验过的路径做流式 I/O，health 可直接探活 | team-lead 裁决 + 架构师更新规范 |

> Round 2 定向回归范围（仅 4 项，不重跑 40 条）：①A-05 未知字段 400 + 正常 201 ②A-11 两处 `Allow` ③stage→commit→list→download 主链路 + `metaSource` 仍服务端派生 ④`go test ./...` + `openapi-typescript` 重新生成后 `diff` 为空。

## 12. Round 2 定向回归结果（D-1 / D-2 修复验证）

**判定：4/4 PASS，路由 `NoOne`。**

| # | 用例 | 结果 | 实际输出 |
| --- | --- | --- | --- |
| 1 | A-05 未知字段 → 400；正常 8 字段 → 201 | **PASS** | 带 `metaSource` / `file` / `touchedFields` 各一次，均 `HTTP=400`：`{"code":400,"data":null,"message":"请求体包含未知字段（仅允许 stageToken/environment/platform/appName/packageId/version/buildNumber/changelog）"}`；随后**同一 token** 提交 8 字段 → `HTTP=201`，`metaSource=parsed`、`downloadCount=0`（证明 400 未消耗 token） |
| 2 | A-11 405 + `Allow` | **PASS** | `PUT /api/v1/builds` → `405`，`Allow: GET, POST`，body `{"code":405,…,"message":"请求方法不支持"}`；`DELETE /api/v1/builds/{id}` → `405`，`Allow: GET`；附加探针：`PUT /builds/stage` → `Allow: POST`、`POST /healthz` → `Allow: GET`、`PUT /upload` → `Allow: GET`（均 405 + JSON 外壳） |
| 3 | 主链路回归 + metaSource 派生 | **PASS** | 详情 `appName=回归应用 / com.demo.r2 / 3.1.4 / 89`、`metaSource=parsed`；列表 `total:1`；下载 `HTTP=200 size=533`、`Content-Type: application/octet-stream`、`cmp` 字节一致；下载后 `download_count=1`（DB 与 DTO 一致）；损坏包 + 空字段 → 兜底 `bad.apk / bad.apk / - / -` 且 `metaSource=fallback`；全库 `distinct meta_source = fallback,parsed` |
| 4 | 契约与测试一致性 | **PASS** | `go vet` 干净、`go test -count=1 ./...` → `ok dandelion/server/internal/contract`；`npx openapi-typescript` 重生成到 /tmp 与仓库 `schema.d.ts` `diff -q` **无差异**（本轮未改契约，符合预期） |

### 12.1 一次假失败的定位过程（记录，供以后避坑）

- 首次 round 2 构建（18:30）测出的现象是「所有 405 的 `Allow` 都等于 `GET`，`PUT /builds/stage` 也不给 `POST`，`POST /healthz` 返回 404」，看似修复不完整。
- 处置：把 `api/` + `server/` 复制到 `/tmp/r2dbg`，**仅在副本** `allowedMethodsFor()` 首行加一条 `log.Printf("[DBG405] path=%q pattern=%q")`，重建后逐路由探测 → 副本行为**全部正确**（`GET, POST` / `POST` / `GET`）。
- 根因：`grep`/`diff` 源码指纹发现 `server/internal/app/router.go` 在 18:30（与我构建同一分钟）又被编辑过（sha256 由 `7afff637…` 变为 `da4c6f63…`）→ **我构建到的是"半写入"快照**，不是最终冻结版。
- 处置：用当前源码重建（`sha256 84215adb…`），构建前后对 6 个关键文件做指纹比对（`A`/`B` 两次 `diff` 为空），再跑 4 项 → 全绿。**方法教训：涉及"并发编辑窗口"的验证，必须「构建前取指纹 → 跑完再取指纹 → 一致才认账」。**

### 12.2 Round 2 收尾

- 进程：18080/18081/18082 全部停止并确认空闲；测试库 `dandelion_qa` 已 `dropdb`（`pg_database` 中 `dandelion%` 计数 0）；`/tmp` 下本轮产物（`/tmp/r2*`、`/tmp/dandelion-r2*`、生成物副本）已删除。
- 仓库：`git status` 中本 QA 仅新增 `docs/07-QA-TEST-PLAN.md`；其余 `M` 项为架构师/工程师本轮同步（`README.md`、`docs/02`、`docs/03`、`docs/06`、`router.go`、`response.go`），QA 未改动源码；`storage/` 仍为 9 个文件（README + 8 个 `.gitkeep`）。


