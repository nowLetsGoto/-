// main.go · 装配层与进程生命周期。
// 职责：加载配置 → 连接 PostgreSQL 并幂等建表 → 建目录/清 tmp → 注入各层 → 监听 → 优雅退出。
// 输入：环境变量（由 config 单点读取）与内嵌前端产物、内嵌建表 DDL。
// 输出：一个监听 PORT 的 HTTP 服务，包含 API、静态资源与 SPA fallback。
// 边界：不含业务逻辑；不定义任何 Publisher / EventBus / Event 类型；
//
//	构造函数参数化本身就是未来接入旁路时唯一需要改动的位置。
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // 注册 database/sql 的 "pgx" 驱动

	staticfs "dandelion/server"
	"dandelion/server/internal/app"
	"dandelion/server/internal/config"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/gitreader"
	"dandelion/server/internal/handler"
	audithandler "dandelion/server/internal/handler/audit"
	"dandelion/server/internal/handler/login"
	"dandelion/server/internal/handler/openapi"
	peoplehandler "dandelion/server/internal/handler/people"
	projecthandler "dandelion/server/internal/handler/project"
	rdchandler "dandelion/server/internal/handler/rdc"
	rolehandler "dandelion/server/internal/handler/role"
	settingshandler "dandelion/server/internal/handler/settings"
	teamshandler "dandelion/server/internal/handler/teams"
	tenanthandler "dandelion/server/internal/handler/tenant"
	userhandler "dandelion/server/internal/handler/user"
	"dandelion/server/internal/modelkey"
	"dandelion/server/internal/repository"
	auditrepo "dandelion/server/internal/repository/audit"
	peopleRepo "dandelion/server/internal/repository/people"
	projectrepo "dandelion/server/internal/repository/project"
	rdcrepo "dandelion/server/internal/repository/rdc"
	rolerepo "dandelion/server/internal/repository/role"
	settingsrepo "dandelion/server/internal/repository/settings"
	teamsRepo "dandelion/server/internal/repository/teams"
	tenantRepo "dandelion/server/internal/repository/tenant"
	userRepo "dandelion/server/internal/repository/user"
	"dandelion/server/internal/service"
	auditsvc "dandelion/server/internal/service/audit"
	authzsvc "dandelion/server/internal/service/authz"
	loginsvc "dandelion/server/internal/service/login"
	peoplesvc "dandelion/server/internal/service/people"
	projectsvc "dandelion/server/internal/service/project"
	rdcsvc "dandelion/server/internal/service/rdc"
	rolesvc "dandelion/server/internal/service/role"
	settingssvc "dandelion/server/internal/service/settings"
	teamssvc "dandelion/server/internal/service/teams"
	tenantsvc "dandelion/server/internal/service/tenant"
	usersvc "dandelion/server/internal/service/user"
	webhooksvc "dandelion/server/internal/service/webhook"
	"dandelion/server/internal/storage"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("Dandelion 启动失败: %v", err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	paths, err := storage.NewPaths(cfg.StorageRoot)
	if err != nil {
		return err
	}
	for _, dir := range []string{paths.TmpDir(), paths.UploadsDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建运行时目录失败 %s: %w", dir, err)
		}
	}

	// 数据库：pgx 的 database/sql 适配层没有默认注册，必须靠副作用导入把 "pgx" 驱动挂上去，
	// 否则 sql.Open("pgx") 会在运行时才报 unknown driver。全仓只有这一处打开连接，放这里最省心。
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("打开数据库连接失败: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelStartup()
	if err := db.PingContext(startupCtx); err != nil {
		return fmt.Errorf("PostgreSQL 不可用，请先启动数据库: %w", err)
	}

	// 建表：DDL 由 repository 包 embed 进二进制，启动时幂等执行（含公开表迁入 team 分区）。
	// 不依赖当前工作目录；容器里由 initdb 先建表，应用启动再执行一次作为双保险。
	if err := repository.EnsureSchema(startupCtx, db); err != nil {
		return err
	}

	// 业务数据分两个分区：单人写 solo、团队写 team，各有一套 projects / builds / audit_logs。
	// 装配层为每个分区各建一份同构仓储，上层只按会话模式取用，不出现 if team / else solo。
	buildRepos := repository.NewBuildPartitioned(db)

	// 项目是业务数据的一级归属：发布与查询都要先按 key 解析出项目。
	projectRepos := projectrepo.NewPartitioned(db)
	projectService := projectsvc.NewService(projectRepos)

	// 基础资料：人员与团队各自持有主表，三张关联表由两侧仓储分别维护。
	// 公司表只提供 /me 需要的名字；归属由身份推导，不由请求参数决定。
	tenantRepository := tenantRepo.NewRepository(db)
	peopleRepository := peopleRepo.NewRepository(db)
	teamsRepository := teamsRepo.NewRepository(db)
	teamsService := teamssvc.NewService(teamsRepository)
	roleRepository := rolerepo.NewRepository(db)

	// 权限判定：超管全量 → 单人业务基线 → 项目角色 → 人员角色 → 空集。
	// scope 由同一个服务推导：team_admin 查 team_admins，member 查 team_members。
	// 提前到这里装配，是因为 peopleService 要用它校验「角色能不能分配给这个人」（防提权）。
	usersRepository := userRepo.NewRepository(db)
	authzService := authzsvc.NewService(usersRepository, peopleRepository, projectRepos, roleRepository, teamsRepository)

	peopleService := peoplesvc.NewService(peopleRepository, tenantRepository, authzService)

	// 系统设置与 Webhook：配置读写归 settings，投递归 webhook；
	// 发布流程只依赖 service.BuildNotifier 接口，因此不需要认识 webhook 的实现。
	settingsRepo := settingsrepo.NewRepository(db)
	settingsService := settingssvc.NewService(settingsRepo, cfg.GitAllowedRoots...)
	webhookService := webhooksvc.NewService(settingsRepo)

	svc := service.NewBuildService(cfg, paths, buildRepos, projectRepos, webhookService)
	if err := svc.PurgeTmp(); err != nil {
		return err
	}

	// 账号：users 表是唯一真相，环境变量只做首次种子。
	// 角色：权限点目录在 contract，库里只存"角色拥有哪些"；内置角色只在缺失时播种。
	roleService := rolesvc.NewService(roleRepository)
	if err := roleService.EnsureBuiltin(context.Background()); err != nil {
		return err
	}
	// 修正存量内置角色：tenant_id 归零为"平台级共享"，并清掉残留的管理类权限点
	// （directory.write / settings.* 等已改由 account_level 直接赋予）。
	if err := roleService.NormalizeRoles(context.Background()); err != nil {
		return err
	}

	// 审计：只追加不修改，写失败只记日志、绝不影响业务操作。
	// 单人 / 团队各写各的分区，单人操作不会混进团队审计。
	auditRepos := auditrepo.NewPartitioned(db)
	auditService := auditsvc.NewService(auditRepos)

	// 研发中心需求池：仓储固定绑定 team 分区，不为 solo 装配第二份；
	// 模式边界由路由的 RequireTeamMode 承担，创建审计复用同一个 auditService。
	// 项目校验器只取 team 分区的仓储：需求归属的是 team.projects，跨公司由 scope 过滤兜住。
	rdcRepository := rdcrepo.NewRepository(db)
	rdcService := rdcsvc.NewService(rdcRepository, projectRepos.For(contract.ModeTeam), paths)
	rdcHandler := rdchandler.NewHandler(rdcService, auditService, paths)

	// 研发任务编排层：仓储固定 team 分区，需求仓储同时充当 RequirementValidator。
	// 7 个操作共用 rdc.dev.read，写审计复用同一个 auditService（team 模式落 team.audit_logs）。
	devRepository := rdcrepo.NewDevRepository(db)
	devService := rdcsvc.NewDevService(devRepository, rdcRepository)
	devHandler := rdchandler.NewDevHandler(devService, auditService)

	// 人工审核中心：审核仓储复用研发仓储的表名与 JOIN；状态写入经 DevService 唯一入口。
	// 5 个操作共用 rdc.review.read，写审计复用同一个 auditService。
	reviewRepository := rdcrepo.NewReviewRepository(db, devRepository)
	reviewService := rdcsvc.NewReviewService(reviewRepository, devService)
	reviewHandler := rdchandler.NewReviewHandler(reviewService, auditService)

	// AI 人机交互层：复用 03 的 team 分区与审核记录表；生产注入 unavailableProvider——
	// 所有产物读取都真实报「执行器未就绪」，UI 显示真实空态、零假数据、零假按钮。
	// Group B 环境解锁后只需把这里的一处 provider 换成真实执行器实现。
	interactionRepository := rdcrepo.NewInteractionRepository(db)
	interactionService := rdcsvc.NewInteractionService(interactionRepository, rdcsvc.NewUnavailableProvider())
	interactionHandler := rdchandler.NewInteractionHandler(interactionService, auditService)

	deliveryRepo := rdcrepo.NewDeliveryRepository(db)
	deliveryService := rdcsvc.NewDeliveryService(deliveryRepo, settingsRepo, gitreader.NewReader(cfg.GitAllowedRoots, cfg.GitTimeoutSeconds))
	deliveryHandler := rdchandler.NewDeliveryHandler(deliveryService, auditService)

	// 第 5 菜单「AI模型」：主密钥由 config 校验后交给唯一加密入口；构造失败直接中止启动。
	// 配置业务与统一 Chat 共用同一个 team 仓储；HTTP 客户端全局唯一，硬超时由契约限定。
	modelCodec, err := modelkey.New(cfg.ModelEncryptionKey)
	if err != nil {
		return fmt.Errorf("初始化 AI 模型加密器失败: %w", err)
	}
	aiModelRepo := rdcrepo.NewAIModelRepository(db)
	// 统计与限流各自一张表、各自一个仓储（生命周期不同，不合并成"胖仓储"）。
	aiModelCallRepo := rdcrepo.NewAIModelCallRepository(db)
	aiModelQuotaRepo := rdcrepo.NewAIModelQuotaRepository(db)
	aiModelService := rdcsvc.NewModelService(aiModelRepo, modelCodec)
	// 注意：清理过期记录（DeleteExpired）本版只提供仓储方法与单测，未接任何调度器；
	// 接定时任务时在此处新增一行调度装配即可，不会影响已有调用路径。
	modelHTTPClient := &http.Client{Timeout: contract.ModelCallHardTimeout}
	modelChatService := rdcsvc.NewModelChatService(
		aiModelRepo, aiModelCallRepo, aiModelQuotaRepo, modelCodec, modelHTTPClient)
	aiModelHandler := rdchandler.NewAIModelHandler(aiModelService, modelChatService, auditService)
	aiModelStatsService := rdcsvc.NewModelStatsService(aiModelCallRepo, aiModelQuotaRepo)
	aiModelUsageHandler := rdchandler.NewAIModelUsageHandler(aiModelStatsService, auditService)

	// 删账号要顺带清掉它在单人分区的项目与文件，因此账号服务依赖清理器与存储路径。
	// teams 仓储用于校验团队管理员的团队归属，防止跨公司/跨团队授权；
	// authzService 用于校验「调用者有没有资格分配这个角色」（非超管拿不到 super-admin）。
	userService := usersvc.NewService(usersRepository, repository.NewAccountCleanup(db), paths, teamsRepository, authzService)

	// 公司管理：建公司时原子地建其平台管理员；只有超管可达（路由层把关）。
	tenantService := tenantsvc.NewService(tenantRepository, roleService)

	loginService := loginsvc.NewService(cfg, usersRepository, tenantRepository)
	if err := loginService.EnsureAdmin(context.Background()); err != nil {
		return err
	}
	loginHandler := login.NewHandler(loginService)
	builds := handler.NewBuildHandler(cfg, svc, paths, auditService, authzService)
	openAPI := openapi.NewHandler(cfg, svc, paths)
	projectHandler := projecthandler.NewHandler(projectService, auditService)
	peopleHandler := peoplehandler.NewHandler(peopleService, authzService, auditService)
	teamsHandler := teamshandler.NewHandler(teamsService, auditService)
	tenantHandler := tenanthandler.NewHandler(tenantService, auditService)
	userHandler := userhandler.NewHandler(userService, auditService)
	auditHandler := audithandler.NewHandler(auditService)
	roleHandler := rolehandler.NewHandler(roleService, auditService)
	settingsHandler := settingshandler.NewHandler(settingsService, webhookService, auditService)
	health := handler.NewHealthHandler(buildRepos.For(contract.ModeTeam), paths)
	public, err := fs.Sub(staticfs.PublicFiles, "public")
	if err != nil {
		return fmt.Errorf("读取内嵌静态资源失败: %w", err)
	}
	router := app.NewRouter(app.Deps{
		Builds:         builds,
		Open:           openAPI,
		Login:          loginHandler,
		Projects:       projectHandler,
		People:         peopleHandler,
		Teams:          teamsHandler,
		Tenants:        tenantHandler,
		Users:          userHandler,
		Audit:          auditHandler,
		Roles:          roleHandler,
		Authz:          authzService,
		Settings:       settingsHandler,
		RDC:            rdcHandler,
		RDCDev:         devHandler,
		RDCReview:      reviewHandler,
		RDCInteraction: interactionHandler,
		RDCDelivery:    deliveryHandler,
		RDCModel:       aiModelHandler,
		RDCModelUsage:  aiModelUsageHandler,
		Verify:         loginService.Verify,
		APIKey:         settingsService.APIKeyByRequest,
		Health:         health,
		StaticFS:       public,
	})

	server := &http.Server{
		Addr:              cfg.Host + ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("优雅退出时关闭 HTTP 服务失败: %v", err)
		}
	}()

	log.Printf("Dandelion 就绪 %s | 对外基址=%s | 手机可扫码=%v | 存储根=%s",
		server.Addr, cfg.PublicBaseURL, cfg.PublicBaseReachable, paths.Root())
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("HTTP 服务异常退出: %w", err)
	}
	log.Printf("Dandelion 已停止，数据库连接已关闭")
	return nil
}
