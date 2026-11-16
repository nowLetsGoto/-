-- server/internal/repository/schema.sql · Dandelion 数据模型（幂等，可重复执行）
-- 职责：定义业务表 builds、项目表 projects、配置表 system_settings 与索引；
--       由 //go:embed 打进二进制，进程启动时幂等执行。
-- 输入：无参数；输出：三张表 + 索引 + 一行默认项目 + 一行默认配置。
-- 边界：environment 只 NOT NULL 不加 CHECK（枚举唯一来源是 api/openapi.yaml）；
--       三个 URL 字段由服务端按 id 派生，绝不入库，换域名时不需刷数据；
--       system_settings 只允许一行（id 恒为 1），避免"多份配置谁是真相"。
--       容器部署时同一份文件也挂给 PG initdb，保证 DB 就绪即有表。

-- ---------------------------------------------------------------------------
-- projects · 业务数据的一级归属
-- key 是 URL 段（/p/{key}/...），只允许小写字母 / 数字 / 短横线，创建后不可改。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS projects (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  key         TEXT        UNIQUE NOT NULL,
  name        TEXT        NOT NULL,
  description TEXT        NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 项目化之前的存量包全部归到默认项目；已有同名项目时跳过。
INSERT INTO projects (key, name, description)
VALUES ('default', '默认项目', '项目化迁移时生成的默认项目')
ON CONFLICT (key) DO NOTHING;

-- 项目库地址（GitLab / GitHub 等）；空串表示未填，详情页据此渲染可点击链接。
ALTER TABLE projects ADD COLUMN IF NOT EXISTS repo_url TEXT NOT NULL DEFAULT '';

-- ---------------------------------------------------------------------------
-- builds · 应用包
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS builds (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id          UUID        REFERENCES projects(id),
  environment         TEXT        NOT NULL,
  platform            TEXT        NOT NULL,
  app_name            TEXT        NOT NULL,
  package_id          TEXT        NOT NULL DEFAULT '',
  version             TEXT        NOT NULL DEFAULT '',
  build_number        TEXT        NOT NULL DEFAULT '',
  changelog           TEXT        NOT NULL DEFAULT '',
  original_file_name  TEXT        NOT NULL,
  file_size           BIGINT      NOT NULL,
  file_ext            TEXT        NOT NULL,
  relative_path       TEXT        NOT NULL,
  meta_source         TEXT        NOT NULL DEFAULT 'fallback',
  download_count      BIGINT      NOT NULL DEFAULT 0,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 存量库补列 + 回填 + 收紧约束（三步都幂等）
ALTER TABLE builds ADD COLUMN IF NOT EXISTS project_id UUID;

UPDATE builds
SET project_id = (SELECT id FROM projects WHERE key = 'default')
WHERE project_id IS NULL;

ALTER TABLE builds ALTER COLUMN project_id SET NOT NULL;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'builds_project_fk') THEN
    ALTER TABLE builds ADD CONSTRAINT builds_project_fk
      FOREIGN KEY (project_id) REFERENCES projects(id);
  END IF;
END $$;

-- 列表查询永远带 project_id，旧索引不再匹配，直接替换。
DROP INDEX IF EXISTS idx_builds_env_platform_created;
CREATE INDEX IF NOT EXISTS idx_builds_project_env_platform_created
  ON builds (project_id, environment, platform, created_at DESC);

-- ---------------------------------------------------------------------------
-- system_settings · 单行系统配置（Webhook + 外部 API Key）
-- webhook_fields 用 TEXT 存 JSON 数组：不依赖驱动的 text[] 扫描实现，
-- 读写统一走 encoding/json，与 contract.WebhookFields 的校验集合天然同构。
-- api_key 首次建表时由数据库生成 32 位随机串，之后只读不改（换 Key 走运维 SQL）。
-- ---------------------------------------------------------------------------
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

-- ---------------------------------------------------------------------------
-- 基础资料 · 人员 / 团队 / 三张多对多关联表
-- people.username 与登录账号对应：为空表示"只在通讯录里，不能登录"。
-- 关联表都带 ON DELETE CASCADE，删人员/团队/项目时不会留下悬挂关系。
-- project_members 是为 P2 的「项目成员 + 角色」预留的落点：届时加 role 列即可。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS people (
  id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  name       TEXT        NOT NULL,
  username   TEXT        UNIQUE,
  email      TEXT        NOT NULL DEFAULT '',
  phone      TEXT        NOT NULL DEFAULT '',
  title      TEXT        NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS teams (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  name        TEXT        NOT NULL,
  description TEXT        NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS team_members (
  team_id   UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  person_id UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  PRIMARY KEY (team_id, person_id)
);

CREATE INDEX IF NOT EXISTS idx_team_members_person ON team_members (person_id);

CREATE TABLE IF NOT EXISTS project_members (
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  person_id  UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  PRIMARY KEY (project_id, person_id)
);

CREATE INDEX IF NOT EXISTS idx_project_members_person ON project_members (person_id);

CREATE TABLE IF NOT EXISTS team_projects (
  team_id    UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  PRIMARY KEY (team_id, project_id)
);

CREATE INDEX IF NOT EXISTS idx_team_projects_project ON team_projects (project_id);

-- ---------------------------------------------------------------------------
-- users · 账号（替代"环境变量单账号"）
-- 密码只存 PBKDF2-HMAC-SHA256 派生值（stdlib crypto/pbkdf2，无外部依赖）；
-- token_version 自增即让该账号已发出的令牌全部失效（强制下线，无需会话表）；
-- person_id 可空：账号可以先建，之后再绑定人员档案。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS users (
  id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  username       TEXT        UNIQUE NOT NULL,
  password_hash  TEXT        NOT NULL,
  person_id      UUID        REFERENCES people(id) ON DELETE SET NULL,
  is_super_admin BOOLEAN     NOT NULL DEFAULT false,
  status         TEXT        NOT NULL DEFAULT 'active',
  token_version  INT         NOT NULL DEFAULT 1,
  last_login_at  TIMESTAMPTZ,
  last_login_ip  TEXT        NOT NULL DEFAULT '',
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_users_person ON users (person_id);

-- ---------------------------------------------------------------------------
-- audit_logs · 审计日志（只读，任何人不可改不可删）
-- actor_name 冗余存姓名：账号改名或被禁用后，历史日志仍可读。
-- detail 存 JSONB：记录 before/after 等结构化差异，展示时再格式化。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS audit_logs (
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
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_created ON audit_logs (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_action ON audit_logs (action, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_actor ON audit_logs (actor_name, created_at DESC);

-- ---------------------------------------------------------------------------
-- roles / role_permissions · 角色与权限点
-- 权限点目录的唯一来源是 contract.Permissions（代码里），这里只存"角色拥有哪些"。
-- 内置角色由启动时的 EnsureBuiltinRoles 播种：只在角色不存在时写默认权限，
-- 之后在页面上改过就不再被覆盖。
-- ---------------------------------------------------------------------------
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

-- ---------------------------------------------------------------------------
-- 人员的角色：一个人只挂一个角色（权限取单一集合，避免"多角色取并集"的歧义）。
-- 放开外键到 roles(key)：角色被删时自动置空，不会让人员记录变成悬空引用。
-- ---------------------------------------------------------------------------
ALTER TABLE people ADD COLUMN IF NOT EXISTS role_key TEXT;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'people_role_fk') THEN
    ALTER TABLE people ADD CONSTRAINT people_role_fk
      FOREIGN KEY (role_key) REFERENCES roles(key) ON DELETE SET NULL;
  END IF;
END $$;

-- ---------------------------------------------------------------------------
-- 项目的角色：进入该项目时的"项目角色"，优先级高于人员自身的角色。
-- 判定顺序：超管 → 本项目角色 → 人员角色 → 空集（无权限）。
-- 允许为空：为空表示"沿用人员角色"，这是默认状态。
-- ---------------------------------------------------------------------------
ALTER TABLE projects ADD COLUMN IF NOT EXISTS role_key TEXT;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'projects_role_fk') THEN
    ALTER TABLE projects ADD CONSTRAINT projects_role_fk
      FOREIGN KEY (role_key) REFERENCES roles(key) ON DELETE SET NULL;
  END IF;
END $$;

-- builds 的操作者：单人模式只显示自己上传的包；外部 API 上传固定记为 system。
ALTER TABLE builds ADD COLUMN IF NOT EXISTS owner_user_id TEXT NOT NULL DEFAULT 'system';
CREATE INDEX IF NOT EXISTS idx_builds_owner ON builds (owner_user_id, created_at DESC);
