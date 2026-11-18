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
-- 团队项目 key 全平台唯一（URL 段 /p/{key} 在团队视图里必须能唯一定位）；
-- 单人项目的 key 只需在本人空间内唯一：两个人各自拥有一个 "app" 互不冲突。
-- ---------------------------------------------------------------------------
CREATE UNIQUE INDEX IF NOT EXISTS projects_key_key ON team.projects (key);
CREATE UNIQUE INDEX IF NOT EXISTS projects_owner_key ON solo.projects (owner_user_id, key);

-- 团队分区的默认项目：项目化之前的存量包都归到这里；单人分区不播种，由本人自建。
INSERT INTO team.projects (key, name, description)
VALUES ('default', '默认项目', '项目化迁移时生成的默认项目')
ON CONFLICT (key) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 四 · 全局表（跨模式共享的事实）
-- ---------------------------------------------------------------------------

-- system_settings：单行系统配置（Webhook + 外部 API Key）。
-- webhook_fields 用 TEXT 存 JSON 数组：不依赖驱动的 text[] 扫描实现，
-- 读写统一走 encoding/json，与 contract.WebhookFields 的校验集合天然同构。
-- api_key 首次建表时由数据库生成 32 位随机串，之后只读不改（换 Key 走运维 SQL）。
CREATE TABLE IF NOT EXISTS system_settings (
  id              SMALLINT    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  webhook_enabled BOOLEAN     NOT NULL DEFAULT false,
  webhook_url     TEXT        NOT NULL DEFAULT '',
  webhook_secret  TEXT        NOT NULL DEFAULT '',
  webhook_fields  TEXT        NOT NULL
                  DEFAULT '["appName","version","buildNumber","environment","platform","downloadPageUrl"]',
  api_key         TEXT        NOT NULL,
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO system_settings (id, api_key)
VALUES (1, replace(gen_random_uuid()::text, '-', ''))
ON CONFLICT (id) DO NOTHING;

-- roles / role_permissions：角色与权限点。权限点目录的唯一来源是 contract.Permissions，
-- 这里只存"角色拥有哪些"；内置角色由启动时的 EnsureBuiltinRoles 播种，
-- 之后在页面上改过就不再被覆盖。
CREATE TABLE IF NOT EXISTS roles (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  key         TEXT        UNIQUE NOT NULL,
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

-- 权限点目录缩减后的存量清理：role_permissions.permission 是裸 TEXT，没有外键，
-- 目录里删掉一个权限点不会自动从库里消失。留着它只会让角色编辑页统计"已选数量"时
-- 多出一个不存在的项（前端只渲染目录内的点，勾不上也去不掉）。
-- 这里删的是两个已收归超管的权限点：角色管理改由 RequireSuperAdmin 把关，见 contract.Permissions。
DELETE FROM role_permissions WHERE permission IN ('role.read', 'role.write');

-- 修掉一处权限倒挂：团队模式的默认基线已含 directory.read（普通成员能看内部通讯录），
-- 而老库里 guest 没有它 —— 分配了 guest 的人反而比"没有角色"的人少看一个菜单。
-- 只补这一个点、只对内置 guest：EnsureBuiltin 不会覆盖已存在的角色，存量库只能靠这条。
INSERT INTO role_permissions (role_id, permission)
SELECT id, 'directory.read' FROM roles WHERE key = 'guest'
ON CONFLICT DO NOTHING;

-- people：人员档案。username 为空表示"只在通讯录里，不能登录"。
-- 一个人只挂一个角色（权限取单一集合，避免"多角色取并集"的歧义）。
CREATE TABLE IF NOT EXISTS people (
  id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  name       TEXT        NOT NULL,
  username   TEXT        UNIQUE,
  email      TEXT        NOT NULL DEFAULT '',
  phone      TEXT        NOT NULL DEFAULT '',
  title      TEXT        NOT NULL DEFAULT '',
  role_key   TEXT        REFERENCES roles(key) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 存量库补列：早期 people 表没有 role_key，缺了外键就补一个。
ALTER TABLE people ADD COLUMN IF NOT EXISTS role_key TEXT;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'people_role_fk') THEN
    ALTER TABLE people ADD CONSTRAINT people_role_fk
      FOREIGN KEY (role_key) REFERENCES roles(key) ON DELETE SET NULL;
  END IF;
END $$;

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

-- 团队项目的角色：进入该项目时的"项目角色"，优先级高于人员自身的角色。
-- 允许为空：为空表示"沿用人员角色"，这是默认状态。
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'projects_role_fk') THEN
    ALTER TABLE team.projects ADD CONSTRAINT projects_role_fk
      FOREIGN KEY (role_key) REFERENCES roles(key) ON DELETE SET NULL;
  END IF;
END $$;
