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
	"log"
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
