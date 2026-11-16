// Package authz · 权限判定。
// 职责：把一个「身份 + 项目」解析成权限集合，并提供给中间件与 handler 使用。
// 输入：repository 的账号 / 人员 / 项目 / 角色读写能力。
// 输出：map[权限点]struct{}，或 contract.ErrForbidden。
// 边界：**只做判定，不做业务**；不缓存（内部工具调用量小，先保证正确性，
//
//	需要时再加一层带失效的短 TTL 缓存）。
//
// 判定顺序（用户明确要求）：
//  1. 超级管理员 → 全量权限（否则会出现"没人能改配置"的死锁）
//  2. 项目角色：请求带了项目且该项目配置了角色 → 用它的权限
//  3. 人员角色：项目没配角色时，回落到人员自身的角色
//  4. 没有角色 → 只有默认的业务读取权限；不会默认获得任何写权限。
//
// 默认能力是独立基线：概览、应用包、应用始终可读，项目列表可供筛选；
// 项目角色与人员角色仍按覆盖而非取并集计算其余权限。
package authz

import (
	"context"
	"errors"
	"sort"

	"dandelion/server/internal/contract"
	peoplerepo "dandelion/server/internal/repository/people"
	projectrepo "dandelion/server/internal/repository/project"
	rolerepo "dandelion/server/internal/repository/role"
	userrepo "dandelion/server/internal/repository/user"
)

// Service 是权限判定的唯一入口。
type Service struct {
	users    *userrepo.Repository
	people   *peoplerepo.Repository
	projects *projectrepo.Repository
	roles    *rolerepo.Repository
}

func NewService(
	users *userrepo.Repository,
	people *peoplerepo.Repository,
	projects *projectrepo.Repository,
	roles *rolerepo.Repository,
) *Service {
	return &Service{users: users, people: people, projects: projects, roles: roles}
}

// Permissions 返回该身份在指定项目下的权限集合；projectKey 为空表示"平台级请求"。
// 项目不存在时返回仓储的错误（通常是 ErrProjectInvalid → 400），与既有列表行为一致。
func (s *Service) Permissions(ctx context.Context, identity contract.Identity, projectKey string) (map[string]struct{}, error) {
	// 1) 超管恒为全量
	if identity.IsSuperAdmin {
		return allPermissions(), nil
	}

	roleKey, err := s.resolveRoleKey(ctx, identity, projectKey)
	if err != nil {
		return nil, err
	}
	permissions := defaultPermissions()
	if roleKey == "" {
		return permissions, nil
	}

	role, err := s.roles.FindByKey(ctx, roleKey)
	if err != nil {
		// 角色已删除时只保留默认读取能力，绝不回退到另一个可能更高权限的角色。
		if errors.Is(err, contract.ErrNotFound) {
			return permissions, nil
		}
		return nil, err
	}
	for _, permission := range role.Permissions {
		permissions[permission] = struct{}{}
	}
	return permissions, nil
}

// Check 是 Permissions 的便捷版：有权限返回 nil，没有返回 contract.ErrForbidden。
// 给"项目上下文在请求体里"的场景用（如提交发布，项目不在 query 上）。
func (s *Service) Check(ctx context.Context, identity contract.Identity, projectKey, permission string) error {
	permissions, err := s.Permissions(ctx, identity, projectKey)
	if err != nil {
		return err
	}
	if _, ok := permissions[permission]; !ok {
		return contract.ErrForbidden
	}
	return nil
}

// PersonID 返回账号对应的人员档案：显式绑定优先，兼容此前按登录名关联的存量数据。
// /me 与权限判定共用它，保证"页面显示的人"和"算权限用的人"永远是同一个。
func (s *Service) PersonID(ctx context.Context, identity contract.Identity) (string, error) {
	if identity.UserID == "" {
		return "", nil
	}
	record, err := s.users.FindByID(ctx, identity.UserID)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	if record.User.PersonID != "" {
		return record.User.PersonID, nil
	}
	// 兼容存量：账号尚未绑定人员时，按登录名找一个同名档案。
	person, err := s.people.FindByUsername(ctx, identity.Username)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return person.ID, nil
}

// resolveRoleKey 按「项目角色 → 人员角色」的顺序找出该用的角色。
func (s *Service) resolveRoleKey(ctx context.Context, identity contract.Identity, projectKey string) (string, error) {
	// 2) 项目角色优先
	if projectKey != "" {
		project, err := s.projects.FindByKey(ctx, projectKey)
		if err != nil {
			return "", err
		}
		if project.RoleKey != "" {
			return project.RoleKey, nil
		}
	}

	// 3) 人员角色兜底：与 /me 共用同一个人员解析入口。
	personID, err := s.PersonID(ctx, identity)
	if err != nil || personID == "" {
		return "", err
	}
	person, err := s.people.FindByID(ctx, personID)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return person.RoleKey, nil
}

// Sorted 把权限集合转成有序切片：响应稳定、便于前端 diff 与断言。
func Sorted(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// defaultPermissions 是已登录账号的最小读取基线。
// build.read 支撑概览/应用包/应用；project.read 只为页面选择项目提供数据，
// 不授予人员、团队、角色、系统设置的查看或任何写入权限。每次返回独立集合，防止调用方污染基线。
func defaultPermissions() map[string]struct{} {
	return map[string]struct{}{"build.read": {}, "project.read": {}}
}

// allPermissions 返回权限目录的全集。
func allPermissions() map[string]struct{} {
	permissions := make(map[string]struct{}, len(contract.Permissions))
	for _, permission := range contract.Permissions {
		permissions[permission.Key] = struct{}{}
	}
	return permissions
}
