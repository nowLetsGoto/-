-- server/internal/repository/schema.sql · Dandelion 数据模型（幂等，可重复执行）
-- 职责：建立 team / solo 两个业务分区（各一套 projects / builds / audit_logs），
--       以及全局表 users / roles / people / teams / system_settings 与索引；
--       由 //go:embed 打进二进制，进程启动时幂等执行。
-- 输入：无参数；输出：两个 schema、六张分区业务表、全局表与索引、团队分区一行默认项目。
-- 边界：environment 只 NOT NULL 不加 CHECK（枚举唯一来源是 api/openapi.yaml）；
--       三个 URL 字段由服务端按 id 派生，绝不入库，换域名时不需刷数据；
--       system_settings 只允许一行（id 恒为 1），避免"多份配置谁是真相"；
--       容器部署时同一份文件也挂给 PG initdb，保证 DB 就绪即有表。
--
-- 为什么这样分区：单人模式与团队模式写下的不是同一批事实，物理分表后
-- 「单人只看自己」不再依赖每条 SQL 都记得加 owner 条件，越权在表边界上就不可能发生。
-- 账号 / 角色 / 人员 / 团队 / 平台配置是跨模式的全局事实（登录发生在选模式之前），
-- 因此留在 public，不参与分区。
--
-- 部署约束：本文件必须由应用自己的数据库账号执行（正常路径就是进程启动时的 EnsureSchema）。
-- 表与 schema 的属主决定访问权，以别的账号手工跑一遍会造出应用连不上的分区。

-- ---------------------------------------------------------------------------
-- 一 · 分区骨架
-- ---------------------------------------------------------------------------
CREATE SCHEMA IF NOT EXISTS team;
CREATE SCHEMA IF NOT EXISTS solo;

-- 存量库迁移：三张业务表原本在 public，整体迁入 team。
-- 外键引用的是表 OID，SET SCHEMA 不会打断 project_members → projects 这类引用；
-- 表拥有的索引、约束与自增序列会随表一起搬迁。
DO $$
DECLARE
  t TEXT;
BEGIN
  FOREACH t IN ARRAY ARRAY['projects', 'builds', 'audit_logs'] LOOP
    IF to_regclass('public.' || t) IS NOT NULL AND to_regclass('team.' || t) IS NULL THEN
      EXECUTE format('ALTER TABLE public.%I SET SCHEMA team', t);
    END IF;
  END LOOP;
END $$;

-- ---------------------------------------------------------------------------
-- 二 · 两个分区共用的业务表结构
-- 同一段 DDL 循环执行两遍：结构只写一次，team 与 solo 永远不会漂移。
-- solo.projects.role_key 恒为空——单人模式没有角色；保留该列是为了两套表同构，
-- 仓储层才能共用同一份 SQL，而不必为"少一列"写两套查询。
-- ---------------------------------------------------------------------------
DO $$
DECLARE
  s TEXT;
BEGIN
  FOREACH s IN ARRAY ARRAY['team', 'solo'] LOOP
    -- 项目：业务数据的一级归属。key 是 URL 段（/p/{key}/...），创建后不可改。
    -- owner_user_id 记录创建者：单人分区依赖它隔离彼此的空间。
    EXECUTE format($ddl$
      CREATE TABLE IF NOT EXISTS %1$I.projects (
        id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
        key           TEXT        NOT NULL,
        name          TEXT        NOT NULL,
        description   TEXT        NOT NULL DEFAULT '',
        repo_url      TEXT        NOT NULL DEFAULT '',
        icon_url      TEXT        NOT NULL DEFAULT '',
        role_key      TEXT,
        owner_user_id TEXT        NOT NULL DEFAULT '',
        created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
      )
    $ddl$, s);

    -- 应用包：文件本体在 storage，这里只存元数据与相对路径。
    EXECUTE format($ddl$
      CREATE TABLE IF NOT EXISTS %1$I.builds (
        id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
        project_id         UUID        NOT NULL REFERENCES %1$I.projects(id),
        environment        TEXT        NOT NULL,
        platform           TEXT        NOT NULL,
        app_name           TEXT        NOT NULL,
        package_id         TEXT        NOT NULL DEFAULT '',
        version            TEXT        NOT NULL DEFAULT '',
        build_number       TEXT        NOT NULL DEFAULT '',
        changelog          TEXT        NOT NULL DEFAULT '',
        original_file_name TEXT        NOT NULL,
        file_size          BIGINT      NOT NULL,
        file_ext           TEXT        NOT NULL,
        relative_path      TEXT        NOT NULL,
        meta_source        TEXT        NOT NULL DEFAULT 'fallback',
        download_count     BIGINT      NOT NULL DEFAULT 0,
        owner_user_id      TEXT        NOT NULL DEFAULT 'system',
        created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
      )
    $ddl$, s);

    -- 审计日志：只读，任何人不可改不可删。actor_name 冗余存姓名，
    -- 账号改名或被禁用后历史日志仍可读；detail 存 JSONB，展示时再格式化。
    EXECUTE format($ddl$
      CREATE TABLE IF NOT EXISTS %1$I.audit_logs (
        id          BIGSERIAL   PRIMARY KEY,
        created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
        actor_id    UUID,
        actor_name  TEXT        NOT NULL,
        action      TEXT        NOT NULL,
        object_type TEXT        NOT NULL,
        object_id   TEXT        NOT NULL DEFAULT '',
        result      TEXT        NOT NULL,
        detail      JSONB       NOT NULL DEFAULT '{}'::jsonb,
        ip          TEXT        NOT NULL DEFAULT ''
      )
    $ddl$, s);
  END LOOP;
END $$;

-- 存量库补列：迁移过来的 team 表可能缺后加的列；新建的 solo 表已有，这里是空操作。
-- 必须在建索引之前跑完，否则索引会引用尚不存在的列。
DO $$
DECLARE
  s TEXT;
BEGIN
  FOREACH s IN ARRAY ARRAY['team', 'solo'] LOOP
    EXECUTE format($q$ALTER TABLE %I.projects ADD COLUMN IF NOT EXISTS repo_url TEXT NOT NULL DEFAULT ''$q$, s);
    EXECUTE format($q$ALTER TABLE %I.projects ADD COLUMN IF NOT EXISTS icon_url TEXT NOT NULL DEFAULT ''$q$, s);
    EXECUTE format('ALTER TABLE %I.projects ADD COLUMN IF NOT EXISTS role_key TEXT', s);
    EXECUTE format($q$ALTER TABLE %I.projects ADD COLUMN IF NOT EXISTS owner_user_id TEXT NOT NULL DEFAULT ''$q$, s);
    EXECUTE format($q$ALTER TABLE %I.builds ADD COLUMN IF NOT EXISTS owner_user_id TEXT NOT NULL DEFAULT 'system'$q$, s);
  END LOOP;
END $$;

-- 索引按查询形态建，两个 schema 各自持有一份同名索引。
DO $$
DECLARE
  s TEXT;
BEGIN
  FOREACH s IN ARRAY ARRAY['team', 'solo'] LOOP
    -- 单人项目管理按创建者列出，索引与查询条件同序。
    EXECUTE format(
      'CREATE INDEX IF NOT EXISTS idx_projects_owner ON %I.projects (owner_user_id, created_at)', s);
    -- 列表查询永远带 project_id。
    EXECUTE format($q$
      CREATE INDEX IF NOT EXISTS idx_builds_project_env_platform_created
        ON %1$I.builds (project_id, environment, platform, created_at DESC)
    $q$, s);
    EXECUTE format(
      'CREATE INDEX IF NOT EXISTS idx_builds_owner ON %I.builds (owner_user_id, created_at DESC)', s);
    EXECUTE format('CREATE INDEX IF NOT EXISTS idx_audit_logs_created ON %1$I.audit_logs (created_at DESC)', s);
    EXECUTE format('CREATE INDEX IF NOT EXISTS idx_audit_logs_action ON %1$I.audit_logs (action, created_at DESC)', s);
    EXECUTE format('CREATE INDEX IF NOT EXISTS idx_audit_logs_actor ON %1$I.audit_logs (actor_name, created_at DESC)', s);
  END LOOP;
END $$;

-- ---------------------------------------------------------------------------
-- 三 · 分区各自的唯一性
-- 团队项目 key 的全局唯一索引这里先删掉，改成「公司内唯一」。
-- 新索引必须在 tenant_id 列存在之后才能建（见本文档下方"项目两张分区表同构加列"之后），
-- 因为这段 DDL 执行时 tenant_id 还没加。
--
-- 为什么改：key 是项目短名（mall / web / default），两家公司各有一个 mall 完全正常，
-- 全平台唯一会让第二家建项目时直接撞库。
-- URL 段 /p/{key} 仍然能唯一定位：FindByKey 始终带 scope（公司 + 团队边界），
-- 同一个 key 在不同公司解析出各自的项目。代价是 URL 本身不再全局唯一，
-- 但那本来就不该是它的职责——归属由登录身份决定，不由 URL 决定。
--
-- 单人项目的 key 只需在本人空间内唯一：两个人各自拥有一个 "app" 互不冲突。
-- ---------------------------------------------------------------------------
DROP INDEX IF EXISTS projects_key_key;
-- 老库里它是唯一约束（contype='u'）而不是独立索引，DROP INDEX 对它无效，必须同时清约束。
-- 两种形态都处理，保证新老库都能收敛。
ALTER TABLE team.projects DROP CONSTRAINT IF EXISTS projects_key_key;
CREATE UNIQUE INDEX IF NOT EXISTS projects_owner_key ON solo.projects (owner_user_id, key);

-- 团队分区的默认项目：项目化之前的存量包都归到这里；单人分区不播种，由本人自建。
-- 注意这里还不能引用 tenant_id（那座列在本段之后才加），所以只按 key 判重。
-- 默认项目全平台只有一个、且属于默认公司，因此在 key 上的判重是充分的。
INSERT INTO team.projects (key, name, description)
SELECT 'default', '默认项目', '项目化迁移时生成的默认项目'
WHERE NOT EXISTS (SELECT 1 FROM team.projects WHERE key = 'default');

-- ---------------------------------------------------------------------------
-- 四 · 全局表（跨模式共享的事实）
-- ---------------------------------------------------------------------------

-- 公司：数据隔离的最高边界。name / code 都唯一（code 用于 API Key 归属与界面展示）。
-- 必须建在 users 之前：users.tenant_id 是它的外键。
-- 一个公司对应一个平台管理员账号，两者在 P2 的创建流程里原子地建立。
CREATE TABLE IF NOT EXISTS tenants (
  id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  name       TEXT        NOT NULL UNIQUE,
  code       TEXT        NOT NULL UNIQUE,
  status     TEXT        NOT NULL DEFAULT 'active',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- system_settings：平台集成配置（Webhook + 外部 API Key），按公司一行。
-- tenant_id 为空的那一行是「平台兜底配置」：存量全局 Key 就是这一行，
-- 用它上传时按项目归属推导公司，行为与改造前一致，现有 CI 不会中断。
--
-- 为什么去掉 id=1 的单例约束：一家公司一份配置是隔离的前提，
-- 否则 A 公司改 Webhook 会改掉所有人的回调地址（改造前的真实缺陷）。
-- webhook_fields 用 TEXT 存 JSON 数组：不依赖驱动的 text[] 扫描实现，
-- 读写统一走 encoding/json，与 contract.WebhookFields 的校验集合天然同构。
CREATE TABLE IF NOT EXISTS system_settings (
  id              INTEGER     GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
  tenant_id       UUID        REFERENCES tenants(id),
  webhook_enabled BOOLEAN     NOT NULL DEFAULT false,
  webhook_url     TEXT        NOT NULL DEFAULT '',
  webhook_secret  TEXT        NOT NULL DEFAULT '',
  webhook_fields  TEXT        NOT NULL
                  DEFAULT '["appName","version","buildNumber","environment","platform","downloadPageUrl"]',
  -- api_key 存 SHA-256 十六进制摘要，不存明文。
  -- 明文只在「轮换」响应的那一次出现，之后无法从库里还原——
  -- 数据库泄露不等于 Key 泄露，这是 Key 类凭据的最低要求。
  api_key         TEXT        NOT NULL,
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 存量库升级：原表是 SMALLINT 单例（id=1 且 CHECK(id=1)），改成每公司一行。
-- 全部幂等，可反复执行（schema.sql 每次启动都会跑）。
ALTER TABLE system_settings DROP CONSTRAINT IF EXISTS system_settings_id_check;
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS git_repo_path TEXT NOT NULL DEFAULT '';
ALTER TABLE system_settings ALTER COLUMN id TYPE INTEGER;
DO $$
BEGIN
  -- identity 列不能 DROP DEFAULT（会报 "is an identity column"），因此整段按"是否已是 identity"分流：
  --   · 不是 identity → 先清掉旧的 SMALLINT 默认值，再改成 identity；
  --   · 已是 identity → 什么都不做（重复启动的常态）。
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'system_settings' AND column_name = 'id' AND is_identity = 'YES'
  ) THEN
    ALTER TABLE system_settings ALTER COLUMN id DROP DEFAULT;
    ALTER TABLE system_settings ALTER COLUMN id ADD GENERATED BY DEFAULT AS IDENTITY;
  END IF;
END $$;

-- 每公司至多一行；平台兜底行（tenant_id 为空）全表至多一行。
CREATE UNIQUE INDEX IF NOT EXISTS idx_settings_one_per_tenant
  ON system_settings (tenant_id) WHERE tenant_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_settings_platform_fallback
  ON system_settings ((true)) WHERE tenant_id IS NULL;
-- API Key 反查归属走唯一索引：外部上传每次请求都要按 Key 找公司，不能全表扫。
-- 唯一约束同时保证一把 Key 不会对应两家公司（否则包会落到哪家就不确定了）。
--
-- 必须是**部分索引**，排除空串：
-- 空串代表"这家公司还没配 Key"，每家公司都可以处于这个状态；
-- 若把它也纳入唯一约束，全平台只能有一家公司没配 Key——
-- 第二家公司的 EnsureRow 会撞索引，让设置页直接 500（真实踩到）。
-- 非空摘要仍然唯一，反查语义不变。
DROP INDEX IF EXISTS idx_settings_api_key;
CREATE UNIQUE INDEX idx_settings_api_key
  ON system_settings (api_key) WHERE api_key <> '';

-- 把存量明文 Key 迁移成 SHA-256 摘要。
--
-- 迁移不会让现有 CI 断掉：调用方仍然发原来的明文，服务端对「收到的值」算同样的 SHA-256
-- 再与库里比对，结果一致。也就是说 —— 用户手里那把 Key 完全不变，只是库里不再存明文。
--
-- 判据用长度而不是「是否含非十六进制字符」：明文是 32 位、摘要固定 64 位。
-- 幂等：已经是 64 位的行不会被二次哈希（否则第二次启动就会把所有 Key 弄失效）。
--
-- 必须排除空串：空串是「这家公司还没配 Key」的标记，不是一把待哈希的明文。
-- 若把它哈希，得到的是空串的固定摘要 e3b0c442...，而每家未配置的公司都会得到同一个值，
-- 撞上 api_key 唯一索引导致启动失败（真实踩到：世界第二家公司空串撞了已存在的 e3b0c442...）。
UPDATE system_settings SET api_key = encode(sha256(api_key::bytea), 'hex')
WHERE length(api_key) <> 64 AND api_key <> '';

-- 播种平台兜底行：只在还没有兜底配置时插入，已有则原样保留（含存量 Key）。
-- 显式指定 id=1：改 identity 后序列还没推进，直接 INSERT 会生成 id=1 撞上存量行。
INSERT INTO system_settings (id, tenant_id, api_key)
SELECT 1, NULL, replace(gen_random_uuid()::text, '-', '')
WHERE NOT EXISTS (SELECT 1 FROM system_settings WHERE tenant_id IS NULL)
ON CONFLICT (id) DO NOTHING;

-- 把 identity 序列推到当前最大值之后：存量行（id=1）是改造前就有的，
-- 序列却从 1 开始，不重置的话第一次 INSERT 就会撞主键（真实踩到的坑）。
SELECT setval(
  pg_get_serial_sequence('system_settings', 'id'),
  GREATEST((SELECT coalesce(max(id), 0) FROM system_settings), 1)
);

-- roles / role_permissions：角色与权限点。权限点目录的唯一来源是 contract.Permissions，
-- 这里只存"角色拥有哪些"。
--
-- **内置角色每家公司各持一份独立副本**（tenant_id = 该公司，is_builtin = true）：
-- 它只是"新公司开张时送的默认模板"，之后改它只影响本公司。若做成一份全局共享行，
-- 甲公司的管理员改 developer 就会改掉乙公司的同名角色——那是越权。
-- 播种在 service/role（建公司时 SeedForTenant、启动时 EnsureBuiltin 全量补齐）。
--
-- **角色里没有「超级管理员」和「平台管理员」**：它们是账号身份
-- （users.is_super_admin / users.account_level），不在这张表里。
CREATE TABLE IF NOT EXISTS roles (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  -- key 只在本公司内唯一（见下方 idx_roles_tenant_key）；内置角色也归属本公司，
  -- 因此同公司内 developer 与自定义 developer 互斥，跨公司可重名。
  --
  -- 建表时临时加单列 UNIQUE：people.role_key 外键在下方 CREATE TABLE people 里就要引用
  -- roles.key，PostgreSQL 要求被引列有单列唯一约束，而下方 idx_roles_tenant_key 的
  -- 复合表达式索引 (coalesce(tenant_id,零), key) 不能被外键引用，且那时还没建。空库若没有这个约束，
  -- 建 people 会直接报 42830（既有 bug）。
  --
  -- 这个单列约束只是"过桥"：所有引用 roles.key 的旧外键（people.role_key /
  -- team.projects.role_key / solo.projects.role_key）在下文中陆续删除，随后
  -- ALTER TABLE roles DROP CONSTRAINT IF EXISTS roles_key_key 把它清掉，
  -- 终态只剩租户内复合唯一索引——与存量库一致。
  key         TEXT        NOT NULL UNIQUE,
  name        TEXT        NOT NULL,
  description TEXT        NOT NULL DEFAULT '',
  is_builtin  BOOLEAN     NOT NULL DEFAULT false,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS role_permissions (
  role_id    UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
  permission TEXT NOT NULL,
  PRIMARY KEY (role_id, permission)
);

-- 一次性迁移的完成标记：仅记录"这次数据搬迁做过了"，不参与业务。
-- 为什么需要表而不是常量：迁移结果必须持久化到数据库，
-- 否则每次启动都会重跑，把管理员后来手工改的菜单授权冲掉。
CREATE TABLE IF NOT EXISTS schema_migrations (
  name       TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- role_menus：角色可见的菜单。与 role_permissions 完全对称——
-- 前者决定"能看哪些页面"，后者决定"能做哪些操作"。
--
-- 授权按角色存储，角色按 tenant_id 隔离，因此每家公司的菜单配置天然独立：
-- 甲公司的 developer 授权了审计日志，不会影响乙公司的 developer。
-- menu_key 用页面路径（如 /overview），与前端路由和 contract.Menus 一一对应，
-- 不需要额外维护一套 id ↔ 路径映射。
CREATE TABLE IF NOT EXISTS role_menus (
  role_id   UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
  menu_key  TEXT NOT NULL,
  PRIMARY KEY (role_id, menu_key)
);

-- 权限点目录的存量对齐由 service/role 在启动时完成（与菜单授权同一机制），
-- 不在这里硬编码"要删哪些"：role.read/write 曾被收归超管、现已重新开放，
-- 写死清理会在每次启动时把管理员刚配好的权限删掉（真实踩到）。

-- 说明：内置角色的默认权限点由 service/role 的 builtinRoles 定义在播种时写入，
-- 不需要在这里逐条回填。历史上有两条回填（guest 的 directory.read、developer 的
-- build.delete）是基于"内置角色全局共享一份"的旧模型，已随每公司独立副本一起移除。

-- people：人员档案。username 为空表示"只在通讯录里，不能登录"。
-- 一个人只挂一个角色（权限取单一集合，避免"多角色取并集"的歧义）。
CREATE TABLE IF NOT EXISTS people (
  id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  name       TEXT        NOT NULL,
  -- username 不再全局唯一：见下方 idx_people_tenant_username。
  -- 两家公司各有一个 zhangsan 是完全正常的，全局唯一会让第二家建不了人。
  username   TEXT,
  email      TEXT        NOT NULL DEFAULT '',
  phone      TEXT        NOT NULL DEFAULT '',
  title      TEXT        NOT NULL DEFAULT '',
  role_key   TEXT        REFERENCES roles(key) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 存量库去掉 people.username 的全局唯一约束，改为「公司内唯一」。
-- 注意 users.username 仍然全局唯一——那是登录标识，必须全局唯一，
-- 否则按用户名登录会随机命中另一个账号。两者是不同的语义，不能一起改。
--
-- 新索引要等 tenant_id 列加上之后才能建（见下方"团队 / 人员 / 角色归属公司"那段）。
ALTER TABLE people DROP CONSTRAINT IF EXISTS people_username_key;
DROP INDEX IF EXISTS people_username_key;

-- 存量库补列：早期 people 表没有 role_key，缺了外键就补一个。
ALTER TABLE people ADD COLUMN IF NOT EXISTS role_key TEXT;

-- 角色关联改为按 roles.id（UUID）而不是 roles.key（文本）。
--
-- 为什么：key 是给人看的业务标识（devops / tester），本该允许两家公司各有一个 devops；
-- 但它当时是外键的目标，只能全平台唯一，于是被迫用「公司 UUID 前 8 位前缀」绕过——
-- 那是权宜之计，用户看到的角色标识变成 a1b2c3d4-devops。
-- 改引用主键之后，key 就只是本公司的展示字段，可以放心地公司内唯一。
--
-- 顺序：先加 role_id 并回填（此时 role_key 还在，可用来对照），再换外键、删旧列。
ALTER TABLE people ADD COLUMN IF NOT EXISTS role_id UUID REFERENCES roles(id) ON DELETE SET NULL;

-- 回填：把已有的 role_key 翻成对应的 roles.id。
-- 只在 role_id 为空时填，保证幂等（重复启动不会覆盖后续的选择）。
UPDATE people p SET role_id = r.id
FROM roles r
WHERE p.role_id IS NULL AND p.role_key IS NOT NULL AND p.role_key <> '' AND r.key = p.role_key;

-- 旧外键与旧列一起清掉：role_key 若保留会变成"第二份真相"，
-- 一旦写入路径漏了同步就会与 role_id 不一致，反而制造 bug。
ALTER TABLE people DROP CONSTRAINT IF EXISTS people_role_fk;
ALTER TABLE people DROP COLUMN IF EXISTS role_key;

CREATE TABLE IF NOT EXISTS teams (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  name        TEXT        NOT NULL,
  description TEXT        NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- users：账号（替代"环境变量单账号"）。密码只存 PBKDF2-HMAC-SHA256 派生值；
-- token_version 自增即让该账号已发出的令牌全部失效（强制下线，无需会话表）；
-- person_id 可空：账号可以先建，之后再绑定人员档案。
-- mode 是账号归属的模式：它决定这行账号属于哪套业务数据，也是账号管理的筛选维度
-- （超管在单人模式下只看到并只能删单人模式的账号）。账号是登录的前提、登录时还没选模式，
-- 所以 users 留在 public，只用 mode 列标注归属，不参与物理分区。
CREATE TABLE IF NOT EXISTS users (
  id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  username       TEXT        UNIQUE NOT NULL,
  password_hash  TEXT        NOT NULL,
  mode           TEXT        NOT NULL DEFAULT 'team',
  person_id      UUID        REFERENCES people(id) ON DELETE SET NULL,
  is_super_admin BOOLEAN     NOT NULL DEFAULT false,
  status         TEXT        NOT NULL DEFAULT 'active',
  token_version  INT         NOT NULL DEFAULT 1,
  last_login_at  TIMESTAMPTZ,
  last_login_ip  TEXT        NOT NULL DEFAULT '',
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 存量库补列：老账号都是团队账号，DEFAULT 'team' 让历史数据自动归位。
ALTER TABLE users ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT 'team';

-- 账号管理按「模式 + 创建时间」列出，索引与查询条件同序。
CREATE INDEX IF NOT EXISTS idx_users_mode_created ON users (mode, created_at);
CREATE INDEX IF NOT EXISTS idx_users_person ON users (person_id);

-- ---------------------------------------------------------------------------
-- 四·补 · 多公司租户化（P0 身份地基）
-- 所有新列用 ADD COLUMN IF NOT EXISTS，索引用 IF NOT EXISTS，约束用 DO 包裹，
-- 于是全新库、存量库、以及"上次执行到一半"的库跑完这段都收敛到同一结构。
-- 这里只落结构，不做存量数据回填——回填属于 P6 迁移，P0 不碰业务数据。
-- ---------------------------------------------------------------------------

-- 账号：身份层级 + 公司归属。is_super_admin 过渡期保留，不删除。
ALTER TABLE users ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
ALTER TABLE users ADD COLUMN IF NOT EXISTS account_level TEXT NOT NULL DEFAULT 'member';

-- 每家公司至多一个 active 平台管理员：部分唯一索引，停用后可再建新的。
-- status 是 users 的既有列，因此这里不需要再补列。
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_one_platform_admin
  ON users (tenant_id) WHERE account_level = 'platform_admin' AND status = 'active';

-- 身份层级四级枚举约束，挡住 account_level 的脏值。
-- pg_constraint 里按名字判存在：ALTER TABLE ... ADD CONSTRAINT 没有 IF NOT EXISTS。
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'users_account_level_check') THEN
    ALTER TABLE users ADD CONSTRAINT users_account_level_check
      CHECK (account_level IN ('super_admin','platform_admin','team_admin','member'));
  END IF;
END $$;

-- 团队 / 人员 / 角色归属公司。一个人只属于一个公司，可关联多个团队。
ALTER TABLE teams ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
CREATE INDEX IF NOT EXISTS idx_teams_tenant ON teams (tenant_id);
ALTER TABLE people ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
CREATE INDEX IF NOT EXISTS idx_people_tenant ON people (tenant_id);
-- 人员登录名的公司内唯一索引：必须在加完 tenant_id 之后建。
-- 条件 username IS NOT NULL AND <> ''：允许任意多个「没填登录名」的人员。
-- coalesce 把 tenant_id 为空的存量档案归到同一个"无公司"分组，保证它们之间仍不重名。
CREATE UNIQUE INDEX IF NOT EXISTS idx_people_tenant_username
  ON people (coalesce(tenant_id, '00000000-0000-0000-0000-000000000000'::uuid), username)
  WHERE username IS NOT NULL AND username <> '';
-- roles.tenant_id 为空表示平台内置（所有公司可见），非空表示公司自定义。
ALTER TABLE roles ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
CREATE INDEX IF NOT EXISTS idx_roles_tenant ON roles (tenant_id);
-- 角色标识的公司内唯一索引：必须在加完 tenant_id 之后建。
-- 内置角色的菜单授权由 service/role.EnsureBuiltin 在启动时补齐，
-- 不在这里硬编码菜单清单——那份清单必须与 contract.Menus 同源，
-- 写进 SQL 就会出现两份随时可能漂移的定义（真实踩过：加菜单时漏改这里）。

-- coalesce 把内置角色（tenant_id 为空）归到同一个"平台"分组，
-- 保证 5 个内置角色之间仍然互不重名（否则可能出现两个 developer）。
CREATE UNIQUE INDEX IF NOT EXISTS idx_roles_tenant_key
  ON roles (coalesce(tenant_id, '00000000-0000-0000-0000-000000000000'::uuid), key);

-- 项目两张分区表同构加列：solo 分区恒为 NULL——单人数据不参与公司隔离，
-- 保留同构列只是让仓储能共用同一份 SQL。
DO $$
DECLARE
  s TEXT;
BEGIN
  FOREACH s IN ARRAY ARRAY['team', 'solo'] LOOP
    EXECUTE format($q$
      ALTER TABLE %I.projects ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id)
    $q$, s);
    EXECUTE format('CREATE INDEX IF NOT EXISTS idx_projects_tenant ON %I.projects (tenant_id)', s);
  END LOOP;
END $$;

-- 团队项目 key 的公司内唯一索引：必须在上面这段加完 tenant_id 之后建。
-- coalesce 把 tenant_id 为空的存量项目归到同一个"无公司"分组，
-- 保证它们之间仍互不重名（P1 回填前的旧数据都在默认公司下，实际不会撞）。
CREATE UNIQUE INDEX IF NOT EXISTS idx_team_projects_tenant_key
  ON team.projects (coalesce(tenant_id, '00000000-0000-0000-0000-000000000000'::uuid), key);

-- 审计日志只有团队分区需要归属维度：单人分区天然以物理表隔离，没有公司/团队概念。
-- team_id 不加外键——日志的归属是"当时"的事实，团队删除后历史仍要可读。
ALTER TABLE team.audit_logs ADD COLUMN IF NOT EXISTS tenant_id UUID;
ALTER TABLE team.audit_logs ADD COLUMN IF NOT EXISTS team_id UUID;
CREATE INDEX IF NOT EXISTS idx_team_audit_tenant ON team.audit_logs (tenant_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- 五 · 项目协作关系：只属于团队分区
-- 单人空间没有"成员"与"项目角色"，所以这两张关联表不参与分区；
-- 外键显式指向 team.projects——团队项目被删除时会级联清理，不留悬挂关系。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS project_members (
  project_id UUID NOT NULL REFERENCES team.projects(id) ON DELETE CASCADE,
  person_id  UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  PRIMARY KEY (project_id, person_id)
);

CREATE INDEX IF NOT EXISTS idx_project_members_person ON project_members (person_id);

CREATE TABLE IF NOT EXISTS team_projects (
  team_id    UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  project_id UUID NOT NULL REFERENCES team.projects(id) ON DELETE CASCADE,
  PRIMARY KEY (team_id, project_id)
);

CREATE INDEX IF NOT EXISTS idx_team_projects_project ON team_projects (project_id);

CREATE TABLE IF NOT EXISTS team_members (
  team_id   UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  person_id UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  PRIMARY KEY (team_id, person_id)
);

CREATE INDEX IF NOT EXISTS idx_team_members_person ON team_members (person_id);

-- 团队管理员关联：账号 ↔ 团队，多对多，只有 account_level='team_admin' 的账号才会出现在这里。
-- 一个管理员可管多个团队；一个团队可有多个管理员（人员变动不必迁移团队数据）。
-- 复合主键保证同一账号对同一团队只有一条关系；user 索引支撑「反查我管哪些团队」。
CREATE TABLE IF NOT EXISTS team_admins (
  team_id UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  PRIMARY KEY (team_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_team_admins_user ON team_admins (user_id);

-- 团队项目的角色：进入该项目时的"项目角色"，优先级高于人员自身的角色。
-- 允许为空：为空表示"沿用人员角色"，这是默认状态。
--
-- 与 people 同样改为引用 roles.id：理由见 people 段的说明（key 要能公司内唯一）。
ALTER TABLE team.projects ADD COLUMN IF NOT EXISTS role_id UUID REFERENCES roles(id) ON DELETE SET NULL;
UPDATE team.projects p SET role_id = r.id
FROM roles r
WHERE p.role_id IS NULL AND p.role_key IS NOT NULL AND p.role_key <> '' AND r.key = p.role_key;
ALTER TABLE team.projects DROP CONSTRAINT IF EXISTS projects_role_fk;
ALTER TABLE team.projects DROP COLUMN IF EXISTS role_key;

-- 单人分区的 role_key 列原本只是为了与团队表同构（单人模式没有角色，恒为空）。
-- 去掉它，改成与团队表同构的 role_id（恒为 NULL）：两套表仍共用同一份查询 SQL，
-- 因此列必须存在；但它不会有人写，也不会命中任何角色。
-- 之所以不顺带把 role_key 留着当"同构列"：列名叫 key 却永远为空，比没有更容易误用。
ALTER TABLE solo.projects ADD COLUMN IF NOT EXISTS role_id UUID REFERENCES roles(id) ON DELETE SET NULL;
ALTER TABLE solo.projects DROP COLUMN IF EXISTS role_key;

-- 到这里 people_role_fk 与 projects_role_fk 都已删除，roles.key 不再被任何外键引用，
-- 可以安全地去掉它的全局唯一约束了。老库里它是 UNIQUE 约束（contype='u'），
-- DROP INDEX 对约束无效，两种形态都清。
ALTER TABLE roles DROP CONSTRAINT IF EXISTS roles_key_key;
DROP INDEX IF EXISTS roles_key_key;

-- ---------------------------------------------------------------------------
-- 六 · 研发中心（团队专属，不进 FOREACH 循环）
-- 只在 team 模式提供服务：RequireTeamMode 是边界，不为用不到的 solo 造表。
-- 与 project_members / team.audit_logs.tenant_id 等同属"团队专属"先例。
-- ---------------------------------------------------------------------------

-- 需求池主表。req_key 是用户可见的稳定编号，UNIQUE 兜底并发分配；
-- creator_user_id 沿用裸 TEXT 惯例（不加 users 外键，账号删除后历史需求仍可读）；
-- tenant_id 提供公司隔离；status 不加 CHECK（枚举唯一来源是 api/openapi.yaml）。
CREATE TABLE IF NOT EXISTS team.rdc_requirements (
  id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  req_key             TEXT        NOT NULL UNIQUE,
  title               TEXT        NOT NULL,
  description         TEXT        NOT NULL DEFAULT '',
  status              TEXT        NOT NULL DEFAULT 'pending_review',
  acceptance_criteria TEXT        NOT NULL DEFAULT '',
  creator_user_id     TEXT        NOT NULL DEFAULT '',
  tenant_id           UUID        REFERENCES tenants(id),
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 列表查询形态：租户 + 状态筛选 + 时间倒序；关键字用 ILIKE 另走全扫兜底（数据量小）。
CREATE INDEX IF NOT EXISTS idx_rdc_requirements_tenant_status_created
  ON team.rdc_requirements (tenant_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_rdc_requirements_creator
  ON team.rdc_requirements (creator_user_id, created_at DESC);

-- 增量 1：需求归属项目。可空以兼容 V1.0 存量（存量行显示"未归属项目"）；
-- ON DELETE SET NULL 让项目删除后需求自动降级，不产生悬挂外键、也无需改项目删除逻辑。
-- 新写入一律必填，校验放在 service（不放 DB 约束，避免存量行违反约束）。
ALTER TABLE team.rdc_requirements
  ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES team.projects(id) ON DELETE SET NULL;

-- 列表筛选形态：租户 + 项目 + 时间倒序。
CREATE INDEX IF NOT EXISTS idx_rdc_requirements_tenant_project_created
  ON team.rdc_requirements (tenant_id, project_id, created_at DESC);

-- 增量 2：需求图片附件（一需求多图）。仅图片类型；relative_path 由 storage 包生成，
-- 本表只存字符串，读取时经 SafeResolve 前缀校验。FK 级联保证删需求即清附件行。
CREATE TABLE IF NOT EXISTS team.rdc_requirement_attachment (
  id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  requirement_id UUID        NOT NULL REFERENCES team.rdc_requirements(id) ON DELETE CASCADE,
  file_name      TEXT        NOT NULL,           -- CleanFileName 清洗后的展示名，不参与拼路径
  content_type   TEXT        NOT NULL,           -- 白名单内 MIME
  file_size      BIGINT      NOT NULL,           -- <= 5MB
  relative_path  TEXT        NOT NULL,           -- rdc/requirements/{attID}.{ext}
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_rdc_requirement_attachment_req
  ON team.rdc_requirement_attachment (requirement_id, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS uq_rdc_requirement_attachment_path
  ON team.rdc_requirement_attachment (relative_path);

-- 按日序号计数器：一行一天，序号分配的唯一来源。
-- UPSERT ... RETURNING 是并发安全的；事务回滚时计数一并回滚（允许复用，不承诺无空洞）。
CREATE TABLE IF NOT EXISTS team.rdc_req_key_sequences (
  day      DATE    PRIMARY KEY,
  next_seq INTEGER NOT NULL DEFAULT 1
);

-- 增量 3：研发任务（一对多："多"侧外键指向需求；需求无删除路径，外键不设级联）。
-- 整体状态不落列：由两条子任务按 contract.AggregateDevStatus 聚合推导。
-- creator_user_id 沿用裸 TEXT 惯例（不加 users 外键，账号删除后历史任务仍可读）；
-- tenant_id 提供公司隔离；status 不加 CHECK（枚举唯一来源是 api/openapi.yaml）。
CREATE TABLE IF NOT EXISTS team.rdc_dev_task (
  id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  requirement_id  UUID        NOT NULL REFERENCES team.rdc_requirements(id),
  title           TEXT        NOT NULL,
  creator_user_id TEXT        NOT NULL DEFAULT '',
  tenant_id       UUID        REFERENCES tenants(id),
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- 列表形态：租户 + 需求 + 时间倒序；单需求多任务的反向查询走 requirement_id 索引。
CREATE INDEX IF NOT EXISTS idx_rdc_dev_task_tenant_created
  ON team.rdc_dev_task (tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_rdc_dev_task_tenant_requirement
  ON team.rdc_dev_task (tenant_id, requirement_id, created_at DESC);

-- 增量 4：子任务。一任务恰好 frontend + backend 各一条，用唯一索引强制；
-- status 默认 pending；note 是编辑接口唯一可改的子任务属性（非状态）；
-- task_id 级联删除，保证删任务后无子任务孤儿行。
CREATE TABLE IF NOT EXISTS team.rdc_dev_subtask (
  id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id    UUID        NOT NULL REFERENCES team.rdc_dev_task(id) ON DELETE CASCADE,
  kind       TEXT        NOT NULL,              -- frontend | backend，枚举见 openapi
  status     TEXT        NOT NULL DEFAULT 'pending',
  note       TEXT        NOT NULL DEFAULT '',   -- 可编辑的非状态配置
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_rdc_dev_subtask_task_kind
  ON team.rdc_dev_subtask (task_id, kind);
CREATE INDEX IF NOT EXISTS idx_rdc_dev_subtask_task
  ON team.rdc_dev_subtask (task_id);

-- 人工审核记录：一条审核动作一行，只追加、无更新/删除。
-- task_id / subtask_id 均级联删除（删任务或子任务时审核历史随行清理，不留孤儿）；
-- reason 仅驳回时有值，commit_sha 仅提交确认时有值，其余动作为空串。
CREATE TABLE IF NOT EXISTS team.rdc_review_record (
  id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id          UUID        NOT NULL REFERENCES team.rdc_dev_task(id) ON DELETE CASCADE,
  subtask_id       UUID        NOT NULL REFERENCES team.rdc_dev_subtask(id) ON DELETE CASCADE,
  action           TEXT        NOT NULL,          -- approve | reject | commit，枚举见 openapi
  reason           TEXT        NOT NULL DEFAULT '',
  commit_sha       TEXT        NOT NULL DEFAULT '',
  reviewer_user_id TEXT        NOT NULL DEFAULT '',
  tenant_id        UUID        REFERENCES public.tenants(id),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_rdc_review_record_task
  ON team.rdc_review_record (task_id, created_at);
CREATE INDEX IF NOT EXISTS idx_rdc_review_record_subtask
  ON team.rdc_review_record (subtask_id, created_at);

-- ---------------------------------------------------------------------------
-- 六·增补 · AI 人机交互证据层（team 专属，循环之外，不为永远为空的 solo 造表）
-- 四张表只承载「决策 / 门禁 / 快照 / 幂等令牌」，都不写子任务状态；
-- 关联键复用 rdc_dev_task / rdc_dev_subtask，级联删除不留孤儿；
-- 枚举不加 CHECK（唯一来源是 api/openapi.yaml 的 x-rdc-interaction-* 扩展）。
-- ---------------------------------------------------------------------------

-- ① Diff 审查会话：files 是 AgentArtifactProvider 产物的不可变 JSONB 快照。
CREATE TABLE IF NOT EXISTS team.rdc_diff_review (
  id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id       UUID        NOT NULL REFERENCES team.rdc_dev_task(id) ON DELETE CASCADE,
  subtask_id    UUID        NOT NULL REFERENCES team.rdc_dev_subtask(id) ON DELETE CASCADE,
  checkpoint_id UUID,                    -- 逻辑关联 rdc_checkpoint.id，无 FK 避免建表顺序依赖
  source        TEXT        NOT NULL,    -- 唯一合法值 agent_executor
  submit_status TEXT        NOT NULL DEFAULT 'pending',
  files         JSONB       NOT NULL DEFAULT '[]'::jsonb,
  tenant_id     UUID        REFERENCES public.tenants(id),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_rdc_diff_review_subtask ON team.rdc_diff_review (subtask_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_rdc_diff_review_tenant  ON team.rdc_diff_review (tenant_id, created_at DESC);

-- ② Hunk 决策：与 files JSONB 内 hunk_id 对齐，单 hunk 可独立更新三态。
CREATE TABLE IF NOT EXISTS team.rdc_hunk_decision (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  review_id   UUID        NOT NULL REFERENCES team.rdc_diff_review(id) ON DELETE CASCADE,
  hunk_id     TEXT        NOT NULL,
  decision    TEXT        NOT NULL DEFAULT 'pending',
  decided_by  TEXT        NOT NULL DEFAULT '',
  decided_at  TIMESTAMPTZ,
  tenant_id   UUID        REFERENCES public.tenants(id),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (review_id, hunk_id)
);
CREATE INDEX IF NOT EXISTS idx_rdc_hunk_decision_review ON team.rdc_hunk_decision (review_id);

-- ③ 计划确认：问卷 + 可确认任务整体存 JSONB；确认时整体重写 + 更新门禁。
CREATE TABLE IF NOT EXISTS team.rdc_plan_confirmation (
  id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id       UUID        NOT NULL REFERENCES team.rdc_dev_task(id) ON DELETE CASCADE,
  subtask_id    UUID        NOT NULL REFERENCES team.rdc_dev_subtask(id) ON DELETE CASCADE,
  questionnaire JSONB       NOT NULL DEFAULT '[]'::jsonb,
  plan_tasks    JSONB       NOT NULL DEFAULT '[]'::jsonb,
  gate_status   TEXT        NOT NULL DEFAULT 'blocked',
  confirmed_by  TEXT        NOT NULL DEFAULT '',
  confirmed_at  TIMESTAMPTZ,
  tenant_id     UUID        REFERENCES public.tenants(id),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_rdc_plan_conf_subtask ON team.rdc_plan_confirmation (subtask_id, created_at DESC);

-- ④ 检查点：一次 AI 动作的文件 before/after 快照 + 幂等回滚令牌。
CREATE TABLE IF NOT EXISTS team.rdc_checkpoint (
  id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id        UUID        NOT NULL REFERENCES team.rdc_dev_task(id) ON DELETE CASCADE,
  subtask_id     UUID        NOT NULL REFERENCES team.rdc_dev_subtask(id) ON DELETE CASCADE,
  ai_action      TEXT        NOT NULL DEFAULT '',
  files          JSONB       NOT NULL DEFAULT '[]'::jsonb,
  rollback_token TEXT        NOT NULL,
  status         TEXT        NOT NULL DEFAULT 'active',
  created_by     TEXT        NOT NULL DEFAULT '',
  tenant_id      UUID        REFERENCES public.tenants(id),
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (rollback_token)
);
CREATE INDEX IF NOT EXISTS idx_rdc_checkpoint_subtask ON team.rdc_checkpoint (subtask_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_rdc_checkpoint_tenant  ON team.rdc_checkpoint (tenant_id, created_at DESC);

-- ⑤ 幂等扩展：审核记录加 detail JSONB，承载四类交互动作的结构化证据。
--    既有 4 列语义不变；action 扩 5 值，DDL 不加 CHECK。
ALTER TABLE team.rdc_review_record ADD COLUMN IF NOT EXISTS detail JSONB NOT NULL DEFAULT '{}'::jsonb;

-- 交付证据只追加；seq 定义同需求当前快照的唯一排序口径。
CREATE TABLE IF NOT EXISTS team.rdc_delivery_record (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  seq BIGSERIAL UNIQUE,
  requirement_id UUID NOT NULL REFERENCES team.rdc_requirements(id),
  tenant_id UUID REFERENCES tenants(id),
  test_result TEXT NOT NULL DEFAULT 'not_registered',
  test_summary TEXT NOT NULL DEFAULT '',
  test_report_ref TEXT NOT NULL DEFAULT '',
  test_registered_by TEXT NOT NULL DEFAULT '',
  test_registered_at TIMESTAMPTZ,
  acceptance_status TEXT NOT NULL DEFAULT 'pending',
  acceptance_items TEXT NOT NULL DEFAULT '[]',
  acceptance_note TEXT NOT NULL DEFAULT '',
  accepted_by TEXT NOT NULL DEFAULT '',
  accepted_at TIMESTAMPTZ,
  archived BOOLEAN NOT NULL DEFAULT false,
  archived_by TEXT NOT NULL DEFAULT '',
  archived_at TIMESTAMPTZ,
  creator_user_id TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_rdc_delivery_tenant_req_seq ON team.rdc_delivery_record (tenant_id, requirement_id, seq DESC);
CREATE INDEX IF NOT EXISTS idx_rdc_delivery_tenant_seq ON team.rdc_delivery_record (tenant_id, seq DESC);

-- 六·增补 · AI 模型配置（team 专属，不进 FOREACH 循环，solo 无此表）。
-- tenant_id 提供公司隔离；RequireTeamMode 是模式边界；明文 Key 绝不入库，
-- api_key_ciphertext / api_key_nonce 为 base64(AES-GCM 密文 / 12B nonce)，
-- api_key_mask 写入时算好，列表读取不解密。枚举唯一来源是 openapi.yaml，DDL 不加 CHECK。
CREATE TABLE IF NOT EXISTS team.rdc_ai_model (
  id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           UUID        REFERENCES public.tenants(id),
  name                TEXT        NOT NULL,
  provider            TEXT        NOT NULL DEFAULT 'scnet',
  protocol            TEXT        NOT NULL DEFAULT 'openai',
  base_url            TEXT        NOT NULL,
  model_id            TEXT        NOT NULL,
  api_key_ciphertext  TEXT        NOT NULL DEFAULT '',
  api_key_nonce       TEXT        NOT NULL DEFAULT '',
  api_key_mask        TEXT        NOT NULL DEFAULT '',
  is_default          BOOLEAN     NOT NULL DEFAULT false,
  thinking_enabled    BOOLEAN     NOT NULL DEFAULT false,
  context_tokens      INTEGER     NOT NULL DEFAULT 8192,
  extra_config        JSONB       NOT NULL DEFAULT '{}'::jsonb,
  capabilities        JSONB       NOT NULL DEFAULT '[]'::jsonb,
  enabled             BOOLEAN     NOT NULL DEFAULT true,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- 列表形态：租户 + 启用态 + 时间倒序；默认置顶由 ORDER BY is_default DESC 完成。
CREATE INDEX IF NOT EXISTS idx_rdc_ai_model_tenant_enabled_created
  ON team.rdc_ai_model (tenant_id, enabled, created_at DESC);
-- 每公司至多一个默认模型；切换默认在事务内先清后置，唯一索引兜底。
CREATE UNIQUE INDEX IF NOT EXISTS uq_rdc_ai_model_default
  ON team.rdc_ai_model (tenant_id) WHERE is_default;

-- 六·增补 · AI 模型调用记录（team 专属，不进 FOREACH 循环，solo 无此表）。
-- 一行 = 一次 Chat/Test 调用事实（含未出站的本地失败，用 used_upstream 区分）。
-- 不设物理外键：记录是历史事实，删模型不得抹掉历史；model_id 保留原值 + 名称/标识快照。
-- token 列**可空**：NULL = 上游没上报 usage（不知道），0 = 上游确实报了 0（如缓存命中）。
-- 这两件事必须分开——把不知道当零会让容量规划系统性偏低，而统计口径本身只依赖
-- tokens_reported 标记（SUM ... FILTER (WHERE tokens_reported)），nullable 不会污染求和。
-- 枚举唯一来源是 openapi.yaml，DDL 不加 CHECK。
CREATE TABLE IF NOT EXISTS team.rdc_ai_model_call (
  id                     UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id              UUID        NOT NULL REFERENCES public.tenants(id),
  model_id               UUID,                              -- 可为 NULL：无匹配 / 限流拒绝时没有目标模型
  model_name             TEXT        NOT NULL DEFAULT '',   -- 快照：模型改名后历史仍可读
  model_identifier       TEXT        NOT NULL DEFAULT '',   -- 快照：上游 model 标识
  protocol               TEXT        NOT NULL DEFAULT '',   -- 快照：当次使用的协议
  scene                  TEXT        NOT NULL DEFAULT '',   -- 调用方场景标识，空串 = 未声明
  success                BOOLEAN     NOT NULL DEFAULT false,
  error_category         TEXT        NOT NULL DEFAULT '',   -- 成功为空串；失败为 7 类之一
  status_code            INTEGER     NOT NULL DEFAULT 0,    -- 上游状态码；本地失败为 0
  prompt_tokens          INTEGER,                           -- NULL = 未上报；0 = 真实 0
  completion_tokens      INTEGER,                           -- 同上
  tokens_reported        BOOLEAN     NOT NULL DEFAULT false,
  latency_ms             BIGINT      NOT NULL DEFAULT 0,    -- 本地拒绝为 0；不含写记录耗时
  used_upstream          BOOLEAN     NOT NULL DEFAULT false,-- 是否真的发出上游请求（限流窗口只数 true）
  used_default           BOOLEAN     NOT NULL DEFAULT false,
  used_fallback          BOOLEAN     NOT NULL DEFAULT false,
  requested_capabilities JSONB       NOT NULL DEFAULT '[]'::jsonb, -- 入参快照：无匹配时可复盘"为什么没选上"
  requested_min_context  INTEGER     NOT NULL DEFAULT 0,
  requested_effort       TEXT        NOT NULL DEFAULT '',
  created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- 存量环境迁移：CREATE TABLE IF NOT EXISTS 不会改已存在表的列约束，
-- 因此这里必须显式放开 NOT NULL。DROP NOT NULL 对已经可空的列是幂等的（不报错），
-- 所以本脚本可以反复执行；同时 DROP DEFAULT 让"未上报"真正落 NULL 而不是被默认值兜成 0。
ALTER TABLE team.rdc_ai_model_call ALTER COLUMN prompt_tokens DROP NOT NULL;
ALTER TABLE team.rdc_ai_model_call ALTER COLUMN prompt_tokens DROP DEFAULT;
ALTER TABLE team.rdc_ai_model_call ALTER COLUMN completion_tokens DROP NOT NULL;
ALTER TABLE team.rdc_ai_model_call ALTER COLUMN completion_tokens DROP DEFAULT;
-- 统计页面与限流窗口的共同入口：同租户 + 时间倒序 range scan。
CREATE INDEX IF NOT EXISTS idx_rdc_ai_model_call_tenant_created
  ON team.rdc_ai_model_call (tenant_id, created_at DESC);
-- 限流窗口专用部分索引：只扫"已出站"的行，"被拒不计入"靠索引条件直接落地。
CREATE INDEX IF NOT EXISTS idx_rdc_ai_model_call_upstream
  ON team.rdc_ai_model_call (tenant_id, created_at DESC) WHERE used_upstream;
-- 按模型分组统计（groupBy=model 的主路径）。
CREATE INDEX IF NOT EXISTS idx_rdc_ai_model_call_tenant_model_created
  ON team.rdc_ai_model_call (tenant_id, model_id, created_at DESC);

-- 六·增补 · AI 模型软限流阈值（team 专属，每公司一行）。
-- 关注点单一：不复用 public.system_settings（那里已是 Webhook / 外部 API Key / Git 路径的集合体，
-- 且全局键值表做"每公司一行"只能靠 key 字符串拼接，加不了唯一约束与类型安全）。
-- 判定只认四元组 (enabled, window_minutes, max_calls, action)；未来加 token 维度不改 DDL，
-- 新维度配置写进 extra_config（未知键原样保留、缺失补默认）。
CREATE TABLE IF NOT EXISTS team.rdc_ai_model_quota (
  tenant_id       UUID        PRIMARY KEY REFERENCES public.tenants(id),
  enabled         BOOLEAN     NOT NULL DEFAULT false,
  max_calls       INTEGER     NOT NULL DEFAULT 1000,
  window_minutes  INTEGER     NOT NULL DEFAULT 60,
  action          TEXT        NOT NULL DEFAULT 'warn',   -- warn | reject
  extra_config    JSONB       NOT NULL DEFAULT '{}'::jsonb,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
