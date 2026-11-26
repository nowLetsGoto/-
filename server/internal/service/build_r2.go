// build_r2.go · 安装包镜像到 Cloudflare R2 的旁路逻辑。
//
// 职责：把本机 uploads 下的包异步镜像到 R2，并在成功后回写 r2_key；
//
//	下载时若已有 r2_key 就签发直链，没有则先走本机、同时补传。
//
// 输入：BuildService 持有的 storage.R2 客户端与 Paths。
// 输出：R2 对象与数据库 r2_key 列。
// 边界：磁盘始终是权威副本——R2 任何失败都只记日志，绝不影响上传成功或下载可用性。
//
//	不阻塞请求链路：同步在独立 goroutine 中完成，失败留给下次下载重试。
//	未配置 R2（s.r2 == nil）时全部方法直接返回，行为与改造前完全一致。
package service

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"time"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/storage"
)

// r2SignedURLTTL 是下载直链有效期。包体积大、家宽慢，给足 30 分钟
// 以防用户在浏览器里暂停后又恢复；同时不至于让链接长期外泄可用。
const r2SignedURLTTL = 30 * time.Minute

// r2SyncTimeout 限制单次上传时长，避免卡住的连接长期占用 goroutine。
const r2SyncTimeout = 30 * time.Minute

// r2PrewarmTimeout 限制预热请求时长。预热只读 256KB，正常远低于此值；
// 给 30 秒余量足以应对跨境冷启动的回源握手。
const r2PrewarmTimeout = 30 * time.Second

// r2PrewarmBytes 是预热时读取的字节数。取 256KB 与 Range 读取的块大小一致：
// 足够触发 Cloudflare 完整回源并建立缓存，又不为预热传输整个包。
const r2PrewarmBytes = 256 * 1024

// R2Enabled 报告是否启用了对象存储镜像。
func (s *BuildService) R2Enabled() bool { return s.r2 != nil }

// R2SignedURL 为已镜像的文件签发下载直链；未启用或未镜像时返回空串。
// 调用方据此决定 302 跳转还是本地流式。
func (s *BuildService) R2SignedURL(ctx context.Context, ref contract.FileRef) string {
	if s.r2 == nil || ref.R2Key == "" {
		return ""
	}
	url, err := s.r2.DownloadURL(ctx, ref.R2Key, r2SignedURLTTL)
	if err != nil {
		// 生成下载地址失败不该让下载失败：记日志并回落到本机流式。
		log.Printf("R2 生成下载地址失败 key=%s err=%v", ref.R2Key, err)
		return ""
	}
	return url
}

// syncToR2 把单个包镜像到 R2 并回写 r2_key。同步执行，由调用方决定是否异步。
// 幂等：已同步过的包直接跳过。
func (s *BuildService) syncToR2(build contract.Build, mode contract.SessionMode) {
	if s.r2 == nil || build.R2Key != "" {
		return
	}
	abs, err := s.paths.SafeResolve(build.RelativePath)
	if err != nil {
		log.Printf("R2 同步跳过：路径不合法 id=%s rel=%s err=%v", build.ID, build.RelativePath, err)
		return
	}
	file, err := os.Open(abs)
	if err != nil {
		log.Printf("R2 同步跳过：打开文件失败 id=%s path=%s err=%v", build.ID, abs, err)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		log.Printf("R2 同步跳过：读取文件信息失败 id=%s err=%v", build.ID, err)
		return
	}
	key := s.r2.Key(path.Base(build.RelativePath))
	contentType, _ := s.contentTypeOf(build.Platform)

	ctx, cancel := context.WithTimeout(context.Background(), r2SyncTimeout)
	defer cancel()

	if err := s.r2.Put(ctx, key, file, info.Size(), contentType); err != nil {
		log.Printf("R2 同步失败 id=%s key=%s err=%v", build.ID, key, err)
		return
	}
	if err := s.repos.For(mode).SetR2Key(ctx, build.ID, key); err != nil {
		// 对象已上传但回写失败：下次下载会再传一次（Put 覆盖，无害）。
		log.Printf("R2 同步成功但回写 r2_key 失败 id=%s key=%s err=%v", build.ID, key, err)
		return
	}
	log.Printf("R2 同步完成 id=%s key=%s", build.ID, key)
	// 镜像完成即预热 CDN：把首个下载者的回源等待转移到此处。
	s.prewarmCDNAsync(key)
}

// syncToR2Async 在后台镜像，不阻塞当前请求。
func (s *BuildService) syncToR2Async(build contract.Build, mode contract.SessionMode) {
	if s.r2 == nil || build.R2Key != "" {
		return
	}
	go s.syncToR2(build, mode)
}

// contentTypeOf 取平台的 MIME 类型；未知平台回落到二进制流，不影响上传。
func (s *BuildService) contentTypeOf(platform string) (string, error) {
	return storage.ContentType(platform)
}

// prewarmCDNAsync 在后台预热 Cloudflare 边缘缓存，让首个下载者不必等待回源。
//
// 原理：CDN 对未被访问过的对象首请求要回源 R2 并填充边缘节点，119MB 包实测
// 首下载只有 0.5MB/s，填满缓存后可达 2.8MB/s。这里主动发一次小 Range 请求触发
// 缓存填充，把这段等待从用户身上转移到发布瞬间。
//
// 只读前 256KB：足以让 Cloudflare 走完整回源路径并建立对象缓存条目，
// 同时不为预热传输整个包。实测缓存命中后任意偏移读取都直接命中同一对象。
//
// 失败仅记日志——预热是纯加速旁路，绝不影响发布结果。
//
// 注意：不使用调用方的 ctx。Create 的请求上下文在响应返回后即被取消，
// 而预热要在那之后继续跑，故用独立的后台上下文与超时。
func (s *BuildService) prewarmCDNAsync(key string) {
	if s.r2 == nil || key == "" {
		return
	}
	url, err := s.r2.PublicURL(key)
	if err != nil {
		// 未配公开域名时没有 CDN 可预热（下载走 S3 或本机），直接跳过。
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), r2PrewarmTimeout)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", r2PrewarmBytes-1))
		resp, err := prewarmClient.Do(req)
		if err != nil {
			log.Printf("CDN 预热失败 key=%s err=%v", key, err)
			return
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		log.Printf("CDN 预热完成 key=%s status=%d cache=%s", key, resp.StatusCode, resp.Header.Get("cf-cache-status"))
	}()
}

// prewarmClient 复用连接池，避免每次预热都重建 TLS 握手。
var prewarmClient = storage.HTTPClientForRange()
