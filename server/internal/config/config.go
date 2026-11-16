// Package config 负责加载并校验运行时配置。
// 职责：读取环境变量、给出默认值、校验对外基址绝对性与网络可达性、校验上传上限与契约一致、校验生产环境令牌密钥。
// 输入：进程环境变量（全仓唯一 os.Getenv 处）；.env 由 Makefile 导出，程序不解析文件。
// 输出：Config 值，或校验失败时返回 error 使进程 fail-fast。
// 边界：不读请求上下文、不拼业务 URL、不访问数据库与文件系统；具体地址生成在 service/build_service.go。
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultMaxUploadMB 与 api/openapi.yaml 的 x-constants.maxUploadMB 一一对应，
// 由 internal/contract/contract_test.go 锁死，两处任改其一测试即失败。
const DefaultMaxUploadMB = 200

const (
	defaultDatabaseURL   = "postgres://dandelion:dandelion@localhost:5432/dandelion?sslmode=disable"
	defaultPublicBaseURL = "http://localhost:8080"
	defaultPort          = "8080"
	defaultHost          = "0.0.0.0"
	defaultStorageDir    = "./storage"
	defaultGOEnv         = "development"

	// 管理员账号默认值：仅便于本地开箱即用；生产环境必须通过环境变量覆盖口令与密钥。
	defaultAdminUsername = "admin"
	defaultAdminPassword = "admin123456"
	// defaultAuthSecret 只用于本地开发；生产环境沿用它会 fail-fast，避免用公开密钥签发令牌。
	defaultAuthSecret = "dandelion-dev-only-secret-change-me"
)

// Config 是启动后只读的配置快照。
type Config struct {
	DatabaseURL string
	// PublicBaseURL 是对外访问入口基址，二维码 / 复制 / 下载三个绝对地址的唯一来源。
	PublicBaseURL string
	// PublicBaseReachable 表示基址能否被其它设备访问；dev 放宽时为 false，前端据此给出中文提示。
	PublicBaseReachable bool
	StorageRoot         string
	MaxUploadMB         int64
	Port                string
	Host                string
	GOEnv               string
	IsProduction        bool

	// AdminUsername / AdminPassword 是唯一管理员账号，登录时定长比较，不落库。
	AdminUsername string
	AdminPassword string
	// AuthSecret 是登录令牌的 HMAC 密钥：只在内存中使用，不进日志、不进响应。
	AuthSecret string
}

// get 是全仓唯一读取环境变量的函数；空值回落到默认值。
func get(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// Load 读取并校验全部配置。生产环境基址非法直接 fail-fast；开发环境放宽但标记不可达。
func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:   get("DATABASE_URL", defaultDatabaseURL),
		PublicBaseURL: get("PUBLIC_BASE_URL", defaultPublicBaseURL),
		Port:          get("PORT", defaultPort),
		Host:          get("HOST", defaultHost),
		GOEnv:         get("GO_ENV", defaultGOEnv),
		AdminUsername: get("ADMIN_USERNAME", defaultAdminUsername),
		AdminPassword: get("ADMIN_PASSWORD", defaultAdminPassword),
		AuthSecret:    get("AUTH_SECRET", defaultAuthSecret),
	}
	cfg.IsProduction = cfg.GOEnv != "development" && cfg.GOEnv != "dev"

	root, err := filepath.Abs(get("STORAGE_DIR", defaultStorageDir))
	if err != nil {
		return Config{}, fmt.Errorf("解析存储根目录失败: %w", err)
	}
	cfg.StorageRoot = root

	rawLimit := get("MAX_UPLOAD_MB", strconv.Itoa(DefaultMaxUploadMB))
	limit, err := strconv.ParseInt(rawLimit, 10, 64)
	if err != nil || limit <= 0 {
		return Config{}, fmt.Errorf("MAX_UPLOAD_MB 必须是正整数，当前为 %q", rawLimit)
	}
	// 契约与 Nginx（210m）都按 200MB 定死，配置只允许调小，不允许放大后绕过前置限制。
	if limit > DefaultMaxUploadMB {
		return Config{}, fmt.Errorf("MAX_UPLOAD_MB 不得超过契约上限 %d MB", DefaultMaxUploadMB)
	}
	cfg.MaxUploadMB = limit

	reachable, err := validatePublicBaseURL(cfg.PublicBaseURL)
	if err != nil {
		return Config{}, err
	}
	if !reachable && cfg.IsProduction {
		return Config{}, fmt.Errorf("PUBLIC_BASE_URL=%q 仅本机/内部可达，生产环境必须配置为手机可访问的绝对地址", cfg.PublicBaseURL)
	}
	cfg.PublicBaseReachable = reachable

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL 不能为空")
	}
	// 默认密钥是公开写在仓库里的，生产环境继续使用等于没有签名校验，直接拒绝启动。
	if cfg.IsProduction && cfg.AuthSecret == defaultAuthSecret {
		return Config{}, fmt.Errorf("AUTH_SECRET 必须配置为随机长串（例如 openssl rand -hex 32），不得使用默认值")
	}
	return cfg, nil
}

// validatePublicBaseURL 校验绝对 http(s) URL 并识别本机 / 容器内部地址。
// 返回 reachable=false 表示地址可用但不该被手机扫码使用（dev 场景）。
func validatePublicBaseURL(raw string) (bool, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" {
		return false, fmt.Errorf("PUBLIC_BASE_URL 必须是形如 https://dandelion.example.com 的绝对地址，当前为 %q", raw)
	}

	host := strings.ToLower(u.Hostname())
	// 容器内部服务名与本机回环地址都不能作为对外基址：二维码会被手机解析成不可达地址。
	forbidden := []string{"localhost", "127.0.0.1", "0.0.0.0", "::1", "db", "server", "nginx", "postgres", "host.docker.internal"}
	for _, name := range forbidden {
		if host == name {
			return false, nil
		}
	}
	// 局域网私有地址（192.168.x.x 等）是本地开发的合法取值，这里只排除回环与通配地址。
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return false, nil
	}
	return true, nil
}
