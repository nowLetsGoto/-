# 02-v2 · 系统架构设计（定稿）｜Dandelion 多板块内部研发交付平台

> 作者：高见远（架构师）｜日期：2026-11-13｜面向：主理人 / 工程师
> **效力**：本文取代 `docs/02-ARCHITECTURE.md`（v1，已废弃）。`docs/03-TASKS.md`（v1）同样已废弃，任务以 `docs/03-TASKS-v2.md` 为准。
> **上游输入**（四份均已完整读取并对齐）：`docs/01-PRD.md`（364 行基线）、`docs/04-DELTA-PRD.md`（146 行增量，冲突处以它为准）、`docs/05-MODULE-ROADMAP.md`（79 行板块路线图）、主理人最终裁决。未提及部分沿用基线。
> **边界**：本文只做设计，**不含实现代码**；SQL / YAML / JSON / 命令 / 目录树 / Mermaid 均为契约、示例或设计说明。
> **纪律**：第一版只实现 10 条 P0；不写空实现、不堆抽象、不建空目录、不写 `TODO` 占位；中文注释。

**章节索引与主理人 13 项核对**

| 主理人要求 | 本文 |
| --- | --- |
| 1 锁定决策 ADR 表 | §1（16 项，每项含「为什么最简且可扩展」） |
| 2 整体框架分层 Mermaid | §2.1 |
| 3 完整目录树 | §3 |
| 4 数据模型 DDL / 字段表 / classDiagram | §4 |
| 5 完整 API 契约 | §6（含 §6.8 契约一致性测试） |
| 6 两条时序图 | §7.1 / §7.2 |
| 7 页面技术设计 | §9 |
| 8 Q1 / Q2 重新正面回答 | §10 |
| 9 Push 扩展点 | §11 |
| **10 ★ 多板块页面位置预留 ★** | **§8** |
| **11 ★ 数据库框架与多板块演进 ★** | **§5** |
| 12 负面清单 + 待明确事项 | §12 |
| 13 `contract.json` 降级附录 | 附录 B |

> **本版相对上一草稿的四处实质修正（最终裁决 + 质量关卡，以本条为准）**
> ① **采纳 `POST /api/v1/builds/stage` 暂存 + token 复用**：一次上传文件 → tmp 暂存 + 同步解析 → 前端带出表单 → 提交只发 token + 表单最终值 → 服务端 rename + INSERT，**提交时不再调用 parser、不覆盖用户值**。原 `/builds/parse` 只读预览端点**取消**。
> ② **下载次数改为「流成功完成后才计数」**：`io.Copy` 成功且 `written == Content-Length` 之后才 `+1`；404 / 路径非法 / 文件缺失 / 客户端中断 / copy 出错 / 字节不足**一律不计数**。原「命中请求即计数、中断仍计数」作废。
> ③ **保留 `Build.downloadPagePath`**，但**唯一用途是前端 react-router 内部跳转**；**禁止**用它生成二维码 / 复制地址 / 下载链接。三个 URL 字段统一由 **`server/internal/service/build_service.go`** 用 `PUBLIC_BASE_URL` 唯一生成（原 `contract/urls.go` 取消该文件，职责并入 service）。
> ④ **质量关卡：删除 `touchedFields` / `mergeFields` / `ParsePreview` 整套机制与 `metaSource` 请求字段**：stage 方案下**表单即唯一真值**，无需 dirty 标记；契约新增 `StageResult` 与 `CommitBuildRequest{stageToken, environment, platform, appName, packageId, version, buildNumber, changelog}`；提交接口命名 `Commit` / `commitBuild`；删除「stage 二次传输」「一步式上传端点」遗留项，遗留项 L3 收敛为「是否按 IP/用户强去重 → P1」。

---

## 0. 结论速览与 v1 差异

| 问题 | v2 定稿结论 |
| --- | --- |
| **Q1 前后端是否同仓** | **同仓**，但取消 npm workspaces：`api/`（契约）+ `web/`（唯一 npm 包）+ `server/`（Go module）+ `deploy/` + `docs/` + 根 `Makefile`。锚点从「共享 TS 类型」变为「共读语言无关契约」 |
| **Q2 如何部署** | **四阶段**：本地（必须先起 PG + 先配 `PUBLIC_BASE_URL`）→ 单机/内网（Go 单二进制 + PG + systemd）→ 容器（db/server/nginx 三服务 + 卷 + HTTPS）→ 规模化。**「内网 HTTP 即可跑通」不再成立**：二维码必须手机可扫，绝对地址基址是硬前提 |
| 一句话架构 | React SPA（3 页）→ `/api/v1` → chi handler → service → repository → PostgreSQL；`parser / storage / middleware` 为职责单一旁路包；`api/openapi.yaml` 是全仓唯一契约 SSOT |

**v1 → v2 关键差异（工程师必读）**

| # | v1（已废弃） | v2 |
| --- | --- | --- |
| 1 | Node 20 + Express + multer + SQLite + tsx | Go 1.22+ / chi v5 / 标准库 multipart / PostgreSQL 16 / `go build` 单二进制 |
| 2 | `packages/shared` TS 包共享类型 | `api/openapi.yaml` + 生成物入库 + 契约一致性测试 |
| 3 | 前端 `window.location.origin` 拼地址 | **服务端 `PUBLIC_BASE_URL` 唯一生成绝对 `downloadPageUrl` / `downloadFileUrl`，前端零拼接**；`downloadPagePath` 仅剩「内部路由跳转」一个用途 |
| 4 | 二维码 / 自动解析 / 下载次数均不做（P1） | 三项进 P0（P0-8 / 9 / 10） |
| 5 | 「一个 Node 进程 + 一个 storage 目录」 | Go 二进制 + PG 服务 + uploads 卷，必须先起 DB |
| 6 | 单板块工具 | 三段式 Layout + 路由命名空间 + 数据库框架预留（§5 / §8），**零占位代码** |

---

## 1. 决策摘要（锁定版 ADR）

> 主理人已锁定，**不再是开放问题**。每项末列给出「为什么最简且可扩展」。

| # | 决策 | 锁定选择 | 理由 | 为什么最简且可扩展 |
| --- | --- | --- | --- | --- |
| ADR-01 | 仓库组织 | **单仓，取消 npm workspaces**；`server/go.mod`、`web/` 唯一 npm 包、根 `Makefile` | 契约文件必须前后端共读一份 | 零构建编排工具；未来分仓只需把 `api/` 抽成版本化契约仓，业务代码不动 |
| ADR-02 | 前端 | Vite 5 + React 18 + TS + react-router-dom + **qrcode.react**；原生 CSS 单文件 | 用户熟悉、3 个页面零设计系统需求 | 不引 MUI/Tailwind/Redux/axios；组件边界清晰，换 UI 库只替换 `components/` |
| ADR-03 | 状态 | 组件内 `useState/useEffect` + URL searchParams；**无全局状态库** | 无跨页共享态、无登录用户态 | 筛选态放 URL 天然可分享、刷新保持，零样板代码 |
| ADR-04 | Web 层 | **Go 1.22+ / chi v5 + 标准库 multipart** | chi 完整保留 `net/http` handler 契约；`r.Route("/api/v1", ...)` 支撑版本化 | 1 个依赖、中间件链即扩展点；上传零封装库（`http.MaxBytesReader` + `r.FormFile` + `io.Copy`） |
| ADR-05 | 静态托管 | 单机 `//go:embed public/*` + **SPA fallback**；容器 Nginx 直出 + **SPA fallback** | 两阶段托管者不同，fallback 责任必须两处都写 | 谁托管静态谁负责 `/d/{id}` 回 `index.html`，避免刷新 404 |
| ADR-06 | 数据库 | **PostgreSQL 16** + `database/sql` + `pgx/v5/stdlib`；一份幂等 `server/internal/repository/schema.sql`（`//go:embed` 进二进制、启动时执行） | JSONB+GIN 承载未来事件/板块特有属性、原子自增、MVCC | 禁止 SQLite/嵌入式 KV/ORM/sqlc/迁移框架；SQL 全在 repository，迁库只换一处 |
| ADR-07 | 契约 SSOT | **`api/openapi.yaml`（OpenAPI 3.1）**；前端 `openapi-typescript` 生成 `schema.d.ts` **入库**；后端手写 DTO + 枚举常量 + **一个契约一致性测试** | Go 无法直接共用 TS 类型 | 生成物入库使前端 dev 不需 Go、后端 build 不需 Node；漂移由 CI 锁死 |
| **ADR-08** | **上传与解析** | **★ `POST /api/v1/builds/stage` 暂存 + token 复用**：选文件触发一次上传 → tmp 暂存 + 同步解析（5s 超时、manifest/plist ≤5MB、zip 条目上限）→ 返回 `{stageToken, parsed, warnings}`（**stage 恒 200**，解析失败只给 warnings）→ 前端带出表单且字段始终可编辑 → 提交 `POST /api/v1/builds` **只发 token + 表单最终值** → 服务端 rename + INSERT，**提交时不再调用 parser、不覆盖用户值** | 文件只传一次；解析发生在用户填表时间窗，不拖慢提交；表单即最终真值，无需服务端二次合并 | 复用既有「tmp → final 两段式落盘」，无中间表/无会话/无缓存；`parser/` 只读单一职责，失败降级不阻塞 |
| ADR-09 | 二维码 | 前端 `QRCodeSVG`（`size=224, level="M", marginSize=4`），**仅下载页**，后端零新接口零字段 | 纯展示逻辑留在视图层 | 后端 0 改动；列表页不做，P1 若加复用同一绝对地址 |
| **ADR-10** | **下载次数** | **`io.Copy` 成功完成且 `written == Content-Length` 之后**才 `UPDATE builds SET download_count = download_count + 1 WHERE id = $1 RETURNING download_count`；**旁路**（2s 短超时、失败只记日志） | 精确对应「成功取得安装包文件」；失败请求不计 | 单语句原子自增、并发安全、零补偿事务；计数失败绝不阻断已完成的下载 |
| ADR-11 | 响应/错误 | 统一 `{code, data, message}`；`AppError` + `middleware/error.go` 集中处理 | 前端一个 `apiClient` 解包 | 路由层零 try/catch；错误码只有 400/404/405/413/500 |
| ADR-12 | 配置 | `server/internal/config/config.go` 唯一读环境变量；含 **`PUBLIC_BASE_URL` 校验** | 配置单点 | 本地之外只需必填 `PUBLIC_BASE_URL` / `DATABASE_URL` |
| ADR-13 | API | 前缀 **`/api/v1/...`**；`/healthz` 独立；无鉴权/会话/Cookie/CSRF | R3 机器可 `curl` 调用 | 版本化前缀 + OpenAPI = 未来 CLI/CI/push 直接复用 |
| ADR-14 | 环境枚举 | 固定 `test/hotfix/pre/prd`，**只定义在 `api/openapi.yaml` 一处**；DB 只 `NOT NULL` **不加 CHECK** | Q9 单一来源 | 增删只改 yaml，DB 不改表；未来板块新增维度是「新定义」不是「复制修改」 |
| ADR-15 | 多板块 | 第一版单库单表 `builds`、**不建 `apps` 表**；三段式 Layout + 配置化导航数组 | 一张表时拆 schema/建实体是过度设计 | 只靠「结构位置 + 契约共享维度 + config 化根目录」预留，零占位代码（§5 / §8） |
| ADR-16 | Push 预留 | 第一版 **push 相关代码行数 = 0**；只保留唯一时点、完整 Build、装配可注入、`/api/v1`、JSONB DDL 示例 | 事件数据全部可从现有 Build 构造 | 未来接入 = 新增一个包 + 改 3~5 行，主流程一行不动（§11） |

**已明确排除**：MySQL、SQLite、GORM、sqlc、golang-migrate、Gin/Echo/Fiber、cgo、外部命令（aapt/unzip）、MQ、Redis、后端二维码接口、列表页二维码、应用图标解析、iOS 直接安装、登录鉴权、API Key、`POST /builds/parse` 只读预览端点、任何为未来板块/push 预埋的表/字段/空模块。

---

## 2. 整体框架设计

### 2.1 分层图

```mermaid
flowchart TB
    subgraph Client["浏览器（桌面上传 / 手机扫码打开下载页）"]
        U["开发者 / 测试同学 / 同事"]
    end

    subgraph Frontend["前端 SPA · web/（Vite + React + TS）"]
        PAGES["页面层：/ 列表 · /upload 上传 · /d/:id 下载页"]
        COMP["组件层：Layout（三段式+导航数组）/ FileDropzone / States"]
        APIC["api/client.ts（fetch 封装）+ api/builds.ts（★ URL 唯一消费处）"]
        CSS["styles.css（唯一样式来源）"]
    end

    subgraph Boundary["═══ 前后端边界：同源 HTTP /api/v1（唯一通道）═══"]
        HTTP["POST /builds/stage · POST /builds · GET /builds · GET /builds/{id} · GET /builds/{id}/file · GET /healthz · SPA fallback"]
    end

    subgraph Backend["后端 · server/（Go 1.22+ / chi v5）"]
        ROUTER["app/router.go<br/>路由表 · 静态托管 · SPA fallback"]
        MW["middleware/error.go<br/>统一错误 · tmp 清理"]
        HANDLER["handler/<br/>HTTP 语义：取参 / 校验 / 响应头 / 状态码"]
        SERVICE["service/build_service.go<br/>★ 业务编排 · ★ 唯一 URL 生成 · ★ 唯一「上传完成」时点"]
        REPO["repository/<br/>★ 唯一 SQL 出入口"]
        PARSER["parser/<br/>APK/IPA 解析（只读，仅 stage 调用）"]
        PATHS["storage/paths.go<br/>★ 唯一路径生成 / 清洗 / 安全解析"]
        CONTRACT["contract/<br/>DTO · 枚举常量 · 契约一致性测试"]
        CONFIG["config/<br/>★ 唯一环境变量读取 + PUBLIC_BASE_URL 校验"]
    end

    subgraph Data["持久化"]
        DB[("PostgreSQL 16<br/>builds 表 · JSONB 能力")]
        FS["storage/<br/>tmp/（stage 暂存） · uploads/{env}/{platform}/"]
    end

    SSOT["📦 api/openapi.yaml（★ 跨语言契约单一来源）<br/>Environment · Platform · Build · StageResult · ApiResponse · 路径 · 错误码"]

    U --> PAGES
    PAGES --> COMP
    PAGES --> APIC
    COMP --> CSS
    APIC --> HTTP
    HTTP --> ROUTER
    ROUTER --> MW
    MW --> HANDLER
    HANDLER --> SERVICE
    SERVICE --> REPO
    SERVICE --> PARSER
    SERVICE --> PATHS
    SERVICE --> CONTRACT
    SERVICE --> FS
    REPO --> DB
    PATHS --> FS
    CONFIG -.-> SERVICE
    CONFIG -.-> REPO
    SSOT -.->|"openapi-typescript 生成 schema.d.ts（入库）"| APIC
    SSOT -.->|"读 enum 比对 Go 常量（CI）"| CONTRACT

    classDef bound fill:#fff4e6,stroke:#e8912d,stroke-width:2px;
    classDef share fill:#e8f3ff,stroke:#2b7cd3,stroke-width:2px;
    class Boundary bound;
    class SSOT share;
```

### 2.2 单一职责边界

| 层 | 只负责 | 明确不做 |
| --- | --- | --- |
| `api/openapi.yaml` | 类型、枚举、路径、请求/响应、错误码 | 不写业务逻辑、不被运行时加载 |
| `web/src/api/` | HTTP 请求、FormData、统一解包、**消费**服务端返回的 URL | **不拼 origin**、不写业务判断、不缓存 |
| `web/src/pages/` | 一个页面：取数 → 三态渲染 → 交互 | 不直接 `fetch`、不拼 API 路径字符串 |
| `web/src/components/` | 纯展示 / 纯输入（含三段式 Layout 与导航数组） | 不发请求、不持有跨页状态、不写占位菜单 |
| `handler/` | HTTP 语义：取参、校验、调 service、设头与状态码 | 不写 SQL、不拼磁盘路径、不直接操作文件 |
| `app/router.go` | 路由注册、静态托管、SPA fallback、挂载错误中间件 | 不做业务判断 |
| `middleware/error.go` | 统一错误格式化、panic 恢复、失败时清理 tmp | 不做业务判断 |
| `service/` | 业务编排：stage 暂存 → 解析 → 返回 token；提交：token 校验 → rename → 写库 → **生成 3 个 URL** → 计数旁路 | 不写 SQL（交 repository）、不解析 multipart（交 handler）、不拼路径（交 storage） |
| `repository/` | **唯一** SQL 出入口、行 ↔ DTO 映射（派生 URL 字段留空，由 service 填充） | 不做业务校验、不碰文件系统、不生成 URL |
| `parser/` | 文件 → 元数据（PRD 8 字段定义，按平台取 4 个） | 不落盘、不写 DB、不发网络、不调 service/repository、不生成 token |
| `storage/paths.go` | **唯一**路径生成、文件名清洗、安全解析 | 不做 IO（只算路径） |
| `config/` | **唯一**读取环境变量 + 校验 | 不被各处 `os.Getenv` 取代 |

> 校验「单一职责」的土办法：全仓搜 `'hotfix'` 只命中 `api/openapi.yaml` + 生成物 + 契约测试断言；搜 `storage/uploads` 只命中 `paths.go`；搜 `SELECT|INSERT|UPDATE` 只命中 `build_repository.go`；搜 `PUBLIC_BASE_URL` 只命中 `config.go` + `build_service.go` + 部署配置；搜 `window.location.origin` 只出现在「禁止」语境。

---

## 3. 仓库目录结构

> 单仓、**无 npm workspaces**。`server/go.mod` 是 Go 模块根；`web/` 是唯一 npm 包。第一版 **Go 源文件 16 个、前端手写源文件 12 个 + 1 个生成物**，均对应 P0 职责，**无空目录、无空模块**。
>
> 📁 **目录布局与分类规则（顶层职责、前后端目录边界、依赖方向、新增文件决策表、命名约束）详见 `docs/06-FOLDER-LAYOUT.md`。**

```
dandelion/                                      # 仓库根
├── Makefile                                    # 统一编排：gen:api / dev / build / test / up / down
├── .env.example                                # 环境变量样例（含 PUBLIC_BASE_URL / DATABASE_URL 与三场景取值说明）
├── .gitignore                                  # 忽略 server/public、storage、.env、bin/、*.log、dist
├── README.md                                   # 快速开始、API 速查、部署、备份、iOS 限制、常见问题
├── api/
│   └── openapi.yaml                            # ★ 唯一契约 SSOT：枚举 / Build / StageResult / 请求 / 响应 / 错误码 / /api/v1 路径
├── web/                                        # 前端（唯一 npm 包）
│   ├── package.json                            # 依赖与 dev/build/gen:api/preview 脚本
│   ├── tsconfig.json                           # TS 严格模式 + Bundler 解析 + noUncheckedIndexedAccess
│   ├── vite.config.ts                          # React 插件、/api proxy→:8080、server.host=0.0.0.0、build.outDir→../server/public
│   ├── index.html                              # SPA 单一挂载点
│   └── src/
│       ├── main.tsx                            # 入口：挂载 React + BrowserRouter + 全局样式
│       ├── App.tsx                             # 路由表（/ · /upload · /d/:id）+ Layout 包裹
│       ├── styles.css                          # 唯一样式：CSS 变量 + 组件类 + 手机断点
│       ├── api/
│       │   ├── schema.d.ts                     # openapi-typescript 生成物（提交入库，不手写）
│       │   ├── client.ts                       # fetch 封装、统一解包 {code,data,message}、抛 ApiError
│       │   └── builds.ts                       # API 函数 + ★ URL 唯一消费处（直接使用 DTO 字段，不拼 origin）
│       ├── components/
│       │   ├── Layout.tsx                      # 三段式：品牌区 + 主导航区（配置化数组，第一版 1 项）+ 内容区
│       │   ├── FileDropzone.tsx                # 拖拽/点击选文件、回显名与大小、移除、选文件后触发 stage
│       │   └── States.tsx                      # Loading / Empty / Error（含重试回调）
│       ├── pages/
│       │   ├── UploadPage.tsx                  # P-01：选文件自动 stage 带出 + 表单（字段始终可编辑）+ 提交 stageToken + 成功卡片
│       │   ├── ListPage.tsx                    # P-02：四环境切换 + 平台筛选 + 列表（含下载次数列）
│       │   └── DownloadPage.tsx                # P-03：详情 + 二维码 + 下载按钮 + 复制地址 + 次数（手机优先）
│       └── utils/
│           └── format.ts                       # formatBytes / formatDate / 标签取值（标签源自契约）
├── server/                                     # Go 模块（go.mod / go.sum）
│   ├── cmd/server/main.go                      # 装配层：config → DB pool → 注入 → HTTP server → 优雅退出
│   ├── public/                                 # 前端构建产物（//go:embed 目标；gitignore，构建时生成）
│   ├── embed.go                                # //go:embed public/* 绑定前端产物（//go:embed 只能与 public/ 同包目录）
│   └── internal/
│       ├── config/config.go                    # ★ 唯一环境变量读取；校验 PUBLIC_BASE_URL 绝对性与禁用 host
│       ├── contract/
│       │   ├── contract.go                     # Build / ApiResponse / StageResult / ParsedMeta / DTO + 环境平台常量
│       │   └── contract_test.go                # 契约一致性测试：读 openapi.yaml enum 与 Go 常量比对
│       ├── handler/
│       │   ├── build_handler.go                # Stage / Commit / List / Get / DownloadFile 五个 HTTP handler
│       │   ├── health_handler.go               # GET /healthz
│       │   └── response.go                     # ok()/fail() 统一响应 + AppError 错误类型与错误码
│       ├── service/build_service.go            # ★ 业务编排 + ★ 唯一 URL 生成 + ★ 唯一「上传完成」时点 + 计数旁路
│       ├── repository/
│       │   ├── build_repository.go             # ★ 唯一 SQL 出入口：Insert / List / NextID / FindByID / IncrDownloadCount
│       │   └── schema.sql                      # 一份幂等 DDL（builds 表 + 索引），//go:embed 进二进制，无迁移框架
│       ├── parser/
│       │   ├── parser.go                       # 统一入口 Parse(io.ReaderAt, size, platform)：超时、大小上限、部分成功
│       │   ├── apk.go                          # androidbinary：Package / VersionName / VersionCode / App.Label
│       │   └── ipa.go                          # archive/zip + howett.net/plist：CFBundle* 四字段
│       ├── storage/paths.go                    # ★ 唯一路径生成、文件名清洗、safeResolve 前缀校验
│       ├── middleware/error.go                 # 统一错误中间件、panic 恢复、失败时清理 tmp
│       └── app/router.go                       # chi 路由表 + 静态托管 + SPA fallback（挂载错误中间件）
├── deploy/
│   ├── Dockerfile                              # 多阶段：node 出 dist → golang 编译 → 运行仅二进制+证书（DDL 已 embed）
│   ├── docker-compose.yml                      # db / server / nginx 三服务 + 卷 + healthcheck
│   ├── nginx.conf                              # 静态直出 + SPA fallback + /api 反代 + client_max_body_size 210m
│   └── dandelion.service                       # systemd 单元（含 After/Requires=postgresql.service）
├── storage/                                    # 运行时数据（gitignore）
│   ├── tmp/                                    # ★ stage 暂存区（token 文件，1 小时过期）
│   └── uploads/{env}/{platform}/{id}.{ext}     # 实际包文件
└── docs/                                       # 01 / 02(v1废弃) / 03(v1废弃) / 04 / 05 / 02-v2 / 03-v2
```

**文件数核对**：Go **16**（main 1 + embed 1 + config 1 + contract 2 + handler 3 + service 1 + repository 1 + parser 3 + storage 1 + middleware 1 + app 1）；前端 12 手写 + 1 生成物；部署 **4**（`schema.sql` 已迁至 `internal/repository/` 并 embed，不再属部署资产）；契约 1；根 4。**没有 `contract/urls.go`（URL 生成并入 service）、没有 `event/`、`pages/updates/` 等为未来而建的目录或文件。**

---

## 4. 数据模型

### 4.1 `builds` 表完整 DDL（`server/internal/repository/schema.sql`，`//go:embed`，幂等）

```sql
-- 工程师按此实现；幂等，可重复执行。第一版只有这一张表。
CREATE TABLE IF NOT EXISTS builds (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  environment         TEXT        NOT NULL,   -- 只 NOT NULL，不加 CHECK（枚举单一来源在 openapi.yaml）
  platform            TEXT        NOT NULL,
  app_name            TEXT        NOT NULL,
  package_id          TEXT        NOT NULL DEFAULT '',   -- applicationId / CFBundleIdentifier；R5「应用」维度
  version             TEXT        NOT NULL DEFAULT '',  -- versionName / CFBundleShortVersionString
  build_number        TEXT        NOT NULL DEFAULT '',  -- versionCode / CFBundleVersion
  changelog           TEXT        NOT NULL DEFAULT '',
  original_file_name  TEXT        NOT NULL,            -- 已清洗，仅用于展示与下载名
  file_size           BIGINT      NOT NULL,
  file_ext            TEXT        NOT NULL,            -- apk | ipa，由 platform 映射，绝不取用户扩展名
  relative_path       TEXT        NOT NULL,            -- 相对 storage/uploads/ 的规范化路径
  meta_source         TEXT        NOT NULL DEFAULT 'fallback',  -- parsed | fallback（服务端派生，内部标注，UI 不强制展示）
  download_count      BIGINT      NOT NULL DEFAULT 0,  -- ★ 板块特有字段，属 builds 表，不抽象成通用计数表
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_builds_env_platform_created
  ON builds (environment, platform, created_at DESC);
```

> **为什么 `environment` 不加 CHECK**：CHECK 里写 `'test','hotfix','pre','prd'` 就是枚举的第 4 处定义，破坏 ADR-07 单一来源。第一版只有一个写入口（提交 handler），合法性由契约 + Go 常量校验足够；未来出现第二个写入方（CI/push）时再补 CHECK 或 FK，取值仍从契约派生。

### 4.2 字段表

| 字段 | 类型 | 来源 | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | `UUID` | DB `gen_random_uuid()` | 自动 | R1 稳定唯一标识；三个 URL 全部由它派生 |
| `environment` | `TEXT` | 提交表单（必选） | **是** | 四值之一，命中契约枚举；决定磁盘目录与列表主条件 |
| `platform` | `TEXT` | 提交表单（必选，按扩展名预选可改） | **是** | `android`/`ios`；决定目录、扩展名、下载 Content-Type |
| `app_name` | `TEXT` | 表单最终值（stage 带出 / 用户改） | 否 | 空时兜底为清洗后原始文件名 |
| `package_id` | `TEXT` | 表单最终值 | 否 | 应用稳定键；**一处两用**：P0-9 解析字段 + R5「应用」订阅维度；为空回退 `app_name` |
| `version` | `TEXT` | 表单最终值 | 否 | 空兜底 `'-'` |
| `build_number` | `TEXT` | 表单最终值 | 否 | 空兜底 `'-'` |
| `changelog` | `TEXT` | 提交表单 | 否 | 更新说明，空兜底 `''` |
| `original_file_name` | `TEXT` | stage 时 multipart 文件名（服务端清洗） | 是 | 展示 + `Content-Disposition` |
| `file_size` | `BIGINT` | 服务端 `stat` | 自动 | 字节 |
| `file_ext` | `TEXT` | 由 `platform` 映射 | 自动 | 防任意文件写入 |
| `relative_path` | `TEXT` | `storage/paths.go` | 自动 | `prd/android/{id}.apk` |
| `meta_source` | `TEXT` | 服务端按「是否发生空值兜底」派生 | 自动 | `parsed`/`fallback`；内部标注，第一版不强制 UI 展示；**不来自请求** |
| `download_count` | `BIGINT` | DB 原子自增 | 自动 | 起点 0、只增不减；**板块特有** |
| `created_at` | `TIMESTAMPTZ` | DB `now()` | 自动 | 统一 ISO 8601 UTC（响应序列化） |

**派生字段不入库**：`downloadPagePath` / `downloadPageUrl` / `downloadFileUrl` 由 `service/build_service.go` 按 `id` + `PUBLIC_BASE_URL` 计算，避免库里存 URL 后换域名要刷数据。

### 4.3 索引与查询

- 唯一查询模式：`WHERE environment = $1 [AND platform = $2] ORDER BY created_at DESC`，由 `idx_builds_env_platform_created` 覆盖。
- 全部参数绑定，禁止字符串拼 SQL；`pgx.ErrNoRows` → 404。
- 第一版**不分页**；空结果 `items: []`（不是 404）。

### 4.4 类图

```mermaid
classDiagram
    direction TB

    class Build {
        <<DTO>>
        +string id
        +Environment environment
        +Platform platform
        +string appName
        +string packageId
        +string version
        +string buildNumber
        +string changelog
        +string originalFileName
        +int64 fileSize
        +string fileExt
        +string relativePath
        +string metaSource
        +int64 downloadCount
        +string downloadPagePath
        +string downloadPageUrl
        +string downloadFileUrl
        +string createdAt
    }
    class ApiResponse~T~ {
        <<DTO>>
        +int code
        +T data
        +string message
    }
    class ParsedMeta {
        <<DTO>>
        +string appName
        +string packageId
        +string version
        +string buildNumber
    }
    class StageResult {
        <<DTO>>
        +string stageToken
        +ParsedMeta parsed
        +string[] warnings
    }
    class CommitBuildRequest {
        <<DTO>>
        +string stageToken
        +Environment environment
        +Platform platform
        +string appName
        +string packageId
        +string version
        +string buildNumber
        +string changelog
    }
    class ListQuery {
        <<DTO>>
        +Environment environment
        +Platform platform
    }
    class OpenApiContract {
        <<SSOT>>
        +ENVIRONMENTS
        +PLATFORMS
        +API_ROUTES
        +LIMITS
        +Build schema
        +StageResult schema
    }
    class Config {
        <<service>>
        +string databaseURL
        +string publicBaseURL
        +string storageRoot
        +int maxUploadMB
        +validate()
    }
    class StoragePaths {
        <<service>>
        +stagePath(token, ext) string
        +tmpPath(name) string
        +finalPath(env, platform, id, ext) string
        +relativePath(env, platform, id, ext) string
        +cleanFileName(name) string
        +safeResolve(rel) string
    }
    class Parser {
        <<service>>
        +Parse(reader, size, platform) ParsedMeta
        -parseApk(reader) ParsedMeta
        -parseIpa(reader) ParsedMeta
    }
    class BuildService {
        <<service>>
        +Stage(file) StageResult
        +Create(input) Build
        +List(query) Build[]
        +Get(id) Build
        +ResolveFile(id) FileRef
        +IncrDownloadCount(id) void
        -buildURLs(id) URLs
        -validateStageToken(token) StagedFile
        -compensate(stage, final) void
    }
    class BuildRepository {
        <<service>>
        +Insert(build) void
        +List(query) Build[]
        +FindByID(id) Build
        +IncrDownloadCount(id) int64
        -rowToBuild(row) Build
    }
    class BuildHandler {
        <<handler>>
        +Stage(w, r)
        +Commit(w, r)
        +List(w, r)
        +Get(w, r)
        +DownloadFile(w, r)
    }
    class Router {
        <<app>>
        +NewRouter() http.Handler
        -spaFallback(w, r)
    }
    class ApiClient {
        <<frontend>>
        +request(path, init) T
        +stageBuild(file) StageResult
        +commitBuild(input) Build
        +listBuilds(query) Build[]
        +getBuild(id) Build
    }
    class UploadPage { <<component>> +parsing bool +stageToken string }
    class ListPage { <<component>> }
    class DownloadPage { <<component>> }
    class Layout { <<component>> +navItems[] }

    OpenApiContract ..> Build : 生成 schema.d.ts
    OpenApiContract ..> Config : 契约测试比对 enum
    ApiResponse ..> Build : data 载体
    ApiResponse ..> StageResult : data 载体
    ParsedMeta <.. Parser : 输出
    StageResult <.. BuildService : Stage 返回
    Build <.. CommitBuildRequest : 构造
    Config ..> BuildService : 提供 PUBLIC_BASE_URL
    BuildService ..> Build : buildURLs 派生 3 个 URL 字段
    Router --> BuildHandler : 挂载 /api/v1
    BuildHandler --> BuildService : 调用
    BuildService --> BuildRepository : 调用
    BuildService --> Parser : 仅 Stage 调用
    BuildService --> StoragePaths : 调用
    BuildRepository ..> Build : 映射（URL 字段留空）
    UploadPage --> Layout
    ListPage --> Layout
    DownloadPage --> Layout
    UploadPage --> ApiClient
    ListPage --> ApiClient
    DownloadPage --> ApiClient
    ApiClient ..> BuildHandler : HTTP /api/v1/*
    ApiClient ..> OpenApiContract : 使用生成类型
```

---

## 5. ★ 数据库框架与多板块演进 ★

> **本章结论（用户最关心，先给）—— `apps` 表 A/B 最终选 B**：**第一版不建 `apps` 表、不拆 schema、不建任何板块表**。保持单库单表 `builds` + `app_name` / `package_id` 字符串；未来热更新板块需要「应用」主体时，用**一次回填迁移**演进（§5.2 给出可执行 SQL，标注「第一版不执行」）。理由：`05-MODULE-ROADMAP.md` §4 把「应用 App」定义为跨板块共享稳定主体，但**第一版只有一张表、一个板块，选 A 会让上传表单多一步「选/建应用」，交互变重，与「简单」冲突**；而 `package_id` 已是独立稳定键，回填迁移**不改上传/查询/下载主流程**。

### 5.1 框架选型 ADR

| 方案 | 做法 | 评价 |
| --- | --- | --- |
| ① 单库多表 + 表名前缀（`pkg_builds` / `upd_bundles`） | 第一版就叫 `pkg_builds` | 第一版只有一个板块，前缀是无意义噪音；未来真要拆时还得改名 |
| ② PostgreSQL schema 分板块（`package.*` / `update.*`） | 第一版即建 `package` schema | **第一版一张表时引入 schema 分层是过度设计**：多一层命名空间、`search_path`、备份与权限复杂度，零收益 |
| ③ 独立库 | 每板块一个 DB | 跨板块共享维度（应用、环境、平台）无法直接引用，运维翻倍，否决 |
| ✅ **推荐：单库单表 `builds`，不提前拆** | 第一版就一张 `builds` 表，无前缀无 schema | 最简；未来板块增多时按下面触发条件升级 |

**升级触发条件**（满足任一才动，均属未来）：① 出现第 2 个真实板块且表数 ≥ 3；② 跨板块 JOIN 且命名开始混淆；③ 需要按板块做独立权限/备份。
**升级步骤**（未来）：`CREATE SCHEMA package; CREATE SCHEMA update;` → `ALTER TABLE builds SET SCHEMA package;` → 新增 `update.upd_bundles` → 契约同步 tag → **上传/查询/下载 handler 与 service 不改**（repository 内表名带 schema 前缀即可）。

### 5.2 ★ 共享维度「应用 App」是否第一版建表（A/B）★

| 方案 | 做法 | 代价 | 结论 |
| --- | --- | --- | --- |
| **A** | 第一版建 `apps` 表 + `builds.app_id` 外键 | 上传表单必须多一步「选/建应用」，多一个实体、页面与查询；与「上传 4 步内完成」冲突 | ❌ |
| ✅ **B** | 第一版单表 + `app_name` / `package_id` 字符串，未来一次回填迁移 | 未来需一次迁移，但**不改上传/查询/下载主流程** | **✅ 最终选 B** |

**为什么 B 站得住**：应用的**身份**在第一版已由 `package_id`（`applicationId` / `CFBundleIdentifier`，解析失败回退 `app_name`）稳定表达并独立成列；缺的只是「可被引用的实体」，完全可以在未来回填补齐，不需要现在把交互做重。`download_count` 则**留在本表**（见 §5.3）。

**未来回填迁移 SQL（★ 第一版不执行、不建表 ★）**

```sql
-- 0002_add_apps.sql 示意（第一版不执行）
CREATE TABLE apps (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  app_key     TEXT NOT NULL UNIQUE,   -- 取 package_id，回退 app_name
  name        TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO apps (app_key, name)
  SELECT DISTINCT COALESCE(NULLIF(package_id, ''), app_name), app_name
  FROM builds;
ALTER TABLE builds ADD COLUMN app_id UUID;
UPDATE builds b SET app_id = a.id
  FROM apps a
  WHERE a.app_key = COALESCE(NULLIF(b.package_id, ''), b.app_name);
-- 核对无 NULL 后再由后续迁移改为 NOT NULL + FK
ALTER TABLE builds ALTER COLUMN app_id SET NOT NULL;
ALTER TABLE builds ADD CONSTRAINT fk_builds_app FOREIGN KEY (app_id) REFERENCES apps(id);
```

> 迁移期间 `app_name` / `package_id` 保留不删（向后兼容），主流程 SQL 不变。**未来热更新板块共用这张 `apps` 表**，满足 PM §4「共享稳定主体不因新增板块被复制或改名」。

### 5.3 共享维度清单（对齐 `05-MODULE-ROADMAP.md` §4 八条）

| 概念 | 归属 | 第一版落地 | 未来演进 |
| --- | --- | --- | --- |
| 应用 App | **跨板块共享** | `builds.app_name` + `builds.package_id` 两个独立字段；不建 `apps` 表 | 按 §5.2 回填成实体，两板块共用 |
| 环境 | **跨板块共享（取值待确认）** | `builds.environment`，四值由 `api/openapi.yaml` 定义，**服务应用包板块** | ⚠️ **PM Q3**：Pushy 类平台无 Deployment 概念、用灰度比例控制范围。**不要把四值设计成全平台强制维度或所有表必填列**；未来热更新板块可复用、也可采用灰度维度 —— 那是**契约里新增枚举/字段**，不是复制修改现有枚举，**与「环境枚举只定义一次」不矛盾**，且**不改 `builds` 表与上传主流程** |
| 平台 android/ios | **跨板块共享** | `builds.platform`，契约 `Platform.enum` | 未来板块复用同一契约定义 |
| 版本 / 构建号 | **跨板块共享** | `builds.version` / `build_number` | 热更新板块用它绑定基线原生版本 |
| 上传 / 发布时间 | **跨板块共享** | `builds.created_at`（ISO 8601 UTC） | 各板块列表排序、时间轴、R4 通知时点 |
| 下载 / 分发地址 | **跨板块共享** | `downloadPageUrl`（绝对）/ 二维码 / `downloadFileUrl` | 热更新板块是「客户端拉取地址」，形态不同但同属稳定可引用地址 |
| 包文件 | **板块特有** | `.apk`/`.ipa` 存 `storage/uploads/`；「上传—持久化—可访问地址」链路 | 热更新包是 ppk/bundle/差量包，存 `storage/bundles/`；**链路一致、目录不复用** |
| **下载次数** | **板块特有** | `builds.download_count`（P0-10） | 热更新板块更关注覆盖率 / 活跃设备数，**口径不同，不宜强行复用** ⇒ **不抽象成全平台通用计数表、不建 `download_stats` 表** |

> 共享概念集中在 `api/openapi.yaml` 定义，未来板块复用契约而非复制改名；板块特有概念各板块自行扩展。

### 5.4 未来热更新板块示意 DDL（★ 第一版不建表 ★）

```sql
-- 0003_upd_bundles.sql 示意（第一版不执行、不建表、不建目录）
CREATE TABLE upd_bundles (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  app_ref          TEXT        NOT NULL,       -- 与 builds 同构：指向应用主体（未来 apps.id）
  environment      TEXT,                       -- 可选：是否复用四值见 PM Q3，故不设 NOT NULL
  platform         TEXT        NOT NULL,       -- 与 builds 同构
  baseline_version TEXT        NOT NULL,       -- 特有：原生基线版本（版本绑定）
  bundle_version   TEXT        NOT NULL,       -- 特有：ppk / bundle 版本
  relative_path    TEXT        NOT NULL,       -- 与 builds 同构：文件定位
  file_size        BIGINT      NOT NULL,       -- 与 builds 同构
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),  -- 与 builds 同构
  rollout_percent  SMALLINT    NOT NULL DEFAULT 0,      -- 特有：灰度 1–100%
  status           TEXT        NOT NULL,       -- 特有：draft / gray / full / rolled_back
  published_at     TIMESTAMPTZ,               -- 特有
  rollback_to      UUID,                      -- 特有：回滚指针
  meta_info        JSONB       NOT NULL DEFAULT '{}'::jsonb,  -- 特有：metaInfo / 差量信息 / 兼容范围
  extra            JSONB       NOT NULL DEFAULT '{}'::jsonb   -- ★ 板块特有属性放 JSONB，避免频繁改表
);
CREATE INDEX idx_upd_meta ON upd_bundles USING GIN (meta_info);
```

- **同构字段**（`app_ref` / `platform` / `relative_path` / `file_size` / `created_at`）：与 `builds` 同名同义，未来复用同一套「上传—持久化—可访问地址」链路与仓储模式。
- **板块特有**（灰度、基线版本、状态、回滚指针、metaInfo）只属于 `upd_bundles`。
- **PG JSONB** 承载板块特有且易变属性（semver 兼容范围、差量包信息、崩溃回滚阈值），**字段增删不需 DDL** —— 与 §11 `outbox_events.payload JSONB` 同理，是 ADR-06 选 PG 的长期收益。

### 5.5 文件存储布局演进

| 项 | 第一版 | 未来 |
| --- | --- | --- |
| App 包 | `storage/uploads/{environment}/{platform}/{id}.{ext}` | 不变 |
| stage 暂存 | `storage/tmp/{stageToken}.{origExt}`（1 小时过期，不持久化） | 不变 |
| 热更新包 | **不建 `storage/bundles/` 目录** | `storage/bundles/...` 独立根目录 |
| 路径算法 | `storage/paths.go` 唯一实现，根目录由 `config.storageRoot` 统一提供 | 新增板块目录只需**加一个配置/常量**，不改路径算法 |
| 链路一致性 | 「上传 → tmp 暂存 → 校验 → rename → 可访问地址」 | **复用同一套路径安全/落盘模式**（`cleanFileName` / `safeResolve` 前缀校验），但**不复用 `uploads/` 目录**（PM §4：链路一致、形态不同） |

> 第一版 `config` 只暴露一个 `storageRoot`，`paths.go` 由它拼出 `uploads/` 与 `tmp/`；未来加 `bundles/` 只在 config 加子目录常量，**`paths.go` 的安全解析逻辑一行不改**。

### 5.6 「未来加板块时改哪些文件 / 哪些一行不动」（与 §11 Push 表并列）

| 类别 | 文件 / 位置 | 改动 |
| --- | --- | --- |
| 修改 | `web/src/components/Layout.tsx` 的**导航数组** | 数组加一项（如「热更新包」→ `/updates`），**三段式结构本身不改** |
| 修改 | `web/src/App.tsx` | 新增路由前缀 `/updates/*` |
| 新增 | `web/src/pages/updates/*`、`web/src/api/updates.ts` | 新板块页面与 API 客户端 |
| 修改 | `api/openapi.yaml` | 新增板块路径与 schema（**不改既有 `/api/v1/builds*` 定义**） |
| 修改 | `server/internal/repository/schema.sql` / 新 migration | 新增 `upd_bundles`（§5.4） |
| 修改 | `server/internal/config/config.go` | 加 `storage/bundles/` 子目录常量 |
| 新增 | `server/internal/{handler,service,repository}/update_*` | 新板块后端层（复用 `storage/paths.go` 安全逻辑） |
| **一行不动** ✅ | `Layout.tsx` 三段式结构、`web/src/pages/DownloadPage.tsx`、`web/src/api/builds.ts`、`server/internal/service/build_service.go`（应用包板块）、`server/internal/repository/build_repository.go`、`api/openapi.yaml` 中 `Build` / `Environment` / `Platform` 定义、`/d/:id` 路由与二维码内容 | — |

### 5.7 负面清单增补（多板块）

❌ 第一版不建 `apps` / `modules` / `upd_bundles` / `module_type` 判别列 / 板块枚举表
❌ 不在 `builds` 加 `module` / `type` / `future_*` 预留字段
❌ 不建空目录（`pages/updates/` 等）、不写空实现、不写 `TODO: 热更新板块`
❌ 不引入插件系统 / 模块注册表 / 动态路由加载 / 板块开关
❌ 不做板块级权限、不建接收方/订阅关系
✅ **只保留**：三段式 Layout + 配置化导航数组、路由命名空间约定、契约里的共享维度、config 化的存储根目录、§5.2/§5.4 的可执行演进 DDL 示例（标注「第一版不执行」）

---

## 6. 完整 API 契约

### 6.1 统一格式与错误码

```ts
// 契约示意（api/openapi.yaml 定义，非实现代码）
interface ApiResponse<T> { code: number; data: T | null; message: string }
```

| 约定 | 规则 |
| --- | --- |
| 成功 | `code: 0`、`data` 为业务数据、`message: 'ok'`；HTTP 200 / 201 |
| 失败 | `code` = HTTP 状态码、`data: null`、`message` 为简体中文可读文案，HTTP 状态码同步设置 |
| **唯一例外** | `GET /api/v1/builds/{id}/file` 成功返回**二进制流**，不套 JSON（失败仍返回统一错误 JSON） |
| 错误来源 | 后端只抛 `AppError`，由 `middleware/error.go` 统一格式化；handler 层零 try/catch |

| HTTP / code | 触发 | 示例 message |
| --- | --- | --- |
| 400 | 缺文件 / 环境或平台缺失或非法 / 平台×扩展名不一致 / 字段超长 / `stageToken` 非法·过期·文件不存在 | `请选择 .apk 或 .ipa 文件` / `环境参数不合法` / `暂存文件已失效，请重新选择文件` |
| 404 | 包不存在、文件缺失、路径校验失败 | `包不存在或已被移除` |
| 405 | 方法不匹配（chi 自动带 Allow 头） | `请求方法不支持` |
| 413 | 超过 `MAX_UPLOAD_MB`（默认 200） | `文件超过 200 MB 上限` |
| 500 | 写 tmp / 写文件 / 写库 / 未预期错误 | `服务内部错误，请稍后重试`（日志保留 id/环境/平台/文件名） |

### 6.2 接口总表

| # | 方法 | 路径 | Content-Type | 用途 |
| --- | --- | --- | --- | --- |
| 1 | `POST` | `/api/v1/builds/stage` | `multipart/form-data` | **暂存 + 解析预览**（选文件后触发，P0-9） |
| 2 | `POST` | `/api/v1/builds` | `application/json` | **提交发布 `CommitBuildRequest`**（只发 token + 表单最终值，P0-2） |
| 3 | `GET` | `/api/v1/builds` | — | 按环境(+平台)查询列表（P0-3） |
| 4 | `GET` | `/api/v1/builds/{id}` | — | 单个包详情（下载页取数） |
| 5 | `GET` | `/api/v1/builds/{id}/file` | — | 下载原始包文件（P0-6 / P0-10 计数） |
| 6 | `GET` | `/healthz` | — | 健康检查（独立于版本化前缀） |
| 7 | `GET` | `/assets/*`、`/`、`/upload`、`/d/:id` | — | 静态资源 + **SPA fallback** |

> **第一版没有 `POST /builds/parse` 端点**，也没有「提交时携带文件」的用法。所有路径常量集中在 `api/openapi.yaml`；前端 `api/builds.ts` 与后端 `app/router.go` 都从契约取值，禁止手写 `'/api/v1/builds'` 字面量。

### 6.3 `Build` DTO 与 `StageResult`

| `Build` 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | `string (uuid)` | 稳定唯一标识 |
| `environment` | `Environment` | `test\|hotfix\|pre\|prd` |
| `platform` | `Platform` | `android\|ios` |
| `appName` / `packageId` / `version` / `buildNumber` / `changelog` | `string` | 元数据 |
| `originalFileName` | `string` | 清洗后原始文件名 |
| `fileSize` | `integer (int64)` | 字节 |
| `fileExt` | `"apk"\|"ipa"` | 由 platform 映射 |
| `relativePath` | `string` | 相对 uploads 的规范化路径 |
| `metaSource` | `"parsed"\|"fallback"` | **服务端派生**的内部来源标注（是否发生空值兜底），UI 不强制展示；**不在请求契约中**。契约 `MetaSource.enum` **必须为 `["parsed","fallback"]`**；`manual` **已废弃、第一版不使用**（若字面量出现，只允许出现在「已废弃」说明中） |
| `downloadCount` | `integer (int64)` | 起点 0、只增不减 |
| **`downloadPagePath`** | `string` | **相对** `/d/{id}`；**唯一用途：前端 react-router 内部跳转**，避免前端硬编码 `/d/` |
| **`downloadPageUrl`** | `string (uri)` | **绝对**：二维码内容 / 复制地址按钮 |
| **`downloadFileUrl`** | `string (uri)` | **绝对**：下载按钮 |
| `createdAt` | `string (date-time)` | ISO 8601 UTC |

| `StageResult` 字段 | 类型 | 说明 |
| --- | --- | --- |
| `stageToken` | `string (uuid)` | 暂存文件标识，1 小时有效，用后失效 |
| `parsed` | `ParsedMeta` | `appName` / `packageId` / `version` / `buildNumber`（4 个规范字段） |
| `warnings` | `string[]` | 解析失败/超时/包损坏的中文可读原因（不阻塞） |

> **PRD 的 8 个字段定义**（Android：应用名 / `applicationId` / `versionName` / `versionCode`；iOS：`CFBundleDisplayName`或`CFBundleName` / `CFBundleIdentifier` / `CFBundleShortVersionString` / `CFBundleVersion`）按平台**各取 4 个**，统一映射到 `ParsedMeta` 的 4 个规范字段，映射表见 §6.6。

**`CommitBuildRequest`（提交契约，恰好 8 个字段，无文件、无 touched 标记）**：`stageToken` / `environment` / `platform` / `appName` / `packageId` / `version` / `buildNumber` / `changelog`。**表单即唯一真值**：服务端直接采用这组值，不再解析、不合并、不覆盖；`metaSource` 由服务端派生，不进入请求。

### 6.4 ★ URL 生成规则（Q2 硬约束的落地点）★

| 项 | 规则 |
| --- | --- |
| **唯一生成处** | **`server/internal/service/build_service.go` 的 `buildURLs(id)`**，基址来自 `config.PUBLIC_BASE_URL`；**`Create` / `List` / `Get` 三条返回路径统一调用它**（repository 的 `rowToBuild` 把 3 个 URL 字段留空，由 service 填充） |
| 生成式 | `downloadPagePath = /d/{id}`（相对）<br/>`downloadPageUrl = {PUBLIC_BASE_URL}/d/{id}`<br/>`downloadFileUrl = {PUBLIC_BASE_URL}/api/v1/builds/{id}/file` |
| **★ 用途限定 ★** | `downloadPagePath` **只允许**用于前端 `navigate()` 内部跳转；**严禁**用它生成二维码、复制地址或下载链接 |
| **禁止** | 前端 `window.location.origin` 拼接；读取请求 `Host` / `X-Forwarded-Proto` 拼地址（反代下会被内部地址污染、可被伪造）；相对路径或 `localhost`/`127.0.0.1` 作为二维码内容 |
| 前端消费 | 二维码 `value={build.downloadPageUrl}`；复制按钮 `build.downloadPageUrl`；下载按钮 `build.downloadFileUrl`；**三者同源，前端零拼接** |
| 稳定性 | 由不变的 `id` 派生 ⇒ 同一包生命周期内二维码内容稳定、不含鉴权/有效期/安装控制信息。⚠️ **域名变更会使既有二维码与已转发链接失效** ⇒ 第一版建议域名一次定稳；未来兼容需短链/重定向（P2） |
| 一致性 | 服务端不读请求上下文 ⇒ **无论请求经 Vite proxy 还是 Nginx 反代，只要 `PUBLIC_BASE_URL` 配的是对外入口，生成结果就一致且手机可达**（§10.4） |

### 6.5 逐接口定义

#### ① `POST /api/v1/builds/stage` — 暂存 + 解析预览

**请求** `multipart/form-data`

| 字段 | 类型 | 必填 | 校验 |
| --- | --- | --- | --- |
| `file` | binary | **是** | 扩展名（忽略大小写）∈ `{.apk,.ipa}`；≤ 200MB。缺文件/类型不符 → 400；超限 → 413；写 tmp 失败 → 500 |

**服务端流程**（`service.Stage()`）
1. 生成 `stageToken`（UUID），按扩展名推导 `platformHint`（`apk→android` / `ipa→ios`）供解析使用；把文件写到 `storage/tmp/{token}.{origExt}`。
2. **同步**调用 `parser.Parse()`：`context.WithTimeout(5s)`、manifest/plist 读取上限 5MB、zip 条目数上限防 zip bomb；允许**部分成功**。
3. **解析失败 / 包损坏 / 异常 / 超时 → 只写 `warnings`，stage 仍返回 200**（「stage 恒 200」仅针对解析失败；文件本身超限或写入失败仍按错误码返回）。

**成功 `200`（无论解析成败）**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "stageToken": "c0a8012e-7b41-4c9d-9f33-1a2b3c4d5e6f",
    "parsed": { "appName": "商城 App", "packageId": "com.example.shop", "version": "1.2.0", "buildNumber": "3" },
    "warnings": []
  }
}
```
解析失败示例：`"parsed": {"appName":"","packageId":"","version":"","buildNumber":""}, "warnings": ["应用名未自动识别，可手动填写"]`。

#### ② `POST /api/v1/builds` — 提交发布 `CommitBuildRequest`（**无文件**）

**请求** `application/json`

| 字段 | 类型 | 必填 | 校验 |
| --- | --- | --- | --- |
| `stageToken` | `string (uuid)` | **是** | UUID 格式 + `safeResolve` 前缀校验 + 文件存在 + 未过期（1h）；失效 → 400 `暂存文件已失效，请重新选择文件` |
| `environment` | `Environment` | **是** | 命中契约枚举 |
| `platform` | `Platform` | **是** | 命中契约枚举**且与 stage 时文件扩展名一致**（不一致 → 400 `所选平台与文件类型不一致`） |
| `appName` / `packageId` / `version` / `buildNumber` / `changelog` | `string` | 否 | 长度上限 100/100/50/50/2000 |

**服务端流程**（`service.Create()`）
1. 校验 token 与表单 → `paths.finalPath(env, platform, newId, ext)`（**ext 由 platform 映射**）。
2. `rename(storage/tmp/{token}.{origExt} → storage/uploads/{env}/{platform}/{id}.{ext})`（原子、token 用后不可重用）+ `stat` 取大小。
3. **组装 Build：直接使用表单最终值（表单即唯一真值），不再调用 `parser`、不做 `mergeFields`、不再用解析结果覆盖任何字段**；仅对空字段兜底（`appName`→清洗后文件名、`packageId`→`app_name`、`version`/`buildNumber`→`'-'`、`changelog`→`''`），并据此派生 `meta_source`（发生兜底→`fallback`，否则→`parsed`）。
4. `repository.Insert`（参数绑定）→ 失败则**补偿**：删除已 move 的正式文件 + 清理 tmp。
5. `buildURLs(id)` 生成 3 个地址字段 → **★ 唯一「上传完成」时点** → 返回完整 Build。

**成功 `201`**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "9f1c0e7e-3b2a-4c8d-9a11-7e5d2b0c4a83",
    "environment": "hotfix",
    "platform": "android",
    "appName": "商城 App",
    "packageId": "com.example.shop",
    "version": "1.2.0",
    "buildNumber": "3",
    "changelog": "修复登录态过期问题",
    "originalFileName": "app-prd-1.2.0.apk",
    "fileSize": 29779558,
    "fileExt": "apk",
    "relativePath": "hotfix/android/9f1c0e7e-3b2a-4c8d-9a11-7e5d2b0c4a83.apk",
    "metaSource": "parsed",
    "downloadCount": 0,
    "downloadPagePath": "/d/9f1c0e7e-3b2a-4c8d-9a11-7e5d2b0c4a83",
    "downloadPageUrl": "http://dandelion.internal/d/9f1c0e7e-3b2a-4c8d-9a11-7e5d2b0c4a83",
    "downloadFileUrl": "http://dandelion.internal/api/v1/builds/9f1c0e7e-3b2a-4c8d-9a11-7e5d2b0c4a83/file",
    "createdAt": "2026-11-13T15:20:00.000Z"
  }
}
```

> **★ stage 对用户透明，不是 PRD A6 的「两步发布」★**：stage 在用户「选定文件」时**自动触发**，用户全程只点一次「上传并发布」；没有中间态页面、没有「下一步」按钮、没有发布确认页。第一版仍是一步上传。

#### ③ `GET /api/v1/builds` — 列表

| query | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `environment` | `Environment` | **是** | 缺失/非法 → 400 |
| `platform` | `Platform \| all` | 否 | 省略/`all` = 不筛 |

**成功 `200`**：`data: { environment, platform, total, items: Build[] }`，按 `created_at DESC`；空结果 `total: 0, items: []`（**不是 404**）。

#### ④ `GET /api/v1/builds/{id}` — 详情

`200 {code:0,data:Build,message:'ok'}`；`404 {code:404,data:null,message:'包不存在或已被移除'}`。下载页只带 id 跳转并自己取数，保证链接独立可分享。

#### ⑤ `GET /api/v1/builds/{id}/file` — 下载

- 成功 `200` **二进制流**（`io.Copy`）；失败仍返回统一错误 JSON。
- **响应头**

| 头 | Android (.apk) | iOS (.ipa，第一版仅文件下载) |
| --- | --- | --- |
| `Content-Type` | `application/vnd.android.package-archive` | `application/octet-stream` |
| `Content-Disposition` | `attachment; filename="app.apk"; filename*=UTF-8''<URL编码中文名>` | 同左（`.ipa`） |
| `Content-Length` | 实际字节数 | 实际字节数 |
| `Cache-Control` | `no-cache` | `no-cache` |
| `X-Content-Type-Options` | `nosniff` | `nosniff` |

> 中文文件名必须**双重编码**（`filename` 兼容旧浏览器 + `filename*=UTF-8''...`）；`Content-Type` 取自契约 `Platform.contentType`，不在 handler 写死字符串。

#### ⑥ `GET /healthz` — 健康检查

```json
{ "code": 0, "data": { "status": "ok", "uptime": 3620, "dbOk": true, "storageWritable": true }, "message": "ok" }
```
独立于 `/api/v1`（运维探活不随 API 版本变化），供 compose `healthcheck` 与内网探活。

#### ⑦ 静态资源与 SPA fallback

- 单机：Go `//go:embed public/*` 托管 `index.html` + `assets/`；所有**非 `/api/v1/*`、非 `/healthz`、非静态命中**的 GET 一律回 `index.html`。
- 容器：Nginx `root` 直出静态 + `try_files $uri /index.html` 做 fallback，`/api/` 反代到 server。
- **两处都必须写 fallback**，否则 `/d/{id}` 直接打开或刷新 404。

### 6.6 8 字段映射、表单即唯一真值与 stage 语义

**8 字段映射（PRD §2.2）**

| `ParsedMeta` 规范字段 | Android 来源 | iOS 来源 |
| --- | --- | --- |
| `appName` | `Application.Label`（`@string/...` 时用 `(*Apk).Label(nil)` 解 resources.arsc） | `CFBundleDisplayName`，缺失回退 `CFBundleName` |
| `packageId` | `Manifest.Package`（≈ `applicationId`，**待验证**见 §12.2） | `CFBundleIdentifier` |
| `version` | `Manifest.VersionName` | `CFBundleShortVersionString` |
| `buildNumber` | `Manifest.VersionCode` | `CFBundleVersion` |

**「表单即唯一真值」的落地（stage 方案下无需 dirty 标记 / 字段合并）**

| 步骤 | 行为 | 谁负责 |
| --- | --- | --- |
| 1 stage 返回 `parsed` + `warnings` | 解析值（可能部分为空） | 服务端 `parser/` |
| 2 表单带出 | 把 `parsed` 4 个值**自动带出**到对应表单字段（解析中显示加载态） | 前端 `UploadPage` |
| 3 人工修正 | **所有字段始终可编辑、不锁定**；用户直接改，改后的表单值即最终值 | 前端 |
| 4 提交 | 只发 `CommitBuildRequest` 8 字段（`stageToken` + **表单最终值**）；**服务端不再解析、不合并、不覆盖** | 前端 → 服务端 |
| 5 空值兜底 | 服务端仅对空字段兜底（`appName`→文件名、`version`/`buildNumber`→`'-'` 等），并派生 `meta_source` | 服务端 `service` |

> 因此「用户改过的字段不被覆盖」**不是靠服务端二次合并或 dirty 比对实现，而是靠「表单即唯一真值」**：解析值只用于预填表单，提交的全是表单当前值，服务端原样采用。解析失败 / 包损坏 / 超时只影响 `warnings` 与表单带出，**绝不阻塞上传**。这比 dirty 标记方案更简单、更不易出错。

**stage 临时态最小治理（不建表、不引组件）**

| 项 | 规则 |
| --- | --- |
| 存储 | `storage/tmp/{stageToken}.{origExt}`，token 为 UUID、不可枚举 |
| 清理 | **启动时清空 `storage/tmp/`**（tmp 是纯临时态，重启后无进行中的上传）+ **每次 stage 前删除超过 1 小时的孤儿文件**；零表、零定时任务、零外部组件 |
| 提交校验 | UUID 格式 + `safeResolve` 前缀校验 + 文件存在 + 未过期（1h）；不通过 → 400 |
| 用后 | `rename` 原子移走，**token 立即不可重用**；失败补偿删除已 move 文件 |
| 重新选文件 | 前端触发一次新 stage，旧 tmp 文件由下次清理回收 |

**R3 机器可调用性**：CLI / CI / 未来 push 平台按 OpenAPI 契约**先调 stage 再调 commit** 完成上传，**文件全程只传一次（仅 stage 携带文件，commit 仅传 token 与字段）**；两个接口均自描述、无会话 / Cookie / CSRF 依赖，脚本可直接 `curl` 调用，无需页面交互。

### 6.7 下载次数口径（★ 最终裁决 ★）

| 项 | 定稿口径 |
| --- | --- |
| **计数时点** | **`io.Copy` 成功完成且 `written == Content-Length` 之后**才执行 `UPDATE builds SET download_count = download_count + 1 WHERE id = $1 RETURNING download_count` |
| **不计数** | 404 / 路径非法 / 文件缺失 / **客户端中途断开** / `io.Copy` 出错 / **写出字节不足** —— **一律不计数**；打开或刷新下载页、扫码、复制地址、列表浏览、上传、查询本就不走 `/file` |
| 并发 | 单语句原子自增（`UPDATE ... + 1`），PG 行锁并发安全；**不在下载事务内持锁** |
| **去重** | **不做强去重**（第一版无账号/会话，按 IP 去重需新增表或缓存）→ 用**下载按钮点击后约 3s 禁用 + 浏览器原生下载**抑制误触；**强去重列 P1** |
| Range / 预取 | 第一版**不做 Range / 断点续传**；一个请求 = 一次完整流，避免多次计数 |
| **旁路隔离** | 计数在**流成功之后**执行，不影响 TTFB 与吞吐；DB 操作 2s 短超时，**失败只记日志、绝不阻断已完成的下载**（响应已发出，无法也不应回滚）；不在下载事务内持锁 |
| 展示 | `Build.downloadCount`；列表页每行 + 下载页「已被下载 N 次」；新包 `DEFAULT 0`、只增不减；产品允许「短暂更新延迟」正好与「成功后才计数」吻合 |

> 与上一草稿的差异已显式作废：**「命中请求即计数、客户端中断仍计数」不再成立**。

### 6.8 契约一致性测试怎么比对

| 步骤 | 做法 |
| --- | --- |
| 输入 | `api/openapi.yaml`（唯一 SSOT）与 `server/internal/contract/contract.go` 的 Go 常量 |
| 工具 | `gopkg.in/yaml.v3`（**仅测试依赖**，不进运行时）；`openapi-typescript`（前端 dev 依赖） |
| 比对内容 | ① `components.schemas.Environment.enum` ↔ Go `Environments` 切片（顺序与取值一致）；② `Platform.enum` ↔ `Platforms` 切片；③ `paths` 中的 `/api/v1/*` 与 `/healthz` ↔ Go `API_ROUTES` 常量；④ `LIMITS.maxUploadMB` ↔ `config` 默认值 |
| 失败行为 | 任一不一致 `t.Fatal`，CI 红灯。**验收演示：把 yaml 的 `pre` 改成 `pre2` 后测试必须失败** |
| 前端侧 | `make gen:api` 重新生成 `schema.d.ts` 后 `git diff --exit-code`，防止忘记提交生成物 |
| 运行 | `go test ./internal/contract/...`；`make test` 统一入口 |

---

## 7. 程序调用流程

### 7.1 上传流程（选文件 → stage → 解析 → 表单带出 → 提交 → rename → 写库 → 失败补偿 → 返回绝对地址）

```mermaid
sequenceDiagram
    autonumber
    actor Dev as 开发者（浏览器）
    participant UP as UploadPage
    participant AC as apiClient / builds.ts
    participant H as build_handler
    participant S as BuildService
    participant P as parser/
    participant PT as storage/paths.go
    participant RP as BuildRepository
    participant FS as storage/ tmp + uploads
    participant DB as PostgreSQL builds

    Dev->>UP: 拖入 / 选择 .apk（或 .ipa）
    UP->>UP: 前端预检扩展名与大小；进入「解析中」加载态
    UP->>AC: stageBuild(file)
    AC->>H: POST /api/v1/builds/stage (multipart file)
    H->>H: MaxBytesReader ≤200MB + 扩展名白名单（400/413）
    H->>FS: 生成 stageToken(UUID)，写 storage/tmp/{token}.{origExt}
    H->>S: Stage(stagedFile)
    Note over S,P: ★ 解析在 tmp 落盘之后；同步执行 + context 超时 5s<br/>manifest/plist ≤5MB + zip 条目上限
    S->>P: Parse(io.ReaderAt, size, platformHint)
    alt 解析失败 / 包损坏 / 异常 / 超时
        P-->>S: 部分结果 + error
        Note over S: 只写 warnings，stage 恒 200，绝不阻塞上传
    else 解析成功
        P-->>S: ParsedMeta（4 规范字段）
    end
    S-->>H: StageResult{stageToken, parsed, warnings}
    H-->>AC: 200 {code:0,data:StageResult,message:'ok'}
    AC-->>UP: 结束加载态；把 parsed 4 个值「自动带出」预填表单
    UP-->>Dev: 表单字段始终可编辑；warnings 提示「部分信息未自动识别，可手动修正」

    Dev->>UP: 人工修正任意字段（表单当前值即最终值）<br/>选环境/平台、填更新说明
    Dev->>UP: 点一次「上传并发布」
    Note over Dev,UP: ★ stage 对用户透明，不是 PRD A6 的「两步发布」
    UP->>AC: commitBuild({stageToken + 表单最终值})
    AC->>H: POST /api/v1/builds (application/json，无文件)
    H->>H: 校验 environment/platform 枚举、平台×扩展名一致、字段长度（400）
    H->>S: Create(input)
    S->>PT: validateStageToken(UUID + safeResolve + 存在 + 未过期 1h)
    alt token 非法 / 过期 / 文件不存在
        PT-->>S: 拒绝
        S-->>H: AppError(400)「暂存文件已失效，请重新选择文件」
        H-->>AC: 400 {code:400,data:null,message}
        AC-->>UP: 可读错误，引导重新选文件
    else token 有效
        S->>PT: finalPath(env, platform, newId, ext)（ext 由 platform 映射）
        S->>FS: rename(tmp → uploads/{env}/{platform}/{id}.{ext}) + stat 取大小
        Note over S: ★ 表单即唯一真值：不再调用 parser、不合并、不覆盖用户值<br/>仅对空字段做文件名/占位值兜底并派生 meta_source
        S->>RP: Insert(build)（参数绑定）
        RP->>DB: INSERT ... RETURNING id, created_at, download_count=0
        alt 写库失败
            DB-->>RP: error
            S->>FS: 失败补偿：删除已 move 的正式文件 + 清理 tmp
            S-->>H: AppError(500)
            H-->>AC: 500 {code:500,data:null,message:'服务内部错误，请稍后重试'}
            AC-->>UP: 显示可读失败原因，按钮恢复
        else 写库成功
            DB-->>RP: row
            S->>S: buildURLs(id)：downloadPagePath / downloadPageUrl / downloadFileUrl<br/>（★ 唯一 URL 生成处，PUBLIC_BASE_URL）
            Note over S: ★ 唯一「上传完成」时点：文件已落盘 + DB 事务提交 + 绝对地址已生成且可访问
            S-->>H: Build（完整 DTO）
            H-->>AC: 201 {code:0,data:Build,message:'ok'}
            AC-->>UP: Build
            UP-->>Dev: 成功卡片：绝对下载页地址 + 复制 / 去下载页(navigate 用相对 path) / 返回列表
        end
    end
```

### 7.2 列表 → 下载页 → 二维码 → 流式下载 → 成功后原子计数（旁路失败只记日志）

```mermaid
sequenceDiagram
    autonumber
    actor QA as 测试同学 / 同事（手机扫码 / 浏览器）
    participant LP as ListPage (/)
    participant DP as DownloadPage (/d/:id)
    participant QR as QRCodeSVG
    participant AC as apiClient / builds.ts
    participant H as build_handler
    participant S as BuildService
    participant RP as BuildRepository
    participant PT as storage/paths.go
    participant DB as PostgreSQL
    participant FS as storage/uploads

    QA->>LP: 打开 /?environment=test
    LP->>AC: listBuilds({environment:'test'})
    AC->>H: GET /api/v1/builds?environment=test
    H->>S: List(query)
    S->>RP: List(query)
    RP->>DB: SELECT ... WHERE environment=$1 ORDER BY created_at DESC（索引命中）
    DB-->>RP: rows
    RP-->>S: Build[]（URL 字段留空）
    S->>S: buildURLs(id) 填充 3 个 URL 字段
    S-->>H: Build[]
    H-->>AC: 200 {code:0,data:{items,total,...}}
    alt 查询失败（400 非法环境 / 500）
        H-->>AC: {code,data:null,message}
        AC-->>LP: 抛 ApiError → 错误态 + 重试
    else items 为空
        LP-->>QA: 空状态「当前环境暂无包」+ [去上传]
    else 有数据
        LP-->>QA: 四环境切换 + 平台筛选 + 时间倒序列表（每行含下载次数）
    end

    QA->>LP: 切到 hotfix / 筛 Android（写入 URL searchParams）
    LP->>AC: listBuilds({environment:'hotfix',platform:'android'})
    AC->>H: GET /api/v1/builds?environment=hotfix&platform=android
    H-->>AC: 200 仅该环境该平台（test 包不再出现）

    QA->>LP: 点「下载页」
    LP->>DP: navigate(build.downloadPagePath)  «相对 path 仅用于内部跳转»
    DP->>AC: getBuild(id)
    AC->>H: GET /api/v1/builds/{id}
    H->>S: Get(id)
    S->>RP: FindByID(id)
    RP->>DB: SELECT ... WHERE id=$1
    alt 包不存在
        DB-->>RP: pgx.ErrNoRows
        S-->>H: AppError(404)
        H-->>AC: 404 {code:404,data:null,message:'包不存在或已被移除'}
        DP-->>QA: 「包不存在」+ 返回列表
    else 存在
        DB-->>RP: row
        S->>S: buildURLs(id) 填充 3 个 URL 字段
        S-->>AC: Build
        AC-->>DP: Build
        DP-->>QA: 应用名 / ★环境标签★ / 平台 / 版本(构建号) / 大小 / 时间 / 更新说明 / 「已被下载 N 次」
        DP->>QR: value={build.downloadPageUrl}（绝对地址，前端零拼接）
        QR-->>QA: SVG 二维码（size=224, level=M, marginSize=4）
        DP-->>QA: 「复制页面地址」消费同一 downloadPageUrl
    end

    QA->>DP: 点「下载 / 安装 App」（按钮禁用约 3s 抑制重复点击）
    DP->>AC: 使用 build.downloadFileUrl（绝对地址）
    AC->>H: GET /api/v1/builds/{id}/file
    H->>S: ResolveFile(id)
    S->>RP: FindByID(id) → relativePath / platform
    S->>PT: safeResolve(relativePath) 前缀校验防穿越
    alt 包不存在 / 文件缺失 / 路径非法
        PT-->>S: 拒绝或不存在
        S-->>H: AppError(404) ⇒ 不计数
        H-->>QA: 404 统一错误 JSON
    else 正常
        PT-->>S: 安全绝对路径
        S-->>H: FileRef(path, contentType, fileName, size)
        H-->>QA: 设置响应头（Android/iOS Content-Type + 双重编码下载名）后 io.Copy 流式传输
        Note over H,FS: ★ 计数时点：流成功完成之后，不在流之前
        alt io.Copy 出错 / 客户端中断 / written != Content-Length
            Note over H: 字节不足或传输失败 ⇒ **不计数**，仅记日志
        else io.Copy 成功且 written == Content-Length
            H->>S: IncrDownloadCount(id)（旁路，2s 短超时）
            S->>RP: IncrDownloadCount(id)
            RP->>DB: UPDATE ... SET download_count = download_count + 1 WHERE id=$1 RETURNING download_count
            alt 计数失败
                DB-->>RP: error
                Note over S: 只记日志，绝不阻断已完成的下载
            else 计数成功
                DB-->>RP: 新值（供后续列表/下载页展示）
            end
        end
        Note over QA: 得到与上传一致的原始包；iOS 第一版仅 .ipa 文件下载，不直接安装
    end
```

---

## 8. ★ 多板块页面位置预留 ★

> 依据 `05-MODULE-ROADMAP.md` §3 与用户追加约束。**「恰好足够」= 只把「位置」预留好**：三段式 Layout + 配置化导航数组 + 路由命名空间约定；**零占位菜单、零空页面、零 TODO、零隐藏入口、零板块开关**。
> ⚠️ **两个易混淆点**：本节的「pushy 热更新包板块」与 §11 的「04-DELTA-PRD push 通知平台预留」是**两件事、两个层次**，都要保留、**不合并**；`pushy.me`（推送通知服务）是同名不同产品，已排除。

### 8.1 三段式 Layout 与配置化导航数组

`web/src/components/Layout.tsx` 固定为**品牌区 + 主导航区 + 内容区**三段；主导航项由**一个配置数组**渲染。

| 项 | 第一版 | 未来 |
| --- | --- | --- |
| 品牌区 | `Dandelion` 产品名，全站固定 | 不变 |
| 主导航数组 | **仅 1 个真实入口**：`{ label: '应用包', path: '/' }` | 数组加 2 项（热更新包 / 其他板块），**平级并列** |
| 内容区 | 上传页 / 包列表页 / 独立下载页 | 各板块自己的页面集合 |

**严禁**：disabled 的「热更新 敬请期待」占位菜单、TODO 注释、空页面组件、隐藏入口、板块开关。**未来新增板块 = 往数组加一项 + 新增路由与该板块页面文件**；`Layout.tsx` 的**三段式结构**、现有三个页面、`api/builds.ts`、`DownloadPage.tsx` **一行不改**（§8.7）。

### 8.2 路由命名空间（★ 硬约束 ★）

| 路径 | 第一版 | 未来 |
| --- | --- | --- |
| `/` | 应用包列表页 | 不变 |
| `/upload` | 上传页 | 不变 |
| **`/d/:id`** | **独立下载页（对外长期可分享地址）** | **★ 永远不能改这个路径 ★** —— 可能被打印在物料上、已转发出去的链接与二维码都依赖它；不得重定向、不得加前缀、不得改变 id 语义 |
| `/updates/*` | 不存在、不建目录 | 未来热更新板块：列表 / 发布 / 灰度 / 回滚 / 版本兼容视图 |
| `/settings/*` | 不存在、不建目录 | 未来其他板块：成员与权限 / 统计看板 / 通知配置 |

> 未来板块用**独立前缀**，与现有三条路径零冲突；`downloadPageUrl` 仍是 `{PUBLIC_BASE_URL}/d/{id}`，二维码内容不受影响。

### 8.3 页面目录组织对比与推荐

| 方案 | 做法 | 评价 |
| --- | --- | --- |
| ✅ **推荐 ① 第一版 `pages/` 扁平** | `pages/{UploadPage,ListPage,DownloadPage}.tsx` | 最简；未来加板块是**一次纯文件移动**（`pages/packages/`），**不改业务逻辑、不改路由、不改组件**，只改 import 路径；与「第一版文件数克制、不建空目录」一致 |
| ② 第一版就按板块分 `pages/packages/` | 现在多一层目录 | 未来零移动，但第一版只有一个板块，目录层级是无意义噪音 |

若团队更偏好 ② 也可采用，两者都满足「未来加板块不改业务逻辑」；**但第一版不得同时建 `pages/updates/` 空目录**。

### 8.4 共享组件下沉

`components/`（`Layout` / `States` / `FileDropzone`）与 `api/client.ts` 是**跨板块复用**的：未来板块应**复用而非复制**（例如热更新包上传同样用 `FileDropzone`、列表空态同样用 `States`）。第一版不为「未来复用」做任何抽象，只保证这些组件本身无板块耦合（不 import `builds.ts`、不写死「应用包」文案）。

### 8.5 下载页不受影响（明确声明）

新增板块**不改** `/d/:id` 页面、**不改** `Build` DTO 的 `downloadPageUrl`、**不改**二维码内容、**不改**下载按钮的 `downloadFileUrl`、**不改**列表/详情/下载接口。下载页是「对外分发地址」，与后台板块导航属于不同关注面。

### 8.6 导航结构示意（现在 vs 未来）

```mermaid
graph LR
    subgraph Now["现在（第一版）"]
        N0["Dandelion 品牌区"] --> N1["主导航：[应用包]"] --> N2["内容区<br/>上传 / 包列表 / 下载页"]
    end
    subgraph Future["未来（示意，非第一版范围）"]
        F0["Dandelion 品牌区"] --> F1["主导航（配置数组加项）"]
        F1 --> F2["[应用包] 不变"]
        F1 --> F3["[热更新包] /updates/*"]
        F1 --> F4["[其他板块] /settings/*"]
        F2 --> F5["上传 / 包列表 / 下载页 /d/:id 永不变"]
        F3 --> F6["列表 / 发布 / 灰度 / 回滚"]
        F4 --> F7["成员与权限 / 统计看板 / 通知配置"]
    end
```

```
现在：  Dandelion ── [应用包]   ── 上传 / 包列表 / 下载页
未来：  Dandelion ── [应用包]   ── 上传 / 包列表 / 下载页（/d/:id 永不变）
                ├─ [热更新包] ── 包列表 / 发布 / 灰度 / 回滚
                └─ [其他板块] ── 成员与权限 / 统计看板 / 通知配置
```

### 8.7 「未来加板块时改哪些文件 / 哪些一行不动」

| 类别 | 文件 | 改动 |
| --- | --- | --- |
| 修改 | `web/src/components/Layout.tsx` 的**导航数组** | 加一项（**三段式结构本身不改**） |
| 修改 | `web/src/App.tsx` | 加 `/updates/*` 等路由前缀 |
| 新增 | `web/src/pages/updates/*`、`web/src/api/updates.ts` | 新板块页面与 API 客户端 |
| **一行不动** ✅ | `Layout.tsx` 三段式结构、`UploadPage.tsx`、`ListPage.tsx`、**`DownloadPage.tsx`**、`api/builds.ts`、`api/client.ts`、`components/States.tsx`、`service/build_service.go`（应用包板块）、`/d/:id` 路径与二维码内容 | — |

---

## 9. 功能页面技术设计

### 9.1 路由表

| 路径 | 页面 | 组件 | 数据获取 |
| --- | --- | --- | --- |
| `/` | 包列表页（P-02） | `ListPage` + `Layout` + `States` | `GET /api/v1/builds?environment=&platform=` |
| `/upload` | 上传页（P-01） | `UploadPage` + `FileDropzone` + `Layout` | `POST /api/v1/builds/stage` + `POST /api/v1/builds` |
| `/d/:id` | 独立下载页（P-03） | `DownloadPage` + `Layout` + `States` + `QRCodeSVG` | `GET /api/v1/builds/{id}` |
| 其他 | 重定向 `/` | — | — |

- `BrowserRouter`；筛选条件写 **URL searchParams**（可分享、刷新保持、返回不丢）。
- 列表→下载页用 `navigate(build.downloadPagePath)`（**相对，唯一用途**）；二维码/复制/下载按钮用**绝对**字段。

### 9.2 组件拆分

| 组件 | 职责 | 是否发请求 |
| --- | --- | --- |
| `App.tsx` | 路由表 + Layout 包裹 | 否 |
| `Layout.tsx` | **三段式**（品牌区 + 主导航区 + 内容区）+ 配置化导航数组（§8） | 否 |
| `FileDropzone.tsx` | 拖拽/点击、回显名与大小、移除、扩展名预检、按扩展名预选平台、**选文件后触发 stage** | 否（回调交给页面） |
| `States.tsx` | Loading / Empty / Error（含重试） | 否 |
| `UploadPage.tsx` | 选文件自动 stage 带出、表单（字段始终可编辑）、提交 `stageToken`、提交中禁用、成功卡片 | 是 |
| `ListPage.tsx` | 四环境切换、平台筛选、列表（含下载次数列）、行点击跳转 | 是 |
| `DownloadPage.tsx` | 详情、二维码、下载按钮、复制地址、下载次数、返回列表 | 是 |
| `api/client.ts` | fetch 封装、统一解包、抛 `ApiError`、FormData 不手动设 Content-Type | — |
| `api/builds.ts` | `stageBuild` / `commitBuild` / `listBuilds` / `getBuild` + **URL 唯一消费处**（不拼 origin） | — |

### 9.3 表单 stage 解析态与字段可编辑（P0-9 落地）

| 步骤 | 行为 |
| --- | --- |
| 1 选文件 | `FileDropzone` 校验扩展名/大小 → 页面进入 `parsing` 态（「解析中…」加载态）→ 调 `stageBuild(file)` |
| 2 带出 | 返回 `StageResult` 后把 `parsed` 4 个值**自动带出**预填表单；**所有字段始终可编辑、不锁定**；`warnings` 显示「部分信息未自动识别，可手动修正」，不阻塞 |
| 3 人工修正 | 用户直接修改任意字段；**表单当前值即最终值**，无需 dirty 标记，提交时服务端不做合并、不覆盖 |
| 4 提交 | JSON 携带 `CommitBuildRequest` 8 字段：`stageToken` + `environment`(默认 `test`) + `platform` + `appName` / `packageId` / `version` / `buildNumber` / `changelog`；**不携带文件、不带解析状态**；提交中按钮 `disabled` |
| 5 成功 | 展示 `downloadPageUrl` + 复制 + 去下载页（`navigate(downloadPagePath)`）+ 返回列表；**第一版不提供上传后编辑页** |
| 服务端 | **不再解析、不覆盖用户值**；仅空字段兜底（§6.6） |

> **`touched` 的定位（避免 QA 误判为违约）**：前端可以用 `useRef` / 局部 state 记录「用户已改动的字段」，仅用于 stage 异步返回时**不覆盖用户已输入的值**（满足 PRD §2.2「用户已改动的字段不被解析结果覆盖」）。它**只是前端局部 UI 状态**：**不进入 API 契约**、**不参与服务端字段合并**、**不出现在 `CommitBuildRequest` 中**；服务端只按表单最终值入库、不做二次解析。

**表单校验**：文件必选且扩展名匹配；`environment` 必选（默认 `test`）；`platform` 必选且与 stage 时扩展名一致（不一致给明确提示）；选填项长度上限；提交中禁用；token 失效时提示重新选文件并自动回到第 1 步。

### 9.4 二维码渲染与地址兜底

- `<QRCodeSVG value={build.downloadPageUrl} size={224} level="M" marginSize={4} />`，黑白高对比、**不嵌 logo**，手机默认相机可识别。
- **只下载页展示**；列表页不做二维码入口。
- **地址兜底**：若 `downloadPageUrl` 为空，或 `PUBLIC_BASE_URL` 被判为仅本机可达（dev 放宽场景），二维码区显示中文提示「当前对外基址不可被手机访问，请配置 PUBLIC_BASE_URL」+ 可复制的地址文本 —— **不渲染错误内容、不静默生成扫不开的码**。
- 「复制页面地址」消费**同一** `downloadPageUrl`；**严禁**用 `downloadPagePath` 生成二维码/复制/下载链接。

### 9.5 下载次数展示与三态

| 类别 | 存放 |
| --- | --- |
| 服务端状态 | 各页面组件内 `useState{data, loading, error}`，不跨页共享、**无全局状态库** |
| 本地 UI 状态 | 表单字段、`parsing`、`stageToken`、提交中、复制成功提示 |
| 筛选状态 | URL searchParams |

| 页面 | 加载 | 空 | 错误 |
| --- | --- | --- | --- |
| 列表 | Loading 占位 | 「当前环境暂无包」+ 去上传 | 中文文案 + 重试 |
| 上传 | **解析中 / 上传中文案**，按钮禁用 | — | 校验就地提示；请求失败显示 `ApiError.message` |
| 下载页 | Loading 占位 | 404 →「包不存在」+ 返回列表 | 查询失败 → 重试 |

**下载次数**：列表页每行新增「下载次数」列；下载页信息区「已被下载 N 次」；两处同一 DTO 字段，允许短暂更新延迟。
**手机视口**：下载页单列居中、`@media (max-width: 640px)` 调整间距、按钮与二维码足够大；扫码打开的绝对地址必须手机可达（§10.4）。

---

## 10. Q1 / Q2 重新正面回答 + 四阶段部署

### 10.1 Q1：前后端代码是否放一起 —— **放一起，但组织方式变了**

**结论：仍同仓 monorepo，取消 npm workspaces**（`api/` + `web/` + `server/` + `deploy/` + `docs/` + 根 `Makefile`）。

| 维度 | 说明 |
| --- | --- |
| 为什么同仓 | v1 同仓为共享 TS 类型；Go 版同仓为**前后端共读同一份 `api/openapi.yaml`**。契约放谁仓里都会产生跨仓同步成本；一条 CI 能按「契约 → 前端 build → 后端 build」顺序产出 |
| 分仓损失 | 枚举/类型要么复制粘贴（正是用户禁止的硬编码漂移），要么发私有包；契约变更要跨仓两个 PR / 两次部署；两次 clone / 两套 CI |
| 组织方式 | `server/go.mod` 是 Go 模块根、`web/` 是唯一 npm 包、**根 `package.json` 与 workspaces 消失**；`Makefile` 编排 `gen:api / dev / build / test / up / down` |
| 环境单一来源 | `api/openapi.yaml` 一处定义；前端生成 `schema.d.ts`（入库）、后端常量 + 契约一致性测试（§6.8）；**DB 不加 CHECK**。**硬指标：全仓搜 `'hotfix'` 只命中 yaml + 生成物 + 契约测试断言** |
| 未来独立部署 / 分仓 | 契约是语言无关的 HTTP 协议文件 ⇒ 把 `api/` 抽成版本化契约仓 / 私有 spec 包，前后端各做 CI 一致性校验；损失从「类型漂移」降为「契约版本对齐」，**业务代码一行不改** |

### 10.2 Q2：如何部署 —— **必须先起 DB，且必须先定对外基址**

> ⚠️ **v1「内网 HTTP 即可跑通全部 P0」作废**：即便内网 HTTP 能跑通上传/列表/下载，**二维码要能被手机扫开**仍要求 `PUBLIC_BASE_URL` 是手机可达的绝对地址（Delta PRD §2.2 / Q2 硬约束）。

### 10.3 四阶段部署

| 阶段 | 做法 |
| --- | --- |
| **① 本地开发** | `docker compose up -d db` → `cp .env.example .env`（**必填 `PUBLIC_BASE_URL` / `DATABASE_URL`**）→ `make dev`（`go run ./cmd/server` :8080 + Vite :5173，Vite `server.host: '0.0.0.0'`、`/api` proxy → :8080）。**必须先起 DB**；前端 dev 不需要 Go、后端 build 不需要 Node |
| **② 单机 / 内网** | `npm --prefix web run build` → `CGO_ENABLED=0 go build -o bin/dandelion ./cmd/server`（`//go:embed public/` 需 dist 先存在）→ systemd `deploy/dandelion.service`（`ExecStart`、`EnvironmentFile` 含 `PUBLIC_BASE_URL`、`After/Requires=postgresql.service`、`Restart=always`）。Go 单二进制托管静态 + **SPA fallback**；只开 8080 或由 Nginx 转 80 |
| **③ 容器化** | 多阶段 Dockerfile（node 阶段出 dist → golang 阶段 `CGO_ENABLED=0 go build` → 运行阶段仅二进制 + 证书）；`docker-compose.yml` 三服务：`db`（`postgres:16-alpine`、`pgdata` 卷、`../server/internal/repository/schema.sql:/docker-entrypoint-initdb.d/01-schema.sql`（DDL 已 `//go:embed` 进二进制、启动幂等执行，**不依赖启动 CWD**；initdb 挂载仅作「DB 就绪即有表」的双保险）、`pg_isready` healthcheck）/ `server`（`depends_on: db: service_healthy`、`DATABASE_URL` + `PUBLIC_BASE_URL`、`uploads` 卷、`/healthz` healthcheck、`restart: unless-stopped`）/ `nginx`（80/443、证书、`client_max_body_size 210m`、`proxy_read_timeout 300s`、**`try_files $uri /index.html` SPA fallback**、`/api/` 反代） |
| **④ 规模化** | 对象存储 + CDN（`relative_path` → objectKey、下载 302 到签名 URL）；读副本 / 连接池；水平扩展前提是「文件与元数据都外置、无本地状态」（第一版单进程有本地卷，不能直接多副本） |

**备份**：`pg_dump -Fc dandelion > backup_YYYY-MM-DD.dump`（恢复 `pg_restore`）+ `tar/rsync storage/uploads/`，同一停写窗口，定时保留 N 天。`storage/tmp/` 不备份（stage 纯临时态）。

### 10.4 ★ `PUBLIC_BASE_URL` 三场景取值与网络可达性 ★

| 项 | 约定 |
| --- | --- |
| 定义 | **对外访问入口基址**（协议+域名/IP+端口），**不等于后端监听地址**；`config/config.go` 唯一读取，`service/build_service.go` 的 `buildURLs()` 唯一消费 |
| 校验 | 必须是绝对 `http(s)://` URL；host 禁止 `localhost` / `127.0.0.1` / `0.0.0.0` / `::1` / 容器内部服务名；**生产环境不合法直接 fail-fast**；开发环境放宽但前端二维码区显示不可用提示（§9.4） |
| 不依赖 | **不读 `Host` / `X-Forwarded-Proto` 拼地址**，避免反代内部地址污染与伪造 |

| 场景 | `PUBLIC_BASE_URL` | 谁提供 `/d/:id` | 手机可达前提 |
| --- | --- | --- | --- |
| **本地开发** | `http://<开发机局域网IP>:5173` | **Vite dev server**（history fallback 提供 SPA；`/api` 经 proxy 到 :8080） | 手机与开发机同 WiFi/同网段；Vite `server.host: '0.0.0.0'`；防火墙放行 5173。⚠️ **不能填 `:8080`**（dev 下 Go 不托管 SPA，手机打开 404），**不能填 localhost** |
| **单机 / 内网** | `http://<内网IP>:8080` 或 `http://dandelion.internal`（Nginx 80） | Go 单二进制 embed 或 Nginx | 手机接入同一内网 / VPN / 内网 DNS；端口放行 |
| **公网 / 容器** | `https://dandelion.example.com` | Nginx（终止 TLS）+ `/api` 反代 | 公网 DNS + 有效证书；手机蜂窝/WiFi 均可（建议 HTTPS） |

**Vite proxy 与 Nginx 反代下如何保持一致**：服务端生成地址**只用 `PUBLIC_BASE_URL`、不读请求上下文**，所以两种拓扑下只要该值配的是**对外入口**，结果就一致 ——
- Vite dev：`downloadPageUrl = http://192.168.1.20:5173/d/{id}`；手机打开后 SPA 内 `/api/v1/...` 请求与 `downloadFileUrl = http://192.168.1.20:5173/api/v1/builds/{id}/file` 都经 **Vite proxy** 落到 :8080，同域可达 ✅
- Nginx：`downloadPageUrl = https://dandelion.example.com/d/{id}`；Nginx 直出静态 + SPA fallback，`/api/` 反代到 server；两个绝对地址同域 ✅
- `proxy_set_header Host/X-Forwarded-*` 仅用于日志/其他用途，**不参与拼地址**。

**手机扫码完整链路**：相机扫 `downloadPageUrl` → GET `/d/{id}` → **谁托管静态谁负责 SPA fallback** → 前端按 id 取详情 → 展示信息与二维码 → 点下载按钮走 `downloadFileUrl` → 流式取包。⚠️ SPA fallback 在单机（Go）与容器（Nginx）**两处都要写**。

**iOS 限制（本次未改变）**：第一版仅提供 `.ipa` 文件下载；浏览器直接安装（P1-4）需 `itms-services` + plist + **公网 HTTPS 域名与有效证书** + 企业签名/Ad Hoc/UDID，故公网 HTTPS 不是第一版硬前提，但一旦勾选 P1-4 则必须升级到阶段 ③。

**环境变量清单**：`DATABASE_URL`（本地 `postgres://dandelion:dandelion@localhost:5432/dandelion?sslmode=disable`，生产 `sslmode=require`）、`PUBLIC_BASE_URL`、`PORT=8080`、`HOST=0.0.0.0`、`STORAGE_DIR=./storage`、`MAX_UPLOAD_MB=200`、`GO_ENV`、`VITE_API_BASE`（默认留空=同源）。配置**仍单点**。

---

## 11. Push 平台扩展点（严格按 ADR-7）

> ⚠️ 与 §5/§8 的「多板块 / pushy 热更新」是**两件事**：本节是「上传完成后把消息推给外部平台」的旁路预留（04-DELTA-PRD §3），不合并、不互相替代。
> **第一版 push 相关代码行数 = 0。**

### 11.1 R1–R5 逐条落地

| R | 要求 | 架构落地 | 第一版是否实现功能 |
| --- | --- | --- | --- |
| R1 稳定唯一标识 | 上传成功即生成全局唯一、生命周期不变 | `builds.id UUID PK`；三个 URL 字段全部由它派生；二维码内容因此生命周期内稳定 | ✅ 本来就是 P0 基础设施 |
| R2 完整元数据 | 上传完成那一刻环境/平台/应用名/包名/版本/构建号/大小/时间/更新说明/**下载页绝对地址**/**下载次数**全部可读 | 唯一 `Build` DTO（契约一处定义）承载；`buildURLs()` 生成绝对地址、`download_count` 入库默认 0；列表/下载页/未来 push 引用同一份 | ✅ P0-8/9/10 的天然结果 |
| R3 机器友好接口 | 行为稳定、自描述、可预测，机器调用不依赖页面 | OpenAPI 3.1 契约 + `/api/v1` + 统一 `{code,data,message}` + **无鉴权/会话/Cookie/CSRF**；`stage` + `commit` 两步均有完整契约描述、**文件只传一次**，CLI/CI 可直接 `curl` | ✅ 契约与接口本身 |
| R4 唯一「上传完成」时点 | 文件与元数据已保存、下载页地址已生成且可访问 | `BuildService.Create()` 中「**rename 成功 + DB 事务提交成功 + `Build`（含绝对地址）构造完成**」之后。不能更早（事务可能回滚→幽灵通知），不能更晚（放 HTTP 层会与响应/重试耦合）。因地址由 id 派生且 SPA fallback 无条件可用，提交成功即地址可访问 | ✅ 只**标注时点**，不写发布代码 |
| R5 订阅维度占位 | 环境 × 平台 × 应用三者独立可区分 | `builds.environment` / `platform` / **`package_id`（= applicationId / CFBundleIdentifier，为空回退 `app_name`）** 三个独立列与 DTO 字段；**不建 apps 实体、不建订阅表、不存接收方** | ✅ 只保证字段结构 |

### 11.2 领域事件边界（仅文档描述，**不落成 Go 类型**）

`build.created`，字段清单（非代码）：`event_id`（消费方幂等去重）、`type`、`occurred_at`（ISO 8601 UTC）、`build.id`、`environment`、`platform`、`package_id`（应用键）、`app_name`、`version`、`build_number`、`file_size`、`download_page_url`、`download_file_url`、`download_count`、`created_at`。
**关键论证**：全部来自现有 `Build` DTO + `PUBLIC_BASE_URL`，**不需要新增任何 DB 字段或预埋结构**就能构造 —— 这是「不预埋也能接入」的原因。

### 11.3 未来接入：新增哪个包 / 改哪 3~5 行 / 哪些文件一行不动

| 类别 | 文件 / 位置 | 改动 |
| --- | --- | --- |
| **新增包** | `server/internal/event/publisher.go` | 发布实现（HTTP POST 到 push 平台 / outbox writer） |
| 新增（可选） | `deploy/migrations/0002_outbox.sql` | 见 §11.4 |
| **改 3~5 行** | `server/internal/service/build_service.go` 的 `Create()` | 事务提交后构造事件 + 调用 publisher（**纯附加旁路，不改函数签名**） |
| 修改（装配层） | `server/cmd/server/main.go` | 初始化 publisher 并注入 service |
| 修改（契约） | `api/openapi.yaml` | 若需订阅/回调 API 则新增路径（不影响既有路径） |
| **一行不动** ✅ | `repository/build_repository.go`、`handler/*`、`parser/*`、`storage/paths.go`、`web/src/pages/*`、`web/src/api/*` 以及**上传/列表/详情/下载四条主流程的输入输出契约与响应格式** | — |

### 11.4 DB schema 演进（★ 第一版不执行、不建表 ★）

```sql
-- 0002_outbox.sql 示意（第一版不执行）
CREATE TABLE outbox_events (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_type         TEXT NOT NULL,
  aggregate_id       UUID NOT NULL,          -- R1: build.id
  environment        TEXT NOT NULL,          -- ┐
  platform           TEXT NOT NULL,          -- ├ R5 订阅维度三元组
  app_key            TEXT NOT NULL,          -- ┘ (= package_id，回退 app_name)
  payload            JSONB NOT NULL,         -- ★ PG JSONB：事件正文字段可演进不改表
  occurred_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  published_at       TIMESTAMPTZ,
  delivery_attempts  INT NOT NULL DEFAULT 0
);
CREATE INDEX idx_outbox_unpublished ON outbox_events (published_at) WHERE published_at IS NULL;
CREATE INDEX idx_outbox_subscription ON outbox_events (environment, platform, app_key, occurred_at DESC);
```
`payload JSONB` + GIN 是选 PG 的长期收益（与 §5.4 `upd_bundles.meta_info` 同理）；第一版 `builds` 表**不加** `published_at` / `event_id` 等任何预留列。

### 11.5 Push 负面清单（硬性）

❌ 不定义 `Publisher` / `Subscriber` / `EventBus` / `EventHandler` / `BuildCreated` 等任何接口、结构体或空实现
❌ 不建 `events` / `outbox` / `subscriptions` / `receivers` 表，不在 `builds` 加 `published_at` / `event_id` 预留列
❌ 不引 Kafka / NATS / Redis / RabbitMQ / MQTT 及任何 SDK
❌ 不写邮件 / IM / Webhook / 短信 / 站内通知代码，不建 `notify` 包，不加 `PUSH_*` / `WEBHOOK_*` 配置项
❌ 不写 `// TODO: 未来接 push` 占位、不做订阅 API / 回调重试 / 死信队列
❌ 不引鉴权中间件、不建用户表、不发 API Key；**不新增任何为 push 准备的表单字段 / 页面 / 配置项**（`PUBLIC_BASE_URL` 属于 P0-8 二维码，不属 push）
✅ 只保留：`/api/v1` 前缀、唯一时点、完整 Build 返回、`main.go` 装配层可注入的构造函数参数、PG 的 JSONB 能力、R1–R5 的字段结构
> R1–R5 属非功能期望，**不进入 P0/P1/P2 需求池、不作为功能验收项**（Delta PRD §3.3）。

---

## 12. 负面清单 + 待明确事项

### 12.1 第一版负面清单（总）

**功能**：iOS 浏览器直接安装；列表页二维码入口；推送 / 通知 / 消息 / Webhook / 订阅 / 回调 / CI 集成 / 开放 API Key / 事件监听；为 push 新增的表单字段 / 页面 / 配置项；登录鉴权与成员体系；密码 / 回答问题安装；有效期 / 定时发布 / 短链；渠道 / 统计报表 / 看板；鸿蒙 / CDN / 多语言 / 大包分发；包删除与上传后编辑；**应用图标解析**；后端二维码接口；热更新包上传 / 管理 / 发布 / 灰度 / 回滚 / 版本绑定 / 差量包；任何多板块功能；**`POST /builds/parse` 只读预览端点 / `ParsePreview` handler / `touchedFields` 请求字段 / `mergeFields` 合并逻辑**；提交接口携带文件；把 `metaSource` 放进请求契约。

**数据 / 多板块**：`apps` / `modules` / `upd_bundles` / `module_type` 判别列 / 板块枚举表；`builds` 上的 `module` / `type` / `future_*` / `published_at` / `event_id` 字段；`events` / `outbox` / `subscriptions` / `receivers` / 通用下载计数表；**第二张表**；SQLite 及任何嵌入式 KV / MySQL / GORM / sqlc / 迁移框架；`environment` 加 CHECK；把四值枚举提升为全平台强制维度。

**工程**：空目录（`pages/updates/`、`event/`、`store/` 等）、空实现、`TODO: 未来接 push` / `TODO: 热更新板块` 占位、禁用占位菜单、隐藏入口、板块开关、插件系统 / 模块注册表 / 动态路由加载；Publisher / EventBus / EventHandler 类型；cgo；外部命令解析（aapt/unzip）；MQ / Redis / Kafka SDK；MUI / Tailwind / Redux / Zustand / React Query / axios；分页；**精确下载去重表或缓存**；Range / 断点续传；前端拼 `window.location.origin`；**用 `downloadPagePath` 生成二维码 / 复制地址 / 下载链接**；`touched` dirty 标记与服务端字段合并；文件树之外的任何新文件。

**✅ 第一版只保留的「预留」**：三段式 Layout + 配置化导航数组（1 项）、路由命名空间约定（`/d/:id` 永不可改）、契约里的共享维度、config 化的存储根目录、§5.2 / §5.4 / §11.4 的**文档中 DDL 示例（标注不执行）**、`/api/v1` 前缀、唯一时点、装配层可注入。

### 12.2 遗留项 / 待明确事项（**均不阻塞第一版**）

| # | 事项 | 归属 | 架构侧默认 / 说明 |
| --- | --- | --- | --- |
| L1 | **真实 APK/IPA 解析成功率实测**（公司真实包 3~5 个：加固包、多渠道包、`applicationIdSuffix`、AAB、加密 IPA、多 `.app`） | 工程师 | **最大风险**；`androidbinary` 的 `Manifest.Package` 是否等于 Gradle `applicationId`（含 suffix）**待验证**；失败一律降级不阻塞 |
| L2 | **`applicationId` 一致性** | 工程师 | 打包后二进制 manifest 的 `package` 是否已含 `applicationIdSuffix`，需真实 debug 包实测；必要时以 `package_id` 回退 `app_name` |
| L3 | **是否要按 IP / 用户做强下载去重** | 产品 | **列 P1，第一版不做**（无账号/会话，强去重需新增表或缓存）；当前用下载按钮约 3s 禁用抑制误触。**计数口径本身已定稿为「流成功完成后 +1」，不再列为待确认** |
| L4 | **Go / PG / 各库小版本** | 工程师 | `chi/v5`、`pgx/v5`、`androidbinary`、`howett.net/plist`、`qrcode.react`、`openapi-typescript` 以实机 `go list -m -versions` / npm tag 为准；**不得凭印象编造 API** |
| L5 | **解析 5s 超时 / 5MB 读取上限对加固包是否合理** | 工程师 | 目标 P95 < 1s；实测校准 |
| L6 | **`PUBLIC_BASE_URL` 手机实机验证** | 工程师 | Vite `0.0.0.0` + 手机同网段扫码打开 `http://<局域网IP>:5173/d/{id}` 全链路 |
| L7 | **域名变更使既有二维码与已转发链接失效** | 产品 | 产品「绝对地址且内容稳定」的固有边界；第一版建议域名一次定稳，未来兼容需短链/重定向（P2） |
| L8 | 下载按钮抑制时长（默认 3s）、二维码 `level` 是否提到 Q（默认 M）、单包上限是否仍 200MB | 产品 | 默认值已可用；200MB 联动 `MaxBytesReader` / Nginx `client_max_body_size 210m` / 解析读取上限 |
| PM Q1 | 「pushy 平台」具体所指：react-native-pushy / 公司自建 / 其它？接入外部还是自建板块？ | 用户 | 未来确认项，不因名称歧义扩大第一版范围 |
| PM Q2 | 热更新包是否与 App 包共用同一「应用」主体？ | 用户 + 架构 | 建议共用（§5.2 回填迁移已铺路），第一版不落地 |
| PM Q3 | 热更新板块「环境」是否复用四值，还是以灰度比例作为发布范围？ | 用户 | 待确认；**四值枚举不提升为全平台强制维度**（§5.3），未来由契约新增维度扩展 |
| PM Q4 | 未来板块是否需要独立权限？ | 用户 | 待确认；第一版不做任何鉴权 |
| PM Q5 | 板块上线顺序与首个「其他功能」板块 | 用户 | 建议热更新包优先；不影响第一版 |
| Q14–Q17 | 推送渠道 / 按环境订阅规则 / API Key / 推送内容 | 用户 | 均为未来场景确认项，不影响第一版（Delta PRD §5） |

---

## 附录 A · 契约一致性验收（硬指标）

1. 全仓（含 Go / TS / SQL / Nginx / 部署配置）搜索 `'hotfix'`，**只允许出现在 4 处**：① `api/openapi.yaml`（契约 SSOT）② `web/src/api/schema.d.ts`（TS 生成物，不手改）③ `server/internal/contract/contract.go`（Go 契约常量镜像）④ `web/src/utils/format.ts`（前端标签镜像）。**后两处必须用中文注释标注「来源：api/openapi.yaml」，并由 `contract_test.go` 校验与 yaml 一致**；**禁止**出现在其他业务代码、SQL 语句、页面组件、部署配置中。
   > 主理人裁决：**不为此 4 值枚举新增代码生成器**——Go 与 TS 运行时都需要四值列表，而 `openapi-typescript` 只产 `.d.ts` 类型、不产运行时常量；需求「简单不冗余」，加生成脚本属过度设计。故接受「4 处字面量 + 后两处注释标注来源 + 契约测试校验」作为等价保障。
2. `make gen:api` 重新生成后 `git diff --exit-code` 通过（CI 校验生成物已入库）。
3. `go test ./internal/contract/...` 通过；**把 yaml 的 `pre` 改成 `pre2` 后测试必须失败**。
4. 全仓搜 `window.location.origin` / `PUBLIC_BASE_URL` 拼接只出现在 `config.go` + `build_service.go`（及「禁止」语境）；搜 `SELECT|INSERT|UPDATE` 只出现在 `build_repository.go`；搜 `storage/uploads` 只出现在 `storage/paths.go`。
5. 增删环境**只改 `api/openapi.yaml` 一处**，DB 不改表，前后端自动跟随。
6. `CGO_ENABLED=0 go build` 成功（无 cgo）；全仓无 `TODO`、无空实现、无占位菜单。

## 附录 B · `api/contract.json` 极简降级方案（**仅说明，不实现**）

若后续认为 OpenAPI + 生成器仍偏重，可把 SSOT 换成 `api/contract.json`：前端 `import contract.json`（TS 原生）、后端 `//go:embed` + 标准库解析，零生成器、零构建顺序，同样做到「只定义一次」。代价：DTO 需手写两份、缺少标准 HTTP 契约描述与 R3 机器可读性。**本次定稿按主理人锁定采用 OpenAPI 3.1，本附录只是演进备选，不落地任何文件。**

## 附录 C · 与产品文档覆盖对照

| 来源 | 项 | 本文 |
| --- | --- | --- |
| 01-PRD | P0-1~7、N1~N9、Q1~Q13 | §6 / §9 / §10 / §12 |
| 04-DELTA-PRD | P0-8 二维码绝对地址 | §6.4、§9.4、§10.4 |
| | P0-9 自动解析 8 字段 + stage 自动带出（表单即唯一真值）+ 失败降级 | §6.5①、§6.6、§7.1、§9.3 |
| | P0-10 下载次数 | §6.7、§7.2、§9.5 |
| | R1–R5、§3.3 负面清单 | §11 |
| | C4 iOS 不进第一版 | §10.4、§12.1 |
| | Q1 / Q2 重新回答 | §10 |
| 05-MODULE-ROADMAP | §3 三段式导航 / 路由位置 | §8 |
| | §4 跨板块共享概念 8 条（应用 App 为稳定主体、下载次数板块特有） | §5.3 |
| | §5 边界负面清单 | §12.1 |
| | §6 Q1–Q5 | §12.2 |
| 用户追加 | 多板块页面位置 + 数据库框架预留 | **§5 + §8** |
| 主理人最终裁决 + 质量关卡 | stage 方案（删除 touched 机制、`CommitBuildRequest`）/ 流成功计数 / `downloadPagePath` 用途限定 | §6.5 / §6.6 / §6.7 / §6.4 |

---

> **配套**：`docs/03-TASKS-v2.md`（有序任务列表，工程师照此实现）。v1 的 `docs/02-ARCHITECTURE.md` / `docs/03-TASKS.md` 已加废弃声明，仅供历史追溯。
