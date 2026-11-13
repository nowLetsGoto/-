# Makefile · Dandelion 统一编排入口
# 职责：契约生成 / 本地开发 / 前后端构建 / 测试 / 容器起停的唯一入口。
# 输入：.env（可选环境变量）、api/openapi.yaml、web/、server/、deploy/。
# 输出：web/src/api/schema.d.ts、server/public/、bin/dandelion。
# 边界：不承载业务逻辑；构建顺序「前端产物 → Go 编译」不可颠倒（//go:embed public/* 需要产物先存在）。

-include .env
export DATABASE_URL PUBLIC_BASE_URL PORT HOST STORAGE_DIR MAX_UPLOAD_MB GO_ENV VITE_API_BASE

.PHONY: gen\:api dev build test up down

# 契约变更时重生成前端类型；生成物入库，前端 dev 不需要 Go、后端 build 不需要 Node。
gen\:api:
	cd web && npx openapi-typescript ../api/openapi.yaml -o src/api/schema.d.ts

# 先在 server/ 编译、再回仓库根运行，使 ./storage 始终指向根目录下同一份数据目录。
dev:
	@cd server && go build -o ../bin/dev-server ./cmd/server
	@( ./bin/dev-server ) & ( npm --prefix web run dev ) & trap 'kill 0' INT TERM; wait

build:
	npm --prefix web run build
	cd server && CGO_ENABLED=0 go build -o ../bin/dandelion ./cmd/server

test:
	cd server && go test ./...

up:
	docker compose -f deploy/docker-compose.yml up -d --build

down:
	docker compose -f deploy/docker-compose.yml down
