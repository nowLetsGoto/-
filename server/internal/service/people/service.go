// Package people · 人员的业务层。
// 职责：列出人员、新建与编辑人员（含关联团队 / 项目的多选结果）、提供「当前登录人」投影。
// 输入：repository/people 的读写能力、contract 的字段上限。
// 输出：contract.PersonList / Person / Me。
// 边界：不碰 HTTP、不做权限判断（P2 才用 person 建账号与角色）、不拼 SQL。
package people

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/authctx"
	"dandelion/server/internal/repository"
	peoplerepo "dandelion/server/internal/repository/people"
	tenantrepo "dandelion/server/internal/repository/tenant"
)

// RoleAssigner 校验「调用者能否把某个角色分配出去」。
// 声明成窄接口而不是直接依赖 authz.Service：本包只用到这一个能力，
// 窄接口让单测可以注入假实现，也不必为它拉起整套权限判定的依赖。
// RoleAssigner 是人员模块对角色模块的只读依赖。
//
// 两个方法都是"按 key 查角色的某个属性"，放在一起是因为它们的调用方都是
// 「给人员分配角色」这条链路：前者校验能不能分配，后者取该角色的菜单授权。
// 窄接口让本模块可以独立单测，不必拉起整套角色服务。
type RoleAssigner interface {
	ValidateRoleAssignable(ctx context.Context, identity contract.Identity, roleKey string) error
	// MenuKeysOf 返回角色被授权的菜单；角色不存在时返回空切片，不报错。
	MenuKeysOf(ctx context.Context, roleKey string, scope contract.Scope) ([]string, error)
}

// Service 是人员模块的唯一业务入口。
type Service struct {
	repo    *peoplerepo.Repository
	tenants *tenantrepo.Repository
	roles   RoleAssigner
}

func NewService(repo *peoplerepo.Repository, tenants *tenantrepo.Repository, roles RoleAssigner) *Service {
	return &Service{repo: repo, tenants: tenants, roles: roles}
}

// List 返回指定页的人员（含关联名字）与总数；page.PageSize=0 时返回全量。
// scope 从 ctx 取（由 InjectScope 注入），本层不重新推导、不接受调用方指定。
func (s *Service) List(ctx context.Context, page contract.PageQuery) (contract.PersonList, error) {
	items, total, err := s.repo.List(ctx, page, authctx.ScopeFrom(ctx))
	if err != nil {
		return contract.PersonList{}, err
	}
	return contract.PersonList{
		Items: items,
		Page:  contract.Page{Page: page.Page, PageSize: page.PageSize, Total: total},
	}, nil
}

// Get 按 id 取单个人员（含关联），供编辑页回填。
// id 不是 UUID 时直接按 404 处理：否则会被绑定到 uuid 列报 SQL 错误而变成 500。
func (s *Service) Get(ctx context.Context, id string) (contract.Person, error) {
	if !contract.IsValidUUID(id) {
		return contract.Person{}, contract.NotFound(contract.ResourcePerson)
	}
	return s.repo.FindByID(ctx, id, authctx.ScopeFrom(ctx))
}

// Create 新建人员。
//
// 分配角色在这里过一道防提权校验：handler 只保证「调用者有管理层级」，
// 「这个角色他有没有资格分配」必须在业务层判——否则团队管理员给人员挂上
// super-admin，那个账号就拿到全量权限（实测确认过这个链路能通）。
func (s *Service) Create(ctx context.Context, in contract.PersonInput) (contract.Person, error) {
	normalized, err := normalize(in)
	if err != nil {
		return contract.Person{}, err
	}
	if err := s.roles.ValidateRoleAssignable(ctx, authctx.From(ctx), normalized.RoleKey); err != nil {
		return contract.Person{}, err
	}
	item, err := s.repo.Create(ctx, normalized, authctx.ScopeFrom(ctx))
	if err != nil {
		return contract.Person{}, constraintConflictToMessage(err)
	}
	return item, nil
}

// Update 全量覆盖人员字段与关联。
// 同 Create：改角色也要过防提权校验（把某人从 developer 改成 super-admin 是同一类越权）。
func (s *Service) Update(ctx context.Context, id string, in contract.PersonInput) (contract.Person, error) {
	if !contract.IsValidUUID(id) {
		return contract.Person{}, contract.NotFound(contract.ResourcePerson)
	}
	normalized, err := normalize(in)
	if err != nil {
		return contract.Person{}, err
	}
	if err := s.roles.ValidateRoleAssignable(ctx, authctx.From(ctx), normalized.RoleKey); err != nil {
		return contract.Person{}, err
	}
	item, err := s.repo.Update(ctx, id, normalized, authctx.ScopeFrom(ctx))
	if err != nil {
		return contract.Person{}, constraintConflictToMessage(err)
	}
	return item, nil
}

// Delete 删除人员。
//
// 只允许删**没有任何引用**的人：还挂在团队/项目里、或还被账号绑定时一律拒绝，
// 并把原因说清楚。这比级联清理安全——删掉一个还绑着账号的人，那个账号会变成
// 「没有档案的账号」，登录后看不到自己的团队与角色，等于半个僵尸，且无法从界面修复。
//
// 拒绝时返回 ValidationError（400）而不是 403：调用者有删除权限，只是当前状态不允许，
// 前端据此提示"先去解除关联"，而不是误以为权限不足。
func (s *Service) Delete(ctx context.Context, id string) error {
	// 先按 scope 取一次：既确认这条记录存在且属于本公司（越界应得 404 而不是 400），
	// 也让后面的引用检查只针对真实存在的记录。
	if _, err := s.Get(ctx, id); err != nil {
		return err
	}
	usage, err := s.repo.CountUsage(ctx, id)
	if err != nil {
		return err
	}
	if !usage.Empty() {
		return &contract.ValidationError{Message: describePersonUsage(usage)}
	}
	return constraintConflictToMessage(s.repo.Delete(ctx, id, authctx.ScopeFrom(ctx)))
}

// describePersonUsage 把引用统计翻成一句能指导操作的话。
// 具体数字比"有依赖"有用得多：用户能据此判断要清几条关系。
func describePersonUsage(usage peoplerepo.Usage) string {
	parts := make([]string, 0, 3)
	if usage.Accounts > 0 {
		parts = append(parts, fmt.Sprintf("绑定了 %d 个账号", usage.Accounts))
	}
	if usage.Teams > 0 {
		parts = append(parts, fmt.Sprintf("属于 %d 个团队", usage.Teams))
	}
	if usage.Projects > 0 {
		parts = append(parts, fmt.Sprintf("参与 %d 个项目", usage.Projects))
	}
	return "该人员" + strings.Join(parts, "、") + "，请先解除关联后再删除"
}

// Me 返回当前登录人的档案投影。
// identity 提供"我是谁 / 我属于哪个公司"；personID 由 authz 服务按"账号绑定优先"解析，
// 和权限判定共用同一人；teamIDs 来自请求上下文里的 scope，由 InjectScope 推导，
// 本层只做透传，绝不重新算一遍——推导必须只有 authz.Service.ScopeOf 一个出口。
//
// 没有对应人员不算错误：仍返回身份字段，前端据此回退到第一个项目。
func (s *Service) Me(ctx context.Context, identity contract.Identity, personID string, teamIDs []string) (contract.Me, error) {
	tenantName, err := s.tenants.NameByID(ctx, identity.TenantID)
	if err != nil {
		return contract.Me{}, err
	}
	if teamIDs == nil {
		teamIDs = []string{}
	}
	me := contract.Me{
		Username:     identity.Username,
		ProjectKeys:  []string{},
		IsSuperAdmin: identity.IsSuperAdmin,
		TenantID:     identity.TenantID,
		TenantName:   tenantName,
		AccountLevel: identity.Level(),
		TeamIDs:      teamIDs,
	}
	// 超管不受菜单授权约束（见 contract.Menus 的设计说明）：直接返回全量。
	// 这一步必须在查人员之前——超管没有业务人员档案。
	if identity.IsSuperAdmin {
		me.MenuKeys = contract.AllMenuKeys()
		return me, nil
	}
	if personID == "" {
		return me, nil
	}
	// 用调用者的 scope 查自己的档案与项目：超管看全部、公司账号看本公司，
	// 自己的记录必然落在自己的可见范围内。
	scope := authctx.ScopeFrom(ctx)
	person, err := s.repo.FindByID(ctx, personID, scope)
	if errors.Is(err, contract.ErrNotFound) {
		return me, nil
	}
	if err != nil {
		return contract.Me{}, err
	}
	keys, err := s.repo.ListProjectKeys(ctx, person.ID)
	if err != nil {
		return contract.Me{}, err
	}
	me.PersonID = person.ID
	me.Name = person.Name
	// 角色原样透出：未分配角色时是空串，由前端统一显示「普通用户」。
	me.RoleKey = person.RoleKey
	me.RoleName = person.RoleName
	me.ProjectKeys = keys

	// 菜单授权跟随角色：未分配角色时是空数组，前端据此只保留基础菜单。
	if person.RoleKey != "" {
		me.MenuKeys, err = s.roles.MenuKeysOf(ctx, person.RoleKey, scope)
		if err != nil {
			return contract.Me{}, err
		}
	}
	if me.MenuKeys == nil {
		me.MenuKeys = []string{}
	}
	return me, nil
}


// constraintConflictToMessage 把人员的数据库约束冲突翻译成可上屏的 400 提示。
//
// 只处理两类约束：登录名重复、关联对象不存在。
// 404 不在这里处理——那是"目标人员不存在"，由仓储的 NotFound(resource) 负责。
func constraintConflictToMessage(err error) error {
	switch {
	case repository.IsUniqueViolation(err):
		return &contract.ValidationError{Message: "本公司内已有相同登录名的人员，换一个试试"}
	case repository.IsForeignKeyViolation(err):
		return &contract.ValidationError{Message: "关联的团队、项目或角色不存在，请刷新后重试"}
	default:
		return err
	}
}

// normalize 收敛字段：去空白、校验长度、去重关联 ID。
func normalize(in contract.PersonInput) (contract.PersonInput, error) {
	out := contract.PersonInput{
		Name:       strings.TrimSpace(in.Name),
		Username:   strings.TrimSpace(in.Username),
		Email:      strings.TrimSpace(in.Email),
		Phone:      strings.TrimSpace(in.Phone),
		Title:      strings.TrimSpace(in.Title),
		RoleKey:    strings.TrimSpace(in.RoleKey),
		TeamIDs:    dedupe(in.TeamIDs),
		ProjectIDs: dedupe(in.ProjectIDs),
	}

	if out.Name == "" {
		return contract.PersonInput{}, &contract.ValidationError{Message: "请填写姓名"}
	}
	if exceed(out.Name, contract.MaxPersonNameLen) {
		return contract.PersonInput{}, &contract.ValidationError{Message: "姓名不能超过 40 个字符"}
	}
	if exceed(out.Username, contract.MaxUsernameLen2) {
		return contract.PersonInput{}, &contract.ValidationError{Message: "登录名不能超过 64 个字符"}
	}
	if exceed(out.Email, contract.MaxEmailLen) || (out.Email != "" && !strings.Contains(out.Email, "@")) {
		return contract.PersonInput{}, &contract.ValidationError{Message: "邮箱格式不正确"}
	}
	if exceed(out.Phone, contract.MaxPhoneLen) {
		return contract.PersonInput{}, &contract.ValidationError{Message: "手机号不能超过 40 个字符"}
	}
	if exceed(out.Title, contract.MaxTitleLen) {
		return contract.PersonInput{}, &contract.ValidationError{Message: "岗位不能超过 60 个字符"}
	}
	if len(out.TeamIDs) > contract.MaxRelationCount || len(out.ProjectIDs) > contract.MaxRelationCount {
		return contract.PersonInput{}, &contract.ValidationError{Message: "关联数量过多"}
	}
	return out, nil
}

func exceed(value string, max int) bool {
	return utf8.RuneCountInString(value) > max
}

// dedupe 去重并保持勾选顺序，避免同一关联被写两遍。
func dedupe(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}
