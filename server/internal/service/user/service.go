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

// errAccountNotFound 是账号场景的 404：跨模式删除与"账号不存在"共用同一句话，
// 这样既不会泄漏另一种模式里有没有这个账号，也不会像通用 ErrNotFound 那样说成"包不存在"。
var errAccountNotFound = &contract.NotFoundError{Message: "账号不存在或已被删除"}

// tempPasswordAlphabet 去掉了容易看错的 0/O/1/l/I。
const tempPasswordAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// Service 是账号管理的唯一业务入口。
type Service struct {
	repo   *userrepo.Repository
	cleanup *repository.AccountCleanup
	paths  *storage.Paths
	// teams 只用于校验 team_admin 的关联团队落在自己可见范围内；窄接口便于单测。
	teams teamLookup
}

// teamLookup 是账号创建时校验团队归属所需的最小读取面。
type teamLookup interface {
	AdminTeamIDs(ctx context.Context, userID string) ([]string, error)
}

func NewService(repo *userrepo.Repository, cleanup *repository.AccountCleanup, paths *storage.Paths, teams teamLookup) *Service {
	return &Service{repo: repo, cleanup: cleanup, paths: paths, teams: teams}
}

// resolveCreateLevel 校验调用者能否创建指定层级，并归一化默认值。
// 这是三级账号体系的安全核心：层级只能往下建、归属只能留在自己公司。
//
// 规则（按调用者层级）：
//   - super_admin：可建 platform_admin / team_admin / member；不能建另一个 super_admin
//     （平台管理员必须走「建公司」流程，与公司原子创建，避免孤儿公司）；
//   - platform_admin：只能建 team_admin / member，归属本公司；
//   - team_admin：只能建 member，归属本公司且团队落在自己关联团队内；
//   - member：不能建账号（路由层已挡，这里再挡一次， defense in depth）。
//
// 返回的 teamIDs 已经过归属校验，未指定的 team_admin 返回空切片（可后续在编辑页分配）。
func resolveCreateLevel(caller contract.Identity, requested string, teamIDs []string, visibleTeamIDs []string) (string, []string, error) {
	callerLevel := caller.Level()
	level := strings.TrimSpace(requested)
	if level == "" {
		// 缺省层级：平台管理员建的是团队管理员之外的普通账号，团队管理员建的也是普通账号。
		level = contract.AccountLevelMember
	}
	if !contract.IsAccountLevel(level) {
		return "", nil, &contract.ValidationError{Message: "账号层级不合法"}
	}

	// 任何人都不能通过账号接口创建超管：那是最高的平台身份，不在本链路里。
	if level == contract.AccountLevelSuperAdmin {
		return "", nil, contract.ErrForbidden
	}
	// 平台管理员**只能随公司创建**（POST /platform/tenants，见 repository/tenant.CreateWithAdmin），
	// 任何路径都不许经账号接口单独建——包括超管。
	//
	// 为什么超管也要拦：本函数下面给账号定归属用的是 caller.TenantID，而超管的 TenantID 恒为空。
	// 放行超管会建出「platform_admin 但 tenant_id 为空」的孤儿账号：它能登录、带 9 项权限点，
	// 但 scope 推导落到「无公司 → 1 = 0」，任何数据都读不到，且没有公司归属、除了超管谁都管不了它。
	// 这种账号没有任何合法用途，只会在账号列表里留下无法解释的一行。
	if level == contract.AccountLevelPlatformAdmin {
		return "", nil, &contract.ValidationError{
			Message: "平台管理员需随公司一起创建（公司管理 → 新建公司）",
		}
	}

	switch callerLevel {
	case contract.AccountLevelSuperAdmin:
		// 超管建账号时归属由请求指定（建公司流程另走 service/tenant，这里仅保留能力）。
	case contract.AccountLevelPlatformAdmin:
		if caller.TenantID == "" {
			return "", nil, contract.ErrForbidden
		}
	case contract.AccountLevelTeamAdmin:
		if level != contract.AccountLevelMember {
			return "", nil, contract.ErrForbidden
		}
		if caller.TenantID == "" {
			return "", nil, contract.ErrForbidden
		}
		// 团队管理员指定的团队必须是自己关联团队的子集，防止跨团队授权。
		allowed := make(map[string]struct{}, len(visibleTeamIDs))
		for _, id := range visibleTeamIDs {
			allowed[id] = struct{}{}
		}
		for _, id := range teamIDs {
			if _, ok := allowed[id]; !ok {
				return "", nil, contract.ErrForbidden
			}
		}
	default:
		return "", nil, contract.ErrForbidden
	}

	// 只有 team_admin 才有团队关联；其他层级即使传了 teamIds 也忽略，避免脏数据。
	if level != contract.AccountLevelTeamAdmin {
		teamIDs = nil
	}
	return level, teamIDs, nil
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
	scope := authctx.ScopeFrom(ctx)
	level, teamIDs, err := resolveCreateLevel(caller, in.AccountLevel, in.TeamIDs, scope.TeamIDs)
	if err != nil {
		return contract.User{}, err
	}
	// isSuperAdmin 是比 account_level 更高的平台身份，任何非超管都不能借建账号把自己提权。
	if in.IsSuperAdmin && !canGrantSuperAdmin(caller) {
		return contract.User{}, contract.ErrForbidden
	}

	hash, err := loginsvc.HashPassword(in.Password)
	if err != nil {
		return contract.User{}, err
	}

	// 归属固定为调用者所属公司：平台管理员与团队管理员都只能在本公司建账号。
	record, err := s.repo.Create(ctx, username, hash, caller.Mode, strings.TrimSpace(in.PersonID),
		in.IsSuperAdmin, level, caller.TenantID, teamIDs)
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

// ensureInScope 校验目标账号在当前调用者的可见范围内。
// 超管看全部；公司账号只能碰本公司账号，跨公司按「不存在」处理，不泄漏对方是否有这个 id。
func ensureInScope(caller contract.Identity, target userrepo.Record) error {
	if caller.Level() == contract.AccountLevelSuperAdmin {
		return nil
	}
	if caller.TenantID == "" || target.TenantID != caller.TenantID {
		return errAccountNotFound
	}
	return nil
}

// Update 改绑定人员、角色与超管标记；账号名与模式不可改。
func (s *Service) Update(ctx context.Context, caller contract.Identity, id string, in contract.UserUpdate) (contract.User, error) {
	current, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return contract.User{}, err
	}
	if err := ensureInScope(caller, current); err != nil {
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

	record, err := s.repo.Update(ctx, id, strings.TrimSpace(in.PersonID), in.IsSuperAdmin, strings.TrimSpace(in.RoleKey))
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
	if err := ensureInScope(caller, record); err != nil {
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
	if err := ensureInScope(caller, current); err != nil {
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
	if err := ensureInScope(caller, current); err != nil {
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
	if err := ensureInScope(caller, current); err != nil {
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
