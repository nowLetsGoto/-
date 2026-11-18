// Package authz · 权限判定与数据范围推导。
// 职责：把一个「身份 + 项目」解析成权限集合，并从身份推导出该请求的数据范围。
// 输入：账号 / 人员 / 项目 / 角色 / 团队关联的读取能力（以窄接口声明，便于单测注入）。
// 输出：map[权限点]struct{}、contract.Scope，或相应错误。
// 边界：**只做判定与推导，不做业务**；不缓存（内部工具调用量小，先保证正确性，
//
//	需要时再加一层带失效的短 TTL 缓存）。
//
// ScopeOf 是数据范围的唯一推导点：只依据身份里的层级与租户，绝不读请求参数。
// 本阶段（P0）只把它推导出来交给中间件存进 ctx；仓储按 scope 过滤属于 P1。
//
// 判定顺序（用户明确要求）：
//  1. 超级管理员 → 全量权限（否则会出现"没人能改配置"的死锁）
//  2. 单人模式 → 业务基线（概览 / 应用包 / 应用），不参与角色区分
//  3. 项目角色：团队模式且请求带了项目、项目配置了角色 → 用它的权限
//  4. 人员角色：项目没配角色时，回落到人员自身的角色
//  5. 没有角色 → 只有默认的业务读取基线（含人员/团队的只读查看）。
//
// 单人模式是"个人工作台"：上传、发布与查看自己的包不需要管理员分配角色；
// 平台管理、账号、角色等权限仍不授予，且由路由层的模式中间件单独拦截。
package authz

import (
	"context"
	"errors"
	"sort"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
	peoplerepo "dandelion/server/internal/repository/people"
	projectrepo "dandelion/server/internal/repository/project"
	rolerepo "dandelion/server/internal/repository/role"
	teamsrepo "dandelion/server/internal/repository/teams"
	userrepo "dandelion/server/internal/repository/user"
)

// userLookup / peopleLookup / teamScopeLookup 是权限判定与 scope 推导所需的最小读取面。
// 声明成窄接口有两个理由：Service 只依赖它真正调用的方法；
// ScopeOf 的四级推导可以用几十行假实现完整覆盖，不必为单测启动 PostgreSQL。
// 真实实现就是对应的 repository 包，分层方向不变（service → repository）。
type userLookup interface {
	FindByID(ctx context.Context, id string) (userrepo.Record, error)
	FindByUsername(ctx context.Context, username string) (userrepo.Record, error)
}

type peopleLookup interface {
	// 身份解析必须能跨租户找到「账号名对应的人」，因此显式接收 scope：
	// PersonID / ScopeOf 传 All（登录时 scope 未算出），业务路径传调用者 scope。
	FindByID(ctx context.Context, id string, scope contract.Scope) (contract.Person, error)
	FindByUsername(ctx context.Context, username string, scope contract.Scope) (contract.Person, error)
}

type teamScopeLookup interface {
	AdminTeamIDs(ctx context.Context, userID string) ([]string, error)
	MemberTeamIDs(ctx context.Context, personID string) ([]string, error)
}

// 编译期约束：仓储一旦改了方法签名，这里立刻报错，而不是等到运行时才发现 scope 推导断了。
var (
	_ userLookup      = (*userrepo.Repository)(nil)
	_ peopleLookup    = (*peoplerepo.Repository)(nil)
	_ teamScopeLookup = (*teamsrepo.Repository)(nil)
)

// Service 是权限判定与 scope 推导的唯一入口。
type Service struct {
	users    userLookup
	people   peopleLookup
	projects repository.Partitioned[*projectrepo.Repository]
	roles    *rolerepo.Repository
	teams    teamScopeLookup
}

func NewService(
	users userLookup,
	people peopleLookup,
	projects repository.Partitioned[*projectrepo.Repository],
	roles *rolerepo.Repository,
	teams teamScopeLookup,
) *Service {
	return &Service{users: users, people: people, projects: projects, roles: roles, teams: teams}
}

// Permissions 返回该身份在指定项目下的权限集合；projectKey 为空表示"平台级请求"。
// 项目不存在时返回仓储的错误（通常是 ErrProjectInvalid → 400），与既有列表行为一致。
//
// 最终权限 = 业务角色权限 ∪ 管理身份的固有权限。
// 后者由 account_level 直接赋予（见 managementPermissions），不通过角色勾选——
// 这样「管人」永远是身份属性，不会出现「给某人勾了 directory.write 就越权管全公司」。
func (s *Service) Permissions(ctx context.Context, identity contract.Identity, projectKey string) (map[string]struct{}, error) {
	permissions, err := s.permissionsOf(ctx, identity, projectKey)
	if err != nil {
		return nil, err
	}
	// 管理权限只在团队模式下生效：单人模式是个人工作台，不参与任何公司/团队管理。
	// 否则有人用管理账号选 solo 登录就能拿到公司管理权，绕过模式隔离。
	if identity.Mode == contract.ModeTeam {
		for _, permission := range managementPermissions(identity.Level()) {
			permissions[permission] = struct{}{}
		}
	}
	return permissions, nil
}

// managementPermissions 返回管理身份层级固有的权限点。
// 只授予完成该层级职责所需的最小集合；团队管理员只看/管自己团队的数据，
// 范围限制由 scope 保证，这里只管「能不能」。
func managementPermissions(level string) []string {
	switch level {
	case contract.AccountLevelPlatformAdmin:
		return []string{
			"build.read", "build.write", "build.delete",
			"project.read", "project.write",
			"directory.read", "directory.write",
			"settings.read", "settings.write",
		}
	case contract.AccountLevelTeamAdmin:
		return []string{
			"build.read", "build.write",
			"project.read", "project.write",
			"directory.read", "directory.write",
		}
	default:
		// 超管已由 allPermissions 全量覆盖；普通成员没有管理权限。
		return nil
	}
}

// permissionsOf 是权限判定的业务部分：超管全量 → solo 基线 → 角色权限 → 默认基线。
// 从 Permissions 拆出来，是为了让「叠加管理权限」只有一个出口，不会漏掉某条 return。
func (s *Service) permissionsOf(ctx context.Context, identity contract.Identity, projectKey string) (map[string]struct{}, error) {
	// 1) 超管恒为全量
	if identity.IsSuperAdmin {
		return allPermissions(), nil
	}

	// 单人模式没有角色概念：固定授予三个业务菜单所需能力，不再查角色。
	if identity.Mode == contract.ModeSolo {
		return soloPermissions(), nil
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

// ScopeOf 是数据范围的唯一推导点。
//
// 它只依据身份里的层级与租户推导，绝不读请求参数：客户端即使伪造 tenantId/teamIds 也无效。
// 需要 ctx 与 error 是因为 team_admin / member 的团队边界必须查关联表（team_admins / team_members）；
// 查不到关联不代表越权，只是返回空边界；真正的数据库错误则向上传递，由中间件转成 500。
//
// 层级映射（与 docs/12 §4.3 一致）：
//   super_admin   → {All:true}
//   platform_admin→ {TenantID: 账号所属公司}
//   team_admin    → {TenantID, TeamIDs: team_admins 中关联的全部团队}
//   member        → {TenantID, TeamIDs: team_members 中关联的全部团队}
func (s *Service) ScopeOf(ctx context.Context, identity contract.Identity) (contract.Scope, error) {
	switch identity.Level() {
	case contract.AccountLevelSuperAdmin:
		// 超管跨全部公司；业务接口即便超管调用仍需落到具体公司，那是 P1 的约束。
		return contract.Scope{All: true}, nil

	case contract.AccountLevelPlatformAdmin:
		// 公司边界内看全部团队，所以 TeamIDs 留空数组（JSON 里是 [] 而不是 null）。
		return contract.Scope{TenantID: identity.TenantID, TeamIDs: []string{}}, nil

	case contract.AccountLevelTeamAdmin:
		teamIDs, err := s.teams.AdminTeamIDs(ctx, identity.UserID)
		if err != nil {
			return contract.Scope{}, err
		}
		// TeamScoped=true：即使没关联任何团队，也只能看到空集，不能退化成看全公司。
		return contract.Scope{TenantID: identity.TenantID, TeamIDs: teamIDs, TeamScoped: true}, nil

	case contract.AccountLevelMember:
		// 成员关系挂在人身上，先复用 /me 与权限判定共用的解析入口，
		// 避免"页面显示的人"和"算 scope 用的人"是两个人。
		personID, err := s.PersonID(ctx, identity)
		if err != nil {
			return contract.Scope{}, err
		}
		if personID == "" {
			// 普通成员的可见面由业务角色与项目成员关系决定，不按团队树收紧，
			// 与 P1 行为一致；否则存量成员（没有团队关联）会突然看不到任何数据。
			return contract.Scope{TenantID: identity.TenantID, TeamIDs: []string{}}, nil
		}
		teamIDs, err := s.teams.MemberTeamIDs(ctx, personID)
		if err != nil {
			return contract.Scope{}, err
		}
		return contract.Scope{TenantID: identity.TenantID, TeamIDs: teamIDs}, nil

	default:
		// 未知层级：失败关闭。零值 scope 不会匹配任何数据，绝不放大成 All。
		return contract.Scope{}, nil
	}
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
	// 用 All：这是身份解析（账号 → 档案），发生在 scope 推导之前，不能反过来依赖 scope。
	person, err := s.people.FindByUsername(ctx, identity.Username, contract.Scope{All: true})
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return person.ID, nil
}

// resolveRoleKey 按「项目角色 → 人员角色」的顺序找出该用的角色。
// 只有团队模式会走到这里（单人模式在上面已提前返回），因此固定查团队分区：
// 单人空间的项目没有角色，查它没有意义。
func (s *Service) resolveRoleKey(ctx context.Context, identity contract.Identity, projectKey string) (string, error) {
	// 2) 项目角色优先
	if projectKey != "" {
		// 项目角色按调用者所属公司过滤：借别公司的项目 key 查角色会落空，
		// 而不是拿到它的角色配置。超管在业务链路里也走 identity.TenantID（通常为空），
		// 因此这里用 All 放行平台视角——项目访问本身另有 RequireProjectPermission 把关。
		roleScope := contract.Scope{All: identity.IsSuperAdmin, TenantID: identity.TenantID}
		project, err := s.projects.For(contract.ModeTeam).FindByKey(ctx, projectKey, "", roleScope)
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
	// 人员角色 key 是身份属性，解析发生在 scope 推导链路中，显式传 All 避免循环依赖。
	person, err := s.people.FindByID(ctx, personID, contract.Scope{All: true})
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

// soloPermissions 是单人模式的业务能力集合。
// 单人模式只看自己的数据，因此不需要管理员分配角色；但只覆盖三个业务菜单，
// 不包含 settings / role / admin 等平台能力，避免"登录即管理员"。
// project.write 也在内：单人空间里项目由本人自建，不需要管理员先建好。
func soloPermissions() map[string]struct{} {
	return map[string]struct{}{
		"build.read":    {},
		"build.write":   {},
		"project.read":  {},
		"project.write": {},
	}
}

// defaultPermissions 是团队模式下已登录账号的最小阅读基线。
// build.read 支撑概览/应用包/应用；project.read 只为页面选择项目提供数据；
// directory.read 让普通成员能查看人员与团队（内部通讯录），但不授予写入能力——
// 「新增 / 编辑人员、团队」由 directory.write 把关，仍须管理员按角色分配。
// 角色、系统设置与账号管理不在基线内：那些属于管理动作，不是"人人可见"的信息。
// 每次返回独立集合，防止调用方污染基线。
func defaultPermissions() map[string]struct{} {
	return map[string]struct{}{
		"build.read":     {},
		"project.read":   {},
		"directory.read": {},
	}
}

// allPermissions 返回权限目录的全集。
func allPermissions() map[string]struct{} {
	permissions := make(map[string]struct{}, len(contract.Permissions))
	for _, permission := range contract.Permissions {
		permissions[permission.Key] = struct{}{}
	}
	return permissions
}
