// r2.go · Cloudflare R2 对象存储客户端（S3 兼容协议）。
//
// 职责：把本机磁盘上的安装包镜像到 R2，并为下载生成短期签名直链，
//
//	让公网下载不再经过本机带宽（家宽 / 小带宽服务器是主要瓶颈）。
//
// 输入：账号 ID、Access Key、Secret Key、桶名、可选自定义域名。
// 输出：Put（上传）、PresignPutURL（直传票据）、DownloadURL（CDN 或签名下载地址）、Enabled（是否可用）。
// 边界：本机磁盘始终是权威副本——R2 只是加速通道；
//
//	任何 R2 错误都由调用方降级为本地流式下载，不让下载能力依赖 R2 可用性。
//	不删对象、不做桶管理、不解析包内容。
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// R2Config 是构造 R2 客户端所需的全部配置。字段全空表示不启用。
type R2Config struct {
	AccountID   string
	AccessKeyID string
	SecretKey   string
	Bucket      string
	// PublicBaseURL 是可选的 R2 自定义域名（如 https://files.example.com）。
	// 为空时使用 <account>.r2.cloudflarestorage.com 端点签名——
	// 该端点在国内访问较慢，生产建议配自定义域名以获得最优路径。
	PublicBaseURL string
}

// Enabled 表示四项必填参数是否齐全。
func (c R2Config) Enabled() bool {
	return c.AccountID != "" && c.AccessKeyID != "" && c.SecretKey != "" && c.Bucket != ""
}

// R2 是并发安全的只读/写入客户端，进程内复用一份。
type R2 struct {
	cfg     R2Config
	client  *s3.Client
	presign *s3.PresignClient
}

// NewR2 构造客户端。未启用时返回 (nil, nil)，调用方据此关闭全部 R2 行为。
func NewR2(ctx context.Context, cfg R2Config) (*R2, error) {
	if !cfg.Enabled() {
		return nil, nil
	}
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("初始化 R2 配置失败: %w", err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		// R2 要求路径式寻址，不能用虚拟主机式 <bucket>.<endpoint>。
		o.UsePathStyle = true
	})
	return &R2{cfg: cfg, client: client, presign: s3.NewPresignClient(client)}, nil
}

// Key 返回对象名：沿用 uploads 下的相对路径，便于从 key 反推磁盘位置。
func (r *R2) Key(relativePath string) string {
	return strings.TrimPrefix(relativePath, "/")
}

// objectCacheControl 让 Cloudflare CDN 长期缓存安装包。
//
// 没有这个头时，R2 公开桶的响应是 cf-cache-status: DYNAMIC，
// 每次下载都回源到海外 R2，实测 100MB 要 180 秒（0.55 MB/s）。
// 安装包按 UUID 命名、内容永不改变，因此可以 immutable 长期缓存：
// 第一次回源后，后续所有用户命中边缘节点，速度提升 10-30 倍。
const objectCacheControl = "public, max-age=31536000, immutable"

// ObjectCacheControl 暴露给直传服务：预签名 PUT 的签名包含此头，
// 浏览器上传时必须原样带上，否则签名校验失败（403 SignatureDoesNotMatch）。
func ObjectCacheControl() string { return objectCacheControl }

// Put 上传对象。size 用于设置 ContentLength，避免 SDK 走分块编码。
func (r *R2) Put(ctx context.Context, key string, body io.Reader, size int64, contentType, fileName string) error {
	_, err := r.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:             aws.String(r.cfg.Bucket),
		Key:                aws.String(key),
		Body:               body,
		ContentLength:      aws.Int64(size),
		ContentType:        aws.String(contentType),
		CacheControl:       aws.String(objectCacheControl),
		ContentDisposition: aws.String(ContentDisposition(fileName)),
	})
	if err != nil {
		return fmt.Errorf("上传 R2 失败 key=%s: %w", key, err)
	}
	return nil
}

// PresignPutURL 生成浏览器直传用的预签名 PUT 地址。
//
// 为什么需要它：文件如果先上传到本机再转 R2，就必须穿过 Cloudflare Tunnel，
// 而免费版 Tunnel 的请求体上限是 100 MB —— 300 MB 的包直接 413。
// 让浏览器拿着签名直接 PUT 到 R2，字节完全不经过本机和 Tunnel，
// 只受 R2 自身限制（单次 5 GiB），因此不需要分片。
//
// size 与 contentType 一并纳入签名：R2 会校验实际请求体的长度与类型，
// 避免用户拿到 URL 后上传任意大小的文件（例如把 300MB 的 URL 用来传 10GB）。
//
// ContentDisposition 也写入签名：前端 PUT 时必须原样带上该头（见 UploadTicket.Header），
// 否则签名不匹配 403。必须在上传时就写好而非事后补写——对象一旦被 CDN 请求过，
// 其响应头会被缓存一年（objectCacheControl 是 immutable），旧头再也无法纠正，
// 浏览器就会拿 URL 末段的 UUID 当下载文件名。
func (r *R2) PresignPutURL(ctx context.Context, key string, size int64, contentType, fileName string, ttl time.Duration) (string, error) {
	out, err := r.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:             aws.String(r.cfg.Bucket),
		Key:                aws.String(key),
		ContentType:        aws.String(contentType),
		ContentLength:      aws.Int64(size),
		CacheControl:       aws.String(objectCacheControl),
		ContentDisposition: aws.String(ContentDisposition(fileName)),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("生成 R2 上传签名失败 key=%s: %w", key, err)
	}
	return out.URL, nil
}

// DownloadTo 把对象读回本地文件，返回写入字节数。
//
// 用途：浏览器直传后，本地需要补一份权威副本（供 parser 解析 manifest）。
//
// 性能：配了自定义域名时优先走 CDN 直链——S3 端点（r2.cloudflarestorage.com）
// 在国内实测仅 0.5 MB/s，119MB 回拉要 4 分钟；CDN 域名实测 2.4 MB/s，
// 同样文件约 50 秒。CDN 不可用时自动回落 S3 API，不影响正确性。
//
// 调用方负责创建目标文件与清理失败残留。
func (r *R2) DownloadTo(ctx context.Context, key string, dst io.Writer) (int64, error) {
	if r.cfg.PublicBaseURL != "" {
		if u, err := r.PublicURL(key); err == nil {
			if n, err := downloadHTTP(ctx, u, dst); err == nil {
				return n, nil
			}
			// CDN 拉取失败回落 S3，不阻断上传流程。
		}
	}
	out, err := r.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return 0, fmt.Errorf("从 R2 读取失败 key=%s: %w", key, err)
	}
	defer out.Body.Close()
	written, err := io.Copy(dst, out.Body)
	if err != nil {
		return written, fmt.Errorf("写入本地失败 key=%s: %w", key, err)
	}
	return written, nil
}

// downloadHTTP 从公开 CDN 直链下载对象。带超时与状态码校验，
// 失败时由 DownloadTo 回落 S3 API。
func downloadHTTP(ctx context.Context, u string, dst io.Writer) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("CDN 返回 HTTP %d", resp.StatusCode)
	}
	return io.Copy(dst, resp.Body)
}

// SetContentDisposition 补写对象的响应头元数据，让 CDN 直链带回原始文件名。
//
// 为什么需要单独一步：直传由浏览器用预签名 URL 完成，签名里若包含
// Content-Disposition，前端 PUT 就必须原样带上该头，否则签名不匹配 403。
// 为不改动前端，改为上传完成后由服务端用 CopyObject 改写自身元数据——
// R2 内部复制不传输对象字节，代价很小。
//
// MetadataDirective 用 REPLACE 会整体替换元数据，故 ContentType 必须一并重写，
// 否则下载时 MIME 变成默认值。
//
// 必须在 CDN 缓存该对象之前完成，否则旧响应头会被缓存一年（见 objectCacheControl）。
func (r *R2) SetContentDisposition(ctx context.Context, key, fileName, contentType string) error {
	_, err := r.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:             aws.String(r.cfg.Bucket),
		Key:                aws.String(key),
		CopySource:         aws.String(url.PathEscape(r.cfg.Bucket + "/" + key)),
		MetadataDirective:  types.MetadataDirectiveReplace,
		ContentType:        aws.String(contentType),
		CacheControl:       aws.String(objectCacheControl),
		ContentDisposition: aws.String(ContentDisposition(fileName)),
	})
	if err != nil {
		return fmt.Errorf("更新对象下载名失败 key=%s: %w", key, err)
	}
	return nil
}

// StatObject 返回对象大小；对象不存在时返回 (0, false, nil) —— 让调用方用
// 「不存在」和「读失败」区分两种情况，不必解析 SDK 的错误类型。
func (r *R2) StatObject(ctx context.Context, key string) (int64, bool, error) {
	out, err := r.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(r.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("读取 R2 对象信息失败 key=%s: %w", key, err)
	}
	size := int64(0)
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	return size, true, nil
}

// DeleteObject 删除对象。淘汰时调用；删除不存在的对象视为成功（幂等）。
func (r *R2) DeleteObject(ctx context.Context, key string) error {
	_, err := r.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(r.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("删除 R2 对象失败 key=%s: %w", key, err)
	}
	return nil
}

// isNotFound 识别 S3 协议的 404：R2 对不存在的对象返回 NoSuchKey / NotFound。
func isNotFound(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		return code == "NoSuchKey" || code == "NotFound" || code == "404"
	}
	return false
}

// PublicURL 返回公开桶的 CDN 直链，不带任何签名参数。
//
// 为什么要走这条：签名 URL 只能用于 S3 API 端点（<account>.r2.cloudflarestorage.com），
// 该端点在国内走海外路由，实测 100MB 要 180 秒（0.55 MB/s）。
// 绑定自定义域名 + 公开读后，同一个文件走 Cloudflare CDN，实测预期 5-20 MB/s，
// 且不产生任何请求签名开销。
//
// 安全性：对象名用不可猜测的 UUID（见 directKeyPrefix 与包 id），
// 下载权限仍由业务层的 shareToken / 登录态控制——CDN 直链只在鉴权通过后由后端返回，
// 因此"能拿到直链"等价于"已通过业务鉴权"。公开桶只意味着直链本身长期有效，
// 不意味着别人能列出桶内对象（R2 公开桶不开放 ListObjects）。
func (r *R2) PublicURL(key string) (string, error) {
	if r.cfg.PublicBaseURL == "" {
		return "", ErrR2Disabled
	}
	base, err := url.Parse(r.cfg.PublicBaseURL)
	if err != nil {
		return "", fmt.Errorf("R2 自定义域名不合法: %w", err)
	}
	prefix := strings.TrimSuffix(base.Path, "/")
	// 路径分隔符 / 必须保留（它是 R2 的目录结构），只编码各段里的特殊字符。
	// 用 PathEscape(key) 会把 / 变成 %2F，导致对象名不匹配而 404。
	segments := strings.Split(key, "/")
	for i, seg := range segments {
		segments[i] = url.PathEscape(seg)
	}
	return base.Scheme + "://" + base.Host + prefix + "/" + strings.Join(segments, "/"), nil
}

// DownloadURL 是下载地址的统一入口。
//
// 配置 R2_PUBLIC_BASE_URL（绑定了公开自定义域名）→ 返回 CDN 直链，最快；
// 未配置 → 回落到私有桶的短期签名 URL（走 S3 端点，较慢但桶保持私有）。
//
// 注意：签名 URL 的 host 参与签名计算，绝不能把它的 host 换成自定义域名——
// 那样会得到 403 SignatureDoesNotMatch。官方也明确说明 presigned URL 不支持自定义域名。
func (r *R2) DownloadURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if r.cfg.PublicBaseURL != "" {
		return r.PublicURL(key)
	}
	out, err := r.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.cfg.Bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("生成 R2 签名地址失败 key=%s: %w", key, err)
	}
	return out.URL, nil
}

// IsPublicCDN 报告是否启用了自定义域名直链，供日志与统计使用。
func (r *R2) IsPublicCDN() bool { return r.cfg.PublicBaseURL != "" }

// ErrR2Disabled 便于调用方区分"未配置"与"配置了但出错"。
var ErrR2Disabled = errors.New("R2 未启用")
