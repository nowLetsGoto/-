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

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
	"dandelion/server/internal/storage"
)

const (
	// uploadTicketTTL 是预签名 URL 与 uploadToken 的有效期。
	// 家宽上行慢，300 MB 可能要传十几分钟，给足 1 小时。
	uploadTicketTTL = time.Hour
	// directKeyPrefix 把直传对象与已发布对象分开，便于区分来源与批量清理。
	directKeyPrefix = "direct"
)

// errDirectDisabled 用于在未配置 R2 时给出可读的拒绝原因。
var errDirectDisabled = errors.New("未启用对象存储，无法使用直传上传")

// IssueUploadTicket 校验文件并签发预签名 PUT 票据。
func (s *BuildService) IssueUploadTicket(ctx context.Context, in contract.UploadTicketInput) (contract.UploadTicket, error) {
	if s.r2 == nil || s.sessions == nil {
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

	contentType, err := storage.ContentType(platform)
	if err != nil {
		return contract.UploadTicket{}, err
	}
	token, err := contract.NewStageToken()
	if err != nil {
		return contract.UploadTicket{}, fmt.Errorf("生成上传标识失败: %w", err)
	}
	key := path.Join(directKeyPrefix, token+"."+ext)

	putURL, err := s.r2.PresignPutURL(ctx, key, in.Size, contentType, uploadTicketTTL)
	if err != nil {
		return contract.UploadTicket{}, err
	}
	session := repository.UploadSession{
		Token:        token,
		R2Key:        key,
		DeclaredSize: in.Size,
		ContentType:  contentType,
		FileExt:      ext,
		OriginalName: storage.CleanFileName(in.FileName),
		State:        repository.UploadStatePending,
		ExpiresAt:    time.Now().Add(uploadTicketTTL),
	}
	if err := s.sessions.Create(ctx, session); err != nil {
		return contract.UploadTicket{}, err
	}
	s.pruneExpiredSessions(ctx)

	return contract.UploadTicket{
		UploadToken: token,
		PutURL:      putURL,
		// 签名里包含 content-type 与 content-length，前端必须原样带上。
		Header: map[string]string{
			"Content-Type":   contentType,
			"Content-Length": fmt.Sprint(in.Size),
			// 签名里含 Cache-Control，浏览器 PUT 必须原样带上，否则 403。
			"Cache-Control": storage.ObjectCacheControl(),
		},
		ExpiresInSeconds: int(uploadTicketTTL.Seconds()),
	}, nil
}

// StageFromR2 把已直传到 R2 的对象回拉本地，产出与 stage 相同的暂存文件。
//
// 校验点：会话存在且未过期、对象确实已上传、大小与声明一致。
// 大小不一致说明浏览器传了别的文件（或传输被截断），一律拒绝。
func (s *BuildService) StageFromR2(ctx context.Context, uploadToken string) (contract.StageResult, error) {
	if s.r2 == nil || s.sessions == nil {
		return contract.StageResult{}, &contract.ValidationError{Message: errDirectDisabled.Error()}
	}
	session, err := s.sessions.Find(ctx, uploadToken)
	if err != nil {
		return contract.StageResult{}, err
	}
	if session == nil {
		return contract.StageResult{}, &contract.ValidationError{Message: "上传会话不存在，请重新选择文件"}
	}
	if time.Now().After(session.ExpiresAt) {
		return contract.StageResult{}, &contract.ValidationError{Message: "上传已过期，请重新选择文件"}
	}

	// 先确认对象真的在：浏览器可能失败在中途，此时不该回拉。
	size, exists, err := s.r2.StatObject(ctx, session.R2Key)
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

	// 回拉本地：产生与 stage 同形态的 tmp 文件 + .name 伴生文件，后续流程完全复用。
	staged, err := s.pullFromR2ToStage(ctx, *session)
	if err != nil {
		return contract.StageResult{}, err
	}
	if err := s.sessions.Delete(ctx, uploadToken); err != nil {
		// 会话删不掉不影响本次上传：过期清理会兜底。
		log.Printf("删除上传会话失败 token=%s err=%v", uploadToken, err)
	}
	// R2 上的 direct/ 对象保留：commit 之后它就是该包的网络副本，
	// 由 commit 回写 r2_key 指向它，避免再传一次。
	return s.Stage(ctx, staged), nil
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
	if s.r2 != nil && session.State != repository.UploadStateCompleted {
		if err := s.r2.DeleteObject(ctx, session.R2Key); err != nil {
			log.Printf("放弃上传时删除 R2 对象失败 key=%s err=%v", session.R2Key, err)
		}
	}
	return s.sessions.Delete(ctx, uploadToken)
}

// FormForDirectUpload 交付 commit 所需信息：把 direct/ 对象名记到暂存上下文里。
// commit 完成后 r2_key 指向该对象，下载即可直接 302，无需二次同步。
func (s *BuildService) DirectKeyOf(uploadToken string) string {
	return path.Join(directKeyPrefix, uploadToken)
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
	written, copyErr := s.r2.DownloadTo(ctx, session.R2Key, dst)
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
	if s.r2 == nil {
		return
	}
	for _, key := range keys {
		if err := s.r2.DeleteObject(ctx, key); err != nil {
			log.Printf("清理孤立 R2 对象失败 key=%s err=%v", key, err)
		}
	}
}
