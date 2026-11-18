// Package role · 角色与权限的业务层。
// 职责：列出/查看角色、新建与编辑角色（含权限点全量覆盖）、首次启动播种内置角色。
// 输入：repository/role、contract.Permissions 目录。
// 输出：contract.Role / RoleList，失败给 ValidationError（400）。
// 边界：不碰 HTTP、不判断"谁能改角色"（路由层保证写操作仅超管）；
//       不校验"权限点是否该给某人"——那是授权分配（下一期）的事。
package role

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
	rolerepo "dandelion/server/internal/repository/role"
)

// SuperAdminKey 是内置超管角色的 key：它的权限恒等于全量目录，不允许被改小。
const SuperAdminKey = "super-admin"

// roleKeyPattern 与项目 key 同款，但允许数字开头：
// 自定义角色 key 的前缀取公司 UUID 前 8 位（可能是数字开头，如 712e101b-xxx），
// 若强制字母开头，这类公司永远建不了角色。分隔符仍只允许短横线，避免歧义。
var roleKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)

// builtinRole 是内置角色的定义（只在库里没有该 key 时写入）。
type builtinRole struct {
	key         string
	name        string
	description string
	permissions []string
}

// builtinRoles 是平台内置的业务角色；tenant_id 为空表示所有公司可见。
// 它们只能由超管改（并在页面提示"平台内置"），公司管理员只能新建自己的自定义角色。
//
// project-owner 已废弃：它的管理能力由 team_admin 身份替代，业务能力被 developer 覆盖。
// 存量角色不物理删除（people.role_key 与 projects.role_key 都引用它），
// 只是在播种时不再创建；已有数据继续可用，见 §9 迁移说明。
var builtinRoles = []builtinRole{
	{
		key: SuperAdminKey, name: "超级管理员", description: "拥有全部权限；权限固定为全量，不可修改",
		permissions: allPermissionKeys(),
	},
	{
		key: "developer", name: "开发", description: "可以上传、发布与删除应用包",
		permissions: []string{
			"build.read", "build.write", "build.delete",
			"project.read", "directory.read",
		},
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

// List 返回当前调用者可见的角色（平台内置 + 本公司自定义），角色数量少不分页。
func (s *Service) List(ctx context.Context, scope contract.Scope) (contract.RoleList, error) {
	items, err := s.repo.List(ctx, scope)
	if err != nil {
		return contract.RoleList{}, err
	}
	return contract.RoleList{Items: items}, nil
}

// Get 按 key 取单个角色，受 scope 限定（看不到别家公司的自定义角色）。
func (s *Service) Get(ctx context.Context, key string, scope contract.Scope) (contract.Role, error) {
	return s.repo.FindByKey(ctx, key, scope)
}

// Create 新建自定义角色；归属公司从 scope 取。
func (s *Service) Create(ctx context.Context, in contract.RoleInput, scope contract.Scope) (contract.Role, error) {
	if scope.All || scope.TenantID == "" {
		// 角色必须挂在某家公司下：超管不直接建业务角色，公司管理员才有归属。
		return contract.Role{}, &contract.ValidationError{Message: "只能在本公司下新建角色"}
	}
	normalized, err := normalize(in, true, scope.TenantID)
	if err != nil {
		return contract.Role{}, err
	}
	// 内置角色的标识对所有公司是保留字：数据库索引把内置（tenant_id 为空）与各公司
	// 分在不同分组，技术上允许重名，但业务上不能允许——
	// 角色列表里会出现两个 developer，而权限解析按 key 查时也不确定命中哪个。
	// 先查一次内置里有没有，有就直接拒。
	if builtin, err := s.repo.FindByKeyAny(ctx, normalized.Key, ""); err == nil && builtin.Key != "" {
		return contract.Role{}, &contract.ValidationError{
			Message: "「" + normalized.Key + "」是平台内置角色的标识，换一个试试",
		}
	} else if err != nil && !errors.Is(err, contract.ErrNotFound) {
		return contract.Role{}, err
	}
	item, err := s.repo.Create(ctx, normalized, scope)
	if err != nil {
		if repository.IsUniqueViolation(err) {
			return contract.Role{}, &contract.ValidationError{Message: "本公司内已有相同标识的角色，换一个试试"}
		}
		return contract.Role{}, err
	}
	return item, nil
}

// Update 覆盖角色名称 / 描述 / 权限；key 不可改。
// scope 限定只能改本公司自定义角色，平台内置角色只有超管能改。
func (s *Service) Update(ctx context.Context, key string, in contract.RoleInput, scope contract.Scope) (contract.Role, error) {
	// 超管角色的权限必须是全量，否则会出现"没人能改角色"的死锁。
	if key == SuperAdminKey {
		return contract.Role{}, &contract.ValidationError{
			Message: "超级管理员角色的权限固定为全量，不可修改",
		}
	}
	normalized, err := normalize(in, false, "")
	if err != nil {
		return contract.Role{}, err
	}
	return s.repo.Update(ctx, key, normalized, scope)
}

// Delete 删除本公司自定义角色。
//
// 三条前置条件，任一不满足都拒绝并说清原因：
//  1. 不是平台内置角色 —— 内置角色属于所有公司，删掉会让别家公司的成员瞬间失去权限；
//  2. 不是超管角色 —— 它的权限恒为全量，删掉等于拆掉"能改配置"的唯一入口，
//     会形成谁也改不了角色/设置的死锁；
//  3. 没有任何人员/项目在用 —— role_id 是 SET NULL，静默删除会把那些人降权到"无角色"，
//     而界面上看不出发生过什么。
//
// 前两条用 ValidationError（业务上不允许），第三条也用它——调用者有删除权限，
// 只是当前状态不允许，前端据此提示"先解除引用"而不是"权限不足"。
func (s *Service) Delete(ctx context.Context, key string, scope contract.Scope) error {
	if key == SuperAdminKey {
		return &contract.ValidationError{Message: "超级管理员角色不可删除"}
	}
	// 用 FindByKey（带 scope）而不是 FindByKeyAny：越权（删别家公司的角色）应得 404，
	// 与"这个 key 不存在"同一个答案，不透露别家公司有没有这个角色。
	role, err := s.repo.FindByKey(ctx, key, scope)
	if err != nil {
		return err
	}
	if role.IsBuiltin {
		return &contract.ValidationError{Message: "平台内置角色不可删除，如需限制权限请改用自定义角色"}
	}

	usage, err := s.repo.CountUsage(ctx, role.ID)
	if err != nil {
		return err
	}
	if !usage.Empty() {
		return &contract.ValidationError{Message: describeRoleUsage(usage)}
	}
	return s.repo.Delete(ctx, key, scope)
}

// describeRoleUsage 把引用统计翻成一句能指导操作的话。
func describeRoleUsage(usage rolerepo.Usage) string {
	parts := make([]string, 0, 2)
	if usage.Persons > 0 {
		parts = append(parts, fmt.Sprintf("%d 名人员", usage.Persons))
	}
	if usage.Projects > 0 {
		parts = append(parts, fmt.Sprintf("%d 个项目", usage.Projects))
	}
	return "该角色已分配给 " + strings.Join(parts, "、") + "，请先改掉他们的角色后再删除"
}

// normalize 去空白、校验格式与权限点合法性；withKey 为 false 时忽略 key（编辑场景）。
//
// 关于 key 的唯一性：roles.key 现在是「公司内唯一」（见 schema.sql 的 idx_roles_tenant_key），
// 角色与人员/项目的关联已改为引用 roles.id，因此两家公司各有一个 developer 互不冲突。
// 早期这里是强制「公司 UUID 前 8 位 -」前缀来绕过全局唯一，那个限制已随本次重构移除。
func normalize(in contract.RoleInput, withKey bool, _ string) (contract.RoleInput, error) {
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

// allPermissionKeys 返回全集 key：可勾选的业务点 + 已从目录移除的管理点。
// 超管角色要显示"全量"，所以管理点也要算进去；普通角色的权限只从 Permissions 目录校验。
func allPermissionKeys() []string {
	keys := make([]string, 0, len(contract.Permissions)+4)
	for _, permission := range contract.Permissions {
		keys = append(keys, permission.Key)
	}
	keys = append(keys,
		contract.PermissionDirectoryWrite, contract.PermissionSettingsRead,
		contract.PermissionSettingsWrite, contract.PermissionAdminAccess)
	return keys
}

// deprecatedPermissions 是已从角色目录移除、但存量角色里可能还留着的管理类权限点。
// 播种时统一清掉：它们已由 account_level 直接决定，留在角色里只会让权限来源出现两个口径。
var deprecatedPermissions = []string{
	contract.PermissionDirectoryWrite,
	contract.PermissionSettingsRead,
	contract.PermissionSettingsWrite,
	contract.PermissionAdminAccess,
}

// NormalizeBuiltins 修正存量内置角色的归属与权限：
//   - 内置角色（is_builtin=true）的 tenant_id 归零为 NULL，表示"平台级、所有公司共享"；
//     P1 回填曾把所有角色归到默认公司，若不修正，其他公司将看不到任何内置角色。
//   - 非超管的内置角色清掉管理类权限点（project-owner 曾挂 directory.write）。
//
// 幂等，启动时调用；只动 is_builtin=true 的行，用户自建角色不受影响。
func (s *Service) NormalizeBuiltins(ctx context.Context) error {
	if err := s.repo.ClearBuiltinTenant(ctx); err != nil {
		return err
	}
	return s.repo.DeletePermissionsExceptRole(ctx, SuperAdminKey, deprecatedPermissions)
}
