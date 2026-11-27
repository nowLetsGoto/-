// build_direct_upload.go · 浏览器直传 R2 的编排。
//
// 职责：签发预签名 PUT 票据、把已上传到 R2 的对象回拉本地并解析元信息、放弃时清理。
// 输入：文件名/大小/类型 → 票据；uploadToken → 回拉后的 stageToken。
// 输出：预签名 URL 与短期 uploadToken；回拉成功后产生与 stage 完全一致的暂存文件。
//
// 为什么需要这条链路：Cloudflare Tunnel 免费版请求体上限 100 MB，
// 300 MB 的包传不到本机（直接 413）。浏览器直传 R2 让字节完全不经过本机和 Tunnel，
// 只受 R2 自身限制（单次 PUT 5 GiB），因此不需要分片。
//
// 边界：回拉完成后落盘的 tmp 文件与 stage 产出的形态完全一致，
//
//	所以后续 commit 无需区分来源；解析失败的降级策略也与 stage 保持一致。
package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"strings"
	"time"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
	"dandelion/server/internal/storage"
)

const (
	// uploadTicketTTL 是预签名 URL 与 uploadToken 的有效期。
	// 家宽上行慢，300 MB 可能要传十几分钟，给足 1 小时。
	uploadTicketTTL = time.Hour
	// directKeyPrefix 是直传对象在"租户/项目"之下的固定段，
	// 与已发布对象分开，便于区分来源与批量清理。
	directKeyPrefix = "direct"
)

// errDirectDisabled 用于在未配置 R2 时给出可读的拒绝原因。
var errDirectDisabled = errors.New("未启用对象存储，无法使用直传上传")

// directKey 构造直传对象名：{租户}/{项目}/{direct}/{token}.{ext}
//
// 为什么把租户与项目编进 key：浏览器在 commit 之前就完成 PUT，
// 此刻数据库还没有这条记录。若 key 只含 token，对象就成了无主孤儿——
// 数据库一旦丢失记录，再也无法判断它属于谁。带上两级归属后，
// 每晚的反向同步可以仅凭 key 就还原归属，多租户也天然隔离。
func directKey(tenantID, projectKey, token, ext string) string {
	return path.Join(tenantID, projectKey, directKeyPrefix, token+"."+ext)
}

// IssueUploadTicket 校验文件并签发预签名 PUT 票据。
//
// 入参带 project：key 需要它来确定归属，同时也借此提前拒绝无效项目，
// 免得用户传完 300MB 才在 commit 阶段发现项目不存在。
func (s *BuildService) IssueUploadTicket(ctx context.Context, in contract.UploadTicketInput, identity contract.Identity) (contract.UploadTicket, error) {
	// 会话仓储是直传的前提之一；它与 R2 无关（只写数据库）。
	// 客户端要等确定公司之后才能取——按公司隔离后，先取客户端就不知道该用哪个桶。
	if s.sessions == nil {
		return contract.UploadTicket{}, &contract.ValidationError{Message: errDirectDisabled.Error()}
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(in.FileName), "."))
	platform, ok := contract.PlatformOfExt(ext)
	if !ok {
		return contract.UploadTicket{}, &contract.ValidationError{Message: "请选择受支持的安装包（.apk / .ipa / .hap）"}
	}
	limit := s.cfg.MaxUploadMB * 1024 * 1024
	if in.Size <= 0 {
		return contract.UploadTicket{}, &contract.ValidationError{Message: "文件大小不合法"}
	}
	if in.Size > limit {
		return contract.UploadTicket{}, &contract.ValidationError{
			Message: fmt.Sprintf("文件超过 %d MB 上限", s.cfg.MaxUploadMB),
		}
	}
	// 提前校验项目归属与权限：key 要用它，也避免传完才失败。
	project, err := s.resolveProject(ctx, in.Project, identity.UserID, identity.Mode)
	if err != nil {
		return contract.UploadTicket{}, err
	}
	tenantID := authctx.ScopeFrom(ctx).TenantID
	if tenantID == "" {
		return contract.UploadTicket{}, &contract.ValidationError{Message: "无法确定所属公司，请重新登录"}
	}
	// 取该公司自己的客户端：不同的公司可能配不同的桶。
	// 未配置的公司在严格隔离下视为未启用 R2，直传随之不可用（降级为普通上传）。
	r2 := s.r2.Get(tenantID)
	if r2 == nil {
		return contract.UploadTicket{}, &contract.ValidationError{Message: errDirectDisabled.Error()}
	}

	contentType, err := storage.ContentType(platform)
	if err != nil {
		return contract.UploadTicket{}, err
	}
	// 原始文件名清洗后用于两处：对象元数据里的下载名，以及暂存记录。
	originalName := storage.CleanFileName(in.FileName)
	token, err := contract.NewStageToken()
	if err != nil {
		return contract.UploadTicket{}, fmt.Errorf("生成上传标识失败: %w", err)
	}
	key := directKey(tenantID, project.Key, token, ext)

	putURL, err := r2.PresignPutURL(ctx, key, in.Size, contentType, originalName, uploadTicketTTL)
	if err != nil {
		return contract.UploadTicket{}, err
	}
	session := repository.UploadSession{
		Token:        token,
		R2Key:        key,
		DeclaredSize: in.Size,
		ContentType:  contentType,
		FileExt:      ext,
		OriginalName: originalName,
		State:        repository.UploadStatePending,
		ExpiresAt:    time.Now().Add(uploadTicketTTL),
		// 记下归属公司：commit / abort 时要凭它选中同一个桶。
		TenantID: tenantID,
	}
	if err := s.sessions.Create(ctx, session); err != nil {
		return contract.UploadTicket{}, err
	}
	s.pruneExpiredSessions(ctx)

	return contract.UploadTicket{
		UploadToken:      token,
		PutURL:           putURL,
		Header:           uploadTicketHeaders(contentType, originalName),
		ExpiresInSeconds: int(uploadTicketTTL.Seconds()),
	}, nil
}

// uploadTicketHeaders 返回浏览器必须显式设置的签名头。
//
// 三个头都参与签名，缺任一个 R2 都会返回 403。Content-Length 同样参与签名，
// 但由浏览器按文件大小自动发送；若列在这里，前端 setRequestHeader 会被
// 浏览器拒绝并报 "Refused to set unsafe header"。
func uploadTicketHeaders(contentType, originalName string) map[string]string {
	return map[string]string{
		"Content-Type":  contentType,
		"Cache-Control": storage.ObjectCacheControl(),
		// 下载名元数据：上传即写入，CDN 首次缓存就带正确文件名。
		"Content-Disposition": storage.ContentDisposition(originalName),
	}
}

// StageFromR2 解析已直传到 R2 的对象，产出与 stage 相同的暂存上下文。
//
// 关键优化：解析只用 HTTP Range 读取 manifest 所需的字节（实测约 2.3MB），
// 不再把整包（可达 300MB）从 R2 拉回本地——那曾让 stage 阶段耗时数分钟并触发前端超时。
// 整包落盘推迟到 Create：届时直接从 R2 下载到正式目录，避免重复搬运。
//
// 校验点：会话存在且未过期、对象确实已上传、大小与声明一致。
// 大小不一致说明浏览器传了别的文件（或传输被截断），一律拒绝。
func (s *BuildService) StageFromR2(ctx context.Context, uploadToken string) (contract.StageResult, error) {
	if s.sessions == nil {
		return contract.StageResult{}, &contract.ValidationError{Message: errDirectDisabled.Error()}
	}
	session, err := s.sessions.Find(ctx, uploadToken)
	if err != nil {
		return contract.StageResult{}, err
	}
	if session == nil {
		return contract.StageResult{}, &contract.ValidationError{Message: "上传会话不存在，请重新选择文件"}
	}
	// 凭会话记录的归属公司取客户端：不同公司可能配不同的桶。
	r2 := s.r2.Get(session.TenantID)
	if r2 == nil {
		return contract.StageResult{}, &contract.ValidationError{Message: errDirectDisabled.Error()}
	}
	if time.Now().After(session.ExpiresAt) {
		return contract.StageResult{}, &contract.ValidationError{Message: "上传已过期，请重新选择文件"}
	}

	// 确认对象存在且大小一致：浏览器可能中途失败，或传了与声明不符的文件。
	size, exists, err := r2.StatObject(ctx, session.R2Key)
	if err != nil {
		return contract.StageResult{}, err
	}
	if !exists {
		return contract.StageResult{}, &contract.ValidationError{Message: "文件尚未上传完成，请重试"}
	}
	if size != session.DeclaredSize {
		return contract.StageResult{}, &contract.ValidationError{
			Message: fmt.Sprintf("文件大小不一致（声明 %d 字节，实际 %d 字节），请重新上传", session.DeclaredSize, size),
		}
	}
	if err := s.sessions.MarkUploaded(ctx, uploadToken); err != nil {
		return contract.StageResult{}, err
	}

	// 写伴生文件，让后续 Create 能识别这是直传包、拿到对象名与原始文件名。
	// 不写包体：直传包的字节只在 R2，Create 时直接落到正式目录。
	if err := s.writeDirectStaging(uploadToken, *session); err != nil {
		return contract.StageResult{}, err
	}
	if err := s.sessions.Delete(ctx, uploadToken); err != nil {
		log.Printf("删除上传会话失败 token=%s err=%v", uploadToken, err)
	}

	// 解析：优先用 Range 只读 manifest 所需字节（秒级）；
	// 未配公开域名时无法 Range，回落到整包回拉再解析（慢但功能完整）。
	if publicURL, err := r2.PublicURL(session.R2Key); err == nil {
		reader := storage.NewHTTPRangeReader(publicURL, storage.HTTPClientForRange())
		return s.ParseFrom(uploadToken, reader, size, session.FileExt), nil
	}
	staged, err := s.pullFromR2ToStage(ctx, *session)
	if err != nil {
		return contract.StageResult{}, err
	}
	return s.Stage(ctx, staged), nil
}

// writeDirectStaging 为直传包写暂存标记，不落整包字节。
//
// 三个文件各有单一职责：
//   - token.ext：0 字节占位，让 validateStageToken 能识别暂存并推断平台；
//     Create 见其为 0 字节便知字节只在 R2，需下载到正式目录。
//   - token.name：原始文件名，供下载名与展示。
//   - token.r2：R2 对象名，供 Create 回写 r2_key 与下载。
func (s *BuildService) writeDirectStaging(token string, session repository.UploadSession) error {
	if err := os.MkdirAll(s.paths.TmpDir(), 0o755); err != nil {
		return fmt.Errorf("创建暂存目录失败: %w", err)
	}
	files := []struct {
		path string
		data []byte
	}{
		{s.paths.StagePath(token, session.FileExt), nil}, // 0 字节占位
		{s.paths.StageNamePath(token), []byte(session.OriginalName)},
		{s.paths.StageR2KeyPath(token), []byte(session.R2Key)},
	}
	for _, f := range files {
		if err := os.WriteFile(f.path, f.data, 0o600); err != nil {
			return fmt.Errorf("写入暂存标记 %s 失败: %w", f.path, err)
		}
	}
	return nil
}

// AbortUpload 放弃直传：删除会话与 R2 上的临时对象。
// 前端在用户改选文件、关闭页面或 PUT 失败时调用；失败也不影响后续（过期会兜底清理）。
func (s *BuildService) AbortUpload(ctx context.Context, uploadToken string) error {
	if s.sessions == nil {
		return nil
	}
	session, err := s.sessions.Find(ctx, uploadToken)
	if err != nil || session == nil {
		return err
	}
	if r2 := s.r2.Get(session.TenantID); r2 != nil && session.State != repository.UploadStateCompleted {
		if err := r2.DeleteObject(ctx, session.R2Key); err != nil {
			log.Printf("放弃上传时删除 R2 对象失败 key=%s err=%v", session.R2Key, err)
		}
	}
	return s.sessions.Delete(ctx, uploadToken)
}

// pullFromR2ToStage 把 R2 对象读进 tmp，并写 .name 伴生文件。
func (s *BuildService) pullFromR2ToStage(ctx context.Context, session repository.UploadSession) (StagedFile, error) {
	token, err := contract.NewStageToken()
	if err != nil {
		return StagedFile{}, fmt.Errorf("生成暂存标识失败: %w", err)
	}
	tmpPath := s.paths.StagePath(token, session.FileExt)
	namePath := s.paths.StageNamePath(token)
	if err := os.MkdirAll(s.paths.TmpDir(), 0o755); err != nil {
		return StagedFile{}, fmt.Errorf("创建暂存目录失败: %w", err)
	}
	dst, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return StagedFile{}, fmt.Errorf("创建暂存文件失败: %w", err)
	}
	// 入口取一次快照后全程复用：这是一次可能持续数分钟的下载，
	// 中途重取会让前后半段使用不同的凭据。
	r2 := s.r2.Get(session.TenantID)
	if r2 == nil {
		_ = dst.Close()
		_ = os.Remove(tmpPath)
		return StagedFile{}, errDirectDisabled
	}
	written, copyErr := r2.DownloadTo(ctx, session.R2Key, dst)
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		// 失败必须清掉半截文件，否则会被当成有效暂存。
		_ = os.Remove(tmpPath)
		if copyErr != nil {
			return StagedFile{}, fmt.Errorf("回拉文件失败: %w", copyErr)
		}
		return StagedFile{}, fmt.Errorf("写入暂存文件失败: %w", closeErr)
	}
	if written != session.DeclaredSize {
		_ = os.Remove(tmpPath)
		return StagedFile{}, &contract.ValidationError{Message: "回拉文件大小不一致，请重新上传"}
	}
	original := session.OriginalName
	if original == "" {
		original = "package." + session.FileExt
	}
	if err := os.WriteFile(namePath, []byte(original), 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return StagedFile{}, fmt.Errorf("写入暂存文件名失败: %w", err)
	}
	// 伴生 .r2 文件记录直传对象名：commit 读到它就能跳过二次上传。
	// 与 .name 同样按 token 命名，token 失效后随 pruneOrphans 一起清掉。
	if err := os.WriteFile(s.paths.StageR2KeyPath(token), []byte(session.R2Key), 0o600); err != nil {
		_ = os.Remove(tmpPath)
		_ = os.Remove(namePath)
		return StagedFile{}, fmt.Errorf("写入暂存对象名失败: %w", err)
	}
	return StagedFile{
		Token:            token,
		AbsPath:          tmpPath,
		Ext:              session.FileExt,
		OriginalFileName: original,
		DirectR2Key:      session.R2Key,
	}, nil
}

// pruneExpiredSessions 清理过期会话并顺带删除 R2 上的孤立对象。
// 每次签发新票据时顺手做一次，不需要独立定时任务。
func (s *BuildService) pruneExpiredSessions(ctx context.Context) {
	if s.sessions == nil {
		return
	}
	keys, err := s.sessions.PurgeExpired(ctx, time.Now())
	if err != nil {
		log.Printf("清理过期上传会话失败: %v", err)
		return
	}
	// 逐条按各自公司取客户端：一次清理可能跨越多个公司的桶，
	// 用同一个客户端会把 A 公司的对象名拿到 B 公司的桶里删（自然删不到，白跑一趟）。
	for _, obj := range keys {
		r2 := s.r2.Get(obj.TenantID)
		if r2 == nil {
			continue // 该公司未配置 R2，对象本就不存在，无需清理。
		}
		if err := r2.DeleteObject(ctx, obj.Key); err != nil {
			log.Printf("清理孤立 R2 对象失败 key=%s err=%v", obj.Key, err)
		}
	}
}
