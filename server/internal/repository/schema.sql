-- server/internal/repository/schema.sql · Dandelion 数据模型（幂等，可重复执行）
-- 职责：定义业务表 builds 与配置表 system_settings，以及 builds 的查询索引；
--       由 //go:embed 打进二进制，进程启动时幂等执行。
-- 输入：无参数；输出：两张表 + 一个索引 + 一行默认配置。
-- 边界：environment 只 NOT NULL 不加 CHECK（枚举唯一来源是 api/openapi.yaml）；
--       三个 URL 字段由服务端按 id 派生，绝不入库，换域名时不需刷数据；
--       system_settings 只允许一行（id 恒为 1），避免"多份配置谁是真相"。
--       容器部署时同一份文件也挂给 PG initdb，保证 DB 就绪即有表。

CREATE TABLE IF NOT EXISTS builds (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
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

CREATE INDEX IF NOT EXISTS idx_builds_env_platform_created
  ON builds (environment, platform, created_at DESC);

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
