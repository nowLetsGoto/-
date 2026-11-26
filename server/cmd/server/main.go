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
	"dandelion/server/internal/handler"
	audithandler "dandelion/server/internal/handler/audit"
	"dandelion/server/internal/handler/login"
	"dandelion/server/internal/handler/openapi"
	peoplehandler "dandelion/server/internal/handler/people"
	projecthandler "dandelion/server/internal/handler/project"
	rolehandler "dandelion/server/internal/handler/role"
	settingshandler "dandelion/server/internal/handler/settings"
	teamshandler "dandelion/server/internal/handler/teams"
	tenanthandler "dandelion/server/internal/handler/tenant"
	userhandler "dandelion/server/internal/handler/user"
	"dandelion/server/internal/repository"
	auditrepo "dandelion/server/internal/repository/audit"
	peopleRepo "dandelion/server/internal/repository/people"
	projectrepo "dandelion/server/internal/repository/project"
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
	rolesvc "dandelion/server/internal/service/role"
	settingssvc "dandelion/server/internal/service/settings"
	teamssvc "dandelion/server/internal/service/teams"
	tenantsvc "dandelion/server/internal/service/tenant"
	usersvc "dandelion/server/internal/service/user"
	webhooksvc "dandelion/server/internal/service/webhook"
	"dandelion/server/internal/storage"
)

func main() {
	// 子命令：把本机已有安装包补齐到 R2。跑完即退出，不启动 HTTP 服务。
	// 放在配置加载之前分派，避免同步工具被建表/监听逻辑牵连。
	if len(os.Args) > 1 && os.Args[1] == "r2sync" {
		if err := runR2Sync(os.Args[2:]); err != nil {
			log.Fatalf("R2 同步失败: %v", err)
		}
		return
	}
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

	// 对象存储镜像：未配置时返回 nil，下载完全走本机磁盘（与改造前一致）。
	// 配置错误也不阻断启动——R2 只是加速通道，不该让它成为服务的硬依赖。
	r2, err := storage.NewR2(context.Background(), storage.R2Config{
		AccountID:     cfg.R2AccountID,
		AccessKeyID:   cfg.R2AccessKeyID,
		SecretKey:     cfg.R2SecretKey,
		Bucket:        cfg.R2Bucket,
		PublicBaseURL: cfg.R2PublicBaseURL,
	})
	if err != nil {
		return fmt.Errorf("初始化对象存储失败: %w", err)
	}
	if r2 != nil {
		if r2.IsPublicCDN() {
			// 公开 CDN 直链：国内走 Cloudflare 节点，比 S3 签名端点快 10-30 倍。
			log.Printf("对象存储镜像已启用 bucket=%s 下载通道=公开CDN(%s)", cfg.R2Bucket, cfg.R2PublicBaseURL)
		} else {
			// 私有桶签名 URL：桶保持私有，但走 S3 端点，国内较慢。
			log.Printf("对象存储镜像已启用 bucket=%s 下载通道=私有签名(S3端点，较慢；配 R2_PUBLIC_BASE_URL 可提速)", cfg.R2Bucket)
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

	// 直传会话仓储只在启用 R2 时有意义：它为预签名上传记录对象名与声明大小。
	// 未启用时保持 nil，服务层的直传接口会给出「未启用对象存储」的明确拒绝。
	var uploadSessions *repository.UploadSessionRepository
	if r2 != nil {
		uploadSessions = repository.NewUploadSessionRepository(db)
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
	settingsService := settingssvc.NewService(settingsRepo)
	webhookService := webhooksvc.NewService(settingsRepo)

	svc := service.NewBuildService(cfg, paths, r2, uploadSessions, buildRepos, projectRepos, webhookService, repository.NewBuildSharePartitioned(db), authzService)
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
		Builds:   builds,
		Open:     openAPI,
		Login:    loginHandler,
		Projects: projectHandler,
		People:   peopleHandler,
		Teams:    teamsHandler,
		Tenants:  tenantHandler,
		Users:    userHandler,
		Audit:    auditHandler,
		Roles:    roleHandler,
		Authz:    authzService,
		Settings: settingsHandler,
		Verify:   loginService.Verify,
		APIKey:   settingsService.APIKeyByRequest,
		Health:   health,
		StaticFS: public,
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
