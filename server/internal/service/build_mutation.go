// 职责：编排应用包编辑与删除，校验展示字段，删除提交后清理文件。
// 输入：可信身份、包 ID 与最终表单值；输出：更新后的包或被删包的审计信息。
// 边界：按真实项目授权；不修改安装文件与存储路径，文件清理失败只记日志。
package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"unicode/utf8"

	"dandelion/server/internal/contract"
)

func normalizeBuildUpdate(in contract.BuildUpdateInput) (contract.BuildUpdateInput, error) {
	in.AppName = strings.TrimSpace(in.AppName)
	in.Version = strings.TrimSpace(in.Version)
	in.BuildNumber = strings.TrimSpace(in.BuildNumber)
	in.Changelog = strings.TrimSpace(in.Changelog)
	if in.AppName == "" || in.Version == "" {
		return in, &contract.ValidationError{Message: "请填写应用名称与版本号"}
	}
	if !contract.IsValidEnvironment(in.Environment) {
		return in, &contract.ValidationError{Message: "环境参数不合法"}
	}
	for _, field := range []struct {
		label string
		value string
		max   int
	}{
		{"应用名称", in.AppName, contract.MaxAppNameLen},
		{"版本号", in.Version, contract.MaxVersionLen},
		{"构建号", in.BuildNumber, contract.MaxBuildNumberLen},
		{"更新说明", in.Changelog, contract.MaxChangelogLen},
	} {
		if utf8.RuneCountInString(field.value) > field.max {
			return in, &contract.ValidationError{Message: fmt.Sprintf("%s超过 %d 个字符", field.label, field.max)}
		}
	}
	return in, nil
}

func (s *BuildService) Update(ctx context.Context, id string, in contract.BuildUpdateInput, identity contract.Identity) (contract.Build, error) {
	_, project, err := s.buildInScope(ctx, id, identity, "build.write")
	if err != nil {
		return contract.Build{}, err
	}
	in, err = normalizeBuildUpdate(in)
	if err != nil {
		return contract.Build{}, err
	}
	build, err := s.repos.For(identity.Mode).Update(ctx, id, project.ID, identity.UserID, in)
	if err != nil {
		return contract.Build{}, err
	}
	items := []contract.Build{build}
	if err := s.fillShareURLs(ctx, items, identity.Mode); err != nil {
		return contract.Build{}, err
	}
	return items[0], nil
}

func (s *BuildService) Delete(ctx context.Context, id string, identity contract.Identity) (contract.Build, error) {
	build, project, err := s.buildInScope(ctx, id, identity, "build.delete")
	if err != nil {
		return contract.Build{}, err
	}
	relativePath, err := s.repos.For(identity.Mode).Delete(ctx, id, project.ID, identity.UserID)
	if err != nil {
		return contract.Build{}, err
	}
	// DELETE 已提交，关联分享授权已级联移除；只清理实际删除行返回的路径。
	abs, resolveErr := s.paths.SafeResolve(relativePath)
	if resolveErr != nil {
		log.Printf("删除包后路径校验失败 id=%s err=%v", id, resolveErr)
	} else if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("删除包后清理文件失败 id=%s err=%v", id, err)
	}
	return build, nil
}
