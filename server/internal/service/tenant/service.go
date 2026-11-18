// Package tenant · 公司管理的业务层（仅平台超级管理员可用）。
// 职责：新建公司（连同其唯一平台管理员账号）、分页列出公司。
// 输入：repository/tenant、登录模块的密码派生、contract 字段规则。
// 输出：contract.Tenant / TenantList / TenantResult（含一次性明文密码）。
// 边界：不碰 HTTP、不写审计（在 handler）、不判断超管身份（路由层 RequireMinLevel）；
//
//	公司的「建」与「平台管理员的建」是一个原子动作：分开做会留下进不去的孤儿公司。
package tenant

import (
	"context"
	"regexp"
	"strings"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
	tenantrepo "dandelion/server/internal/repository/tenant"
	loginsvc "dandelion/server/internal/service/login"
)

// 公司标识进 URL 与对外展示，限定为小写字母 / 数字 / 短横线，避免特殊字符与歧义。
var codePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)

// Service 是公司模块的唯一业务入口。
type Service struct {
	repo *tenantrepo.Repository
}

func NewService(repo *tenantrepo.Repository) *Service {
	return &Service{repo: repo}
}

// List 分页返回公司列表，供超管的平台视角查看。
func (s *Service) List(ctx context.Context, page contract.PageQuery) (contract.TenantList, error) {
	items, total, err := s.repo.List(ctx, page)
	if err != nil {
		return contract.TenantList{}, err
	}
	return contract.TenantList{
		Items: items,
		Page:  contract.Page{Page: page.Page, PageSize: page.PageSize, Total: total},
	}, nil
}

// Create 新建公司并创建其平台管理员账号。
// 明文密码只在返回的 TenantResult 里出现一次，之后只能走重置密码。
func (s *Service) Create(ctx context.Context, in contract.TenantInput) (contract.TenantResult, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Code = strings.ToLower(strings.TrimSpace(in.Code))
	in.AdminUsername = strings.TrimSpace(in.AdminUsername)

	if in.Name == "" {
		return contract.TenantResult{}, &contract.ValidationError{Message: "请填写公司名称"}
	}
	if !codePattern.MatchString(in.Code) {
		return contract.TenantResult{}, &contract.ValidationError{
			Message: "公司标识只能用小写字母、数字与短横线，2-32 位且以字母数字开头",
		}
	}
	if !accountPattern.MatchString(in.AdminUsername) {
		return contract.TenantResult{}, &contract.ValidationError{
			Message: "管理员账号名只能用字母、数字、点、下划线、短横线，3-32 位",
		}
	}
	if err := contract.ValidatePassword(in.AdminPassword); err != nil {
		return contract.TenantResult{}, err
	}

	hash, err := loginsvc.HashPassword(in.AdminPassword)
	if err != nil {
		return contract.TenantResult{}, err
	}

	item, err := s.repo.CreateWithAdmin(ctx, in, hash)
	if err != nil {
		if repository.IsUniqueViolation(err) {
			return contract.TenantResult{}, &contract.ValidationError{Message: "公司名称、标识或管理员账号名已被占用"}
		}
		return contract.TenantResult{}, err
	}
	return contract.TenantResult{Tenant: item, Password: in.AdminPassword}, nil
}

// accountPattern 与 service/user 的账号名规则保持一致：账号名会进令牌，必须是安全字符。
var accountPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{3,32}$`)
