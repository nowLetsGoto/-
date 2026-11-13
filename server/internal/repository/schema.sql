-- server/internal/repository/schema.sql · Dandelion 第一版数据模型（幂等，可重复执行）
-- 职责：定义唯一的业务表 builds 与其查询索引；由 //go:embed 打进二进制，进程启动时幂等执行。
-- 输入：无参数；输出：builds 表 + idx_builds_env_platform_created 索引。
-- 边界：第一版只有这一张表；environment 只 NOT NULL 不加 CHECK（枚举唯一来源是 api/openapi.yaml）；
--       三个 URL 字段由服务端按 id 派生，绝不入库，换域名时不需刷数据。
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
