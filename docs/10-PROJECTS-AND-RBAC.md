# 10 · 项目化管理 + 账号与权限体系（设计稿）

> 日期：2026-11-16 ｜ 状态：**P1 已实施（2026-11-16），P2/P3/P4 待排期**
> 关联：`docs/05-MODULE-ROADMAP.md`（多板块）、`docs/08-UI-REDESIGN-v3.md`（当前骨架）
> 结论先行：**项目做一等实体；权限用「项目成员 + 项目角色 + 权限串」；菜单是权限的投影；不做通用字段级权限，改为行级隔离 + 敏感字段掩码。**

---

## 1. 现状与要解决的问题

| 现状 | 问题 |
| --- | --- |
| `builds` 表没有归属，所有包平铺在一个池子里 | 无法支撑多项目；同名应用在不同项目会互相覆盖 |
| 「应用」是从 `appName` 派生的只读视图 | 没有稳定实体，热更新的「基线版本」无处挂载 |
| 只有一个管理员账号（配置里的 `admin`） | 无法区分谁能上传、谁能发生产、谁只能看 |
| 前端菜单写死，没有权限概念 | 加人就要共享密码，操作无法追溯 |
| 平台只支持 apk / ipa | 鸿蒙 `.hap` 无法上传 |

---

## 2. 项目模型

### 2.1 概念

```
项目 Project（一等实体）
  ├─ 成员：谁可以进来、以什么角色
  ├─ 应用 App：项目内的稳定实体（图标、包名、平台）
  ├─ 应用包 Build：项目 × 环境 × 平台 的一次发布
  └─ 热更新 Bundle：项目 × 应用 × 基线版本（P4）
```

**环境（test/hotfix/pre/prd）是项目内的维度，不是项目属性**：每个项目都有这四个环境，枚举仍由契约统一（`x-environments`）。这样契约不动，只是查询多一个 `project_id` 过滤。

> 若将来要「每个项目自定义环境」（例如 pushy 的灰度渠道），再把环境提升为项目配置表；当前不做，避免过度设计。

### 2.2 路由与切换（友好设计的核心）

采用 **路径前缀**，而不是查询参数：

```
/p/:projectKey/overview              项目概览
/p/:projectKey/packages?env=prd      包列表（环境仍在查询参数里）
/p/:projectKey/apps
/p/:projectKey/settings              项目设置
/settings                            平台级设置（Webhook / API Key / 账号）
/d/:id                               对外下载页（不需要项目前缀，靠 id 定位）
```

理由：
- 分享链接自带项目上下文，不会出现「发给同事后他看到别的项目的包」；
- 侧边栏高亮、面包屑、返回逻辑都自然；
- 项目切换 = 换 URL 前缀，浏览器前进/后退可用。

**切换器位置**：侧边栏品牌区下方，显示「项目头像 + 名称 + 当前角色」，点击弹出可搜索的项目列表（最近访问置顶）+「新建项目」。这是 Vercel / Linear / Slack 的通行做法，比顶部下拉更容易发现。

**默认项目**：访问 `/` 时跳转到 `最近访问的项目`（localStorage 记住），没有则取第一个成员项目；超级管理员取「默认项目」。

### 2.3 数据模型

```sql
CREATE TABLE projects (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  key         TEXT UNIQUE NOT NULL,        -- URL 段，小写字母数字与短横线
  name        TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'active',   -- active | archived
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE builds ADD COLUMN project_id UUID REFERENCES projects(id);
-- 回填默认项目后：ALTER COLUMN project_id SET NOT NULL
CREATE INDEX idx_builds_project_env_platform
  ON builds (project_id, environment, platform, created_at DESC);
```

`apps` 表（P3）同样带 `project_id`，并把 `UNIQUE(project_id, name)` 作为「同一项目内应用唯一」的约束。

### 2.4 鸿蒙平台

| 项 | 变化 |
| --- | --- |
| 契约 | `Platform.enum` 增加 `harmony`；`x-platforms.harmony = {label: 鸿蒙, ext: hap, contentType: application/octet-stream}` |
| 上传 | 扩展名白名单增加 `.hap` |
| 解析 | **暂不解析**：stage 直接返回 warning「暂不支持解析 .hap，请手动填写应用名与版本」，不阻塞发布 |
| 前端 | 平台徽标增加一色（与 Android/iOS 区分），筛选与上传表单自动跟随契约 |

---

## 3. 权限模型

### 3.1 业内四种做法

| 模型 | 代表产品 | 机制 | 适合 | 对本平台的判断 |
| --- | --- | --- | --- | --- |
| **RBAC（角色）** | GitLab、Jira、若依 | 用户→角色→权限；角色可绑定资源 | 组织内固定分工 | ✅ 主力模型 |
| **ABAC（属性）** | OPA / Cedar | 按属性与策略表达式求值 | 规则频繁变化的平台 | ❌ 过重，团队小、规则稳定 |
| **ReBAC（关系）** | Google Zanzibar、Notion | (用户, 关系, 对象) 元组，支持深层继承 | 复杂共享（文档逐层授权） | ❌ 层级只有「项目」一层，用不上 |
| **ACL（逐资源）** | 早期文件系统 | 每个资源挂一张授权表 | 资源少且共享随意 | ❌ 无法回答「谁能上传」这类动作权限 |

参考实现：
- **GitLab**：`Owner / Maintainer / Developer / Reporter / Guest` 五档，权限按资源层级继承；菜单由角色推导。
- **若依（RuoYi）**：用户→角色→菜单（目录/菜单/按钮三级，菜单表带 `perms` 串），另有数据范围（全部/本部门/本人）。
- **Vercel/Linear**：工作区角色 + 团队成员关系，项目切换器在侧栏顶部。

### 3.2 本平台采用：两层 RBAC

```
全局层：超级管理员（唯一内置角色，短路所有检查）
项目层：项目负责人 / 开发 / 只读（授予时绑定 project_id）
```

一个人可以在 A 项目是负责人、在 B 项目是只读——这正是「项目管理」带来的自然结果，也满足「每个账号独立配置」。

**权限串（permission key）是唯一真相**：后端接口用它做校验，前端菜单由它投影。

```
overview.view
package.view | package.upload | package.download
bundle.view | bundle.publish | bundle.rollout | bundle.rollback
app.view | app.manage
project.view | project.manage
member.view | member.manage
settings.view | settings.manage
audit.view
```

角色 → 权限（内置，可在设置页只读查看）：

| 角色 | 范围 | 权限 |
| --- | --- | --- |
| 超级管理员 `super_admin` | 全局 | 全部（代码里短路，不查表） |
| 项目负责人 `project_owner` | 项目 | 该项目全部权限，含成员管理 |
| 开发 `developer` | 项目 | `package.view/upload/download`、`bundle.*`、`app.view`、`overview.view` |
| 只读 `viewer` | 项目 | `*.view`、`package.download` |

### 3.3 数据模型

```sql
CREATE TABLE users (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  username      TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,                    -- bcrypt(cost 12)
  display_name  TEXT NOT NULL,
  is_super_admin BOOLEAN NOT NULL DEFAULT false,
  status        TEXT NOT NULL DEFAULT 'active',   -- active | disabled
  perm_version  INT  NOT NULL DEFAULT 1,          -- 权限变更时 +1，用于缓存失效
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE project_members (
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  role       TEXT NOT NULL,                       -- project_owner | developer | viewer
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, project_id)
);

-- 仅用于「这个人就是比别人多/少一项」的例外，第一版可不做
CREATE TABLE user_permissions (
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  project_id UUID REFERENCES projects(id) ON DELETE CASCADE,
  permission TEXT NOT NULL,
  effect     TEXT NOT NULL,                       -- allow | deny
  PRIMARY KEY (user_id, project_id, permission)
);
```

**判定顺序**（`authz` 包唯一实现）：

```
1. 用户被禁用            → 401
2. is_super_admin        → 通过
3. 解析 project_id（从路径或资源反查）
4. 不是该项目成员        → 404（不泄露项目存在）
5. deny 覆盖表命中       → 403
6. 角色权限 ∪ allow 覆盖 → 含所需权限则通过，否则 403
```

### 3.4 菜单权限怎么落

**不建独立的菜单权限表**。菜单树在代码里定义（一处），每项声明所需权限：

```go
// internal/authz/menu.go（示意）
var MenuTree = []MenuItem{
  {Key: "overview", Label: "概览", Perm: "overview.view"},
  {Key: "packages", Label: "应用包", Perm: "package.view",
    Children: []MenuItem{
      {Key: "packages.upload", Label: "上传包", Perm: "package.upload", Type: Action},
    }},
  {Key: "apps", Label: "应用", Perm: "app.view"},
  {Key: "settings", Label: "系统设置", Perm: "settings.view"},
}
```

`GET /api/v1/me` 返回**已按当前用户裁剪**的菜单树（带 `granted` 标记），前端只负责渲染：

```json
{
  "user": { "id": "...", "username": "zhangsan", "isSuperAdmin": false },
  "project": { "id": "...", "key": "shop", "name": "商城", "role": "developer" },
  "permissions": ["package.view", "package.upload", "overview.view"],
  "menus": [{ "key": "packages", "label": "应用包", "granted": true }]
}
```

为什么不用若依那种「菜单表 + 角色-菜单关联」：菜单和接口权限会各自漂移，最后出现「菜单看得见、点进去 403」或「菜单藏了、接口还能调」。**权限串是唯一真相，菜单只是它的视图**；而设置页的勾选界面仍可按菜单树呈现（勾菜单 = 授予该菜单及其按钮所需的最小权限集合），运营体验不变。

**按账号独立配置**：用户详情页给一棵菜单树，三种状态——
`来自角色（勾选且锁定）` / `额外授予（勾选可取消）` / `未授予`。
勾选「额外授予」实际写 `user_permissions(allow)`，不产生新角色，也不会污染角色定义。

### 3.5 认证改造（现有 token 的限制）

现在令牌载荷只有到期时间，**改了权限也不能让已发出的令牌失效**。改造：

- 载荷改为 `uid|permVersion|exp`，HMAC 不变；
- 中间件按 uid 加载用户与权限，**内存缓存 10 秒**（key = uid + perm_version）；
- 改权限 / 禁用账号时 `perm_version += 1` → 下一次请求立即生效；
- 管理员账号改为**数据库托管**：`ADMIN_PASSWORD` 只在首次建表时作为种子密码，之后改密码走界面（否则环境变量会悄悄覆盖数据库密码，这是常见的事故源）。

其他安全项：登录失败按「用户名 + IP」限速（5 次/5 分钟）、密码 bcrypt、会话 12 小时、禁用账号立即踢下线（靠 perm_version + status 检查）。

---

## 4. 字段级权限：我的判断

**不需要做通用字段级权限。** 理由：

1. **数据敏感度低**。这个平台的字段是应用名、包名、版本号、大小、下载次数——不是薪资、身份证、客户手机号。字段级权限在 CRM/HR/多租户 SaaS 里才有价值。
2. **真正的敏感项是凭据，不是业务字段**。`apiKey`、`webhook.secret`、`password_hash` 这类东西用「接口级隔离 + 掩码」就够，做成通用字段权限是杀鸡用牛刀。
3. **成本不对称**。字段权限要在 DTO 投影、搜索、排序、导出、前端表单只读态、测试矩阵里各加一轮，成本是接口权限的 3-5 倍，而收益在这个场景接近于零。
4. **会用错**。字段权限一多，排查「为什么他看不到这个字段」的成本会远高于它挡住的风险。

**但有两件「半个字段权限」值得做**（成本低、收益明确）：

| 措施 | 说明 | 归属阶段 |
| --- | --- | --- |
| **行级隔离** | 非项目成员看不到该项目的数据（404）；这是必须做的数据权限 | P2 |
| **敏感字段掩码** | `apiKey` / `webhook.secret` 对非超管返回 `abcd****wxyz`，只有超管能看明文 | P2 |

所以问题回到你：**接受「不做通用字段权限」这个判断吗？** 如果想保留可扩展性，我会在 `authz` 里预留 `CanField(user, resource, field)` 的口子（不实现规则），将来真有需求时不用改数据模型。

---

## 5. 界面影响

| 位置 | 变化 |
| --- | --- |
| 侧栏顶部 | 新增项目切换器（头像 + 名称 + 我的角色），点击可搜索/新建 |
| 侧栏导航 | 「概览 / 应用包 / 应用」跟随项目；「系统设置」拆成「项目设置」（成员、项目信息）与「平台设置」（Webhook、API Key、账号） |
| 顶栏 | 项目名进面包屑；环境切换保留（项目内维度） |
| 无权限时 | 菜单不渲染；直接访问 URL 显示 403 页（不重定向，避免"被踢走"的困惑） |
| 用户管理 | 新增「账号与权限」页：列表、新建、禁用、重置密码、项目成员配置、菜单勾选 |
| 首次登录 | 强制修改初始密码（种子账号） |

---

## 6. 迁移路径（不停机）

1. 建 `projects`，插入 `{key:'default', name:'默认项目'}`；
2. `builds.project_id` 加列 → 全量回填 default → 加 NOT NULL 与索引；
3. 建 `users`，把现有 `ADMIN_USERNAME` 写入并置 `is_super_admin = true`，密码用当前 `ADMIN_PASSWORD` 做 bcrypt 种子；
4. 老链接 `/packages?env=prd` → 301 到 `/p/default/packages?env=prd`（前端重定向，保留查询参数）；
5. 下载页 `/d/{id}` 不变（id 全局唯一，不需要项目前缀）。

---

## 7. 风险与对策

| 风险 | 对策 |
| --- | --- |
| 升级后所有人都只能看到 default 项目 | 迁移脚本把「超级管理员」设为全项目可见；其余账号由管理员分配 |
| 权限缓存导致改了不生效 | 缓存 10 秒 + 变更时 `perm_version + 1`，最坏 10 秒生效 |
| 自己被降权导致进不去 | 「最后一个超级管理员」保护：不允许删除/降级/禁用最后一名超管 |
| 项目 key 与现有路由冲突 | key 只允许 `[a-z0-9-]{2,32}`，且路由前缀固定 `/p/` |
| 鸿蒙包解析不了被当成失败 | stage 恒 200 + warning，与现有降级策略一致 |

---

## 8. 备选方案（未采用）

| 方案 | 为什么不选 |
| --- | --- |
| 项目用查询参数 `?project=` | 链接离开页面就丢上下文，容易串项目 |
| 每个项目一套独立部署 | 运维成本 ×N，且账号体系会分裂 |
| 直接上 ABAC/OPA | 规则引擎本身成为维护负担，团队规模用不上 |
| 只做全局角色（不分项目） | 无法表达「A 项目负责人、B 项目只读」，与项目化目标冲突 |
| 菜单独立授权（若依式） | 菜单与接口两套真相，必然漂移（见 §3.4） |

---

## 9. 分阶段落地

| 阶段 | 内容 | 是否动权限 | 可独立上线 |
| --- | --- | --- | --- |
| **P1 项目化** ✅ **已完成（2026-11-16）** | `projects` 表、`builds.project_id`、项目切换器、`/p/:key/*` 路由、鸿蒙平台 | 否 | ✅ |
| **P2 账号与 RBAC** | `users`/`project_members`、登录改造、`authz` 判定、`/me` 菜单投影、账号管理页、403 页 | 是 | ✅ |
| **P3 项目成员** | 项目成员配置、用户级权限增补（菜单三态勾选）、项目设置页 | 是 | ✅ |
| **P4 热更新 + 审计** | `bundle.*` 权限、`audit_logs`、操作留痕 | 是 | ✅ |

P1 不动权限也能上，先把「多项目」跑通；P2 再收口账号。这样每一步都可回滚。

### 9.1 P1 实际落地（与设计稿的差异，以实现为准）

| 项 | 实际做法 |
| --- | --- |
| 路由前缀 | `/p/:key/` 下挂 overview、packages、apps、upload 四个板块；平台级 `/settings` 不带前缀；公开下载页仍是 `/d/:id`。导航链接由 `web/src/components/navigation.ts` 的 `navHref(item, projectKey)` 统一拼，页面组件不感知前缀 |
| 侧栏切换器 | `ProjectSwitcher` 在侧栏品牌区下方：显示「首字母 + 项目名 + /key」，点开可切换、可就地新建；**切项目保留当前板块**（在「应用包」切过去仍在「应用包」），新建成功后整页跳转以确保列表缓存刷新 |
| 项目列表 | `GET /api/v1/projects` + `POST /api/v1/projects`（均需登录）。key 规则 `^[a-z0-9][a-z0-9-]{1,31}$`，全仓唯一、创建后不可改；**没有** `status`（archived）字段，删除/归档功能未做 |
| 默认项目迁移 | 建 `projects` 后插入 `{key:'default', name:'默认项目'}`，存量 `builds` 全量回填该 id，再加 `NOT NULL` + 外键 + `(project_id, environment, platform, created_at DESC)` 索引。`/` 会跳到「最近访问的项目」，无记录则取第一个 |
| 无效项目 key | URL 写成不存在的 key 时，由 `web/src/components/ProjectGate.tsx` 在骨架层拦成空态卡片（「项目不存在」+ 回到最近项目 / 新建项目），不再是页面里的红色 400 报错 |
| 鸿蒙平台 | 契约 `Platform.enum` 已含 `harmony`（扩展名 `hap`），上传 / 暂存 / 落盘 / 下载全链路可用，服务端用 `contract.PlatformOfExt` 反查扩展名（未写死 apk/ipa）；**只支持上传不解析**：stage 返回 warning「暂不支持解析 .hap，请手动填写应用名、包名与版本号」，不阻塞发布 |
| **权限相关一律未做** | 没有 `users` / `project_members` / `user_permissions` 表，没有 `authz` 判定、没有 `/me`、没有 403 页、菜单不做裁剪；**任何登录用户能看到并操作所有项目**（只有一层管理员口令）。行级隔离与敏感字段掩码都不存在——这些是 P2 的范围，不要按本文件前几节以为已经生效 |

---

## 10. 需要你拍板

| # | 问题 | 我的建议 |
| --- | --- | --- |
| Q1 | 通用字段级权限做不做 | 不做；保留 `CanField` 扩展口 |
| Q2 | 项目内的角色够不够，还是要逐账号勾权限 | 角色为主 + 用户级增补（三态菜单树），兼顾两者 |
| Q3 | 项目用路径前缀 `/p/:key` 还是查询参数 | 路径前缀 |
| Q4 | 管理员密码从环境变量迁到数据库 | 迁移，环境变量只做首次种子 |
| Q5 | 鸿蒙 `.hap` 先只支持上传不解析，可以吗 | 可以，与现有「解析失败不阻塞」一致 |

**P1 拍板结果（2026-11-16）**：Q1 不做字段权限（只做行级隔离 + 敏感字段掩码）、Q2 项目角色 + 账号级增补、Q3 路径前缀 `/p/:key`、Q4 管理员迁到数据库（P2 执行）、Q5 鸿蒙先只上传。

---

## 11. 已知问题与后续

| 级别 | 问题 | 影响 | 建议修法 |
| --- | --- | --- | --- |
| P2（不阻塞） | 访问失效项目链接时，首帧 `ProjectGate` 的 `notFound` 仍为 `false`，`OverviewPage` / `AppsPage` 会用失效 key 发 4 次 `GET /builds`，浏览器控制台留下 4 条 400 | 用户无感（页面正确显示空态），仅控制台噪音与 4 次无效请求 | `useAllBuilds` 改为按「已解析的 project」而非 key 触发；或在项目列表加载中让 `ProjectGate` 渲染 `Loading` 而非 children（后者会让首次进入多一次闪烁，需权衡） |
| P3 | API Key 尚无轮换入口，只能改库 | 泄露时需运维介入 | 设置页加「重新生成」，写入 `perm_version` 体系后一并做 |
| P4 | 无审计日志 | 权限上线后无法追溯「谁发的生产包」 | 与 RBAC 同批做 `audit_logs`（actor / action / target / 时间） |

