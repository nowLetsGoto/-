# 11 · 超级管理员菜单设计

> 日期：2026-11-16 ｜ 状态：**P2.1 身份地基 + P2.2 账号管理与审计已实施**（P2.3/P2.4/P2.5 按 §8 推进）
> 关联：`docs/10-PROJECTS-AND-RBAC.md`（权限模型）、`docs/06-FOLDER-LAYOUT.md`（目录规范）、`docs/08-UI-REDESIGN-v3.md`（视觉系统）
> 一句话结论：**超级管理员是一个独立的一级菜单区，只有超管可见；它管「谁能做什么、系统发生了什么」，不重复「基础资料」对人和项目本身的管理。**

---

## 0. 前提：现在还没有权限体系

必须先说清楚现状，否则菜单做出来是摆设：

| 现状 | 影响 |
| --- | --- |
| 只有一个账号（`ADMIN_USERNAME`/`ADMIN_PASSWORD` 环境变量） | 没有"多账号"，也就没有"超管 vs 普通用户" |
| 无 `users` / 角色 / 权限点表 | 无法表达"谁能进哪个菜单" |
| 后端只判断「是否登录」，不判断「能不能」 | 任何登录者都能改 API Key、删包、发生产包 |
| `project_members(project_id, person_id)` 无 `role` 列 | 项目成员关系有了，但没有角色语义 |

因此本设计的落地顺序是：**先立"超管"这个身份，再上菜单**。身份的判定规则见 §2。

---

## 1. 对标与取舍

| 参考产品 | 可借鉴 | 不照搬 |
| --- | --- | --- |
| **GitLab Admin Area** | 独立 Admin 区域、与日常菜单彻底分离、顶部有 Admin 角标；监控页汇总实例指标 | 它的菜单层级过深（3 层），我们保持两级 |
| **Ant Design Pro** | 系统管理四件套：用户 / 角色 / 菜单 / 日志；权限矩阵表格化 | 它的"菜单权限"细到按钮级配置，我们不做通用字段级权限（见 docs/10 拍板） |
| **Grafana Server Admin** | 左侧固定 Admin 分组、Org/User 一览、插件与设置集中 | 多租户（Org）我们不需要 |
| **Vercel / Linear** | 危险操作（删项目、轮换 Key）要求输入名称确认 + 明示影响面 | 它们的计费与用量页与我们无关 |

**采纳的五条原则**

1. **物理分离**：超管菜单是独立一级区，位于侧栏最底部，带 `Admin` 角标与独立强调色，避免与日常操作混排误点。
2. **三层防线**：前端不渲染 → 路由守卫 403 → 接口 403。前端隐藏**不是**安全边界。
3. **不重复造**：人员/团队/项目本身的管理留在「基础资料」；超管只做"授权、追踪、平台级配置"。
4. **一切写操作留痕**：进入审计日志，含前后值。
5. **危险操作显式确认**：二次确认 + 影响面数字 + 高危操作输入名称。

---

## 2. 身份模型与可见性规则

### 2.1 平台角色

```
平台角色：super_admin（内置、唯一语义：可管理平台本身）
项目角色：owner / developer / tester / guest（P2.3 才落，见 docs/10）
```

超管**不是**项目角色的放大版：他能管理账号与系统设置，但**不自动等于所有项目的数据权限**——是否允许超管越权查看所有项目数据，见 §10 待拍板 Q1。默认建议：**超管可看全部项目数据**（内部工具，排障需要），并在审计里记录"超管访问"。

### 2.2 判定来源（分两个阶段）

| 阶段 | 判定方式 | 说明 |
| --- | --- | --- |
| **现在（无 users 表）** | 会话用户名 == `config.AdminUsername` | 零改动即可跑通菜单与拦截，`/me` 增 `isSuperAdmin` |
| **P2.2 之后（有 users 表）** | `users.is_super_admin = true` | 管理员入库，支持多超管、可转让 |

两种阶段对前端**完全透明**：只看 `me.isSuperAdmin`。后端在 `authz` 里做一次判定，两阶段共用同一接口。

### 2.3 三层防线

```
① 菜单层：navigation.ts 的 items 带 requireSuperAdmin: true
          → Sidebar 用 me.isSuperAdmin 过滤，非超管整组不渲染
② 路由层：<SuperAdminGate> 包裹 /admin/* 路由
          → 非超管直接渲染 403 页（不是 404：admin 区存在不是秘密）
③ 接口层：middleware.RequireSuperAdmin 挂在 /api/v1/admin/* 整组
          → 非超管一律 403「无权访问该功能」
```

**为什么路由用 403 而不是 404**：项目数据用 404 是为了不暴露"这个项目存在"；而"平台有超管区"是公开的产品事实，403 语义更准确，也不会让运维怀疑接口挂了。

---

## 3. 菜单结构

```
总览
分发       应用包 / 应用
基础资料    人员管理 / 团队管理 / 项目管理
平台       系统设置
──────────────────────────  （视觉分隔线）
超级管理员  [Admin]                      ← 仅超管渲染，整组
           管理总览
           账号管理
           角色与权限
           访问总览
           审计日志
           平台集成
```

**与现有菜单的边界**

| 能力 | 归属 | 理由 |
| --- | --- | --- |
| 新建/编辑人员、团队、项目 | 基础资料（已有） | 业务主数据，日常高频 |
| 谁能登录、谁能进哪个项目、什么角色 | **超级管理员 → 账号管理 / 角色与权限** | 授权是平台级动作 |
| Webhook 地址、API Key | **超级管理员 → 平台集成** | 现在放在「系统设置」，属于越权暴露，应迁过来并对非超管隐藏 |
| 包体积、磁盘、版本、启动时长 | **超级管理员 → 管理总览** | 运维视角 |
| 谁在什么时候改了什么 | **超级管理员 → 审计日志** | 合规与排障 |

> 「系统设置」现有内容（Webhook + API Key）会**拆走**到「平台集成」，`/settings` 仅保留非敏感的展示项或整体下线（见 §10 Q2）。

---

## 4. 页面功能设计

### 4.1 管理总览 `/admin`

**目的**：一眼看清平台健康状况，替代"登服务器看日志"。

| 区块 | 指标 | 交互 |
| --- | --- | --- |
| 规模 | 项目数 / 包总数 / 总占用空间 / 人员数 / 账号数 | 点击跳到对应列表（带筛选） |
| 近 7 天发布 | 按天柱状：发布次数、失败次数 | 悬停显示明细 |
| 环境分布 | 四环境包数量与体积占比 | — |
| 系统健康 | 数据库连接、迁移版本、进程启动时长、Go 版本、应用版本与构建时间 | 20s 轮询；异常项标红 |
| 待办提醒 | 未绑定人员的账号数、超 90 天未发布的包数、Webhook 最近失败次数 | 点击跳转 |

**空态**：全新实例 → 引导"创建第一个项目"。

### 4.2 账号管理 `/admin/accounts`

**目的**：账号生命周期（新建、禁用、重置密码、强制下线）。

| 列 | 说明 |
| --- | --- |
| 账号 / 姓名 | 关联 `people` 的姓名；未关联显示「未绑定人员 + 去绑定」 |
| 角色 | `超级管理员` / `普通用户` |
| 状态 | 启用 / 已禁用；禁用时显示原因与时间 |
| 最近登录 | 时间 + IP；从未登录显示「—」 |
| 操作 | 重置密码 / 禁用 / 强制下线 / 编辑 |

**关键设计**

- **强制下线**：`token_version` 自增，旧令牌立即失效（无需会话表）。
- **不可自锁**：不能禁用自己、不能取消自己的超管身份、不能删最后一个超管——三个按钮对自己直接禁用并给 tooltip 说明。
- **重置密码**：生成一次性临时密码，弹窗展示一次并提示"刷新后不可再查看"；不通过邮件（内网无邮件服务）。
- **禁用**语义是"保留数据、禁止登录"，不提供物理删除（删除会破坏审计里的操作者引用）。

### 4.3 角色与权限 `/admin/roles`

**目的**：定义"能做什么"，是菜单与接口判定的唯一来源。

| 区块 | 内容 |
| --- | --- |
| 角色列表（左） | 内置角色（不可删）：超级管理员、项目负责人、开发、测试、访客；自定义角色可增删改 |
| 权限矩阵（右） | 行 = 权限点，列 = 读/写/删；按模块分组：项目、应用包、基础资料、平台设置 |
| 数据范围 | 每个角色一个下拉：全部项目 / 仅我参与的项目 |

**权限点命名**：`build.read` `build.write` `build.delete` `project.read` `project.write` `directory.read` `directory.write` `settings.read` `settings.write` `admin.*`（与 docs/10 的权限串约定一致）。

**只读约束**：内置角色可看不可改（避免把自己锁死）；修改自定义角色时右侧实时显示"受影响的账号数"。

### 4.4 访问总览 `/admin/access`

**目的**：回答"这个人在哪些项目、什么角色"这种跨项目问题——逐个进项目看太慢。

- 一张表：人员 × 项目 × 角色，支持按人员、项目、角色三个维度筛选
- 行内直接改角色（下拉），变更后即时写入审计
- 顶部三个数字：未加入任何项目的人数、无成员的项目数、角色分布

**与 4.3 的区别**：4.3 定义「角色是什么」，4.4 分配「谁在哪儿是什么角色」。

### 4.5 审计日志 `/admin/audit-logs`

**目的**：谁、什么时候、从哪个 IP、对什么、做了什么、结果如何。

| 列 | 说明 |
| --- | --- |
| 时间 | 精确到秒，本地时区 |
| 操作者 | 账号 + 姓名；系统动作用 `system` |
| 动作 | `build.created` `build.deleted` `project.updated` `user.disabled` `apikey.rotated` … |
| 对象 | 类型 + 标识（可点击跳到对象） |
| 结果 | 成功 / 失败（失败带原因） |
| 来源 IP / UA | 折叠展示 |

**设计要点**

- **只读**：任何人（含超管）都不能改或删审计日志；保留期 180 天，到期归档而非删除。
- **详情抽屉**：显示 before/after 的字段级 diff（复用 `DetailDrawer`）。
- **筛选**：时间区间、操作者、动作类型、对象类型、结果；常用筛选可存为快捷入口（本期不做，列 P4）。
- **保留策略**：单表按月分区（量大再拆），先做索引 `(created_at desc)` + `(actor_id, created_at desc)`。

### 4.6 平台集成 `/admin/integrations`

**目的**：把"密钥与外部通道"从业务设置里剥离，集中管控。

| 卡片 | 内容 | 危险操作 |
| --- | --- | --- |
| API Key | 掩码显示 `abcd****wxyz`、创建时间、最近使用时间；一键轮换 | **轮换**：确认弹窗明示"旧 Key 将在 5 分钟后失效，外部系统需同步更新"，需输入 `ROTATE` 确认 |
| Webhook | 地址、启用开关、推送字段勾选、最近 20 次投递记录（时间/状态码/耗时/响应摘要） | 「发送测试」即时回显 |
| 投递记录 | 失败重试一次（手动） | — |

**为什么迁到这里**：`GET /settings` 目前对任何登录者返回 API Key 明文，这是当前最实际的风险；迁走后 `/settings` 不再返回密钥，掩码规则见 docs/10（非超管只看掩码）。

### 4.7 系统信息（并入 4.1 的「系统健康」卡片，不单独成页）

版本 / commit / 构建时间 / 启动时长 / DB 版本 / 是否容器运行 / 数据目录与占用。**不放任何可写开关**——运行时配置改动走环境变量或部署，避免"页面上改一半、重启失效"。

---

## 5. 危险操作保护规范（全局统一）

| 等级 | 场景 | 交互要求 |
| --- | --- | --- |
| L1 普通 | 新建、编辑 | 表单校验 + 保存反馈 |
| L2 谨慎 | 禁用账号、改角色、删项目 | 二次确认弹窗，写明**具体影响面数字**（如"该账号 3 个项目将失去成员身份"） |
| L3 高危 | 轮换 API Key、重置密码、清空临时文件 | 输入指定词确认（`ROTATE` / 账号名）；操作后提示"已写入审计日志 #id" |
| L4 不可逆 | 批量删除、删除最后一个超管 | **禁止**（按钮不出现，或 disabled + 原因） |

补充：
- 所有 L2+ 操作成功后在页面右上角给出带"查看审计"链接的提示。
- 批量操作上限 20 条，超出要求分批（与个人文件安全规范一致）。

---

## 6. 数据模型增量（P2 实施时）

```sql
-- 账号（替代环境变量单账号）
users (
  id uuid pk, username text unique not null, password_hash text not null,
  person_id uuid null references people(id),      -- 关联人员档案，可空
  is_super_admin boolean not null default false,
  status text not null default 'active',          -- active | disabled
  token_version int not null default 1,           -- 自增即强制下线
  last_login_at timestamptz null, last_login_ip text not null default '',
  created_at timestamptz not null default now()
)

-- 项目成员角色（在既有 project_members 上加列）
alter table project_members add column role text not null default 'guest';

-- 角色与权限点（P2.3）
roles (id uuid pk, key text unique, name text, is_builtin boolean, data_scope text, created_at)
role_permissions (role_id uuid, permission text, primary key (role_id, permission))

-- 审计（P2.2，越早越好）
audit_logs (
  id bigserial pk, created_at timestamptz default now(),
  actor_id uuid null, actor_name text not null,   -- 冗余姓名：账号改名后日志仍可读
  action text not null, object_type text not null, object_id text not null,
  result text not null, reason text not null default '',
  ip text not null default '', user_agent text not null default '',
  detail jsonb not null default '{}'::jsonb       -- before/after diff
)
```

---

## 7. 接口草案（全部挂在 `/api/v1/admin/*`，整组过 `RequireSuperAdmin`）

```
GET  /admin/overview                 → 规模 / 趋势 / 健康 / 待办
GET  /admin/users?status=&keyword=&page=&pageSize=
POST /admin/users                    → 新建账号（账号名 + 初始密码 + 是否超管 + 绑定人员）
PUT  /admin/users/{id}               → 改姓名绑定 / 超管标记（受"最后一个超管"保护）
POST /admin/users/{id}/reset-password→ 返回一次性临时密码
POST /admin/users/{id}/disable|enable
POST /admin/users/{id}/kick          → token_version++
GET  /admin/roles                    → 角色 + 权限点矩阵
PUT  /admin/roles/{key}/permissions   → 内置角色 403
GET  /admin/access?personId=&projectKey=&role=&page=&pageSize=
PUT  /admin/access                   → 单条改角色（写审计）
GET  /admin/audit-logs?from=&to=&actor=&action=&result=&page=&pageSize=
GET  /admin/integrations             → Key 掩码 + Webhook 配置 + 最近投递
POST /admin/integrations/api-key/rotate
POST /admin/integrations/webhook/test
```

分页一律复用现有 `?page=&pageSize=` 约定与 `contract.Page`；列表页统一走 `DataTable` 的 `serverPagination`。

---

## 8. 实施分期

| 期 | 内容 | 交付后能做什么 | 依赖 |
| --- | --- | --- | --- |
| **P2.1 身份地基** | `users` 表 + 密码入库 + `isSuperAdmin` + `RequireSuperAdmin` 中间件 + `/me` 扩展 + 403 页 | 多账号可登录；非超管访问 admin 接口被拒 | 无 |
| **P2.2 账号 + 审计** | 账号管理页 + 重置密码 + 禁用/踢下线 + 审计写入（先记关键动作） | 能管理账号；关键操作可追溯 | P2.1 |
| **P2.3 角色 + 访问** | 角色权限矩阵 + 项目成员角色 + 访问总览页 | 能按角色授权、跨项目查关系 | P2.2 |
| **P2.4 平台集成** | API Key 轮换 + Webhook 投递记录 + `/settings` 剥离密钥 | 密钥不再裸奔，可轮换 | P2.1 |
| **P2.5 总览 + 系统信息** | 指标聚合接口 + 管理总览页 + 健康检查卡 | 不用登服务器看状态 | P2.2 |

> **不建议先做菜单壳子**：本仓库的既定约束是「没有任何占位菜单、空页面」。`/admin/*` 的每一项都依赖 P2.1/P2.2 的数据表，先挂 6 个空页面进去只会让导航变重——这也是为什么设计先行。

---

## 9. 验收标准

1. 非超管登录后，侧栏看不到「超级管理员」整组；直接输入 `/admin/users` 得到 403 页（不是白屏、不是 404）。
2. 非超管构造请求打 `/api/v1/admin/users` 得到 403，响应体符合统一错误结构。
3. 超管禁用某账号后，该账号当前令牌立即失效（下一次请求 401 回登录页）；`token_version` 自增可见。
4. 超管无法禁用自己、无法取消自己最后一个超管身份（按钮 disabled 且有原因提示）。
5. 轮换 API Key 后：旧 Key 立即拒绝（或按约定 5 分钟宽限），审计里出现 `apikey.rotated` 记录。
6. 每一条 L2+ 操作都能在审计日志里查到「谁 / 何时 / 对谁 / 结果 / IP」。
7. 所有列表页分页走服务端，`?page=&pageSize=` 生效，刷新后停在原页。
8. 页面无 console 错误；`antd` 仍只出现在 `DataTable` / `DetailDrawer` / `theme` 三个封装内。

---

## 10. 待你拍板

| # | 问题 | 我的建议 |
| --- | --- | --- |
| Q1 | 超管是否默认能看所有项目数据 | **能看**（内部工具，排障需要），但所有访问写入审计；P3 可加"隐身模式"开关 |
| Q2 | 现有「系统设置」怎么处理 | 拆分为「平台集成」归超管；`/settings` 仅保留"个人/展示"类配置，或直接下线 |
| Q3 | 账号是否需要注册/邀请 | 不需要。超管创建账号 + 一次性临时密码，内网无邮件服务 |
| Q4 | 审计保留期与是否可删 | 180 天，任何人不可删；到期归档成冷文件而非物理删除 |
| Q5 | 是否需要"多超管" | 需要。内置至少保留 1 个，允许新增；删除最后一个超管时禁止 |
| Q6 | 角色是否允许超管自定义 | 允许自定义角色，内置 5 个角色只读 |
