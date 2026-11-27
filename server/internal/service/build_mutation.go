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

// Delete 删除一个包，并清理它的两份字节：本机磁盘与 R2 对象。
//
// 为什么要连同 R2 一起删：下载已 302 到 CDN，R2 才是实际被访问的副本。
// 只删本地会留下永久占用的孤儿对象，而界面上再无任何入口能发现它。
//
// 清理顺序：先删数据库行（拿到 relative_path 与 r2_key），再删两份字节。
// 字节删除失败只记日志——数据库记录已消失，重试无从定位，因此不阻断响应；
// 落下的孤儿对象由 Worker 的孤儿扫描兜底。
func (s *BuildService) Delete(ctx context.Context, id string, identity contract.Identity) (contract.Build, error) {
	build, project, err := s.buildInScope(ctx, id, identity, "build.delete")
	if err != nil {
		return contract.Build{}, err
	}
	relativePath, err := s.repos.For(identity.Mode).Delete(ctx, id, project.ID, identity.UserID)
	if err != nil {
		return contract.Build{}, err
	}
	// 关联分享授权已随 DELETE 级联移除，这里只清理字节。
	s.removeBuildBytes(ctx, id, relativePath, build.R2Key)
	return build, nil
}

// removeBuildBytes 清理一个包在本机与 R2 上的字节，两份都可缺失。
//
// 独立成方法是为了让 Delete 与后续的淘汰执行器共用同一套清理语义，
// 避免两处各写一遍"删文件 + 删对象"而逐渐漂移。
func (s *BuildService) removeBuildBytes(ctx context.Context, id, relativePath, r2Key string) {
	if abs, err := s.paths.SafeResolve(relativePath); err != nil {
		log.Printf("清理包字节：路径校验失败 id=%s err=%v", id, err)
	} else if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("清理包字节：删除本地文件失败 id=%s err=%v", id, err)
	}
	r2 := s.r2.Get()
	if r2Key == "" || r2 == nil {
		return
	}
	if err := r2.DeleteObject(ctx, r2Key); err != nil {
		log.Printf("清理包字节：删除 R2 对象失败 id=%s key=%s err=%v", id, r2Key, err)
	}
}
