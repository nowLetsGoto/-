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
	"dandelion/server/internal/secret"
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
	tunnelsvc "dandelion/server/internal/service/tunnel"
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

	// 对象存储按公司隔离：客户端不再于此处构造，而是等数据库就绪后
	// 按每家公司的配置逐个装载（见下方 bootstrapR2）。
	// 严格隔离下没有「平台统一存储」——未配置的公司直接视为未启用 R2。
	r2Ref := storage.NewR2Ref()

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

	// 直传会话仓储无条件创建：它只写数据库，与 R2 是否启用无关。
	// 能否直传由服务层按「该公司是否配了 R2」判断（s.r2.Get(tenantID) 是否为 nil）。
	uploadSessions := repository.NewUploadSessionRepository(db)

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

	// 凭据加密：密钥由 AUTH_SECRET 经 HKDF 派生，与令牌签名使用不同子密钥。
	// 构造失败即 fail-fast —— 没有它就无法读写 R2 密钥，
	// 而带着一个"半可用"的加密能力启动服务，故障会推迟到管理员保存配置时才暴露。
	credentialCipher, err := secret.New(cfg.AuthSecret)
	if err != nil {
		return fmt.Errorf("初始化凭据加密失败: %w", err)
	}
	// fallback 用启动配置里的 R2 参数：换机器部署时，这些值原本只在 .env / plist 里，
	// 设置页打开一片空白，管理员得从别处抄一遍。传进去让页面能预填。
	// 一旦有人在页面上保存过，就以库里为准（判定在 service 的 stored）。
	r2SettingsService := settingssvc.NewR2Service(settingsRepo, credentialCipher, storage.R2Config{
		AccountID:     cfg.R2AccountID,
		AccessKeyID:   cfg.R2AccessKeyID,
		SecretKey:     cfg.R2SecretKey,
		Bucket:        cfg.R2Bucket,
		PublicBaseURL: cfg.R2PublicBaseURL,
	}, r2Ref)

	// 启动时按库里的配置装载各公司的客户端。
	// 只装载已配置的公司：未配置的公司不建实例，服务层取到 nil 即按未启用处理。
	// 单个公司配置有误只记日志、不阻断启动——R2 是加速通道，
	// 让它挡住整个服务启动是本末倒置（其余公司仍可正常工作）。
	for _, tenantID := range configuredTenants(startupCtx, settingsRepo) {
		if err := r2SettingsService.Apply(startupCtx, tenantID); err != nil {
			log.Printf("装载公司 R2 客户端失败 tenant=%s err=%v（该公司的下载将走本机）", tenantID, err)
			continue
		}
		if client := r2Ref.Get(tenantID); client != nil {
			log.Printf("对象存储已装载 tenant=%s bucket=%s 通道=%s",
				tenantID, client.Bucket(), channelOf(client))
		}
	}
	if r2Ref.TenantCount() == 0 {
		log.Printf("对象存储未启用：没有任何公司配置 R2，全部下载走本机磁盘")
	}

	svc := service.NewBuildService(cfg, paths, r2Ref, uploadSessions, buildRepos, projectRepos, webhookService, repository.NewBuildSharePartitioned(db), authzService)
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
	settingsHandler := settingshandler.NewHandler(settingsService, r2SettingsService, tunnelsvc.NewService(cfg), webhookService, auditService)
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

	// 存储淘汰巡检：每 2 小时遍历所有租户，把超出配额的对象移出 R2。
	// 在 HTTP 服务之前启动，让它随 ctx 一起在退出信号到来时停止；
	// 未配置对象存储时内部直接返回，不影响服务启动。
	svc.RunRetentionWorker(ctx, tenantRepository)

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

// configuredTenants 列出需要在启动时装载客户端的公司 id。
//
// 查询失败只记日志并返回空列表：R2 是可选加速通道，
// 一次数据库抖动不该让服务起不来；代价是本次启动所有下载走本机。
func configuredTenants(ctx context.Context, repo *settingsrepo.Repository) []string {
	ids, err := repo.ListConfiguredTenants(ctx)
	if err != nil {
		log.Printf("读取已配置 R2 的公司失败，本次启动不装载任何客户端: %v", err)
		return nil
	}
	return ids
}

// channelOf 描述客户端的下载通道，仅用于启动日志。
//
// 公开 CDN 与私有签名端点的速度差 10-30 倍，管理员看日志就能判断
// 是否漏配了自定义域名——这比事后排查「下载为什么慢」便宜得多。
func channelOf(client *storage.R2) string {
	if client.IsPublicCDN() {
		return "公开CDN"
	}
	return "私有签名(S3端点，较慢；配自定义域名可提速)"
}
