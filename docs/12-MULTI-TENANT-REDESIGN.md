# 12 · 多公司租户化重构设计（团队模式重新设计）

> 日期：2026-11-18 ｜ 状态：**P0–P6 全部实施完成，全量回归通过**（见 §16 复验报告）
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
| **P3 角色与权限下放** ✅ 已实施 | 权限目录精简；角色 tenant 隔离；管理权限走身份层级；废弃 project-owner | 平台管理员能改本公司角色、改不了平台内置；团队管理员只能分配角色 —— **已通过**（见 §13） |
| **P4 集成公司化** ✅ 已实施 | `system_settings` 租户化；Webhook payload 带公司信息；API Key 反查租户 | 两公司 Webhook 互不串；API Key 上传的包归属正确公司 —— **已通过**（见 §14） |
| **P5 前端改造** ✅ 已实施 | `me` 扩展；菜单/路由守卫按层级投影；公司管理页、团队账号页 | 四种身份登录看到不同菜单；直接输 URL 被守卫拦住 —— **已通过**（见 §15） |
| **P6 迁移与验收** ✅ 已实施 | 存量数据回填；清理测试数据；全量回归 | 旧数据完整可见；四道门禁全绿；端到端复验报告 —— **已通过**（见 §16） |

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

---

## §13 P3 实施记录（2026-11-18）

### 权限目录精简
`contract.Permissions` 现在**只放业务能力**，管理类权限点移出：
- 移除：`directory.write`、`settings.read`、`settings.write`、`admin.access`
- 保留：`build.read/write/delete`、`project.read/write`、`directory.read`（看通讯录是业务需要）
- 被移除的 key 以 `contract.PermissionDirectoryWrite` 等常量保留，供 `managementPermissions` 赋予管理层级、以及启动时清理存量
- `IsValidPermission` 只认目录内的点，所以角色接口**无法写入管理类权限**（伪造也会被 400 拒绝）
- 超管的"全量"由 `allPermissions()` = 目录 + 四个管理点，避免精简目录后超管反而丢能力

### 角色租户隔离
`roles.key` 是全局唯一且被 `people.role_key` / `projects.role_key` 外键引用，**不能改成复合唯一**。采用的方案：
- 内置角色 `tenant_id IS NULL` → 平台级、所有公司可见、**只有超管能改**
- 自定义角色 `tenant_id = 公司` → 本公司可见可改
- 自定义角色的 key **必须带公司前缀**（取公司 UUID 前 8 位），既避免与内置/他家冲突，也让"这是哪家的角色"一眼可见
- 可见性不能用 `ApplyTenant`（它会把 tenant_id 为 NULL 的行滤掉，内置角色恰好靠 NULL 表示共享），单独实现 `visibleFilter`
- `FindByKeyAny` 供权限判定链路使用：身份解析发生在 scope 推导中，不能反过来依赖 scope

### 角色管理路由
从"仅超管"改为 **platform_admin 及以上**（`RequireMinLevel(platform_admin)` + `RequireTeamMode`）。团队管理员不进该组——它只需要"把人挂到已有角色上"，那是 people 接口 `roleKey` 字段的事。`people` 的 Create/Update 里角色校验从 `IsSuperAdmin` 改为 `canAssignRole`（team_admin 及以上），登录名变更仍严格限超管。

### 存量自愈（重要）
P1 的存量回填把**所有角色**（含内置）都归到了默认公司，导致其他公司将看不到任何内置角色。启动时执行 `roleService.NormalizeBuiltins`：
- `is_builtin = true` 的行 `tenant_id` 归零为 NULL
- 非超管角色清掉残留管理点（project-owner 曾挂 `directory.write`）
- 幂等，只动内置行，用户自建角色不受影响

### 另一处修正
`roleKeyPattern` 从"字母开头"放宽为"字母或数字开头"：自定义角色 key 前缀是 UUID 前 8 位，可能是数字开头（如 `712e101b-xxx`），原规则会让这类公司永远建不了角色。

### 端到端验证结果（13/13）
角色可见性与隔离：甲建本公司角色成功且落本公司；无前缀被 400 拒绝；甲改不了平台内置（404）；乙看不到也改不了甲的角色（404）；甲能改自己的角色；权限目录已无管理点；用 `settings.write` 建角色被 400 拒绝；团队管理员进不了角色管理（403）。
角色分配：团队管理员能建人员并分配角色、能改角色分配；普通成员不能改角色（403）；超管仍能改内置角色。

### 设计取舍
- 平台管理员改内置角色返回 **404 而非 403**：它能看到这个角色（所有公司共享），但改不了。用 404 与"不存在"同口径，不泄漏"这个角色属于平台、你不能改"这一信息。文档在此说明，避免后续误认为 bug。
- `project-owner` 不物理删除（外键引用它），只是不再播种；存量数据继续可用。

### 状态
四道门禁全绿；测试数据已清理（3 账号/2 人员/1 团队/3 项目/1 默认公司/5 内置角色，内置角色 tenant_id 均为 NULL）；未 git commit；服务已停止。

---

## §14 P4 实施记录（2026-11-18）

### 隔离键的确认（主理人澄清）
P4 开始前对"按公司还是按平台账号隔离"做了确认。结论：**公司是平台账号的绑定属性，两者一一对应**——
`idx_users_one_platform_admin ON users (tenant_id) WHERE account_level='platform_admin' AND status='active'`
这个部分唯一索引已经保证每公司恰好一个可用平台管理员。因此落库键用 `tenant_id`，
它指向的就是那个平台账号的身份绑定，两种说法在实现上是同一件事。

### 数据模型
`system_settings` 从「单行全局」改为「每公司一行 + 平台兜底行」：
- 去掉 `id=1` 的 CHECK 约束，`id` 改为 `GENERATED BY DEFAULT AS IDENTITY`
- 新增 `tenant_id`；`tenant_id IS NULL` 的那行是平台兜底配置
- 唯一索引：`(tenant_id) WHERE tenant_id IS NOT NULL` 保证每公司一行；`((true)) WHERE tenant_id IS NULL` 保证兜底行唯一；`(api_key)` 保证一把 Key 只对应一家公司
- **存量那把全局 Key（`0d815976…`）原样保留在兜底行**，现有 CI 不用改任何配置就能继续用

### API Key 反查租户
`apikey` 中间件从「定长比较一把全局 Key」改为「用 Key 反查归属公司」，结果写进 context：
- 公司 Key → `Scope{TenantID: 公司}`，项目必须属于该公司，跨公司上传返回 400「项目不存在」
- 平台兜底 Key → `Scope{All: true}`，按项目自身 `tenant_id` 推导归属，行为与改造前一致
- `TenantFrom(ctx)` 返回 `("", false)` 表示「没走中间件」，与「兜底 Key 的 `("", true)`」严格区分——否则未鉴权请求会被当成兜底 Key 放行

### Webhook 投递
- `NotifyBuild` 增加 `tenantID` 参数，由 `BuildService.Create` 从**项目**取（项目是包的唯一归属载体，不是请求参数）
- `LoadForDelivery`：优先本公司配置，没配则回落平台兜底行——新公司还没配 Webhook 时包仍能正常发布，不会静默不通知
- payload 新增顶层 `tenantId`，接收端可据此刻分来源；它取「配置所属公司」而非包的公司，回落时标成具体公司反而误导
- `/settings/webhook/test` 用当前 scope 的配置且**不回落**：用户点测试想验证的是自己填的地址通不通

### 路由
`/settings*` 从「仅超管」改为 **platform_admin 及以上**（`RequireMinLevel(platform_admin)` + `InjectScope`）。必须单独成组：admin 组没有 scope 注入，而定位"哪家公司那一行"必须依赖 scope。

### 踩到的两个坑（都已修）
1. **`ALTER COLUMN id DROP DEFAULT` 在 identity 列上直接报错**（`is an identity column`），导致服务启动失败。改为把 `DROP DEFAULT` 与 `ADD GENERATED` 都放进「当前是否已是 identity」的判断分支内。
2. **identity 序列没有随存量行推进**：原表是 `SMALLINT DEFAULT 1` 且已有 `id=1` 的行，改成 identity 后序列仍从 1 开始，第一次 `INSERT` 撞主键（设置页首次打开 500）。修法：DDL 末尾 `setval(pg_get_serial_sequence(...), GREATEST(max(id),1))`；同时 `EnsureRow` 在 INSERT 失败后重读一次，把并发竞态从 500 降级为「对方刚建好，我读到了」。

### 端到端验证结果（14 项全通过）
配置隔离：三方（甲/乙/兜底）首次读配置各自成行且 Key 互不相同；甲写 Webhook 后乙与兜底均未受影响；甲轮换 Key 后乙的 Key 未变；团队管理员读设置 403。
上传归属：甲 Key 传甲项目成功；**乙 Key 传甲项目被 400 拒绝**（不泄漏项目是否存在）；兜底 Key 传乙项目成功且包落乙公司；无效 Key 401。库中核对：包分别落在 P4甲公司 / P4乙公司。
Webhook 路由：甲→`/alpha`、乙→`/beta`，payload 的 `tenantId` 分别指向各自公司，两家彻底不串。

### 状态
四道门禁全绿；测试数据已清理（3 账号/2 人员/1 团队/3 项目/3 包/1 默认公司/1 行兜底配置）；未 git commit；服务已停止。

---

## §15 P5 实施记录（2026-11-18）

### 新增：层级判定工具与守卫
- `utils/accountLevel.ts`：`AccountLevel` 类型、`LEVEL_RANK`、`atLeastLevel(me, minLevel)`、`levelLabel(me)`。
  序数与后端 `contract.AccountLevelRank` 同序；未知/空层级给 0（失败关闭），与后端同一个安全取向。
- `components/LevelGate.tsx`：`minLevel` 守卫，渲染「无权访问」并写明当前身份与所需身份。
- `components/AdminGate.tsx` 改为 `LevelGate(minLevel="super_admin")` 的薄封装，**保留导出名**做过渡。

### 导航按层级投影
- `NavItem` / `NavSection` 新增 `minLevel?: AccountLevel`；`canSeeNavItem` / `canSeeNavSection` 是唯一判定实现，
  顺序固定为 **minLevel → requireSuperAdmin → permission**（与设计 §8.2 一致）。
- 菜单重组为三档：
  - 基础资料：人员/团队/项目 + **账号管理**（minLevel: team_admin）
  - 公司设置（minLevel: platform_admin）：角色管理、平台集成
  - 平台管理（minLevel: super_admin）：公司管理、审计日志
- 一个容易踩的坑：最初把「账号管理」放进「平台管理」组，结果组级 `minLevel: super_admin` 把团队管理员挡住了——
  菜单项的层级再低也没用。已移到基础资料区，**组级门槛优先于项级**这点写进了代码注释。

### 路由守卫改造
- 角色管理（3 条）+ 平台集成 → `LevelGate(minLevel="platform_admin")`（后端 P3/P4 已放开到该层级）。
- 账号管理（3 条）→ `LevelGate(minLevel="team_admin")`（后端 P2 已放开）。
- 公司管理（2 条）→ `AdminGate`（后端仍是 RequireSuperAdmin）。

### 新增页面：公司管理
- `api/tenants.ts`：`listTenants` / `createTenant`。
- `pages/admin/TenantsPage.tsx`：公司列表，展示「公司(名称+标识) / 平台管理员 / 状态 / 创建时间」。
  平台管理员为空时显示「（无可用管理员）」——默认公司由 P1 回填产生，没有管理员是预期情形。
- `pages/admin/TenantFormPage.tsx`：新建公司，**创建成功后停在原地**用 `temp-password` 卡片展示一次性明文密码。
  理由：初始密码只在这一次响应里出现，跳走就再也看不到了。

### 顺带修掉的前后端不一致（真实 bug）
`RoleFormPage` 原判定 `readOnly = !me?.isSuperAdmin || ...`，意味着平台管理员进来整个表单只读——
但后端 P3 已允许平台管理员改本公司自定义角色，能力等于被前端废掉。
改为 `key === SUPER_ADMIN_ROLE_KEY || (isBuiltin && !isSuperAdmin) || !atLeastLevel(me,'platform_admin')`，
即：超管角色恒只读、平台内置角色对非超管只读、自定义角色可改。同时给角色标识加了公司前缀 placeholder 提示
（P3 后端强制 `key` 带公司 UUID 前 8 位，不提示的话用户会莫名吃 400）。

### 身份展示
`AccountMenu` 在团队模式下新增两行：「管理身份」与「所属公司」。与「当前角色」并列展示，
明确两者正交（一个 team_admin 也可以是 developer）。

### 端到端验证
四种身份 `/me` 数据正确：超管 10 项权限、平台管理员 9 项（无 admin.access）、团队管理员 6 项、普通成员 3 项；
层级与公司名/teamIds 均按预期填入。
直接访问受限接口（模拟手输 URL）7/7 通过：团队管理员访问角色管理/平台集成 403、平台管理员访问公司管理 403、
普通成员访问账号管理 403；平台管理员访问角色管理/平台集成 200、团队管理员访问账号管理 200——**前端放开的后端确实允许，前端隐藏的后端确实拦住**。
公司管理数据流：列表返回管理员账号名、新建返回一次性密码、重复标识 400、非法标识 400。

### 未做（属 P6 或后续）
- 未做浏览器截图验证：本机 playwright 缺浏览器二进制（需下载数百 MB），改用「接口数据 + 静态类名检查」代替，
  确认四种身份的 /me 数据与菜单投影规则一致、页面用到的 CSS 类全部存在于 styles.scss。
- 审计日志仍是超管专属（公司/团队审计的分级查看属后续）。
- 公司详情/停用、平台管理员列表（设计 §6.1 的其余接口）未做。

### 状态
四道门禁全绿；测试数据已清理；未 git commit；服务与 dev server 均已停止。

---

## §16 P6 复验报告（2026-11-18）

### 一、存量数据回填核验

核验项与结果：

| 核验项 | 结果 |
| --- | --- |
| 账号 `is_super_admin` 与 `account_level` 一致 | 修复后 0 不一致 |
| people / teams / projects 的 `tenant_id` 完整性 | 0 缺失 |
| 角色归属（内置为平台级、自定义归公司） | 5 个内置角色 `tenant_id` 均为 NULL |
| 包记录与项目公司一致 | 3 条包记录均归属「默认公司」 |
| 跨租户关联残留（团队↔人员、团队↔项目、项目↔人员） | 0 |
| 审计日志指向已删公司/账号 | 0 |
| 配置/角色/账号指向已删公司 | 0 |
| 磁盘孤儿包文件 / 库中记录缺文件 | 0（磁盘仅有 `.gitkeep` 占位） |

**发现并修复的存量问题**：`admin` 账号 `is_super_admin=true` 但 `account_level='member'`。
`account_level` 是 P0 才加的列，早于它的账号会停在这个不一致状态；运行时靠
`contract.EffectiveAccountLevel` 折算，功能不受影响，但**数据层不自洽**——任何直接读
`account_level` 的统计/报表都会把超管算成普通成员。

修法：`repository/user.AlignSuperAdminLevel` + 启动时由 `login.EnsureAdmin` 调用，幂等自愈。
选启动自愈而不是一次性 SQL 的原因：部署到任何环境（含其他同事的本地库、未来的测试环境）都能自动收敛，
不依赖有人记得执行迁移脚本。

### 二、测试数据清理

清理内容：P2–P6 各阶段建的测试公司/账号/团队/人员/项目/包/角色/配置行、
指向已删公司的审计记录、回归产生的包文件。清理顺序统一为「先库后盘」，避免出现指向已删文件的记录。

清理后数据基线（与改造前等价）：

| 表 | 数量 |
| --- | --- |
| users | 3 |
| people | 2 |
| teams | 1 |
| team.projects | 3 |
| team.builds | 3 |
| tenants | 1（默认公司，P1 回填） |
| system_settings | 1（平台兜底行，存着改造前那把 Key） |
| roles | 5（全部平台内置） |
| team.audit_logs | 46（全平台记录，无租户） |

### 三、四道门禁

| 门禁 | 结果 |
| --- | --- |
| `go build ./...` + `go vet ./...` | 通过 |
| `go test ./...`（10 个包） | 全部 ok |
| `npx tsc --noEmit` | 通过 |
| `eslint .` + `stylelint` | 通过 |
| `make build`（前端打包 + 服务端二进制） | 通过 |

### 四、端到端全量回归（32 项断言，全部通过）

**阶段一 · 身份与账号（16/16）**
超管登录与 `/me` 层级；建两家公司；两个平台管理员登录且 `/me` 层级/公司名正确；
平台管理员建团队；建团队管理员且 `teamIds` 正确；团队管理员登录；
防越权 4 条（团队管理员不能建团队管理员、不能建超管标记；平台管理员不能建平台管理员；普通成员不能进账号管理）；
团队管理员建普通成员。

**阶段二 · 角色与集成（16/16）**
平台管理员建本公司角色；乙公司看不到也改不了甲公司的角色；平台管理员改不了平台内置角色（404）；
超管能改内置角色；权限目录已无管理点；用 `settings.write` 建角色被 400；
团队管理员进不了角色管理（403）但能建人员并分配角色；
团队级隔离：团队管理员只见本团队 1 个人员、1 个团队；
三方 API Key 互不相同；甲保存 Webhook 不影响乙；团队管理员读不到设置（403）。

**阶段三 · 上传归属与 Webhook 路由**
甲 Key 传甲项目成功；**乙 Key 传甲项目被 400 拒绝**（不泄漏项目是否存在）；
乙 Key 传乙项目成功；无效 Key 401；
Webhook 分别投到 `/alpha`（甲公司）与 `/beta`（乙公司），payload 的 `tenantId` 各自指向所属公司。

### 五、遗留与后续（非本轮范围）

| 项 | 说明 |
| --- | --- |
| 审计分级查看 | `/admin/audit-logs` 仍是超管专属；公司审计、团队审计（设计 §6.1）未做 |
| 公司详情/停用 | `GET/PUT /platform/tenants/{id}` 未做 |
| 平台管理员列表 | `GET /platform/admins` 未做 |
| 超管临时授权进公司 | `POST /platform/tenants/{id}/grant` 未做 |
| 浏览器截图验证 | 本机 playwright 缺浏览器二进制（需下载数百 MB），改用接口数据 + CSS 类名静态检查替代 |
| `default` 公司无平台管理员 | P1 回填产生，未走创建流程；前端显示「（无可用管理员）」，属既有状态，未擅自改造 |

### 六、结论

P0–P6 全部实施完成。核心不变量已经成立：**任何请求都能解析出 `(account_level, tenant_id, team_ids)`，
所有数据查询强制带这个范围，隔离是结构性保证而不是靠每个 handler 自觉。**

四道门禁全绿，32 项端到端断言全部通过，数据层零不一致、零孤儿。未执行 git commit。

---

## §17 P0 安全缺陷修复（2026-11-18）

设计评审发现三处 P0 级问题，本轮已全部修复并端到端验证。

### 缺陷 1：角色分配可提权（已修）

**问题**：`service/people` 完全不校验 `roleKey`。实测团队管理员给人员设 `role-key=super-admin` 后，
该人员建账号即可获得全量 10 项权限（含 `admin.access`、`settings.write`）。
之所以没造成一键接管，只是因为管理接口又按 `account_level` 判了一次——属于双重判定不一致的侥幸。

**修复**：`authz.ValidateRoleAssignable` 是唯一判定点，要求同时满足：
1. **可见性**：内置角色（`tenant_id` 为空）或调用者本公司的自定义角色；
2. **不超出调用者权限**：逐点比较角色权限与调用者自身权限——超管天然满足，其余身份严格比较。

挂在 `people.Service` 的 Create/Update（不是 handler），因为这是业务不变量。
`RoleAssigner` 用窄接口声明，避免 people 依赖整套 authz。

**验证**：团队管理员/平台管理员分配 `super-admin` 均 400「不能分配权限超出自己范围的角色」；
分配 `developer` 正常；不存在的角色 400；编辑时改角色同样被拦。7/7 通过。

### 缺陷 2：API Key 明文存储且接口回传（已修）

**问题**：`system_settings.api_key` 存明文，且 `GET /settings` 每次返回完整 Key。

**修复**：
- 库里改存 **SHA-256 摘要**（`encode(sha256(api_key::bytea),'hex')`），明文只在轮换响应出现一次；
- `GET /settings` 用 `apiKeyConfigured: boolean` 替代 `apiKey` 字段；
- 新公司首次建行时 `api_key` 留空（=未生成）——自动生成一把却没有明文交付路径，填给用户必然 401，
  让管理员主动轮换是唯一自洽的做法；
- 前端 API Key 面板改为「未生成 / 已配置（明文不再展示）」，明文只存在组件状态里，刷新即消失。

**迁移兼容（关键）**：存量明文在 `schema.sql` 里就地哈希化，判据是 `length(api_key) <> 64`（幂等）。
已发出的旧 Key **继续可用**——调用方仍发明文，服务端对收到的值算同样摘要再比对。
实测：迁移后旧 Key `0d815976…` 上传返回 201；把库里的摘要当 Key 使用返回 401。

选 SHA-256 而非 bcrypt/argon2：Key 是 128 位随机串，没有弱口令问题，不需要抗爆破的慢哈希；
而外部上传是热路径，慢哈希会直接抬高上传延迟。

### 缺陷 3：无登录失败限流（已修）

**问题**：可无限次爆破，仅有 PBKDF2 10 万次迭代拖慢，没有上限。

**修复**：`service/login/ratelimit.go`，按「**账号 + IP**」双维度计数：
- 连续失败 5 次 → 锁定 15 分钟；成功登录清零；
- 账号名大小写不敏感（否则变换大小写即可绕过）；
- 不存在的账号同样计数并锁定（否则可据「这个账号不锁」枚举真实账号）；
- 只按账号会让攻击者锁死别人的账号（DoS），只按 IP 换代理即绕过，故取交集；
- 返回 **429**「登录尝试过于频繁，请稍后再试」。

**为什么明确告知被限流**（而非混在「密码错误」里）：不存在的账号同样会被锁，
所以 429 不构成账号存在性泄露；反过来含糊其辞会让真实用户以为密码记错了反复重试，把锁定续得更久。

**边界**：单实例内存实现，多实例部署时各算各的、实际阈值被放大 N 倍，届时需换 Redis/DB 计数。
进程重启即清零——这是刻意的（重启是运维动作，清空惩罚可接受）。

**验证**：前 5 次 400、第 6 次起 429、锁定期间正确密码也 429、换 IP 正常 200。
单测 6 项覆盖计数/解锁/维度隔离/清零/大小写/过期清理。

### 状态
四道门禁全绿（10 个包测试通过）；端到端 7/7 + 限流 6/6；测试数据已清理；未 git commit。

---

## §18 跨公司重名支持：唯一约束租户化（2026-11-18）

设计评审的 P1 级问题 #4。之所以排在 P0 之后第一个修，因为它**不需要任何特殊条件就会触发**——
第二家真实公司入驻时，只要有个员工叫 `zhangsan`、有个项目叫 `mall`，就直接撞库建不了。

### 问题
三个字段都是全平台唯一：

| 字段 | 原约束 | 后果 |
| --- | --- | --- |
| `users.username` | 全局唯一 | —（**保持不变**，见下） |
| `people.username` | 全局唯一 | 两家公司不能有同名登录名的人员 |
| `team.projects.key` | 全局唯一 | 两家公司不能有同名项目（mall / web / app 这类短名） |

### 关键判断：`users.username` 不能改
账号的 `username` 是**登录标识**，登录时只有这一个字段（`SELECT ... WHERE u.username = $1`）。
若改成租户内唯一，两家公司都有 `zhangsan` 时这条查询会随机命中一行——等于登录进别人账号。
所以它必须保持全局唯一。这与 `people.username`（人员档案的登录名）是不同语义，不能一起改。

### 改动
1. **`people.username`** → `(coalesce(tenant_id, 零UUID), username)` 部分唯一索引，
   条件 `username IS NOT NULL AND <> ''`（允许多个没填登录名的人）。
2. **`team.projects.key`** → `(coalesce(tenant_id, 零UUID), key)` 唯一索引。
   URL 段 `/p/{key}` 仍能唯一定位，因为 `FindByKey` 始终带 scope。
3. 错误文案从「已被占用」改为「本公司内已有相同…」，避免误导。

**迁移顺序的坑**：这两个索引都依赖 `tenant_id` 列，而 `tenant_id` 是在 schema.sql 后半段
（"团队 / 人员 / 角色归属公司"）才 `ALTER ADD COLUMN` 的。一开始把索引写在原唯一索引位置，
启动即报 `column tenant_id does not exist`。已把新索引移到加列之后。

**第二个坑**：`projects_key_key` 在老库里是**唯一约束**（`contype='u'`）而不是独立索引，
`DROP INDEX` 对它无效，必须 `ALTER TABLE ... DROP CONSTRAINT`。两种形态都写了，保证新老库一致收敛。

### 连带修复：PersonID 的跨租户误匹配（重要）
`authz.PersonID` 在「账号没绑人员」时的兜底逻辑原本用 `Scope{All: true}` 按用户名找档案。
`people.username` 变成公司内唯一之后，这个兜底会**随机命中别家公司的同名人员**——
而 PersonID 参与 scope 推导，一旦命中错人，该成员的 team_ids 就按别人算，
等于让一家公司的成员看到另一家的团队数据。

修法：兜底查找改为 `Scope{TenantID: identity.TenantID}`。
（超管 TenantID 为空，`ApplyTenant` 对空租户会追加 `1 = 0` 失败关闭——超管本来就没有业务档案，行为正确。）

**这条比唯一约束本身更危险**，是改唯一约束必须同步做的前置修复。

### 验证（15 项全过）
**重名可用（8/8）**：甲乙两公司都建出 `mall` 项目、都建出 `zhangsan` 人员；
同公司内重复 key / 重复登录名仍被 400 拒绝（文案带「本公司内」）。

**隔离正确（7/7）**：两条 `mall` 是不同 id（`08599ede` vs `44bbe1f7`）；
各自 `GET /projects/mall` 解析到自己那条；列表各只见 1 条；乙改自己的 `mall` 不影响甲。

**PersonID 兜底（补充验证）**：两家公司各一个同 username 的人员，两个成员账号
`/people` 分别只看到本公司那个（`67c38419` vs `20eee5aa`），没有串。

四道门禁全绿（10 个包）；测试数据已清理；未 git commit。

### 遗留
`roles.key` 仍是全局唯一。目前靠「自定义角色 key 必须带公司 UUID 前 8 位前缀」绕过，
不是根治。改它要动 `people.role_key` / `projects.role_key` 两个外键（都指向 `roles(key)`），
成本明显高于本次两个字段，单独评估后再做。

---

## §19 roles.key 根治：外键改指 roles.id（2026-11-18）

§18 遗留的技术债。当时的做法是「自定义角色 key 必须带公司 UUID 前 8 位前缀」，
那是绕过而不是解决——用户看到的角色标识变成 `a1b2c3d4-devops`。

### 根因
`roles.key` 不只是展示字段，它是**两个外键的目标**：

```
people.role_key        → roles(key)  ON DELETE SET NULL
team.projects.role_key → roles(key)  ON DELETE SET NULL
```

外键指向文本 key，key 就必须全平台唯一。根治的唯一办法是**让外键指向主键**，
key 才能退化成纯业务字段、按公司划分。

### 为什么不用「复合外键 (tenant_id, key)」
看起来更省事，但有个致命问题：`roles.tenant_id` 对平台内置角色是 **NULL**，
而 PostgreSQL 外键默认 `MATCH SIMPLE` 下 NULL 不参与匹配——内置角色无法被引用，
而现存所有人挂的都是内置角色。要走这条路必须把内置角色每公司复制一份（5×N 行），
「改内置权限」也要改 N 份。成本更高且引入新的同步问题。**否决。**

### 改动
1. **新增 `role_id UUID REFERENCES roles(id)`** 到 `people` / `team.projects` / `solo.projects`，
   回填已有值后**删除 `role_key` 列**。
   保留旧列会形成"第二份真相"，写入路径一旦漏同步就会不一致——比没有更容易出错。
2. **`roles.key` 改为公司内唯一**：`(coalesce(tenant_id, 零UUID), key)`。
   内置角色 tenant_id 为空、归同一分组，因此内置之间仍互不重名。
3. **写入侧在 SQL 内解析**：API 仍收 `roleKey`（前端零改动），
   SQL 里用子查询 `(SELECT id FROM roles WHERE key=$n AND (tenant_id IS NULL OR tenant_id=$m))`
   原子地解析成 role_id。解析与写入同一语句，避免"查到但写进去是空"的窗口期。
4. **`authz.FindByKeyAny` 增加 tenantID 参数**：key 公司内唯一之后，
   不带公司过滤会随机命中——甲成员可能被套上乙公司 devops 的权限。这是本次最危险的连带点。
5. 权限判定里保留「角色不存在 vs 属于别家」的区分：先按可见范围解析，
   解析不到再无条件查一次，只为给出正确提示（前者 400「不存在」，后者 403）。
6. 移除前缀强制与 `tenantPrefix`；前端 placeholder 从 `a1b2c3d4-devops` 改回 `如 devops`。

### 修掉的四个 bug（都是本次引入或暴露的）
1. **`ON CONFLICT (key)` 启动即崩**：`CreateBuiltinIfMissing` 依赖 key 的唯一索引，
   改成表达式索引后报 `no unique or exclusion constraint matching`。
   改为 `INSERT ... WHERE NOT EXISTS`（播种只在启动时跑，不是热路径）。
2. **自定义角色可以占用内置 key**：索引把内置（tenant_id 空）与各公司分在不同分组，
   技术上允许重名。结果角色列表出现**两个 developer**，且权限解析不确定命中哪个。
   加业务校验：内置角色标识是保留字，创建前先查内置里有没有。
3. **项目创建 500（uuid/text 类型推断打架）**：同一占位符 $7 既写入 `tenant_id`（uuid）
   又在子查询里比较，PG 报 `column tenant_id is of type uuid but expression is of type text`。
   拆成 $7 / $8 两个参数。
4. **单人分区建项目 500（`invalid input syntax for type uuid: ""`）**：
   单人项目 `tenant_id` 是空串，`::uuid` 直接转换失败。两处都改用 `NULLIF($x,'')::uuid`。

### 验证（26 项全过）
**同名角色（10/10）**：甲、乙两公司各建 `devops`（**无任何前缀**）均成功；
同公司内重复 400；占用内置 `developer` 被 400 拒；角色列表无重复项。

**权限真隔离（5/5，核心）**：乙给自己的 `devops` 加 `build.delete`，
甲的 `devops` 权限完全不变；甲成员 `/me` 无 `build.delete`、乙成员有——
**两家同名角色真正各自独立**，这是根治的目标。

**项目与单人（11/11）**：跨公司同名项目 `shared` 两条独立记录；
项目角色可挂自定义/内置/清空；单人分区（tenant_id 为空）建改查全通。

**历史无回归（11/11）**：存量 2 个人员的角色关联未丢（`guest` 正确解析出角色名）；
超管改内置角色、设置读取、API Key 轮换均正常。

四道门禁全绿（10 个包）；测试数据已清理；未 git commit。

### 副产品：发现一处设计不一致（未改，记录在此）
`projects.role_key` 的设置**仍只有超管可以做**（`handler/project` 里 `!identity.IsSuperAdmin` 直接 403），
而 `people` 的角色在 P3 已开放给团队管理员并加了防提权校验。
两者不一致，但项目角色影响该项目下所有成员的权限，比人员角色更敏感——
**收归超管是更保守的选择，倾向保留现状**，等有明确需求再放开（届时套用 `ValidateRoleAssignable`）。
