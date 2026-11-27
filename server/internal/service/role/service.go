// Package role · 角色与权限的业务层。
// 职责：列出/查看角色、新建与编辑角色（含权限点全量覆盖）、为公司播种内置角色。
// 输入：repository/role、contract.Permissions 目录。
// 输出：contract.Role / RoleList，失败给 ValidationError（400）。
// 边界：不碰 HTTP、不判断"谁能改角色"（路由层保证写操作仅 platform_admin 及以上）；
//
//	不校验"权限点是否该给某人"——那是授权分配（下一期）的事。
//
// 重要：**角色里没有「超级管理员」和「平台管理员」**。
// 这两个是账号的身份（users.is_super_admin / users.account_level），不是角色：
//   - 超级管理员全平台固定一个，靠 is_super_admin 标记，权限判定第一步就短路，从不查角色；
//   - 平台管理员由超管在「公司管理」建公司时连带创建，同样靠 account_level。
//
// 早先存在一个 key=super-admin 的内置角色，它从未被权限判定读取过（判定只看身份），
// 却会出现在角色列表里让人误以为"挂上它就是超管"。已删除，见 §23 迁移说明。
package role

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
	rolerepo "dandelion/server/internal/repository/role"
)

// roleKeyPattern 与项目 key 同款，允许数字开头（公司 UUID 前缀可能以数字开头）。
// 分隔符只允许短横线，避免歧义。
var roleKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)

// builtinRole 直接复用仓储的定义结构，避免同一份字段写两遍、转换时漏字段。
// 用类型别名而不是再声明一个同构结构：两者永远保持一致。
type builtinRole = rolerepo.BuiltinDefinition

// builtinRoles 是内置的业务角色清单。
//
// **每家公司各持一份独立副本**（roles.tenant_id = 该公司），不是一个全局共享行：
// 内置角色只是"新公司开张时送的默认模板"，之后改它只影响本公司。
// 若做成全局共享，甲公司把 developer 的 build.delete 勾掉会让乙公司的开发也失去删除权——
// 一家公司的管理员凭什么能改另一家公司的权限？那是越权。
//
// 因此 is_builtin 的含义是「本公司预置的、可以改但不能删的角色」，
// 而不再是「平台级、所有公司共享」。
var builtinRoles = []builtinRole{
	{
		Key: "project-owner", Name: "项目负责人", Description: "负责项目整体推进：可管理项目与应用包，但不参与平台配置",
		Permissions: []string{
			"build.read", "build.write", "build.delete",
			"project.read", "project.write", "project.delete",
			"directory.read",
		},
	},
	{
		Key: "developer", Name: "开发", Description: "可以上传、发布与删除应用包",
		Permissions: []string{
			"build.read", "build.write", "build.delete",
			"project.read", "directory.read",
		},
	},
	{
		Key: "tester", Name: "测试", Description: "可以查看与下载应用包",
		Permissions: []string{"build.read", "project.read", "directory.read"},
	},
	{
		Key: "guest", Name: "访客", Description: "只读，适合外部协作方",
		Permissions: []string{"build.read", "project.read", "directory.read"},
	},
}

// Service 是角色模块的唯一业务入口。
type Service struct {
	repo *rolerepo.Repository
}

func NewService(repo *rolerepo.Repository) *Service {
	return &Service{repo: repo}
}

// EnsureBuiltin 为**所有公司**补齐内置角色：缺失的补上，已存在的不动。
//
// 为什么是"遍历所有公司"而不是"播种一次全局"：内置角色每公司独立一份（见 builtinRoles 的说明），
// 新建公司时也要立刻拥有这四个角色，否则它一进去就是"没有任何角色可分配"的空状态。
// 放在启动时全量补齐，比在"建公司"里再复制一份逻辑更不容易漏——
// 建公司走的是 repository/tenant 的事务，不该反过来依赖角色模块。
//
// 幂等：靠唯一索引 (tenant_id, key) + ON CONFLICT DO NOTHING，重复执行不会产生重复行，也不会覆盖用户改过的权限。
func (s *Service) EnsureBuiltin(ctx context.Context) error {
	tenantIDs, err := s.repo.ListTenantIDs(ctx)
	if err != nil {
		return err
	}
	for _, tenantID := range tenantIDs {
		for _, definition := range builtinRoles {
			if _, err := s.repo.CreateBuiltinIfMissing(ctx, tenantID, definition); err != nil {
				return err
			}
		}
	}
	// 目录会随版本演进，启动时要做两件对齐（都放在这里而不是 schema.sql，
	// 是为了让菜单清单与权限清单各只有 contract 一处定义）：
	//   1. 权限点：清掉已下线的 key（防止"看不见却生效"的历史权限）；
	//   2. 菜单：内置角色补齐新菜单、清掉已移除的菜单。
	// 查看权限随菜单版本演进（如账号管理的查看点从 directory.write 改为 account.read），
	// 每次启动补一次缺口，保证存量角色不会因为换了权限点而突然进不去页面。
	if err := s.repo.BackfillViewPermissions(ctx); err != nil {
		return err
	}
	if err := s.repo.ReconcilePermissions(ctx); err != nil {
		return err
	}
	if err := s.repo.ReconcileBuiltinMenus(ctx); err != nil {
		return err
	}
	// 存量角色对齐到新规则（按权限点重算菜单 + 按菜单补查看权限）。
	// 放在最后：此时权限点目录与内置角色都已就位，推导结果才准确。
	return s.Reconcile(ctx)
}

// SeedForTenant 给指定公司补齐内置角色；建公司后立即调用，也可重复调用（幂等）。
// EnsureBuiltin 遍历所有公司时会复用它，保证"建公司时铺一次"与"启动时补齐"是同一份行为。
func (s *Service) SeedForTenant(ctx context.Context, tenantID string) error {
	if tenantID == "" {
		// 空 tenantID 属于调用方 bug：内置角色必须挂在某家公司下。
		// 静默跳过会让问题更晚才暴露，这里直接拒绝。
		return fmt.Errorf("播种内置角色缺少公司标识")
	}
	for _, definition := range builtinRoles {
		if _, err := s.repo.CreateBuiltinIfMissing(ctx, tenantID, definition); err != nil {
			return err
		}
	}
	return nil
}

// List 返回当前调用者可见的角色（本公司内置 + 本公司自定义），角色数量少不分页。
func (s *Service) List(ctx context.Context, scope contract.Scope) (contract.RoleList, error) {
	items, err := s.repo.List(ctx, scope)
	if err != nil {
		return contract.RoleList{}, err
	}
	return contract.RoleList{Items: items}, nil
}

// Get 按 id 取单个角色，受 scope 限定（看不到别家公司的角色）。
//
// 用 id 而不是 key：key 只保证「公司内唯一」，超管视角下按 key 取会歧义。
func (s *Service) Get(ctx context.Context, id string, scope contract.Scope) (contract.Role, error) {
	if !contract.IsValidUUID(id) {
		return contract.Role{}, contract.NotFound(contract.ResourceRole)
	}
	return s.repo.FindByID(ctx, id, scope)
}

// Create 新建自定义角色；归属公司从 scope 取。
func (s *Service) Create(ctx context.Context, in contract.RoleInput, scope contract.Scope) (contract.Role, error) {
	if scope.All || scope.TenantID == "" {
		// 角色必须挂在某家公司下：超管不直接建业务角色，公司管理员才有归属。
		return contract.Role{}, &contract.ValidationError{Message: "只能在本公司下新建角色"}
	}
	// 未传 menuKeys 时默认全量：既兼容旧客户端，也符合"新建 = 全量，勾选 = 收窄"的心智。
	// 显式传空数组则表示"这个角色看不到任何受控菜单"，是管理员的主动选择。
	if in.MenuKeys == nil {
		in.MenuKeys = contract.AllMenuKeys()
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
//
// scope 限定只能改本公司的角色：内置角色也是本公司那一份，改它不影响其他公司。
// 这正是"每公司独立副本"的价值——公司管理员维护自己的角色定义，无需超管介入。
func (s *Service) Update(ctx context.Context, id string, in contract.RoleInput, scope contract.Scope) (contract.Role, error) {
	// id 非 UUID 时按 404 处理：否则会被绑定到 uuid 列报 SQL 错误而变成 500
	// （用旧路径 /roles/project-owner 访问就会踩到）。与 people / teams 同一口径。
	if !contract.IsValidUUID(id) {
		return contract.Role{}, contract.NotFound(contract.ResourceRole)
	}
	normalized, err := normalize(in, false, "")
	if err != nil {
		return contract.Role{}, err
	}
	return s.repo.Update(ctx, id, normalized, scope)
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
func (s *Service) Delete(ctx context.Context, id string, scope contract.Scope) error {
	if !contract.IsValidUUID(id) {
		return contract.NotFound(contract.ResourceRole)
	}
	// 用 FindByID（带 scope）而不是无 scope 的查询：越权（删别家公司的角色）应得 404，
	// 与"这个 id 不存在"同一个答案，不透露别家公司有没有这个角色。
	role, err := s.repo.FindByID(ctx, id, scope)
	if err != nil {
		return err
	}
	if role.IsBuiltin {
		return &contract.ValidationError{Message: "内置角色不可删除，可以修改它的权限；如需另一套配置请新建角色"}
	}

	usage, err := s.repo.CountUsage(ctx, role.ID)
	if err != nil {
		return err
	}
	if !usage.Empty() {
		return &contract.ValidationError{Message: describeRoleUsage(usage)}
	}
	return s.repo.Delete(ctx, id, scope)
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
		MenuKeys:    dedupe(in.MenuKeys),
	}
	// 权限由菜单树推导，不直接信任请求体里的 permissions：
	// 查看权限 ← 勾选菜单自动授予；操作权限 ← 只保留对应菜单已勾选的。
	// 详见 derivePermissions。
	out.Permissions = derivePermissions(out.MenuKeys, in.Permissions)

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
	// 菜单 key 必须来自平台目录：挡住拼错与伪造值。
	// 目录是只读的（见 contract.Menus 的说明），不接受自定义菜单。
	for _, menuKey := range out.MenuKeys {
		if !contract.IsValidMenu(menuKey) {
			return contract.RoleInput{}, &contract.ValidationError{Message: "包含无效的菜单：" + menuKey}
		}
	}
	return out, nil
}

// derivePermissions 从「勾选的菜单 + 请求里的操作权限」推导最终权限集合。
//
// 两条规则，对应产品的新设计：
//
//  1. 查看权限不单独配置。勾选某菜单就自动获得它的 ViewPermission；
//     反过来，即使请求体里塞了 role.read，只要没勾「角色管理」菜单也会被丢弃——
//     这是"有菜单才有查看权"的硬约束，堵住绕过菜单配置直接拿能力的路径。
//
//  2. 操作权限必须挂在已勾选的菜单下。例如没勾「应用包」却传 build.write，
//     会被清掉——一个看不到应用包的角色，写应用包没有意义。
//
// 之所以在后端再推导一次（而不是只靠前端）：
// 前端可能被绕过、也可能是旧客户端，服务端必须自己保证这条不变量。
func derivePermissions(menuKeys, requested []string) []string {
	// 已勾选菜单授予的查看权限 + 它允许的操作权限集合。
	granted := make(map[string]struct{})
	allowedActions := make(map[string]struct{})
	for _, key := range menuKeys {
		menu, ok := menuByKey(key)
		if !ok {
			continue
		}
		if menu.ViewPermission != "" {
			granted[menu.ViewPermission] = struct{}{}
		}
		for _, action := range menu.Actions {
			allowedActions[action.Key] = struct{}{}
		}
	}
	// 只接收"该菜单允许的操作权限"；查看权限一概忽略（由上面自动授予）。
	viewPermissions := contract.ViewPermissions()
	for _, permission := range requested {
		if _, isView := viewPermissions[permission]; isView {
			continue
		}
		if _, allowed := allowedActions[permission]; allowed {
			granted[permission] = struct{}{}
		}
	}
	result := make([]string, 0, len(granted))
	for permission := range granted {
		result = append(result, permission)
	}
	sort.Strings(result) // 稳定输出，便于断言与 diff
	return result
}

// menusForPermissions 按权限点反推应该拥有哪些菜单（迁移用）。
//
// 规则很直接：角色已经有的每个权限点，都对应到"它能打开哪张菜单"。
// 一个权限点可能被多张菜单共享（build.read 对应概览/应用包/应用），
// 全部给出——因为它们是同一类业务能力的不同入口。
//
// 用途：历史角色的 menuKeys 是"全量勾满"的（§28 图省事的默认），
// 与它实际的权限点严重不符（例如 guest 也被勾了角色管理菜单）。
// 迁移时按权限点重算菜单，让"能看到的菜单"与"实际有的权限"重新对齐。
func menusForPermissions(permissions []string) []string {
	owned := make(map[string]struct{}, len(permissions))
	for _, permission := range permissions {
		owned[permission] = struct{}{}
	}
	menus := make([]string, 0)
	for _, menu := range contract.Menus {
		if _, ok := owned[menu.ViewPermission]; ok {
			menus = append(menus, menu.Key)
		}
	}
	return menus
}

// Reconcile 把存量角色对齐到新规则：按权限点重算菜单，再按菜单推导回权限。
//
// **只执行一次**：由 schema_migrations 记录完成状态。
// 若每次启动都跑，管理员后来在界面上手工调整的菜单授权会被反复冲掉——
// 迁移是"一次性搬家"，不是"持续同步"。
//
// 两个方向都走一遍的原因：
//   - 旧菜单是"全量勾满"的，与角色定位不符 → 先按权限点重算，收窄到合理范围；
//   - 旧权限里缺查看类权限（role.read 等） → 再按菜单推导，自动补齐。
//
// 幂等：连续跑多次结果不变（menusForPermissions 与 derivePermissions 都满足幂等）。
func (s *Service) Reconcile(ctx context.Context) error {
	const migrationName = "role-authz-derived-from-menus"
	applied, err := s.repo.MigrationApplied(ctx, migrationName)
	if err != nil {
		return fmt.Errorf("检查迁移状态失败: %w", err)
	}
	if applied {
		return nil
	}
	all, err := s.repo.List(ctx, contract.Scope{All: true})
	if err != nil {
		return fmt.Errorf("读取全部角色做对齐失败: %w", err)
	}
	for _, role := range all {
		menus := menusForPermissions(role.Permissions)
		permissions := derivePermissions(menus, role.Permissions)
		if err := s.repo.ReplaceAuthorization(ctx, role.ID, permissions, menus); err != nil {
			return fmt.Errorf("对齐角色 %s 失败: %w", role.Key, err)
		}
	}
	if err := s.repo.MarkMigrationApplied(ctx, migrationName); err != nil {
		return fmt.Errorf("记录迁移完成状态失败: %w", err)
	}
	return nil
}

// menuByKey 按路径查菜单定义。目录很小，线性查找足够，
// 不为它再建一份 map——两处数据反而可能不同步。
func menuByKey(key string) (contract.Menu, bool) {
	for _, menu := range contract.Menus {
		if menu.Key == key {
			return menu, true
		}
	}
	return contract.Menu{}, false
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

// deprecatedPermissions 是已从角色目录移除、但存量角色里可能还留着的管理类权限点。
// 播种时统一清掉：它们已由 account_level 直接决定，留在角色里只会让权限来源出现两个口径。
var deprecatedPermissions = []string{
	contract.PermissionDirectoryWrite,
	contract.PermissionSettingsRead,
	contract.PermissionSettingsWrite,
	contract.PermissionAdminAccess,
}

// NormalizeRoles 是启动时的一次性收敛，幂等：
//
//  1. 删除历史遗留的 super-admin 角色。它从未被权限判定读取过（判定只看 is_super_admin），
//     却出现在角色列表里让人误以为"挂上它就是超管"，是误解的来源。
//     挂过它的人由外键 ON DELETE SET NULL 自动变成"未分配"，回到默认读取基线。
//  2. 把历史遗留的全局内置角色（tenant_id 为空）下发给每家公司各一份副本，
//     随后删掉全局那份。旧模型是"一份角色所有公司共享"，新模型是"每公司一份"。
//  3. 清掉角色里残留的管理类权限点：它们已由 account_level 直接决定，
//     留在角色里会让权限来源出现两个口径。
func (s *Service) NormalizeRoles(ctx context.Context) error {
	if err := s.repo.DeleteLegacySuperAdminRole(ctx); err != nil {
		return err
	}
	if err := s.repo.AdoptGlobalBuiltins(ctx, builtinRoles); err != nil {
		return err
	}
	return s.repo.PurgeDeprecatedPermissions(ctx, deprecatedPermissions)
}
