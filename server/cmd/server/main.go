// main.go · 装配层与进程生命周期。
// 职责：加载配置 → 连接 PostgreSQL 并幂等建表 → 建目录/清 tmp → 注入各层 → 监听 → 优雅退出。
// 输入：环境变量（由 config 单点读取）与内嵌前端产物、内嵌建表 DDL。
// 输出：一个监听 PORT 的 HTTP 服务，包含 API、静态资源与 SPA fallback。
// 边界：不含业务逻辑；不定义任何 Publisher / EventBus / Event 类型；
//       构造函数参数化本身就是未来接入旁路时唯一需要改动的位置。
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

	staticfs "dandelion/server"
	"dandelion/server/internal/app"
	"dandelion/server/internal/config"
	"dandelion/server/internal/handler"
	loginhandler "dandelion/server/internal/handler/login"
	"dandelion/server/internal/repository"
	"dandelion/server/internal/service"
	loginsvc "dandelion/server/internal/service/login"
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

	repo := repository.NewBuildRepository(db)
	// DDL 由 repository 包 embed 进二进制，启动时幂等执行：不依赖当前工作目录，
	// 容器里由 initdb 先建表，应用启动再执行一次作为双保险。
	if err := repo.EnsureSchema(startupCtx); err != nil {
		return err
	}

	svc := service.NewBuildService(cfg, paths, repo)
	if err := svc.PurgeTmp(); err != nil {
		return err
	}

	loginService := loginsvc.NewService(cfg)
	loginHandler := loginhandler.NewHandler(loginService)
	builds := handler.NewBuildHandler(cfg, svc, paths)
	health := handler.NewHealthHandler(repo, paths)
	public, err := fs.Sub(staticfs.PublicFiles, "public")
	if err != nil {
		return fmt.Errorf("读取内嵌静态资源失败: %w", err)
	}
	router := app.NewRouter(app.Deps{
		Builds:   builds,
		Login:    loginHandler,
		Verify:   loginService.Verify,
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
