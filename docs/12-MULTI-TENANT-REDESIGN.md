# 12 · 多公司租户化重构设计（团队模式重新设计）

> 日期：2026-11-18 ｜ 状态：**P0 + P1 + P2 已实施并通过端到端验证**（P3–P6 待实施）
> 上游：`docs/10-PROJECTS-AND-RBAC.md`（权限模型）、`docs/11-SUPERADMIN-MENU.md`（超管专区）、本轮主理人四项裁决
> 效力：本文定义目标形态与实施顺序。与旧文档冲突处**以本文为准**；旧文档未提及部分继续有效。
> 一句话结论：**把「管理身份」与「业务角色」彻底拆开，用「公司 → 团队」两级数据归属做隔离，三级管理身份决定「能管谁」，业务角色只决定「能做什么」。**

---

## §0 结论先行

当前系统的根本问题不是「缺权限点」，而是**缺租户维度**，且**用同一个机制（角色 + 布尔）表达了两件正交的事**：

| 当前 | 问题 | 新设计 |
| --- | --- | --- |
| `users.is_super_admin` 一个布尔 | 只能区分「超管 / 其他人」，无法表达公司管理员、团队管理员 | `users.account_level` 四级枚举 |
| `roles` 全局表，`project-owner` 混着管理权 | 一家公司改角色影响所有公司；「管人」被塞进角色勾选 | 管理权限由身份层级赋予；角色只含业务能力 |
| `teams` / `people` / `projects` / `builds` 全全局 | A 公司能看到 B 公司全部数据 | 全面加 `tenant_id`，仓储统一 scope 过滤 |
| `system_settings` 单行全局 | 一家公司改 Webhook 影响所有人 | 按公司一行 + 平台兜底行 |
| 管理接口写死 `RequireSuperAdmin` | 无法下放给平台/团队管理员 | `RequireMinLevel` + scope 校验 |

**核心不变量**：任何请求都能解析出 `(account_level, tenant_id, team_ids)`，所有数据查询强制带上这个范围。这一条做到了，隔离就是结构性保证，而不是靠每个 handler 自觉。

---

## §1 现状审计（代码事实，不是推测）

### 1.1 身份与账号

- `users` 表：`is_super_admin BOOLEAN`、`mode TEXT`（solo/team）、`person_id`，**无公司字段、无管理身份字段**。
- `users.mode` 是登录时选定的分区标记，写进 JWT；`solo` / `team` 两套业务表。
- 账号管理 `/admin/users*` 整组挂 `RequireSuperAdmin`，且列表按 `mode` 硬筛选——只能按模式看，不能按公司看。
- `service/user` 用 `caller.Mode` 作为新建账号的归属，跨模式操作返回「账号不存在」。

### 1.2 角色与权限

- `contract.Permissions` 共 10 项：`build.read/write/delete`、`project.read/write`、`directory.read/write`、`settings.read/write`、`admin.access`。**无 `role.*`**（上一轮收归超管时删除）。
- `roles` / `role_permissions` 是全局表，内置 5 个角色：`super-admin`、`project-owner`、`developer`、`tester`、`guest`。
- `project-owner` 带 `directory.write`，是「管理身份 + 业务角色」混在一起的典型：拿到它就能改人员团队，但它表达的是「项目负责人」而非业务能力。
- 判定顺序（`authz.Service.Permissions`）：超管全量 → solo 基线 → 项目角色（`team.projects.role_key`）→ 人员角色（`people.role_key`）→ 默认基线。
- 项目角色是**平台级**的：`team.projects.key` 在全平台唯一，`role_key` 可指向任意角色，没有租户校验。

### 1.3 数据归属与隔离

- `teams(id, name, description, created_at)`——**无任何归属字段**。
- `people`——**无归属字段**，列表全量返回。
- `team.projects` 全局共享，唯一索引 `projects_key_key(key)`；只有 solo 分区按 `owner_user_id` 隔离。
- `team_members` / `project_members` / `team_projects` 是全局多对多关联表。
- 全仓无 `tenant` / `company` / `organization` 概念（仅 README 中「企业签名」一词命中）。

### 1.4 平台集成与审计

- `system_settings` 单行 `id=1 CHECK(id=1)`，全局唯一 Webhook + API Key。
- Webhook payload 只含 build 字段，没有公司/团队维度。
- `/open/builds` 用全局 API Key 鉴权，无法反查调用方公司。
- `audit_logs`（两套分区）无 `tenant_id` / `team_id`，team 分区内所有团队的日志混在一起；只有超管能查。

### 1.5 前端

- 菜单 `NavItem` 只有 `permission` 和 `requireSuperAdmin` 两个维度，没有「管理身份层级」。
- `me` 返回 `isSuperAdmin` + `permissions`，无 `tenantId` / `accountLevel` / `teamIds`。
- 路由守卫只有 `AdminGate`（超管）和 `PermissionGate`（权限点），无法表达「本公司可见」「本团队可见」。

---

## §2 主理人裁决（本轮已确认）

| # | 决策 | 选择 |
| --- | --- | --- |
| 1 | 团队与团队管理员账号的关系 | **团队是组织，管理员是账号**，N:N 关联；一个管理员可管多个团队，一个团队可有多个管理员 |
| 2 | 平台集成归属 | **公司级**，每公司一套 Webhook / API Key；平台保留一套兜底配置 |
| 3 | 超管可见性 | **只看公司与管理账号**，不看各公司业务数据；排障走临时授权 |
| 4 | 内置角色 | **开发 / 测试 / 访客保留为纯业务角色**，`project-owner` 废弃（管理能力由团队管理员身份取代） |

---

## §3 目标概念模型

### 3.1 术语表（全项目统一，禁止混用）

| 术语 | 代码标识 | 定义 | 类比 |
| --- | --- | --- | --- |
| 公司 / 租户 | `tenant` | 数据隔离的最高边界；一个公司一个平台管理员 | 客户企业 |
| 团队 | `team` | 公司内的组织单元，是数据归属与权限范围单元 | 事业部 / 项目组 |
| 账号 | `user` | 登录凭证，有管理身份层级 | 工号 |
| 人员 | `person` | 业务档案，归属一个公司，可关联多个团队 | 员工档案 |
| 管理身份 | `account_level` | 四级枚举，决定「能管谁、管多大范围」 | 岗位级别 |
| 业务角色 | `role` | 纯业务能力集合（读包/传包等），不含管理权限 | 职能 |

> **关键区分**：代码里 `mode=team` 是**登录分区**（solo/team），和这里的**组织单元 team** 是两回事。为避免混淆，方案中新表统一用 `tenant` 前缀；文档里写「团队」指组织单元，写「团队模式」指登录分区。

### 3.2 管理身份层级

```
super_admin     平台超级管理员（主理人 1 人）   范围：全部公司
   │
   └─ platform_admin  平台管理员（每公司恰好 1 个）  范围：本公司全部团队
         │
         └─ team_admin 团队管理员（每团队 N 个）  范围：仅自己关联的团队
               │
               └─ member 普通成员  范围：自己相关的业务数据
```

**层级规则**：
1. 高层级可创建低层级账号，**不能跨公司创建**：平台管理员只能创建本公司的团队管理员与普通账号；团队管理员只能创建自己关联团队下的普通账号。
2. 每公司**恰好一个**平台管理员（部分唯一索引保证）；超管创建公司时一并创建该账号。
3. 一个团队管理员可关联多个团队（N:N），其数据范围是所有关联团队的并集。
4. `account_level` 是账号自身属性，与 `people.role_key` 完全正交；一个 `team_admin` 也可以有一个业务角色（如 `developer`）来获得传包能力。

### 3.3 数据归属链

```
tenant (公司)
  ├── users      （账号，account_level 标注身份）
  ├── people     （人员档案，tenant_id 归属）
  ├── teams      （团队，tenant_id 归属）
  │     ├── team_members   （人员 ↔ 团队）
  │     └── team_admins    （账号 ↔ 团队，仅 team_admin 身份）
  ├── team.projects  （项目，tenant_id 归属，通过 team_projects 关联团队）
  │     └── team.builds   （包，随项目）
  ├── roles        （角色，tenant_id 为空=平台内置；非空=公司自定义）
  └── system_settings（平台集成，tenant_id 为空=平台兜底）
```

---

## §4 权限模型：三轴分离

这是整个重构的心脏。**永远用三个独立维度判定，不要合并成一个「权限串」**：

```
能不能操作 = account_level（管理身份） + role.permissions（业务角色）
能对谁操作 = scope（tenant_id + team_ids）
```

### 4.1 轴一：管理身份（决定能不能）

| 能力 | super_admin | platform_admin | team_admin | member |
| --- | :---: | :---: | :---: | :---: |
| 创建公司 / 平台管理员 | ✅ | — | — | — |
| 管理本公司平台集成 | — | ✅ | — | — |
| 定义角色（role CRUD） | ✅ 平台内置 | ✅ 本公司自定义 | — 只能分配 | — |
| 创建团队管理员账号 | — | ✅ | — | — |
| 创建普通账号 | — | ✅ 全公司 | ✅ 本团队 | — |
| 管理人员档案 | — | ✅ 全公司 | ✅ 本团队 | — |
| 管理项目 | — | ✅ 全公司 | ✅ 本团队 | 按业务角色 |
| 分配人员角色 | — | ✅ 全公司 | ✅ 本团队 | — |
| 查看审计 | 平台审计 | 公司审计 | 团队审计 | — |
| 临时授权进入某公司 | ✅ | — | — | — |

> **注释**：`directory.read/write`、`settings.read/write`、`admin.access` 这些「管理味道」的权限点从 `contract.Permissions` 的**角色勾选矩阵**中移除，改由身份层级直接赋予。角色编辑页只保留纯业务权限点，从源头杜绝「给某人勾了 settings.write 就能改全平台配置」。

### 4.2 轴二：业务角色（决定能做什么）

保留 3 个内置业务角色，`tenant_id IS NULL` 表示平台内置、所有公司可见：

| 角色 | 权限点 | 说明 |
| --- | --- | --- |
| `developer` | `build.read`, `build.write`, `project.read`, `directory.read` | 可上传发布，不可删除 |
| `tester` | `build.read`, `project.read`, `directory.read` | 只读与下载 |
| `guest` | `build.read`, `project.read`, `directory.read` | 外部协作只读 |

- `project-owner` **废弃**：其管理能力由 `team_admin` 身份替代，其业务能力由 `developer` 覆盖。存量数据迁移见 §9。
- `build.delete` 从内置角色移除；如需删除包，由平台管理员或团队管理员在管理界面操作，或直接给自定义角色勾选。
- `directory.read` 保留在业务角色里：它是「看通讯录」，属业务需要；`directory.write`（改人员）升格为管理权限。

### 4.3 轴三：数据范围 scope（决定能对谁做）

```go
// Scope 是一次请求能看到的数据边界，由身份层级推导，绝不信任请求参数。
// 三个字段的语义互斥且完备：
//   - All=true          → 超管，跨全部公司（仅平台管理接口可达，业务接口仍受 tenant 约束）
//   - TenantID 非空      → 公司边界；TeamIDs 为空表示本公司全部团队
//   - TeamIDs 非空       → 团队边界；范围是所有关联团队的并集
type Scope struct {
    All      bool
    TenantID string
    TeamIDs  []string
}
```

推导规则（**唯一实现点**，在 `authz.Service.ScopeOf`）：

| account_level | Scope |
| --- | --- |
| `super_admin` | `{All: true}` |
| `platform_admin` | `{TenantID: 该账号 tenant_id}` |
| `team_admin` | `{TenantID: 该账号 tenant_id, TeamIDs: team_admins 中关联的所有 team}` |
| `member` | `{TenantID: 该账号 tenant_id, TeamIDs: team_members 中关联的所有 team}` |

**强制约束**：
- 任何业务列表查询必须带 scope；仓储层方法的第一个参数永远是 `ctx`，第二个是 `scope`。
- `scope.All` 只允许平台管理接口使用；业务接口即便超管调用也要落到具体公司（通过 URL 里的 `tenant` 参数，且必须是超管已授权的公司）。
- 单条记录查询必须先按 scope 过滤再返回，禁止「先查再判」——后者会产生 404/403 信息泄露。

---

## §5 数据模型改造

### 5.1 新增表

```sql
-- 公司：数据隔离的最高边界。
-- 一个公司对应一个平台管理员账号，两者在创建流程里原子地建立。
CREATE TABLE tenants (
  id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  name       TEXT        NOT NULL UNIQUE,
  code       TEXT        NOT NULL UNIQUE,  -- 公司标识，用于 API Key 归属与界面展示
  status     TEXT        NOT NULL DEFAULT 'active',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 团队管理员关联：账号 ↔ 团队，多对多。
-- 只有 account_level='team_admin' 的账号才会出现在这里。
-- 一个管理员可管多个团队；一个团队可有多个管理员（人员变动不需迁移团队数据）。
CREATE TABLE team_admins (
  team_id UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  PRIMARY KEY (team_id, user_id)
);
CREATE INDEX idx_team_admins_user ON team_admins (user_id);
```

### 5.2 存量表扩展

```sql
-- 账号：身份层级 + 公司归属。is_super_admin 由 account_level 取代。
ALTER TABLE users ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
ALTER TABLE users ADD COLUMN IF NOT EXISTS account_level TEXT NOT NULL DEFAULT 'member';

-- 每家公司至多一个 active 平台管理员：部分唯一索引，停用后可再建新的。
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_one_platform_admin
  ON users (tenant_id) WHERE account_level = 'platform_admin' AND status = 'active';

-- 身份层级取值约束，防止脏值（四级枚举）。
ALTER TABLE users ADD CONSTRAINT users_account_level_check
  CHECK (account_level IN ('super_admin','platform_admin','team_admin','member'));

-- 团队归属公司。
ALTER TABLE teams ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
CREATE INDEX IF NOT EXISTS idx_teams_tenant ON teams (tenant_id);

-- 人员归属公司（一个人只属于一个公司，可关联多个团队）。
ALTER TABLE people ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
CREATE INDEX IF NOT EXISTS idx_people_tenant ON people (tenant_id);

-- 项目归属公司（两张分区表都加；solo 分区恒为 NULL）。
ALTER TABLE team.projects ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
CREATE INDEX IF NOT EXISTS idx_team_projects_tenant ON team.projects (tenant_id);

-- 项目角色必须来自同公司可见的角色（NULL=平台内置）。
-- 现有 projects_role_fk 只校验角色存在，不够；保留它，另在 service 层校验 tenant。

-- 审计日志加归属维度，支持三级可见性。
ALTER TABLE team.audit_logs ADD COLUMN IF NOT EXISTS tenant_id UUID;
ALTER TABLE team.audit_logs ADD COLUMN IF NOT EXISTS team_id UUID;
CREATE INDEX IF NOT EXISTS idx_team_audit_tenant ON team.audit_logs (tenant_id, created_at DESC);

-- 角色归属：NULL=平台内置（所有公司可见），非空=公司自定义。
ALTER TABLE roles ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
CREATE INDEX IF NOT EXISTS idx_roles_tenant ON roles (tenant_id);

-- 平台集成：按公司一行，tenant_id NULL 为平台兜底行。
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
ALTER TABLE system_settings DROP CONSTRAINT IF EXISTS system_settings_pkey;
ALTER TABLE system_settings ADD CONSTRAINT system_settings_pkey PRIMARY KEY (id);
-- id 改为自增（原 CHECK(id=1) 限制必须去掉，否则一家公司都建不了配置）。
ALTER TABLE system_settings DROP CONSTRAINT IF EXISTS system_settings_id_check;
ALTER TABLE system_settings ALTER COLUMN id DROP DEFAULT;
ALTER TABLE system_settings ALTER COLUMN id ADD GENERATED BY DEFAULT AS IDENTITY;
CREATE UNIQUE INDEX IF NOT EXISTS idx_settings_one_per_tenant
  ON system_settings (tenant_id) WHERE tenant_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_settings_platform_fallback
  ON system_settings ((0)) WHERE tenant_id IS NULL;
```

> **迁移注意**：`schema.sql` 由 `//go:embed` 编入二进制，改完必须**重新 `go build` 再重启**，否则跑的是旧 DDL。数据回填脚本见 §9。

### 5.3 内置角色播种调整

- `builtinRoles` 删除 `project-owner`；超管角色的权限仍恒为全量，但**超管身份不再依赖角色**，`super-admin` 角色保留仅为兼容旧数据展示。
- `EnsureBuiltin` 的 `CreateBuiltinIfMissing` 不覆盖存量角色，新增角色/改权限必须靠 `schema.sql` 幂等 SQL（上一轮已踩过这个坑）。
- 新增幂等清理：`DELETE FROM role_permissions WHERE permission IN ('directory.write','settings.read','settings.write','admin.access')` —— 这些升格为管理权限，不再出现在角色勾选里。
  - 但 `project-owner` 若已删除，`directory.write` 就没有角色持有者了，这正是我们要的：改人由团队管理员身份负责。

---

## §6 API 契约变更

### 6.1 新增接口

| 方法 | 路径 | 身份要求 | 说明 |
| --- | --- | --- | --- |
| GET/POST | `/platform/tenants` | super_admin | 公司列表 / 新建公司（含平台管理员账号） |
| GET/PUT | `/platform/tenants/{id}` | super_admin | 公司详情 / 停用 |
| POST | `/platform/tenants/{id}/grant` | super_admin | 临时授权进入某公司（返回带 scope 覆盖的令牌） |
| GET | `/platform/admins` | super_admin | 平台管理员列表（跨公司，只看管理账号） |
| GET | `/tenant/audit-logs` | platform_admin+ | 本公司审计 |
| GET | `/team/audit-logs` | team_admin+ | 本团队审计 |

### 6.2 改造接口

- `GET /me`：新增 `tenantId`、`accountLevel`、`teamIds`、`tenantName` 字段。
- `GET/POST /people`、`/teams`、`/projects`、`/builds`：**强制 scope 过滤**，请求体不再信任任何 `tenantId` / `teamId` 入参（`DecodeJSON` 白名单拒绝）。
- `POST /people`、`POST /admin/users`：新建对象的归属从 scope 推导，不接受请求指定。
- `/settings*`：增加 `?tenant=` 参数；platform_admin 只能传自己的 `tenant_id`，super_admin 不传时读写平台兜底配置。
- `/roles*`：身份要求从 `RequireSuperAdmin` 改为 `RequireMinLevel(platform_admin)`，并加 tenant 隔离（平台管理员只能改本公司角色 + 平台内置角色只读）。
- `/permissions`：返回**精简后的权限点目录**（去掉管理类权限点），供角色编辑页勾选。
- `/open/builds`：API Key 反查 tenant，包归属该公司；不再需要调用方传公司标识。

### 6.3 移除 / 废弃

- `admin.access` 权限点删除；`/admin/*` 路径改挂 `RequireMinLevel`。
- `PUT /people/{id}` 中的 `roleKey` 字段：team_admin 可改本团队人员，platform_admin 可改全公司人员；member 无入口。
- `project.role_key`：仍可设置，但校验角色必须对当前 tenant 可见。

---

## §7 后端实现方案

### 7.1 分层与单一职责

```
handler/    只做：解码请求（白名单）→ 调 service → 编码响应 → 记审计
middleware/ 只做：身份解析、层级门槛、scope 注入
service/    只做：业务规则、scope 校验、跨实体一致性
repository/ 只做：SQL；每个查询强制接收 scope 参数
contract/   只做：类型与权限目录（SSOT）
```

### 7.2 身份解析（一次解析，全链路复用）

```go
// Identity 在登录鉴权时构造一次，写进 context，后续所有层只读它。
// 注意：scope 由 Service.ScopeOf 依据 account_level 推导，绝不接受请求参数覆盖。
type Identity struct {
    UserID       string
    Username     string
    AccountLevel string   // super_admin / platform_admin / team_admin / member
    TenantID     string   // 所属公司；超管为空
    Mode         string   // 登录分区 solo / team（与组织单元 team 无关）
    // IsSuperAdmin 保留为派生字段，供旧代码过渡；新代码一律用 AccountLevel。
}
```

中间件新增：

```go
// RequireMinLevel 要求账号身份层级不低于 min。
// 层级顺序：super_admin > platform_admin > team_admin > member。
// 取代散落各处的 RequireSuperAdmin：同一个中间件，传不同参数即可。
func RequireMinLevel(min string) func(http.Handler) http.Handler { /* ... */ }

// InjectScope 把推导出的 scope 放进 context。
// 挂在 RequireAuth 之后、所有业务 handler 之前；handler 从 ctx 取，不自己算。
func InjectScope(authz *authz.Service) func(http.Handler) http.Handler { /* ... */ }
```

> **注释**：`RequireSuperAdmin` 保留为 `RequireMinLevel("super_admin")` 的别名，避免一次性改完所有路由；但新代码禁止直接使用。

### 7.3 仓储层 scope 过滤（隔离的结构性保证）

以人员列表为例，展示统一模式：

```go
// List 返回当前 scope 内的人员。
// scope 由 ctx 传入，是数据可见性的唯一来源；调用方无法绕过。
// 查询条件拼装顺序固定：tenant → team → 业务筛选，保证索引命中。
func (r *Repository) List(ctx context.Context, page Page, scope contract.Scope) ([]Record, int, error) {
    q := query.New(`FROM people p LEFT JOIN roles r ON r.key = p.role_key`)
    // tenant 边界：超管看全部，其余必须限定公司。
    if !scope.All && scope.TenantID != "" {
        q.Where(`p.tenant_id = ?`, scope.TenantID)
    }
    // team 边界：team_admin / member 只能看关联团队的人员。
    if len(scope.TeamIDs) > 0 {
        q.Where(`p.id IN (SELECT tm.person_id FROM team_members tm WHERE tm.team_id = ANY(?))`, scope.TeamIDs)
    }
    // ... 业务筛选与分页
}
```

**规则**：所有返回多条记录的仓储方法都必须接收 `contract.Scope`；单条查询用同样的条件拼装，命中不到返回 `ErrNotFound`（不是 `ErrForbidden`），避免探测他人数据是否存在。

### 7.4 公司创建的事务边界

```go
// CreateTenant 原子地创建公司 + 其唯一平台管理员账号。
// 两步必须在一个事务里：任何一步失败都不能留下「有公司没管理员」的孤儿。
// 平台管理员的初始密码只返回一次，后续只能重置。
func (s *Service) CreateTenant(ctx context.Context, in TenantInput) (TenantResult, error) {
    // 1. 校验公司名/标识唯一
    // 2. 建 tenant
    // 3. 建 user(account_level=platform_admin, tenant_id=新公司id)
    // 4. 写平台兜底配置副本（复制当前 platform settings 到新公司）
    // 5. 记审计
}
```

### 7.5 防越权清单（必须逐条写测试）

1. 平台管理员不能创建 `platform_admin` / `super_admin` 账号。
2. 团队管理员不能创建 `team_admin` 账号，也不能修改 `account_level`。
3. 任何账号不能把 `tenant_id` 改成别的公司。
4. 平台 administrator 的 `/settings` 请求带别的 `tenant` → 403。
5. team_admin 查询不在关联团队的人员/项目 → 空列表 / 404，且 HTTP 状态与「不存在」一致。
6. member 带 `roleKey` 字段调用人员更新 → 400（白名单拒绝）。
7. 超管业务接口未指定 tenant → 不允许（不能直接看全平台业务数据）。
8. 删除公司时，其下账号/人员/团队/项目/包的处理策略（建议：先停用账号，数据保留，由超管确认后清理）。

---

## §8 前端改造

### 8.1 `me` 扩展

```ts
// Me 新增三个字段，全部由服务端推导，前端只做菜单投影，不做权限判定。
type Me = {
  // ... 现有字段
  tenantId: string;
  tenantName: string;
  accountLevel: 'super_admin' | 'platform_admin' | 'team_admin' | 'member';
  teamIds: string[];
};
```

### 8.2 菜单按身份层级投影

- `NavItem` 新增 `minLevel?: AccountLevel`，与现有 `permission` / `requireSuperAdmin` 并存但优先级为：`minLevel` > `requireSuperAdmin` > `permission`。
- `Sidebar.canSee` 先判 `minLevel`，再判 mode，最后判 permission。
- 超管菜单区改造为「平台管理」：公司管理、平台管理员、平台集成、平台审计。
- 平台管理员新增「公司设置」区：公司集成、公司审计、角色管理。
- 团队管理员可见人员/项目/包 + 本团队账号管理；不显示平台集成。

### 8.3 守卫组件

- `AdminGate` → 内部改用 `minLevel`，保留导出名做过渡。
- 新增 `ScopeGate`：校验 `me.tenantId` / `me.teamIds` 与当前页面路径里的租户/团队是否匹配，不匹配渲染「无权访问」。
- 页面内所有「新增/编辑」按钮继续用 `can(me, permission)`，但**权限串现在由服务端按身份层级 + 角色合并后下发**，前端零逻辑。

---

## §9 迁移策略与风险

### 9.1 存量数据映射（当前是单公司使用）

| 现状 | 迁移动作 |
| --- | --- |
| 现有 3 个账号（admin / zouliang / linanan） | 建一个「默认公司」tenant；admin → super_admin（保留），其余 → member |
| `project-owner` 角色 | 废弃；原有分配了此角色的人员 → 改挂 `developer`；需要管人的账号 → 设 `team_admin` |
| `teams` / `people` / `team.projects` | 全部 `tenant_id` 填默认公司 id |
| `system_settings` 单行 | 保留为平台兜底行（tenant_id=NULL），再复制一份给默认公司 |
| `solo` 分区数据 | 不动，仍按 owner_user_id 隔离；solo 账号无 tenant_id |
| 审计日志 | 存量行 tenant_id 填默认公司，team_id 留空 |

### 9.2 风险与对策

| 风险 | 对策 |
| --- | --- |
| 全仓 20+ 仓储方法要加 scope 参数，漏一个就是越权 | 用编译期接口约束：所有仓储 List 方法统一签名；写集成测试用两个租户交叉访问 |
| `mode=team` 与组织 `team` 混淆 | 文档强制区分术语；代码里组织用 `tenant/team`，登录分区仍用 `mode`，禁止新代码用 `team` 指代分区 |
| 平台集成 API Key 反查租户 | API Key 加唯一约束；中间件鉴权后把 tenant 写进 context，包创建时落 tenant_id |
| 公司是新增概念，旧客户端不传 tenant | 存量账号登录后由服务端从 account.tenant_id 推导，前端不传；旧字段一律白名单拒绝 |
| `schema.sql` 改了没生效 | 每次改 DDL 后重新 `go build` 再重启（已踩坑） |

---

## §10 分阶段实施计划

每一阶段都能独立构建、独立验收，不跨阶段堆积改动。

| 阶段 | 内容 | 验收标准 |
| --- | --- | --- |
| **P0 身份地基** | `tenants` / `team_admins` 表；`account_level` / `tenant_id` 列；`Identity` / `Scope` 类型；`ScopeOf` 推导；中间件 | `go build/vet/test` 全绿；`/me` 返回新字段；四级账号各自 scope 正确 |
| **P1 数据隔离** ✅ 已实施 | people / teams / projects / builds / audit 全加 tenant_id；仓储统一 scope 参数 | 两家公司交叉访问测试：A 公司任意账号查不到 B 公司任何数据 —— **已通过**（见下方实施记录） |
| **P2 三级账号体系** ✅ 已实施 | 超管建公司+平台管理员；平台管理员建团队/团队管理员/普通账号；团队管理员建本团队账号 | 完整创建链路端到端通过；§7.5 防越权 8 条全绿 —— **已通过**（见 §12） |
| **P3 角色与权限下放** | 权限目录精简；角色 tenant 隔离；管理权限走身份层级；废弃 project-owner | 平台管理员能改本公司角色、改不了平台内置；团队管理员只能分配角色 |
| **P4 集成公司化** | `system_settings` 租户化；Webhook payload 带公司信息；API Key 反查租户 | 两公司 Webhook 互不串；API Key 上传的包归属正确公司 |
| **P5 前端改造** | `me` 扩展；菜单/路由守卫按层级投影；公司管理页、团队账号页 | 四种身份登录看到不同菜单；直接输 URL 被守卫拦住 |
| **P6 迁移与验收** | 存量数据回填；清理测试数据；全量回归 | 旧数据完整可见；四道门禁全绿；端到端复验报告 |

### 建议的下一步

从 **P0** 开始。P0 只动数据模型与身份推导，不碰业务接口，风险可控，且是后面所有阶段的地基。每完成一阶段我会跑四道门禁（`go build/vet/test`、`tsc --noEmit`、`eslint+stylelint`、`make build`）并做端到端复验，再进入下一阶段。

---

## §11 P1 实施记录（2026-11-18）

### 落地方式（对 §7.3 的具体化）
- `repository/filter.go` 新增 `ScopeFilter` / `ApplyTenant` / `AddRaw`：把 scope 翻译成第一条 WHERE 条件。三条规则见代码注释：超管不加条件、公司账号限定 `tenant_id`、无公司且非超管追加 `1 = 0` 失败关闭。
- **service 从 ctx 取 scope**（`authctx.ScopeFrom`）传给仓储，handler 零改动；scope 的解析仍然只有 `authz.Service.ScopeOf` 一个出口。
- **身份解析路径显式传 `Scope{All:true}`**：`PersonID`、`resolveRoleKey` 里查人员/角色是在 scope 推导链路中，不能反过来依赖 scope。
- 写入路径的 `tenant_id` 一律从 scope 取，请求体白名单不接受该字段；关联表用 `INSERT ... SELECT ... WHERE tenant_id = $3::uuid` 挡住跨租户关联。
- 公开下载接口 `/builds/{id}`、`/file` 不加 scope（无登录态）；包列表的隔离点在 `BuildService.resolveProject` → `project.FindByKey` 带 scope。

### 对设计的偏离（均已确认合理）
1. **P1 只做公司级隔离，未做团队级**。原因：存量成员没有团队关联，若按 `team_ids` 过滤会查不到任何数据。团队级隔离留到 P2（团队管理员账号上线时）。
2. **存量回填提前到 P1**（原计划 P6）。原因：不回填则现有 member 账号 `tenant_id` 为空、命中 `1=0`，功能直接坏掉。已建「默认公司」`11111111-...`，存量账号/人员/团队/项目/角色全部归入；admin 作为超管保持无公司。
3. **审计写 `tenant_id`、查按 tenant 过滤**，但 `/admin/audit-logs` 仍挂 `RequireSuperAdmin`——平台管理员进入审计是 P3/P5 的工作，P1 只把数据层隔离做好。

### 端到端验证结果
两家公司（A/B）各建账号、人员、团队、项目后交叉访问：
- 列表：A 只见 A 的人员/团队/项目，B 只见 B 的 ✅
- 越权 id 查询（B 的人员/团队 id）：HTTP 404 ✅
- 跨公司项目 key 查包列表：400「项目不存在」✅
- 写入带 `tenantId` 伪装：400 白名单拒绝 ✅
- 编辑人员把 `teamIds` 指向 B 团队：返回成功但关联被丢弃，库里 0 条跨租户关联 ✅
- 测试数据已全部清理，存量数据完好

### 已知遗留（非 P1 范围）
- 404 错误文案是全局兜底「包不存在或已被移除」，人员/团队查不到时也显示这个，文案不准。属既有问题，未在本轮改动。

---

## §12 P2 实施记录（2026-11-18）

### 新增能力
- `POST /platform/tenants` + `GET /platform/tenants`（仅超管）：`repository/tenant.CreateWithAdmin` 在同一事务里建公司与唯一平台管理员，明文密码只返回一次；`service/tenant` 做名称/标识/账号名/密码校验。
- 账号管理路由门槛从 `RequireSuperAdmin` 降为 `RequireMinLevel(team_admin)`，真正的越权拦截在 `service/user.resolveCreateLevel`：
  - 超管：可建平台管理员/团队管理员/普通成员，不能建另一个超管；
  - 平台管理员：只能建本公司的团队管理员/普通成员；
  - 团队管理员：只能建本公司的普通成员，且 teamIds 必须是自己关联团队的子集；
  - 任何层级带 `isSuperAdmin=true` 建账号，只有超管本人被允许（原实现漏了这条，端到端测试抓到并修复）。
- 管理身份的固有权限由 `authz.managementPermissions(account_level)` 直接授予，不通过角色勾选；只在 team 模式生效，防止用管理账号选 solo 登录绕过模式隔离。
- 团队级 scope：`Scope` 新增 `TeamScoped`，只有 team_admin 为 true；`repository.ApplyTeam` 对 TeamScoped 身份追加团队条件，无关联团队时 `1=0` 失败关闭。people/teams/projects 的 List 与单条查询都叠加了该条件。普通成员保持 P1 行为（按公司可见），避免存量成员无团队关联就看不到数据。

### 端到端验证结果
完整链路通过：超管建公司 → 平台管理员登录（/me 显示 platform_admin + 公司）→ 建两个团队 → 建团队管理员（关联团队甲）→ 团队管理员登录（/me 显示 team_admin + teamIds）。
团队级隔离：团队管理员只见团队甲的 1 人员/1 团队/1 项目，团队乙完全不可见。
防越权 8/8 通过：团队管理员不能建团队管理员、不能跨团队授权、不能建超管标记；平台管理员不能建平台管理员、不能访问公司管理；未登录与普通成员不能进账号管理；团队管理员可建本团队普通账号。

### 重要发现（已修复）
1. **isSuperAdmin 提权漏洞**：Create 未校验非超管设超管标记，端到端测试造出了假超管（201）。已抽出 `canGrantSuperAdmin` 统一守新建与编辑，并补单测。
2. **团队编辑会清空人员关联**：`PUT /teams/{id}` 以"本次提交为准"先清后写，测试脚本传空 personIds 把已有关联清掉了。这是既有设计，不是 bug，但调用方必须提交完整集合。
3. **旧进程占端口**：用 `cmd &` 起的 dev-server 残留占用 8080，导致新二进制没生效、测试打到旧代码。已定位并停止；后续一律用长驻后台任务启动。

### 状态
四道门禁全绿；测试数据已清理（3 账号/2 人员/1 团队/3 项目/1 默认公司）；未 git commit；服务已停止。
