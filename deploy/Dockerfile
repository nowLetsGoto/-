# deploy/Dockerfile · 三阶段构建，全链路无 cgo。
# 职责：node 阶段产出前端 dist → golang 阶段把 dist 放进 embed 目录并编译单二进制 → 运行阶段只留二进制与证书。
# 输入：仓库根作为 build context（web/、server/（含 internal/repository/schema.sql 与 public/）、api/）。
# 输出：可运行镜像；CMD 直接启动 /app/dandelion。
# 边界：使用 glibc 的 bookworm-slim（不用 alpine），better-sqlite3 类 cgo 依赖不存在，但仍保证 CGO_ENABLED=0；
#       构建顺序不可颠倒：//go:embed public/* 要求前端产物先拷入 server/public/。

FROM node:20-bookworm-slim AS frontend
WORKDIR /src
COPY web/package.json web/package-lock.json ./web/
RUN cd web && npm ci
COPY web ./web
COPY api ./api
RUN cd web && npm run build

FROM golang:1.24-bookworm AS backend
WORKDIR /src
COPY server/go.mod server/go.sum ./server/
RUN cd server && go mod download
COPY server ./server
COPY --from=frontend /src/server/public ./server/public
RUN cd server && CGO_ENABLED=0 go build -o /out/dandelion ./cmd/server

FROM debian:bookworm-slim AS runtime
WORKDIR /app
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
COPY --from=backend /out/dandelion /app/dandelion
ENV HOST=0.0.0.0 PORT=8080 STORAGE_DIR=/app/storage GO_ENV=production
EXPOSE 8080
CMD ["/app/dandelion"]
