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
	"fmt"
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

// 公司状态的两种取值；与 tenants.status 的默认值一致。
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// SetStatus 启用 / 停用公司。
//
// 停用的效果由登录链路保证：Authenticate 会校验账号所属公司的状态，
// 因此停用后该公司所有账号立即无法登录（已签发的令牌在下次请求时也会被拒，
// 因为 Verify 每次都会重建身份并走到同一处判定）。
//
// 这里只接受两个合法值，不做"任意字符串透传"——status 是枚举，
// 让它自由取值只会在库里积累出无法解释的状态。
func (s *Service) SetStatus(ctx context.Context, id, status string) (contract.Tenant, error) {
	if status != StatusActive && status != StatusDisabled {
		return contract.Tenant{}, &contract.ValidationError{Message: "公司状态只能是启用或停用"}
	}
	return s.repo.SetStatus(ctx, id, status)
}

// Delete 删除公司。
//
// 只允许删**空公司**（没有人员/团队/项目/账号/自定义角色）。
// 这是刻意的保守选择：公司是数据隔离的最高边界，级联删掉它意味着
// 一次性丢弃一整套业务数据与磁盘上的包文件，没有任何撤销手段。
// 需要清空时由人一步步删——慢一点，但每一步都看得见。
func (s *Service) Delete(ctx context.Context, id string) error {
	usage, err := s.repo.CountUsage(ctx, id)
	if err != nil {
		return err
	}
	if !usage.Empty() {
		return &contract.ValidationError{Message: describeTenantUsage(usage)}
	}
	return s.repo.Delete(ctx, id)
}

// describeTenantUsage 把数据量翻成一句能指导操作的话。
func describeTenantUsage(usage tenantrepo.Usage) string {
	parts := make([]string, 0, 5)
	if usage.Members > 0 {
		parts = append(parts, fmt.Sprintf("%d 名人员", usage.Members))
	}
	if usage.Teams > 0 {
		parts = append(parts, fmt.Sprintf("%d 个团队", usage.Teams))
	}
	if usage.Projects > 0 {
		parts = append(parts, fmt.Sprintf("%d 个项目", usage.Projects))
	}
	if usage.Accounts > 0 {
		parts = append(parts, fmt.Sprintf("%d 个账号", usage.Accounts))
	}
	if usage.Roles > 0 {
		parts = append(parts, fmt.Sprintf("%d 个自定义角色", usage.Roles))
	}
	return "该公司还有" + strings.Join(parts, "、") + "，请先清空后再删除"
}

// accountPattern 与 service/user 的账号名规则保持一致：账号名会进令牌，必须是安全字符。
var accountPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{3,32}$`)
