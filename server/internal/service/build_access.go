// 职责：登录下载预览按包的真实项目校验范围和 build.read，再读取详情、历史或文件。
// 输入：可信身份及包 ID；输出：可访问版本或文件引用。
// 边界：不信任 query 中的项目，不跨分区查找；免登录分享走 build_share.go。
package service

import (
	"context"
	"errors"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
)

type buildPermissionChecker interface {
	Check(context.Context, contract.Identity, string, string) error
}

func (s *BuildService) authorizedBuild(ctx context.Context, id string, identity contract.Identity) (contract.Build, error) {
	if identity.UserID == "" || !identity.Mode.IsValid() {
		return contract.Build{}, contract.ErrUnauthorized
	}
	if !contract.IsValidUUID(id) {
		return contract.Build{}, contract.NotFound(contract.ResourceBuild)
	}
	repo := s.repos.For(identity.Mode)
	key, err := repo.ProjectKeyForBuild(ctx, id, identity.UserID, authctx.ScopeFrom(ctx))
	if err != nil {
		return contract.Build{}, err
	}
	project, err := s.resolveProject(ctx, key, identity.UserID, identity.Mode)
	if err != nil {
		if errors.Is(err, contract.ErrProjectInvalid) {
			return contract.Build{}, contract.NotFound(contract.ResourceBuild)
		}
		return contract.Build{}, err
	}
	if err := s.authz.Check(ctx, identity, project.Key, "build.read"); err != nil {
		return contract.Build{}, err
	}
	return repo.FindInProject(ctx, id, project.ID, identity.UserID)
}

func (s *BuildService) Get(ctx context.Context, id string, identity contract.Identity) (contract.Build, error) {
	b, err := s.authorizedBuild(ctx, id, identity)
	if err != nil {
		return contract.Build{}, err
	}
	items := []contract.Build{b}
	if err := s.fillShareURLs(ctx, items, identity.Mode); err != nil {
		return contract.Build{}, err
	}
	return items[0], nil
}

func (s *BuildService) Versions(ctx context.Context, id string, includePlatforms bool, identity contract.Identity) ([]contract.PublicBuildVersion, error) {
	if _, err := s.authorizedBuild(ctx, id, identity); err != nil {
		return nil, err
	}
	builds, key, err := s.repos.For(identity.Mode).ListSiblingVersions(ctx, id, includePlatforms)
	if err != nil {
		return nil, err
	}
	items := make([]contract.PublicBuildVersion, 0, len(builds))
	for _, b := range builds {
		pagePath, _, _ := s.buildURLs(b.ID, "")
		items = append(items, contract.PublicBuildVersion{
			ID: b.ID, Version: b.Version, BuildNumber: b.BuildNumber, FileSize: b.FileSize,
			Changelog: b.Changelog, Environment: b.Environment, Platform: b.Platform,
			CreatedAt: b.CreatedAt, ProjectKey: key,
			DownloadPagePath: pagePath,
		})
	}
	return items, nil
}

func (s *BuildService) ResolveFile(ctx context.Context, id string, identity contract.Identity) (contract.FileRef, error) {
	b, err := s.authorizedBuild(ctx, id, identity)
	if err != nil {
		return contract.FileRef{}, err
	}
	return s.resolveBuildFile(b)
}
