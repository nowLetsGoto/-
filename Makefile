# Makefile · Dandelion 统一编排入口
# 职责：契约生成 / 本地开发 / 前后端构建 / 测试 / 容器起停的唯一入口。
# 输入：.env（可选环境变量）、api/openapi.yaml、web/、server/、deploy/。
# 输出：web/src/api/schema.d.ts、server/public/、bin/dandelion。
# 边界：不承载业务逻辑；构建顺序「前端产物 → Go 编译」不可颠倒（//go:embed public/* 需要产物先存在）。

-include .env
export DATABASE_URL PUBLIC_BASE_URL PORT HOST STORAGE_DIR MAX_UPLOAD_MB GO_ENV VITE_API_BASE ADMIN_USERNAME ADMIN_PASSWORD AUTH_SECRET

.PHONY: gen\:api dev dev-web build deploy test lint up down

# 契约变更时重生成前端类型；生成物入库，前端 dev 不需要 Go、后端 build 不需要 Node。
gen\:api:
	cd web && npx openapi-typescript ../api/openapi.yaml -o src/api/schema.d.ts

# 先在 server/ 编译、再回仓库根运行，使 ./storage 始终指向根目录下同一份数据目录。
dev:
	@cd server && go build -o ../bin/dev-server ./cmd/server
	@( ./bin/dev-server ) & ( npm --prefix web run dev ) & trap 'kill 0' INT TERM; wait

# 只启动本地前端；VITE_API_BASE 指定要联调的后端，留空时仍代理到本地 :10088。
dev-web:
	npm --prefix web run dev -- --port 5173 --strictPort

build:
	npm --prefix web run build
	cd server && CGO_ENABLED=0 go build -o ../bin/dandelion ./cmd/server

# 构建并让「正在对外服务的后台进程」立即跑上新版本。
# 为什么需要单独一步：launchd 常驻的是 ~/dandelion-runtime/dandelion 的副本，
# 直接把新构建拷过去并重启服务，公网才会看到最新代码。
# 若后台服务尚未安装，这一步只做构建，不报错。
deploy:
	@$(MAKE) build
	@mkdir -p "$$HOME/dandelion-runtime/logs"
	@cp -f bin/dandelion "$$HOME/dandelion-runtime/dandelion"
	@chmod +x "$$HOME/dandelion-runtime/dandelion"
	@if launchctl print "gui/$$(id -u)/com.dandelion.server" >/dev/null 2>&1; then \
		launchctl kickstart -k "gui/$$(id -u)/com.dandelion.server"; \
		echo "已重启服务，公网将使用新版本"; \
	else \
		echo "后台服务未安装（跳过重启）。需要常驻请执行：bash scripts/install-autostart.sh"; \
	fi

test:
	cd server && go test ./...

# 前端静态检查：ESLint（TS/TSX）+ Stylelint（CSS）。格式由 prettier 在提交时统一处理。
lint:
	npm --prefix web run lint

up:
	docker compose -f deploy/docker-compose.yml up -d --build

down:
	docker compose -f deploy/docker-compose.yml down
