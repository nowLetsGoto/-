// Package role · 角色与权限的业务层。
// 职责：列出/查看角色、新建与编辑角色（含权限点全量覆盖）、首次启动播种内置角色。
// 输入：repository/role、contract.Permissions 目录。
// 输出：contract.Role / RoleList，失败给 ValidationError（400）。
// 边界：不碰 HTTP、不判断"谁能改角色"（路由层保证写操作仅超管）；
//       不校验"权限点是否该给某人"——那是授权分配（下一期）的事。
package role

import (
	"context"
	"regexp"
	"strings"
	"unicode/utf8"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
	rolerepo "dandelion/server/internal/repository/role"
)

// SuperAdminKey 是内置超管角色的 key：它的权限恒等于全量目录，不允许被改小。
const SuperAdminKey = "super-admin"

// roleKeyPattern 与项目 key 同款：小写字母开头，允许数字与短横线。
var roleKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// builtinRole 是内置角色的定义（只在库里没有该 key 时写入）。
type builtinRole struct {
	key         string
	name        string
	description string
	permissions []string
}

// builtinRoles 是内置模板角色；除超级管理员外，权限可以在页面上调整。
// 角色管理不在任何角色的权限里：它只对超管开放（见 contract.Permissions 的说明）。
// 三个业务角色都带 directory.read——人员与团队是内部通讯录，能登录就该能查；
// 写入能力（directory.write）只给项目负责人。
var builtinRoles = []builtinRole{
	{
		key: SuperAdminKey, name: "超级管理员", description: "拥有全部权限；权限固定为全量，不可修改",
		permissions: allPermissionKeys(),
	},
	{
		key: "project-owner", name: "项目负责人", description: "可以管理项目、删除包、维护人员与团队",
		permissions: []string{
			"build.read", "build.write", "build.delete",
			"project.read", "project.write",
			"directory.read", "directory.write",
		},
	},
	{
		key: "developer", name: "开发", description: "可以上传与发布应用包，不能删除",
		permissions: []string{"build.read", "build.write", "project.read", "directory.read"},
	},
	{
		key: "tester", name: "测试", description: "可以查看与下载应用包",
		permissions: []string{"build.read", "project.read", "directory.read"},
	},
	{
		key: "guest", name: "访客", description: "只读，适合外部协作方",
		permissions: []string{"build.read", "project.read", "directory.read"},
	},
}

// Service 是角色模块的唯一业务入口。
type Service struct {
	repo *rolerepo.Repository
}

func NewService(repo *rolerepo.Repository) *Service {
	return &Service{repo: repo}
}

// EnsureBuiltin 播种内置角色：已存在的角色不会被默认权限覆盖。
func (s *Service) EnsureBuiltin(ctx context.Context) error {
	for _, definition := range builtinRoles {
		if _, err := s.repo.CreateBuiltinIfMissing(ctx, definition.key, definition.name, definition.description, definition.permissions); err != nil {
			return err
		}
	}
	return nil
}

// List 返回全部角色（含权限点），角色数量少，不分页。
func (s *Service) List(ctx context.Context) (contract.RoleList, error) {
	items, err := s.repo.List(ctx)
	if err != nil {
		return contract.RoleList{}, err
	}
	return contract.RoleList{Items: items}, nil
}

// Get 按 key 取单个角色。
func (s *Service) Get(ctx context.Context, key string) (contract.Role, error) {
	return s.repo.FindByKey(ctx, key)
}

// Create 新建自定义角色。
func (s *Service) Create(ctx context.Context, in contract.RoleInput) (contract.Role, error) {
	normalized, err := normalize(in, true)
	if err != nil {
		return contract.Role{}, err
	}
	item, err := s.repo.Create(ctx, normalized)
	if err != nil {
		if repository.IsUniqueViolation(err) {
			return contract.Role{}, &contract.ValidationError{Message: "该角色标识已存在"}
		}
		return contract.Role{}, err
	}
	return item, nil
}

// Update 覆盖角色名称 / 描述 / 权限；key 不可改。
func (s *Service) Update(ctx context.Context, key string, in contract.RoleInput) (contract.Role, error) {
	// 超管角色的权限必须是全量，否则会出现"没人能改角色"的死锁。
	if key == SuperAdminKey {
		return contract.Role{}, &contract.ValidationError{
			Message: "超级管理员角色的权限固定为全量，不可修改",
		}
	}
	normalized, err := normalize(in, false)
	if err != nil {
		return contract.Role{}, err
	}
	return s.repo.Update(ctx, key, normalized)
}

// normalize 去空白、校验格式与权限点合法性；withKey 为 false 时忽略 key（编辑场景）。
func normalize(in contract.RoleInput, withKey bool) (contract.RoleInput, error) {
	out := contract.RoleInput{
		Key:         strings.ToLower(strings.TrimSpace(in.Key)),
		Name:        strings.TrimSpace(in.Name),
		Description: strings.TrimSpace(in.Description),
		Permissions: dedupe(in.Permissions),
	}

	if withKey && !roleKeyPattern.MatchString(out.Key) {
		return contract.RoleInput{}, &contract.ValidationError{
			Message: "角色标识只能用小写字母、数字与短横线，2-32 位且以字母开头",
		}
	}
	if out.Name == "" {
		return contract.RoleInput{}, &contract.ValidationError{Message: "请填写角色名称"}
	}
	if utf8.RuneCountInString(out.Name) > 40 {
		return contract.RoleInput{}, &contract.ValidationError{Message: "角色名称不能超过 40 个字符"}
	}
	if utf8.RuneCountInString(out.Description) > 200 {
		return contract.RoleInput{}, &contract.ValidationError{Message: "角色描述不能超过 200 个字符"}
	}
	for _, permission := range out.Permissions {
		if !contract.IsValidPermission(permission) {
			return contract.RoleInput{}, &contract.ValidationError{Message: "包含无效的权限点：" + permission}
		}
	}
	return out, nil
}

func dedupe(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

// allPermissionKeys 返回权限目录的全部 key，供超管角色使用。
func allPermissionKeys() []string {
	keys := make([]string, 0, len(contract.Permissions))
	for _, permission := range contract.Permissions {
		keys = append(keys, permission.Key)
	}
	return keys
}
