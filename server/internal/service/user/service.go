// Package user · 账号管理的业务层（仅超级管理员可用）。
// 职责：列表 / 新建 / 编辑（超管标记与绑定人员）/ 重置密码 / 启用禁用 / 删除，
// 以及「改自己的密码」，并守住四条自锁红线。
// 输入：repository/user、repository/account_cleanup、登录模块的密码派生、调用者身份（由中间件保证已是超管）。
// 输出：contract.User / UserList，失败给 ValidationError（400）或 ErrForbidden（403）。
// 边界：不碰 HTTP、不写审计（审计在 P2.2 接）、不判断"是不是超管"——那是中间件的职责；
//
//	这里只做业务护栏：不能禁用自己、不能取消自己超管、不能弄丢最后一个超管、不能删除自己。
package user

import (
	"context"
	"crypto/rand"
	"errors"
	"log"
	"math/big"
	"os"
	"regexp"
	"strings"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
	userrepo "dandelion/server/internal/repository/user"
	loginsvc "dandelion/server/internal/service/login"
	"dandelion/server/internal/storage"
)

const (
	statusActive   = "active"
	statusDisabled = "disabled"
)

// 账号名会进令牌载荷（用 | 分段）与 URL，因此只允许安全字符。
var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{3,32}$`)

// errAccountNotFound 是账号场景的 404：目标不存在、跨模式访问、无权查看三种情况共用同一句话，
// 不透露账号是否真实存在。用统一工厂构造，文案由 contract 生成，这里只声明"缺的是账号"。
var errAccountNotFound = contract.NotFound(contract.ResourceUser)

// tempPasswordAlphabet 去掉了容易看错的 0/O/1/l/I。
const tempPasswordAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// Service 是账号管理的唯一业务入口。
type Service struct {
	repo    *userrepo.Repository
	cleanup *repository.AccountCleanup
	paths   *storage.Paths
	// teams 只用于校验 team_admin 的关联团队落在自己可见范围内；窄接口便于单测。
	teams teamLookup
	// roles 只用于校验「调用者能不能分配这个角色」；窄接口，不必为它拉起整套权限判定。
	roles roleAssigner
}

// teamLookup 是账号边界校验所需的最小读取面：
//   - AdminTeamIDs：调用者作为团队负责人管理哪些团队（scope 的来源）；
//   - MemberTeamIDs：目标人员属于哪些团队（判断目标是否落在调用者团队内）。
//
// 两个方法都在 teams 仓储里，因此装配时传同一个对象，不必多依赖一个包。
type teamLookup interface {
	AdminTeamIDs(ctx context.Context, userID string) ([]string, error)
	MemberTeamIDs(ctx context.Context, personID string) ([]string, error)
}

// roleAssigner 是账号模块对授权服务的只读依赖，两个方法都由 authz.Service 实现：
//   - ValidateRoleAssignable：校验能否把某角色分配给人员（防提权）；
//   - Permissions：取调用者的最终权限，用来判断能否建账号。
//
// 合在一个窄接口里而不是加第二个依赖：两者本就是同一次授权判定的两个视角，
// 装配时传同一个 authz.Service，单测时一个假对象就能覆盖。
type roleAssigner interface {
	ValidateRoleAssignable(ctx context.Context, identity contract.Identity, roleKey string) error
	Permissions(ctx context.Context, identity contract.Identity, projectKey string) (map[string]struct{}, error)
}

func NewService(repo *userrepo.Repository, cleanup *repository.AccountCleanup, paths *storage.Paths, teams teamLookup, roles roleAssigner) *Service {
	return &Service{repo: repo, cleanup: cleanup, paths: paths, teams: teams, roles: roles}
}

// resolveCreateLevel 校验账号接口能否创建账号，并归一化层级。
//
// 产品决策后的规则：
//
//   - 能力门槛：必须有 directory.write（路由层已守一次，这里 defense in depth）；
//   - 经账号接口**只能建 member**。team_admin 与 member 现已统一由角色配置驱动，
//     层级本身不再有管理含义，建一个 team_admin 没有价值；
//   - 硬底线：super_admin 是平台身份、platform_admin 只能随公司创建，
//     两者都不允许经账号接口发放——这条不受任何权限点影响。
//
// 归属固定为调用者所属公司（在 Create 里取 caller.TenantID），请求体无法越权指定。
// 返回值只保留 level：团队归属来自人员档案（team_members），账号这一层不接受 teamIds。
func resolveCreateLevel(requested string, hasAccountWrite bool) (string, error) {
	level := strings.TrimSpace(requested)
	if level == "" {
		level = contract.AccountLevelMember
	}
	if !contract.IsAccountLevel(level) {
		return "", &contract.ValidationError{Message: "账号层级不合法"}
	}
	// 两条硬底线，放在能力校验之前：无论谁、无论配了什么权限都不能突破。
	if level == contract.AccountLevelSuperAdmin {
		return "", contract.ErrForbidden
	}
	if level == contract.AccountLevelPlatformAdmin {
		return "", &contract.ValidationError{
			Message: "平台管理员需随公司一起创建（公司管理 → 新建公司）",
		}
	}
	if !hasAccountWrite {
		return "", contract.ErrForbidden
	}
	return contract.AccountLevelMember, nil
}

// List 返回该模式的账号页；keyword 同时匹配登录名与绑定人员姓名。
// 账号跟着登录模式分流：单人模式管单人账号，团队模式管团队账号，两个视图互不干扰。
func (s *Service) List(ctx context.Context, page contract.PageQuery, mode contract.SessionMode, keyword string) (contract.UserList, error) {
	// 公司账号只看本公司；超管（All）看全部。归属是硬边界，不接受请求参数覆盖。
	items, total, err := s.repo.List(ctx, page, mode, keyword, authctx.ScopeFrom(ctx))
	if err != nil {
		return contract.UserList{}, err
	}
	return contract.UserList{
		Items: items,
		Page:  contract.Page{Page: page.Page, PageSize: page.PageSize, Total: total},
	}, nil
}

// Create 新建账号：账号名校验 → 密码派生 → 落库。
//
// 账号的模式取自 caller.Mode（创建者登录时选定的模式），表单不再让用户挑：
// 登录已经确定了这次会话属于哪套数据，在单人模式里建出一个团队账号会让它从
// 创建者自己的列表里消失，这不是"更灵活"，只是让人找不到自己刚建的账号。
func (s *Service) Create(ctx context.Context, caller contract.Identity, in contract.UserInput) (contract.User, error) {
	username := strings.TrimSpace(in.Username)
	if !usernamePattern.MatchString(username) {
		return contract.User{}, &contract.ValidationError{
			Message: "账号名只能用字母、数字、点、下划线、短横线，3-32 位",
		}
	}
	if err := validatePassword(in.Password); err != nil {
		return contract.User{}, err
	}

	// 层级与归属从调用者推导，请求体无法越权指定。
	// team_admin 建普通账号时，其可见团队由 ScopeOf 注入的 scope 提供。
	// 建账号的能力来自权限点：角色配了 directory.write 就能建。
	// 判定复用 authz 服务（它已按"身份固有 ∪ 角色配置"算好最终权限），
	// 不在这里重新实现一遍——权限判定必须只有一个出口。
	permissions, err := s.roles.Permissions(ctx, caller, "")
	if err != nil {
		return contract.User{}, err
	}
	_, hasAccountWrite := permissions[contract.PermissionDirectoryWrite]

	level, err := resolveCreateLevel(in.AccountLevel, hasAccountWrite)
	if err != nil {
		return contract.User{}, err
	}
	// isSuperAdmin 是比 account_level 更高的平台身份，任何非超管都不能借建账号把自己提权。
	if in.IsSuperAdmin && !canGrantSuperAdmin(caller) {
		return contract.User{}, contract.ErrForbidden
	}

	personID := strings.TrimSpace(in.PersonID)
	roleKey := strings.TrimSpace(in.RoleKey)
	// 先确认「能绑谁」再确认「能发什么角色」：团队负责人的两个边界缺一不可，
	// 且归属校验失败时的提示更基础（先让他选对人员，再谈角色）。
	if err := s.ensurePersonAssignable(ctx, caller, personID); err != nil {
		return contract.User{}, err
	}
	// 角色写的是 people.role_key：先校验调用者有没有资格发这个角色，
	// 再落库——不合格的请求不该在库里留下半截数据。
	if err := s.ensureAssignableRole(ctx, caller, personID, roleKey); err != nil {
		return contract.User{}, err
	}

	hash, err := loginsvc.HashPassword(in.Password)
	if err != nil {
		return contract.User{}, err
	}

	// 归属固定为调用者所属公司：平台管理员与团队管理员都只能在本公司建账号。
	record, err := s.repo.Create(ctx, username, hash, caller.Mode, personID, roleKey,
		in.IsSuperAdmin, level, caller.TenantID, nil)
	if err != nil {
		if repository.IsUniqueViolation(err) {
			return contract.User{}, &contract.ValidationError{Message: "该账号名已存在"}
		}
		if repository.IsForeignKeyViolation(err) {
			return contract.User{}, &contract.ValidationError{Message: "绑定的人员不存在，请刷新后重试"}
		}
		return contract.User{}, err
	}
	// 响应里带出团队关联：仓储读的是 users 表，team_admins 的关联由本层补齐，
	// 否则前端建完团队管理员会看到空的 teamIds，与 /me 的结果不一致。
	if level == contract.AccountLevelTeamAdmin {
		record.User.TeamIDs, err = s.repo.AdminTeamIDs(ctx, record.User.ID)
		if err != nil {
			return contract.User{}, err
		}
	}
	return record.User, nil
}

// canGrantSuperAdmin 判断调用者能否给用户设置超管标记（新建与编辑共用）。
// 只有平台超管本人可以——这是比 account_level 更高的身份，
// 任何管理层级借建账号设这个标记都能把自己/他人提成超管，必须单独守死。
func canGrantSuperAdmin(caller contract.Identity) bool {
	return caller.Level() == contract.AccountLevelSuperAdmin
}

// ensureAssignableRole 校验「调用者能给这个账号配这个角色」。
//
// 两条路径都要守：roleKey 非空时校验角色本身（调用者要把它写进 people.role_key）；
// roleKey 为空但绑定了人员时，校验该人员**当前**的角色——把账号绑到一个
// super-admin 角色的人身上，登录后就继承了那份权限，这和不传 roleKey 的绕过没有区别。
// 角色归属校验统一交给 authz.ValidateRoleAssignable，与人员管理页同一条判据。
func (s *Service) ensureAssignableRole(ctx context.Context, caller contract.Identity, personID, roleKey string) error {
	if roleKey != "" {
		if personID == "" {
			return &contract.ValidationError{Message: "角色要挂在绑定人员上，请先选择绑定人员"}
		}
		return s.roles.ValidateRoleAssignable(ctx, caller, roleKey)
	}
	if personID == "" {
		return nil
	}
	current, err := s.repo.PersonRoleKey(ctx, personID)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return &contract.ValidationError{Message: "绑定的人员不存在，请刷新后重试"}
		}
		return err
	}
	return s.roles.ValidateRoleAssignable(ctx, caller, current)
}

// ensureInScope 校验目标账号是否落在调用者的可管理范围内。三层依次判定：
//
//  1. 超管：不管（但超管账号本身仍归超管管，见下）。
//  2. 公司边界：跨公司一律按「不存在」处理，不透露对方是否有这个 id。
//  3. 团队边界（仅团队负责人）：只能管「自己团队里的普通成员」，外加自己。
//     平台管理员不属于 TeamScoped，直接放行——它本来就要管整家公司。
//
// 越界统一返回 404 而不是 403：账号是否存在本身就是信息。团队负责人通过通讯录
// 能看到公司组织架构（人员与团队是 directory.read），因此它心里清楚那个人存在；
// 但"能不能管"是另一回事，用 403 会把两个概念混在一起。
func (s *Service) ensureInScope(ctx context.Context, caller contract.Identity, target userrepo.Record) error {
	if caller.Level() == contract.AccountLevelSuperAdmin {
		return nil
	}
	// 超管账号对公司管理员不可见（按 id 访问一律 404，不透露它存在）。
	// 只看 is_super_admin：超管是账号身份，不可能通过挂某个角色获得。
	if target.User.IsSuperAdmin {
		return errAccountNotFound
	}
	if caller.TenantID == "" || target.TenantID != caller.TenantID {
		return errAccountNotFound
	}
	return s.ensureTeamScope(ctx, caller, target)
}

// ensureTeamScope 落实普通账号（team_admin / member）的横向边界。
//
// 谁能操作谁：
//   - 自己：始终可以，否则连自己的账号都碰不到；
//   - 同团队的账号：可以，范围由 scope.TeamIDs 决定；
//   - 跨团队：一律按"不存在"处理，不透露对方账号是否有这个 id。
//
// 与旧设计的区别：过去 team_admin 被视为"管理层"，不能操作其他 team_admin；
// 现在 team_admin 与 member 统一由角色配置驱动，不再有这层身份特权，
// 因此判断只看"目标是不是平台治理者"，不看 team_admin/member 之分。
//
// 平台管理员（scope.TeamScoped=false）不受本函数限制——它管整家公司。
func (s *Service) ensureTeamScope(ctx context.Context, caller contract.Identity, target userrepo.Record) error {
	scope := authctx.ScopeFrom(ctx)
	if !scope.TeamScoped {
		return nil
	}
	if target.User.ID == caller.UserID {
		return nil
	}
	// 平台治理者账号（platform_admin）绝不能由普通账号操作——
	// 它们不在角色配置范围内，是超管建公司时生成的身份，
	// 允许操作等于让公司内的任意角色拿到公司级治理权。
	targetLevel := contract.EffectiveAccountLevel(target.User.AccountLevel, target.User.IsSuperAdmin)
	if targetLevel != contract.AccountLevelMember && targetLevel != contract.AccountLevelTeamAdmin {
		return errAccountNotFound
	}
	inTeam, err := s.personInTeams(ctx, target.User.PersonID, scope.TeamIDs)
	if err != nil {
		return err
	}
	if !inTeam {
		return errAccountNotFound
	}
	return nil
}

// ensurePersonAssignable 校验「能不能把账号绑到这个人员上」，创建与换绑共用。
//
// 两条规则，按身份分层：
//
//  1. 必须绑定人员（超管除外）。账号的权限就是绑定人员的权限：
//     没有人员的账号登录后既无角色也无团队，什么数据都看不到，只是一条"看着正常、实际废掉"的记录。
//     超管豁免——它没有业务人员档案，也不需要。
//     这条对平台管理员同样成立，与前端 AccountFormPage 的必填校验保持一致
//     （两层各说一套会让人以为界面拦错了）。
//
//  2. 团队负责人还要求「本团队」。否则可以把别团队的人挂到自己能管的账号上，
//     等于绕过团队边界把那个人的权限借过来。
//     平台管理员管整家公司，不设这一层；下拉里的人员天然受 people.List 的 scope 限制。
func (s *Service) ensurePersonAssignable(ctx context.Context, caller contract.Identity, personID string) error {
	if caller.Level() == contract.AccountLevelSuperAdmin {
		return nil
	}
	if personID == "" {
		return &contract.ValidationError{Message: "请为该账号绑定人员：账号的权限来自绑定的人员"}
	}
	// 只有受团队边界约束的账号（TeamScoped）才需要确认"这个人是不是同团队"。
	// 平台管理员 scope 不按团队收敛，因此不受这一层限制。
	if !authctx.ScopeFrom(ctx).TeamScoped {
		return nil
	}
	inTeam, err := s.personInTeams(ctx, personID, authctx.ScopeFrom(ctx).TeamIDs)
	if err != nil {
		return err
	}
	if !inTeam {
		// 用 ValidationError 而不是 404：这里校验的是请求体里的字段，
		// 前端需要一句能指导操作的话（去选本团队的人），而不是一个空白的"不存在"。
		return &contract.ValidationError{Message: "只能为本团队的人员创建账号"}
	}
	return nil
}

// personInTeams 判断人员是否属于给定团队中的任意一个。
// 空人员或空团队都按「不在」处理：无团队归属的账号对团队负责人不可见，
// 这正是「失败关闭」——宁可少管一个，也不能因为配置缺失放大成"能管全公司"。
func (s *Service) personInTeams(ctx context.Context, personID string, teamIDs []string) (bool, error) {
	if personID == "" || len(teamIDs) == 0 {
		return false, nil
	}
	personTeams, err := s.teams.MemberTeamIDs(ctx, personID)
	if err != nil {
		return false, err
	}
	allowed := make(map[string]struct{}, len(teamIDs))
	for _, id := range teamIDs {
		allowed[id] = struct{}{}
	}
	for _, id := range personTeams {
		if _, ok := allowed[id]; ok {
			return true, nil
		}
	}
	return false, nil
}

// Update 改绑定人员、角色与超管标记；账号名与模式不可改。
func (s *Service) Update(ctx context.Context, caller contract.Identity, id string, in contract.UserUpdate) (contract.User, error) {
	current, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return contract.User{}, err
	}
	if err := s.ensureInScope(ctx, caller, current); err != nil {
		return contract.User{}, err
	}
	// 与删除同一条边界：跨模式的账号在这个视图里不存在，也就不能被编辑。
	if current.User.Mode != caller.Mode {
		return contract.User{}, errAccountNotFound
	}

	// 非超管不能把目标账号提成超管：isSuperAdmin 是平台最高身份，不在公司管理链路里。
	if in.IsSuperAdmin && !canGrantSuperAdmin(caller) {
		return contract.User{}, contract.ErrForbidden
	}

	// 红线一：不能取消自己的超管身份，否则会当场把自己锁在门外。
	if current.User.ID == caller.UserID && current.User.IsSuperAdmin && !in.IsSuperAdmin {
		return contract.User{}, &contract.ValidationError{Message: "不能取消自己的超级管理员身份"}
	}
	// 红线二：不能让平台失去最后一个超管。
	if current.User.IsSuperAdmin && !in.IsSuperAdmin {
		if err := s.ensureAnotherSuperAdmin(ctx, current.User.ID); err != nil {
			return contract.User{}, err
		}
	}

	personID := strings.TrimSpace(in.PersonID)
	roleKey := strings.TrimSpace(in.RoleKey)
	// 只在"真的换了角色或换了绑定人员"时校验：存量账号可能已经绑着高权限人员，
	// 平台管理员改它的其他字段（例如超管标记）不该因为历史数据而莫名 403。
	// 判据用「角色是否变化」而不是「roleKey 是否非空」——前端会回传当前值，
	// 没改动就不该重新过一遍权限门槛。
	if personID != current.User.PersonID || roleKey != current.User.RoleKey {
		// 换绑人员同样受团队边界约束：否则可以把别团队的人挂到自己能管的账号上，
		// 等于绕过 create 那一道校验。放在同一个 if 里是为了与角色校验同进同退：
		// 没改动就不重新过门槛，存量数据不会因为历史归属而莫名被拒。
		if err := s.ensurePersonAssignable(ctx, caller, personID); err != nil {
			return contract.User{}, err
		}
		if err := s.ensureAssignableRole(ctx, caller, personID, roleKey); err != nil {
			return contract.User{}, err
		}
	}

	record, err := s.repo.Update(ctx, id, personID, in.IsSuperAdmin, roleKey)
	if err != nil {
		if repository.IsForeignKeyViolation(err) {
			return contract.User{}, &contract.ValidationError{Message: "绑定的人员或角色不存在，请刷新后重试"}
		}
		return contract.User{}, err
	}
	return record.User, nil
}

// Get 按 id 取单个账号，供编辑页回填；跨模式按"不存在"处理。
func (s *Service) Get(ctx context.Context, caller contract.Identity, id string) (contract.User, error) {
	record, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return contract.User{}, err
	}
	if record.User.Mode != caller.Mode {
		return contract.User{}, errAccountNotFound
	}
	if err := s.ensureInScope(ctx, caller, record); err != nil {
		return contract.User{}, err
	}
	if record.AccountLevel == contract.AccountLevelTeamAdmin {
		record.User.TeamIDs, err = s.repo.AdminTeamIDs(ctx, record.User.ID)
		if err != nil {
			return contract.User{}, err
		}
	}
	return record.User, nil
}

// SetStatus 启用/禁用账号；禁用会自增令牌版本，做到"立即下线"。
func (s *Service) SetStatus(ctx context.Context, caller contract.Identity, id, status string) (contract.User, error) {
	if status != statusActive && status != statusDisabled {
		return contract.User{}, &contract.ValidationError{Message: "状态只能是启用或禁用"}
	}
	current, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return contract.User{}, err
	}
	if err := s.ensureInScope(ctx, caller, current); err != nil {
		return contract.User{}, err
	}
	if status == statusDisabled {
		// 红线三：不能禁用自己，也不能禁用最后一个可用超管。
		if current.User.ID == caller.UserID {
			return contract.User{}, &contract.ValidationError{Message: "不能禁用当前登录的账号"}
		}
		if current.User.IsSuperAdmin {
			if err := s.ensureAnotherSuperAdmin(ctx, current.User.ID); err != nil {
				return contract.User{}, err
			}
		}
	}

	if err := s.repo.SetStatus(ctx, id, status); err != nil {
		return contract.User{}, err
	}
	record, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return contract.User{}, err
	}
	return record.User, nil
}

// ResetPassword 生成一次性临时密码并返回明文（只在本次响应里出现一次）。
func (s *Service) ResetPassword(ctx context.Context, caller contract.Identity, id string) (string, error) {
	current, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return "", err
	}
	if err := s.ensureInScope(ctx, caller, current); err != nil {
		return "", err
	}
	password, err := generateTempPassword()
	if err != nil {
		return "", err
	}
	hash, err := loginsvc.HashPassword(password)
	if err != nil {
		return "", err
	}
	// SetPassword 会顺带自增令牌版本：旧设备上的会话立即失效。
	if err := s.repo.SetPassword(ctx, id, hash); err != nil {
		return "", err
	}
	return password, nil
}

// Delete 删除账号，返回被删的记录，供 handler 写审计时带上账号名。
//
// 删除只在「同模式」内成立：caller.Mode 既是筛选条件也是权限边界——
// 单人模式下看不到团队账号，也就不该能删它。模式不符按「账号不存在」答复，
// 不泄漏另一种模式里有没有这个账号。
//
// 无论账号声明的是哪种模式，都在同一笔操作里清理它在 solo 分区的遗留数据：
// 登录不校验账号的模式（见 login.Service.Authenticate），一个 team 账号完全可以
// 用单人模式登录并留下项目与包，而这些数据的 owner_user_id 是裸 TEXT、没有外键，
// 账号一消失它们就永远没人能看见了。因此判定依据只能是「这个 id 在 solo 分区有没有数据」，
// 而不是「账号标签写着什么」——后者只是个默认视图，不保证数据不存在。
// team 分区的数据不受影响：那些项目与包属于团队，不随账号消失。
func (s *Service) Delete(ctx context.Context, caller contract.Identity, id string) (contract.User, error) {
	current, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return contract.User{}, err
	}
	if current.User.Mode != caller.Mode {
		return contract.User{}, errAccountNotFound
	}
	if err := s.ensureInScope(ctx, caller, current); err != nil {
		return contract.User{}, err
	}
	// 红线四：不能删自己——删完当场失去登录能力，且没有撤销入口。
	if current.User.ID == caller.UserID {
		return contract.User{}, &contract.ValidationError{Message: "不能删除当前登录的账号"}
	}
	// 删超管等于"减少一个可用超管"，与降级、禁用走同一条护栏。
	if current.User.IsSuperAdmin && current.User.Status == statusActive {
		if err := s.ensureAnotherSuperAdmin(ctx, current.User.ID); err != nil {
			return contract.User{}, err
		}
	}

	// 不留跨分区的孤儿数据：不按 users.mode 判断（见上方注释），一律尝试清理。
	// 账号在 solo 分区没有数据时它就是一次空转，代价是两条 DELETE，换来的是"永远不会有孤儿"。
	relativePaths, err := s.cleanup.PurgeSoloData(ctx, current.User.ID)
	if err != nil {
		return contract.User{}, err
	}

	// 仓储层同时按 id 与模式删除，即使前面漏了校验，跨模式删除也会命中 0 行。
	if err := s.repo.Delete(ctx, id, caller.Mode); err != nil {
		return contract.User{}, err
	}
	s.removePackages(relativePaths)
	return current.User, nil
}

// removePackages 删除已从库中清理掉的包文件。
// 放在删库之后、且不返回错误：库里已经没有这些包的记录，留下来的文件只会占磁盘，
// 删不掉也不该让"账号已删除"这个事实变成一次失败（记日志供运维排查即可）。
func (s *Service) removePackages(relativePaths []string) {
	for _, relativePath := range relativePaths {
		abs, err := s.paths.SafeResolve(relativePath)
		if err != nil {
			log.Printf("清理账号包文件时路径校验失败 rel=%s err=%v", relativePath, err)
			continue
		}
		if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("清理账号包文件失败 path=%s err=%v", abs, err)
		}
	}
}

// ChangeOwnPassword 修改当前登录账号自己的密码：校验旧密码 → 派生新密码 → 落库。
// 成功后令牌版本自增，当前令牌立即失效，调用方必须回登录页重新登录——
// 这是刻意的：改密码意味着"我认为旧凭据可能已泄漏"，此时保留任何旧会话都不合理。
func (s *Service) ChangeOwnPassword(ctx context.Context, caller contract.Identity, in contract.PasswordChange) error {
	if err := validatePassword(in.NewPassword); err != nil {
		return err
	}
	if in.OldPassword == in.NewPassword {
		return &contract.ValidationError{Message: "新密码不能与当前密码相同"}
	}

	current, err := s.repo.FindByID(ctx, caller.UserID)
	if err != nil {
		return err
	}
	// 旧密码不对与账号不存在返回同一个错误，避免被拿来做密码试探。
	if !loginsvc.VerifyPassword(current.PasswordHash, in.OldPassword) {
		return &contract.ValidationError{Message: "当前密码不正确"}
	}

	hash, err := loginsvc.HashPassword(in.NewPassword)
	if err != nil {
		return err
	}
	return s.repo.SetPassword(ctx, current.User.ID, hash)
}

// ensureAnotherSuperAdmin 在「即将减少一个超管」时确认还有别人兜底。
func (s *Service) ensureAnotherSuperAdmin(ctx context.Context, excludingID string) error {
	total, err := s.repo.CountSuperAdmins(ctx)
	if err != nil {
		return err
	}
	// 当前这个仍是超管且启用，所以至少要有 2 个才允许降级/禁用。
	if total <= 1 {
		return &contract.ValidationError{Message: "系统必须保留至少一个可用的超级管理员"}
	}
	_ = excludingID
	return nil
}

func validatePassword(password string) error {
	return contract.ValidatePassword(password)
}

// generateTempPassword 生成 12 位临时密码，字符集剔除了易混淆字符。
func generateTempPassword() (string, error) {
	const length = 12
	password := make([]byte, length)
	for i := range password {
		index, err := rand.Int(rand.Reader, big.NewInt(int64(len(tempPasswordAlphabet))))
		if err != nil {
			return "", err
		}
		password[i] = tempPasswordAlphabet[index.Int64()]
	}
	return string(password), nil
}
