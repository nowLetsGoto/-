// Package user · 账号管理的业务层（仅超级管理员可用）。
// 职责：列表 / 新建 / 编辑（超管标记与绑定人员）/ 重置密码 / 启用禁用，并守住三条自锁红线。
// 输入：repository/user、登录模块的密码派生、调用者身份（由中间件保证已是超管）。
// 输出：contract.User / UserList，失败给 ValidationError（400）或 ErrForbidden（403）。
// 边界：不碰 HTTP、不写审计（审计在 P2.2 接）、不判断"是不是超管"——那是中间件的职责；
//
//	这里只做业务护栏：不能禁用自己、不能取消自己超管、不能弄丢最后一个超管。
package user

import (
	"context"
	"crypto/rand"
	"math/big"
	"regexp"
	"strings"
	"unicode/utf8"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
	userrepo "dandelion/server/internal/repository/user"
	loginsvc "dandelion/server/internal/service/login"
)

const (
	statusActive   = "active"
	statusDisabled = "disabled"

	minPasswordLen = 8
	maxPasswordLen = 64
)

// 账号名会进令牌载荷（用 | 分段）与 URL，因此只允许安全字符。
var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{3,32}$`)

// tempPasswordAlphabet 去掉了容易看错的 0/O/1/l/I。
const tempPasswordAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// Service 是账号管理的唯一业务入口。
type Service struct {
	repo *userrepo.Repository
}

func NewService(repo *userrepo.Repository) *Service {
	return &Service{repo: repo}
}

// List 返回指定页的账号；keyword 同时匹配登录名与绑定人员姓名。
func (s *Service) List(ctx context.Context, page contract.PageQuery, keyword string) (contract.UserList, error) {
	items, total, err := s.repo.List(ctx, page, keyword)
	if err != nil {
		return contract.UserList{}, err
	}
	return contract.UserList{
		Items: items,
		Page:  contract.Page{Page: page.Page, PageSize: page.PageSize, Total: total},
	}, nil
}

// Create 新建账号：账号名校验 → 密码派生 → 落库。
func (s *Service) Create(ctx context.Context, in contract.UserInput) (contract.User, error) {
	username := strings.TrimSpace(in.Username)
	if !usernamePattern.MatchString(username) {
		return contract.User{}, &contract.ValidationError{
			Message: "账号名只能用字母、数字、点、下划线、短横线，3-32 位",
		}
	}
	if err := validatePassword(in.Password); err != nil {
		return contract.User{}, err
	}

	hash, err := loginsvc.HashPassword(in.Password)
	if err != nil {
		return contract.User{}, err
	}

	record, err := s.repo.Create(ctx, username, hash, strings.TrimSpace(in.PersonID), in.IsSuperAdmin)
	if err != nil {
		if repository.IsUniqueViolation(err) {
			return contract.User{}, &contract.ValidationError{Message: "该账号名已存在"}
		}
		if repository.IsForeignKeyViolation(err) {
			return contract.User{}, &contract.ValidationError{Message: "绑定的人员不存在，请刷新后重试"}
		}
		return contract.User{}, err
	}
	return record.User, nil
}

// Update 改超管标记与绑定人员；账号名不可改。
func (s *Service) Update(ctx context.Context, caller contract.Identity, id string, in contract.UserUpdate) (contract.User, error) {
	current, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return contract.User{}, err
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

	record, err := s.repo.Update(ctx, id, strings.TrimSpace(in.PersonID), in.IsSuperAdmin)
	if err != nil {
		if repository.IsForeignKeyViolation(err) {
			return contract.User{}, &contract.ValidationError{Message: "绑定的人员不存在，请刷新后重试"}
		}
		return contract.User{}, err
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
func (s *Service) ResetPassword(ctx context.Context, id string) (string, error) {
	if _, err := s.repo.FindByID(ctx, id); err != nil {
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
	if utf8.RuneCountInString(password) < minPasswordLen || utf8.RuneCountInString(password) > maxPasswordLen {
		return &contract.ValidationError{Message: "密码长度需在 8-64 位之间"}
	}
	return nil
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
