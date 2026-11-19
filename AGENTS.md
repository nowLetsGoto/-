# AGENTS.md · Dandelion 项目全局协作规范

> 面向在本仓库工作的 AI Agent 与工程师。目标：先读本文件，再按"事实来源 → 技术栈 → 分层边界 → 领域不变量 → 变更配方 → 自检"顺序工作。
>
> **生效优先级**：当前代码与测试 > `api/openapi.yaml` > 本文件 > `README.md` > `docs/` 设计稿。
> `docs/02-ARCHITECTURE.md` / `docs/03-TASKS.md` 是废弃 v1；`docs/10`、`docs/11` 含尚未实施的设计提案，不能当作现状。README 第 1 节的早期"明确不做"条款也已被后续实现覆盖。

## 0. 项目一句话

Dandelion 是内部多环境 App 包分发平台：上传 `.apk` / `.ipa`，按项目、环境、平台组织，生成长期可访问、可扫码的下载页；同时包含账号、角色、审计与平台集成管理。

- 仓库根 = 唯一工作目录；`Makefile` = 唯一编排入口。
- `api/openapi.yaml` = 跨语言契约唯一事实来源。
- `server/internal/repository/schema.sql` = DDL 唯一事实来源，编译进二进制，启动幂等执行。
- 不新建第二套配置系统、第二套 HTTP 客户端、第二份权限目录。
- **UI 组件统一使用 `@seakoi/console-kit` 根入口命名导出**；禁止使用缺失 JS 的 `/antd` 子路径。

## 1. 技术栈与命令

| 层 | 技术 |
| --- | --- |
| 后端 | Go 1.24、`net/http` + chi v5、pgx/v5、PostgreSQL 16 |
| 前端 | React 18、TypeScript、Vite 5、原生 CSS 变量；UI 组件统一来自 `@seakoi/console-kit` 根入口命名导出 |
| 鉴权 | PBKDF2-HMAC-SHA256 密码、带版本与模式的 HMAC 无状态令牌 |
| 构建 | 前端产物先写入 `server/public/`，再由 Go `//go:embed public/*` 打成单二进制 |

```bash
make gen:api                        # 修改契约后必做，生成 web/src/api/schema.d.ts
make test                           # Go 全量测试（含契约一致性）
npm --prefix web run build          # 前端类型检查 + 构建
make build                          # 前端 → server/public → bin/dandelion，顺序不可颠倒
make dev                            # 本地 Go :8080 + Vite :5173
```

Go 没有热重载；替换二进制后必须重启进程。前端 dev 通过 Vite 代理 `/api` 到 8080。

## 2. 分层与依赖方向

后端严格单向，禁止反向依赖与循环依赖：

```text
cmd/server/main.go（只装配）
    → internal/app（路由、SPA fallback、中间件挂载）
    → internal/handler（HTTP 语义）
    → internal/service（业务编排）
    → internal/repository（唯一 SQL 出入口）
```

横向约束：

- `handler`：读参数、校验请求形状、调 service、写响应；**不写 SQL、不实现业务规则**。
- `service`：业务规则、事务编排、字段兜底、失败补偿、URL 生成；**不碰 `*http.Request` / `http.ResponseWriter`**。
- `repository`：唯一 SQL 与行映射；**不调 service、不做业务判断、不生成 URL**。
- `internal/contract`：DTO、枚举、权限目录、路由常量；不含业务逻辑。
- `internal/config`：唯一读取环境变量处；其他层不得直接 `os.Getenv`。
- `internal/storage/paths.go`：唯一路径生成与解析处；只算路径，不做 IO。
- `internal/parser`：纯解析；不落盘、不写库、不发网络、不碰 HTTP。
- `internal/authctx`：只在 context 中存读 `contract.Identity`；独立成包是为了打断 `handler ↔ middleware` 的 import 环。
- `cmd/server/main.go`：只负责配置、连接池、注入、启动与退出，不写业务逻辑。

前端严格单向：

```text
pages → hooks → api → client.ts
pages → components（纯 UI） / theme / utils
App.tsx（路由） → pages
main.tsx（挂载） → App
```

- `api/client.ts` 是唯一 `fetch` 封装；业务模块 API 文件是对应 URL 的唯一消费处；页面不拼接口 URL。
- `components/` 不放业务请求、不持有页面级状态；页面级私有组件留在页面文件内。
- `pages/` 之间禁止互相 import；叶子页面自己负责 loading / error / empty 三态。
- `utils/` 只放无副作用纯函数；`theme/antd.ts` 只做 token 映射。
- 全局环境维度只经 `useEnvironment` 读写 URL，不在页面里另存一份。

## 3. 文件与命名

- 每个手写文件顶部用**中文注释**写清四件事：**职责 / 输入 / 输出 / 边界**。
- 一个包或目录只因一个变化原因存在；禁止 `utils.go`、`common.go`、`helpers.go`、`misc.go` 等大杂烩。
- Go：包名小写单数，导出标识符 PascalCase；DB 列 `snake_case`；JSON / TS 字段 `camelCase`。
- 功能跨层实现按功能名归位，例如 `internal/{handler,service,repository}/role/`；某层没有职责时不建空壳。
- `web/src/api/schema.d.ts` 是生成物，**禁止手改**；任何字段变更都从 `api/openapi.yaml` 开始。
- 建议单文件 ≤ 400 行；超过时优先拆职责，而不是增加注释块。
- 禁止空文件、空目录、占位菜单、`TODO` 占位实现；`gitkeep` 仅限 `storage/uploads/**` 的运行时骨架。

## 4. 契约、错误与分页

- 新增/修改接口：`api/openapi.yaml` → Go DTO / `contract.APIRoutes` → `make gen:api` → 前端 API 文件 → 测试。契约测试会比较 YAML 路径数与 Go 路由常量数，漏一处会失败。
- 统一响应外壳 `{code, data, message}`：成功 `code=0`，失败 `code` 等于 HTTP 状态码且 `data=null`；文件下载成功直接返回二进制流。
- 认证失败 401，无权限 403，资源缺失 404，参数非法 400，项目 key 非法 400，方法不匹配 405。
- 列表分页统一 `?page=&pageSize=`；默认 10、上限 200、`pageSize=0` 表示全量。响应平铺 `{items,total,page,pageSize}`。
- 人员、团队、项目走服务端分页；`DataTable` 传 `serverPagination` 即受控分页，不传时退化为前端分页（当前应用包列表是后者）。
- 页码放 URL（`usePageState`），刷新后保持当前页；分页导航用 `replace`，避免后退键在页码间反复跳。

## 5. 核心领域不变量

### 数据分区（单人 / 团队分表）

- 单人模式与团队模式的业务数据**物理分表**：`team` 与 `solo` 两个 schema，各持一套同构的 `projects` / `builds` / `audit_logs`。分表是隔离的唯一手段——越权在表边界上就不可能发生，不依赖每条 SQL 都记得加 owner 条件。
- `users` / `roles` / `role_permissions` / `people` / `teams` / `team_members` / `system_settings` 是跨模式的全局事实（登录发生在选模式之前），留在 `public`，不参与分区。
- `project_members` / `team_projects` 只属于团队分区，外键显式 `REFERENCES team.projects(id) ON DELETE CASCADE`；单人空间没有「成员」与「项目角色」。
- 唯一性按分区各表各定：`team.projects (key)` 全平台唯一；`solo.projects (owner_user_id, key)` 只在本人空间内唯一——两个人各自拥有一个 `web` 互不冲突。
- 代码层的唯一投影是 `repository.Partitioned[T]`：装配层为两个分区各建一份同构仓储，上层用 `.For(mode)` 取用，业务代码里**不出现 `if team / else solo`**。公开链路没有登录态，用 `.All()` 跨分区查找。
- 分区失败安全：`PartitionFor` 只有明确的 `ModeSolo` 才进 solo，其余（含零值）一律 team——宁可给团队数据，也不让人误入单人空间。
- 单人隔离由三层共同承担，缺一不可：
  1. **表边界**：solo 与 team 是不同 schema，跨模式在物理上就访问不到；
  2. **项目 owner 过滤**：`solo.projects.owner_user_id` 由项目仓储的 `ownerScoped` 强制追加；
  3. **包的 owner 过滤**：`solo.builds` 查询同样叠 `owner_user_id` 条件（`BuildRepository.ownerScoped`）。
- **新增/修改 build 查询必须走 `resolveProject(..., ownerUserID, mode)`**：这条既是调用点约定，也是仓储层的兜底——漏写 `resolveProject` 时仓储仍会把 owner 变成 SQL 条件，不会读到别人的包。隔离是仓储的性质，不是调用者的纪律。
- 团队分区的两个 `ownerScoped` 都是 `false`：团队数据本就共享，同一份代码用分区参数而不是 `if team / else solo` 来区分。
- `schema.sql` 幂等且由 `//go:embed` 打进二进制，启动时 `EnsureSchema` 执行一次；存量 `public` 表通过 `ALTER TABLE ... SET SCHEMA team` 迁入分区（外键引用表 OID，不会被打断）。
- **部署约束**：`schema.sql` 必须由应用自己的数据库账号执行。分区与表的属主决定访问权，用别的账号手工跑一遍会造出应用连不上的 `solo` 分区。

### 项目与包

- 项目是一级归属，不是筛选装饰。项目 key 在所属分区内唯一、创建后不可改，并作为 URL 段。
- 项目内页面统一使用 `/p/:key/*` 前缀；环境放查询参数；切项目只换前缀、不丢板块。
- 包列表必须先按项目收敛；`builds.project_id` 必填且有外键与复合索引。
- 下载页 `/d/:id` 与原始文件下载是公开接口：扫码的人没有账号。
- 上传是"一次上传"体验：`stage` 在选文件时自动完成，解析失败只写 `warnings`，不阻塞；`commit` 只收 JSON，不收文件、不收 `metaSource`。
- `metaSource` 由服务端派生；提交接口中的表单字段即使来自解析结果，也以用户最终确认为准。
- `downloadPagePath` 仅用于前端 router 内部跳转；二维码与复制地址只能用绝对的 `downloadPageUrl`；下载按钮用 `downloadFileUrl`。
- 三个绝对 URL 只能由 `service/build_service.go` 的 `buildURLs` 生成，唯一基址是 `PUBLIC_BASE_URL`，不读请求 `Host` / `X-Forwarded-*`。
- 下载次数只在完整流式下载成功且字节数符合 `Content-Length` 后自增；失败旁路不阻断下载。

### 主数据

- 人员 / 团队 / 项目是主数据，关联由两侧各自维护自己那一半（`team_members`、`project_members`、`team_projects`）；编辑一律事务内先清后写、全量覆盖。
- 列表接口直接返回关联的 `{id,name}`；页面不得为了显示名字再挂别人的列表接口。
- 角色权限点目录在 `contract.Permissions`；数据库 `role_permissions` 只存"角色拥有哪些权限"。新增权限点不改表结构。
- 内置角色只在缺失时播种默认权限；用户改过的权限不会被重启覆盖。`super-admin` 权限恒为全量且不可修改。
- **角色管理整块归超管**：`/permissions`、`/roles`、`/roles/{key}`（GET/POST/PUT）五个接口全部挂在 `RequireSuperAdmin` + `RequireTeamMode` 上，普通账号连角色清单都看不到（前端菜单与路由也用 `requireSuperAdmin` / `AdminGate` 拦）。
  - 因此权限目录里**没有** `role.read` / `role.write`：能看角色的人一定是超管，留一个"勾了也判不过"的权限点只会在角色编辑页造出无效选项。`role_permissions` 是裸 TEXT 无外键，删目录项不会自动清库，靠 `schema.sql` 里的幂等 `DELETE` 收尾。
- 角色授予（人员的 `role_key`、项目的 `role_key`）只有超管能改，避免普通编辑者自提权。
- 审计日志只追加，没有 UPDATE / DELETE 接口；`actor_name` 冗余存储，账号改名或删除后历史仍可读。审计写入失败只记日志，不让业务回滚。
- 审计日志的「模式」列不是新增字段：`solo.audit_logs` / `team.audit_logs` 物理分区本身就是模式来源，`repository.Partition.Mode()` 做逆映射后由仓储回填，零迁移。跨模式查询不存在，因为每次会话只查自己那套分区。

### 账号、权限与登录模式

- `users` 表是账号唯一真相；环境变量中的种子管理员只在表为空时写入一次。
- 密码只存 PBKDF2-HMAC-SHA256 派生值，迭代 10 万次，盐随机；任何日志都不记录密码或临时密码。
- 令牌载荷：`账号|版本|到期时间戳|模式.HMAC-SHA256签名`。`token_version` 自增可使已发令牌立即失效；模式参与签名，不能由客户端伪造。
- 权限判定顺序：**超管 → 单人业务基线 → 项目角色 → 人员角色 → 团队默认读取基线**。超管始终全量；团队模式项目角色优先于人员角色。
- **单人模式没有角色区分**：solo 会话固定拥有 `build.read` / `build.write` / `project.read` / `project.write`，因此普通账号也能完整使用概览、应用包、应用（含上传发布）与项目管理（含自建项目）；不查项目角色或人员角色，但平台设置、角色、账号等权限仍不授予。
- 单人模式可以自建项目：`solo.projects.owner_user_id` 记创建者，项目 key 只需在本人空间内唯一。界面按模式裁掉「项目角色」列与表单字段，其余与团队一致。
- 团队模式的默认读取基线是 `build.read` / `project.read` / `directory.read`：前两个支撑业务菜单，`directory.read` 让普通成员能看内部通讯录（人员与团队），但**不含写入能力**；单人模式例外地包含 `build.write` 与 `project.write`，这是业务菜单的完整能力。
- 平台接口刻意忽略 `?project=`，只能用人员角色判定；项目内接口才按 `project` 取项目角色。提交发布时项目在请求体里，因此在 handler 内显式判定 `build.write`。
- 登录模式：`solo`（默认）/`team`，写进签名令牌。
  - `solo`：菜单为概览 / 应用包 / 应用 / 项目管理；只看到自己产生的数据；**上传发布不依赖角色**。
  - `solo` 的项目管理：列表与详情都只涵盖本人的项目（分区 + owner 过滤天然隔离，不靠请求参数），不返回项目角色、关联人员/团队。
  - `solo` + 超管：额外看到账号管理、审计日志、平台集成；账号接口完整可操作。
  - `team`：完整平台视图。
- 前端菜单只是会话投影；服务端 `RequireTeamMode`、`RequireSuperAdmin`、`RequirePermission` 才是安全边界。
- **前端所有写操作入口都按权限点隐藏**，判定统一走 `web/src/utils/permission.ts` 的 `can(me, permission)`，不要在页面里重复写 `me?.permissions.includes(...)`：
  - 「上传包」：`build.write`
  - 「新增 / 编辑人员、团队」：`directory.write`
  - 「新增 / 编辑项目」：`project.write`
  - `me` 尚未加载完（为 `null`）时一律按无权限处理——宁可少渲染一个按钮，也不要先画出来再让它点了报 403。
  - 单人模式不需要在前端分模式判断：它的基线权限已由服务端算进 `permissions`。
- **「超管专属」用 `requireSuperAdmin` / `AdminGate`，不要伪装成权限点**：角色管理就是这种情况（没有 `role.*` 可判）。`NavItem.requireSuperAdmin` 决定菜单是否渲染，`AdminGate` 决定整页能不能进，两者都只是体验层，服务端 `RequireSuperAdmin` 才是边界。
- `/me` 返回 `roleKey` / `roleName` 供账号卡展示「当前角色」；没有角色时两者都是空串，**前端统一显示「普通用户」**，后端不为此造一个假角色。
- 禁用账号、重置密码、禁止自己取消超管、禁止禁用自己、禁止丢掉最后一个超管：这些护栏属于业务层，不能只靠 UI。
- **账号本身带模式**：`users.mode`（`solo` / `team`）记录账号归属哪套数据，**创建时定下、之后不可改**。账号管理列表、详情、编辑、删除都**按当前登录模式筛选**：单人模式只看得到、也只能操作单人账号，反之亦然；跨模式在 `service` 一律判为「账号不存在」，仓储的 `DELETE ... WHERE id=$1 AND mode=$2` 再兜一层，不泄漏另一种模式里有没有这个账号。
- **建账号的模式取自调用者的登录模式，不来自请求体**：`service.Create(ctx, caller, in)` 用 `caller.Mode`（handler 从 `authctx.From(ctx)` 取令牌里的模式）落库，契约 `UserInput` 里**没有 `mode` 字段**，`DecodeJSON` 的未知字段白名单也不含它。账号表单上的模式是**只读展示**（来自 `readSession()?.mode`），用户不能切换——登录时选了哪个模式，就在哪个模式里建账号。
  - `NormalizeAccountMode` 仅剩一个用武之地：种子管理员这类**没有登录上下文**的内部调用。业务请求路径不应再调用它。
- 删除账号：`DELETE /admin/users/{id}`。`users` 没有被任何外键引用（`projects` / `builds.owner_user_id` 均为 TEXT 非 FK，只有 `users.person_id → people(id) ON DELETE SET NULL`），所以删除本身不级联破坏历史数据。
  - **无论账号是什么模式，都必须清理它在 `solo` 分区的项目与包**。`owner_user_id` 是裸 TEXT，账号一旦消失这些数据就成了永久孤儿（库里查不到、界面进不去、磁盘还占着）。**判定依据只能是「这个 id 在 solo 分区有没有数据」，不能是 `users.mode == 'solo'`**：`login.Service.Authenticate` 完全不校验账号模式，一个 `team` 账号照样能以单人模式登录并产出 solo 数据。多清一次只是一次空转（两条 DELETE），少清一次就是永久孤儿。
  - 清理在 `AccountCleanup.PurgeSoloData` 的一个事务里完成，顺序不可颠倒：先删 `solo.builds`（`RETURNING relative_path`）再删 `solo.projects`——`builds.project_id` 是外键且**没有** `ON DELETE CASCADE`，反过来会被约束挡下。
  - 包的 owner 判定覆盖两个来源：`b.owner_user_id`（本人上传）**或** `p.owner_user_id`（挂在本人项目下）。
  - 磁盘文件在事务**提交之后**才删（文件系统没有事务，塞进事务只会造出"库回滚了文件却没了"的假象）；删文件失败只记日志，不把已成功的删除翻成失败。
  - **团队分区的数据不清理**：团队的项目与包属于团队，账号消失后仍应留在团队视图里。这与上一条不冲突——清的是 solo 分区里以该 id 为 owner 的数据，不是团队分区。
  - 审计日志一律不删：它是"只追加"的事实记录，且已冗余 `actor_name`，账号消失后仍可读。
  - 禁止删自己；删在用超管前先确保还有别的超管。删除不可撤销，UI 需同时给出「想暂时停用请改用禁用」的替代路径，并按模式说明清理范围。
- **账号没有自己的角色，角色挂在人身上**：真相来源是 `people.role_key → roles.key`，账号侧通过 `users.person_id` 关联。列表与详情里的 `User.roleKey/roleName` 是仓储两次 `LEFT JOIN`（`users → people → roles`）**JOIN 出来的展示投影**，不是 `users` 表的列。
  - 因此账号编辑页改角色，改的是**绑定人员的档案**；与人员管理页改角色是同一件事，两个入口写同一列。不为了"账号自己的角色"给 `users` 加 `role_key`，那会造出第二个真相来源。
  - `UserUpdate.roleKey` 改角色；`UserInput.roleKey` 在新建时一并写入——两者写的都是 `people.role_key`，`users` 表仍然没有角色列。新建时传空表示"沿用该人员现有角色"；绑定人员为空却传了角色会被拒（角色必须挂在人身上）。
  - **角色分配是一条权限边界，不只是字段校验**：`service/user` 的新建与编辑都会过 `authz.ValidateRoleAssignable`（与人员管理页同一条判据），非超管只能分配权限点不超出自己范围的角色，因此拿不到 `super-admin`。`roleKey` 为空但换了绑定人员时，校验的是**该人员当前的角色**——绑一个带 super-admin 角色的人，等价于把那份权限发出去。
  - **super-admin 对非超管不可见**：`GET /roles` 列表里过滤掉这一行、按 key 查按 404 处理；`GET /admin/users` 列表隐藏 `is_super_admin` 与"角色是 super-admin"的账号，单条读写同样按 404 处理（列表隐藏 + 单条拒绝必须同口径，否则知道 id 就能绕过）。前端只做同一规则的投影：可分配角色 = `role.permissions ⊆ me.permissions`，可绑定人员 = 角色可分配的人。
  - 前端新建账号时非超管没有「未绑定」与「未分配」选项：没有人员的账号没有角色，登录后什么都做不了，不给这条入口比建完再解释更省事。
  - `repository/user.Update` 用**一个事务**同时写 `users.person_id / is_super_admin` 与 `people.role_key`，保证「换绑定人员 + 改角色」不会半截生效。`Create` 同理：建号、绑人、定角色同事务。`UPDATE people` 影响 0 行不算错误（人员合法性已由 `users.person_id` 的外键保证）。
  - 账号编辑页 := `/admin/accounts/new` 与 `/admin/accounts/:id/edit` 复用同一个 `AccountFormPage`；编辑时登录名 `disabled`、不渲染密码输入，回填走 `GET /admin/users/{id}`。
- 修改自己的密码：`PUT /me/password`。`SetPassword` 必然自增 `token_version`，改密成功即等于旧令牌当场失效，所以前端必须清会话回登录页——不要试图换发新令牌把用户留在原地。

## 6. UI 规范

- **UI 组件库统一从 `@seakoi/console-kit` 根入口的命名导出获取**，当前已覆盖 Table / Drawer / Modal / Select / Button / Input / TextArea / Checkbox / Radio / Upload / ConfigProvider / zhCN / QRCode 及表格与主题类型。业务代码 **不直接 import `antd` 或 `qrcode.react`**。
- `console-kit@0.5.16` 的 `./antd` 子入口在 npm 包中**只有 .d.ts、没有 JS**，Vite 无法解析；**禁止使用该子路径**。根入口可用，已通过 `vite.config.ts` 的 `manualChunks['console-kit']` 独立拆包，避免业务代码更新导致组件库重新下载。
- `antd` 仅保留为 console-kit 的 peer 依赖与 Vite 的 chunk 名；新增组件一律先查 console-kit 根入口，没有合适能力才写自定义组件。
- 业务页面不直接散用组件库裸控件：Table / Drawer / Modal / Select 仍经 `components/` 的四个封装使用，便于统一主题、空态、分页与安全交互；ConfigProvider / zhCN 只在 `main.tsx` 装配，ThemeConfig 只在 `theme/antd.ts` 映射。
- 基础按钮/输入控件经 `components/Controls.tsx` 适配：原生 `type` 映射为库按钮的 `htmlType`，输入框 `trim=false`，防止改变表单提交语义或裁剪密码；Checkbox/Radio 用库自带 label，不再外套 label。
- **筛选/切换类 tab 一律用 `Controls.Segmented`**（泛型封装库的 `Segmented<T extends string>`），class 为 `app-segmented`。历史上手写的 `.segment` / `.env-switch` / `.env-option` 已整组删除，其 `align-items: baseline` 正是「字体没有垂直居中」的根因，禁止再引入等价写法。
- 文件选择使用 `Upload.Dragger`，`beforeUpload` 返回 false（非法文件返回 `LIST_IGNORE`），禁止库自动上传；页面仍通过唯一 stage 接口上传一次。
- HTML `form`、路由 `Link`、下载链接及说明性表格保留语义元素，不因引入库而改变提交/导航行为。
- 二维码统一用 console-kit 的 `QRCode`（`errorLevel` / `color` / `bgColor`），不再使用 `qrcode.react`。
- 主题主色 `#0369a1`；改主色必须同步三处：`styles.css` 的 `:root`、`pages/login/login.css` 的 `--login-*`、`theme/antd.ts` 的 palette（antd CSS-in-JS 读不到 CSS 变量）。
- 主色用于可点击/强调文字；不默认加下划线，悬停时加深并显示下划线。高危操作使用琥珀色，不与主色混淆。
- 列表默认服务端分页；空列表交给空态卡片，不挂分页条。数据量少的列表也常驻分页条，避免误以为"不支持分页"。
- 名称类可点击字段（项目名、角色名）作为详情入口，不为同一个详情同时保留"详情"按钮；编辑按钮单独保留。
- 表单页与列表页分开：`/new`、`/:key/edit`；详情优先用抽屉，不为详情单开页面。
- 危险操作必须写清影响面，不只问"确定吗"；轮换 Key、重置密码等不可逆动作要求显式确认。
- 不新增占位菜单或空页面；导航项必须有对应真实页面与接口。

## 7. 变更配方

| 要改什么 | 必须同步 |
| --- | --- |
| 新增接口 | OpenAPI → Go DTO / `APIRoutes` → `make gen:api` → 前端 api 文件 → router → 测试 |
| 给 DTO 加字段 | OpenAPI → `contract` → repository 行映射 → service 赋值 → 前端页面/API → 必要时 DDL |
| 新增环境变量 | `internal/config/config.go` → `.env.example` → `README` / `deploy` 三处 |
| 新增数据库表/列 | `internal/repository/schema.sql`（幂等 DDL）→ repository → service；DDL 不写入 `deploy/` |
| 新增分区业务表 | 在 `schema.sql` 的 `FOREACH s IN ARRAY ARRAY['team','solo']` 里写一次（两分区同构）；`Partitioned` 装配两份仓储；确认 `project_members` 类关联表显式指向 `team.` |
| 新增项目内板块 | 后端按层建同名功能目录；前端 `navigation.ts` 加 `scope:'project'` 项；`App.tsx` 加到 `/p/:key/*` 下；接口带 `project` |
| 新增一级菜单 | `navigation.ts` 加分组/项；`pages/` 新建同名目录；`App.tsx` 加路由；后端权限点同步 |
| 改权限 | `contract.Permissions` → 内置角色默认权限 → 前端权限文案 → 接口上的 `RequirePermission` → 测试 |
| 把某功能收归超管 | 路由挪进 `admin` 组（`RequireSuperAdmin`）→ 删掉对应权限点 → `schema.sql` 幂等清库 → `NavItem.requireSuperAdmin` / `AdminGate` → 清理各页面对该权限点的引用 |
| 引入 UI 组件 | 从 `@seakoi/console-kit` 根入口按命名导出引入；通用控件放进 `components/` 封装，页面不散用裸控件；禁止直接 import antd / qrcode.react |
| 新增 tab / 分段筛选 | 用 `Controls.Segmented`，不要手写 `.segment` 一类样式；选项文案与说明放 `utils/format.ts` 统一供多处复用 |
| 改登录/令牌 | `handler/login`、`service/login`、`middleware/login`、`api/session.ts`、OpenAPI；改载荷格式会让旧令牌失效，需明确告知用户重新登录 |

## 8. 提交前自检

```bash
make gen:api
git diff --exit-code -- web/src/api/schema.d.ts   # 若契约改了，生成物应已入库
make test
npm --prefix web run build
make build
```

人工检查：

- [ ] 契约、代码、生成物一致；没有手改 `schema.d.ts`。
- [ ] handler 无 SQL、repository 无业务、service 无 HTTP、components 无 fetch、pages 无互引。
- [ ] 新增接口已注册路由、鉴权中间件和权限点，不只是写了 handler。
- [ ] 新增列表明确是服务端分页还是前端分页，分页参数已进 URL。
- [ ] 新增 UI 来自 `@seakoi/console-kit` 根入口；没有直接 import antd 或 qrcode.react，也没有使用失效的 `/antd` 子路径。
- [ ] 新增文件有"职责 / 输入 / 输出 / 边界"中文注释。
- [ ] 没有新占位代码、重复实现、第二份权限目录或第二处 URL 拼接。
- [ ] 危险操作有确认与影响面说明；审计记录写入正确。
- [ ] 若改了主色，三处色值已同步。
- [ ] 若改了令牌格式，已验证旧令牌失效并通知用户重新登录。

## 9. 文档索引

- `README.md`：使用说明、快速开始、部署、自检命令。
- `docs/06-FOLDER-LAYOUT.md`：目录分类与边界的详细展开。
- `docs/08-UI-REDESIGN-v3.md`：视觉基线（以当前 CSS 为准）。
- `docs/10-PROJECTS-AND-RBAC.md`：项目化与权限设计稿（含未实施提案）。
- `docs/11-SUPERADMIN-MENU.md`：超管菜单设计（仅已实现部分作为现状）。

遇到文档冲突：先运行 `make test` 与 `npm --prefix web run build`，再读对应模块代码，最后才参考设计稿；不要把"设计稿里写了"等同于"代码里已经有"。
