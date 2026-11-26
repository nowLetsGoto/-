// 职责：签发和校验绑定单个版本的分享凭证，提供公开详情及同应用可切换的有效分享版本。
// 输入：现有服务密钥、固定分区的授权记录、包 ID；输出：签名凭证、公开详情或文件引用。
// 边界：不读 HTTP、不写 SQL；与登录令牌及 API Key 隔离用途，每次访问都读取授权状态。
package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
)

type shareLookup interface {
	FindByID(context.Context, string) (repository.BuildShare, error)
	ListByBuildIDs(context.Context, []string) (map[string]repository.BuildShare, error)
}

// shareSigner 使用用途隔离的派生密钥，不改变现有登录令牌格式。
type shareSigner struct{ key []byte }

func newShareSigner(secret string) shareSigner {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("dandelion/download-share/v1"))
	return shareSigner{key: mac.Sum(nil)}
}

func shareActive(share repository.BuildShare, now time.Time) bool {
	return share.Revision > 0 && share.RevokedAt == nil && (share.ExpiresAt == nil || now.Before(*share.ExpiresAt))
}

func (s shareSigner) signature(mode contract.SessionMode, share repository.BuildShare) string {
	mac := hmac.New(sha256.New, s.key)
	_, _ = fmt.Fprintf(mac, "s1|%s|%s|%s|%d", mode, share.ID, share.BuildID, share.Revision)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s shareSigner) issue(mode contract.SessionMode, share repository.BuildShare) string {
	return "s1." + string(mode) + "." + share.ID + "." + s.signature(mode, share)
}

// parseShareToken 只接受已知版本、明确模式及规范 UUID；拒绝登录令牌与公司 Key。
func parseShareToken(token string) (contract.SessionMode, string, string, bool) {
	if len(token) > 160 {
		return "", "", "", false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != "s1" {
		return "", "", "", false
	}
	mode := contract.SessionMode(parts[1])
	if !mode.IsValid() || !contract.IsValidUUID(parts[2]) || len(parts[3]) != 43 {
		return "", "", "", false
	}
	return mode, parts[2], parts[3], true
}

func (s *BuildService) validateShare(ctx context.Context, id, token string) (repository.BuildShare, contract.SessionMode, error) {
	missing := contract.NotFound(contract.ResourceShare)
	mode, shareID, signature, ok := parseShareToken(token)
	if !ok || !contract.IsValidUUID(id) {
		return repository.BuildShare{}, "", missing
	}
	share, err := s.shares.For(mode).FindByID(ctx, shareID)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return repository.BuildShare{}, "", missing
		}
		return repository.BuildShare{}, "", err
	}
	if share.BuildID != id || !shareActive(share, time.Now()) ||
		!hmac.Equal([]byte(signature), []byte(s.signer.signature(mode, share))) {
		return repository.BuildShare{}, "", missing
	}
	return share, mode, nil
}

func (s *BuildService) sharedBuild(ctx context.Context, id, token string) (contract.Build, contract.SessionMode, error) {
	share, mode, err := s.validateShare(ctx, id, token)
	if err != nil {
		return contract.Build{}, "", err
	}
	// 分区由已验签的凭证固定，包由数据库授权绑定，不跨分区猜测。
	build, err := s.repos.For(mode).FindByID(ctx, share.BuildID)
	if errors.Is(err, contract.ErrNotFound) {
		return contract.Build{}, "", contract.NotFound(contract.ResourceShare)
	}
	if err != nil {
		return contract.Build{}, "", err
	}
	s.fillURLs(&build, token)
	return build, mode, nil
}

// SharedVersions 先验证来源包，再从同分区、同项目、同应用中筛选已有有效分享。
// 不消费访问者身份，不允许任意目标 ID；每个选择项携带目标包自身的凭证。
func (s *BuildService) SharedVersions(ctx context.Context, id, token string) ([]contract.PublicBuildVersion, error) {
	_, mode, err := s.sharedBuild(ctx, id, token)
	if err != nil {
		return nil, err
	}
	builds, _, err := s.repos.For(mode).ListSiblingVersions(ctx, id, true)
	if err != nil {
		return nil, err
	}
	return s.sharedVersionChoices(ctx, builds, mode)
}

func (s *BuildService) sharedVersionChoices(ctx context.Context, builds []contract.Build, mode contract.SessionMode) ([]contract.PublicBuildVersion, error) {
	if err := s.fillShareURLs(ctx, builds, mode); err != nil {
		return nil, err
	}
	items := make([]contract.PublicBuildVersion, 0, len(builds))
	for _, b := range builds {
		if b.DownloadPageURL != "" {
			items = append(items, buildVersion(b, ""))
		}
	}
	return items, nil
}

func (s *BuildService) GetShared(ctx context.Context, id, token string) (contract.SharedBuild, error) {
	b, _, err := s.sharedBuild(ctx, id, token)
	if err != nil {
		return contract.SharedBuild{}, err
	}
	return contract.SharedBuild{
		ID: b.ID, Environment: b.Environment, Platform: b.Platform, AppName: b.AppName,
		ProjectIconURL: b.ProjectIconURL, PackageID: b.PackageID, Version: b.Version,
		BuildNumber: b.BuildNumber, Changelog: b.Changelog, FileSize: b.FileSize,
		FileExt: b.FileExt, DownloadCount: b.DownloadCount, CreatedAt: b.CreatedAt,
		DownloadPagePath: b.DownloadPagePath, DownloadPageURL: b.DownloadPageURL, DownloadFileURL: b.DownloadFileURL,
	}, nil
}

func (s *BuildService) ResolveSharedFile(ctx context.Context, id, token string) (contract.FileRef, contract.SessionMode, error) {
	build, mode, err := s.sharedBuild(ctx, id, token)
	if err != nil {
		return contract.FileRef{}, "", err
	}
	ref, err := s.resolveBuildFile(build)
	if err == nil {
		// 历史包首次下载时补传 R2：本次仍走本机，下次起走直链。
		s.syncToR2Async(build, mode)
	}
	return ref, mode, err
}

// fillShareURLs 批量签发已有有效授权，避免列表逐包查询；撤销后不会自动创建新授权。
func (s *BuildService) fillShareURLs(ctx context.Context, builds []contract.Build, mode contract.SessionMode) error {
	ids := make([]string, len(builds))
	for i := range builds {
		ids[i] = builds[i].ID
	}
	shares, err := s.shares.For(mode).ListByBuildIDs(ctx, ids)
	if err != nil {
		return err
	}
	for i := range builds {
		share, ok := shares[builds[i].ID]
		token := ""
		if ok && shareActive(share, time.Now()) {
			token = s.signer.issue(mode, share)
		}
		s.fillURLs(&builds[i], token)
	}
	return nil
}
