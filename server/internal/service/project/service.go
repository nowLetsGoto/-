// Package project · 项目的业务层。
// 职责：列出项目、查看详情（含关联人员/团队）、新建与编辑项目（含库地址）。
// 输入：repository/project 的分区读写能力、contract 的字段上限与 URL 校验。
// 输出：contract.ProjectList / Project / ProjectDetail。
// 边界：不碰 HTTP、不做权限判断（P2 再做成员角色）、不拼 SQL；
// 分表由仓储层承担，这里只负责"用哪个分区"与"单人视图该裁掉什么"。
package project

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/authctx"
	"dandelion/server/internal/repository"
	projectrepo "dandelion/server/internal/repository/project"
)

// View 描述调用者的项目视图。
// Solo 为真时隐藏角色与成员信息（单人模式没有角色区分）；
// OwnerUserID 非空时只看该账号自己的项目，非空与 Solo 同时成立是常态。
type View struct {
	Solo        bool
	OwnerUserID string
	Mode        contract.SessionMode
}

// Service 是项目模块的唯一业务入口。
type Service struct {
	repos repository.Partitioned[*projectrepo.Repository]
}

func NewService(repos repository.Partitioned[*projectrepo.Repository]) *Service {
	return &Service{repos: repos}
}

// List 返回指定页的项目与总数；下拉筛选传 pageSize=0 取全量。
// 单人视图会清空角色字段；团队视图保持全量（含角色名）。
func (s *Service) List(ctx context.Context, page contract.PageQuery, view View) (contract.ProjectList, error) {
	scope := authctx.ScopeFrom(ctx)
	items, total, err := s.repos.For(view.Mode).List(ctx, page, view.OwnerUserID, scope)
	if err != nil {
		return contract.ProjectList{}, err
	}
	if view.Solo {
		hideProjectMeta(items)
	}
	return contract.ProjectList{
		Items: items,
		Page:  contract.Page{Page: page.Page, PageSize: page.PageSize, Total: total},
	}, nil
}

// Detail 返回项目详情：团队视图带关联人员/团队；单人视图只返回项目基础信息。
func (s *Service) Detail(ctx context.Context, key string, view View) (contract.ProjectDetail, error) {
	repo := s.repos.For(view.Mode)
	// 单人分区的 owner 过滤由仓储层强制执行：查不到别人的项目，天然就是 404。
	item, err := repo.FindByKey(ctx, key, view.OwnerUserID, authctx.ScopeFrom(ctx))
	if err != nil {
		return contract.ProjectDetail{}, err
	}
	if view.Solo {
		item.RoleKey = ""
		item.RoleName = ""
		return contract.ProjectDetail{Project: item, Persons: []contract.RelationRef{}, Teams: []contract.RelationRef{}}, nil
	}
	persons, err := repo.ListPersons(ctx, item.ID)
	if err != nil {
		return contract.ProjectDetail{}, err
	}
	teams, err := repo.ListTeams(ctx, item.ID)
	if err != nil {
		return contract.ProjectDetail{}, err
	}
	return contract.ProjectDetail{Project: item, Persons: persons, Teams: teams}, nil
}

// Create 新建项目：key 在所属分区内唯一且创建后不可改（它是 URL 段）。
// 单人模式允许自建：owner 记为自己，key 只需在本人空间内唯一。
func (s *Service) Create(ctx context.Context, in contract.ProjectInput, view View) (contract.Project, error) {
	normalized, err := normalize(in, true)
	if err != nil {
		return contract.Project{}, err
	}
	// 单人模式没有项目角色，强制清空：避免表单残留值触发角色外键或越权。
	if view.Solo {
		normalized.RoleKey = ""
	}
	item, err := s.repos.For(view.Mode).Create(ctx, normalized, view.OwnerUserID, authctx.ScopeFrom(ctx))
	if err != nil {
		if repository.IsUniqueViolation(err) {
			return contract.Project{}, &contract.ValidationError{Message: "本公司内已有相同标识的项目，换一个试试"}
		}
		if repository.IsForeignKeyViolation(err) {
			return contract.Project{}, &contract.ValidationError{Message: "项目角色不存在，请刷新后重试"}
		}
		return contract.Project{}, err
	}
	return item, nil
}

// Update 按 key 编辑名称 / 描述 / 库地址；key 不在可编辑范围内。
func (s *Service) Update(ctx context.Context, key string, in contract.ProjectInput, view View) (contract.Project, error) {
	normalized, err := normalize(in, false)
	if err != nil {
		return contract.Project{}, err
	}
	if view.Solo {
		normalized.RoleKey = ""
	}
	item, updateErr := s.repos.For(view.Mode).Update(ctx, key, view.OwnerUserID, normalized, authctx.ScopeFrom(ctx))
	if updateErr != nil {
		if repository.IsForeignKeyViolation(updateErr) {
			return contract.Project{}, &contract.ValidationError{Message: "项目角色不存在，请刷新后重试"}
		}
		return contract.Project{}, updateErr
	}
	return item, nil
}

// Delete 按 key 删除项目。
//
// 只允许删**没有任何引用**的项目：下面还有包、还有成员、还被团队关联时一律拒绝。
// 包尤其不能级联——它是不可恢复的资产（对应磁盘文件），静默删掉等于丢数据；
// 成员与团队关系则代表"有人正在用"，删掉会悄悄改变别人的可见范围。
//
// 拒绝返回 ValidationError（400）：调用者有删除权限，只是当前状态不允许，
// 前端据此提示"先去清理"，而不是误以为权限不足。
func (s *Service) Delete(ctx context.Context, key string, view View) error {
	repo := s.repos.For(view.Mode)
	scope := authctx.ScopeFrom(ctx)

	usage, err := repo.CountUsage(ctx, key, view.OwnerUserID, scope)
	if err != nil {
		return err
	}
	if !usage.Empty() {
		return &contract.ValidationError{Message: describeProjectUsage(usage)}
	}
	return repo.Delete(ctx, key, view.OwnerUserID, scope)
}

// describeProjectUsage 把引用统计翻成一句能指导操作的话。
func describeProjectUsage(usage projectrepo.Usage) string {
	parts := make([]string, 0, 3)
	if usage.Builds > 0 {
		parts = append(parts, fmt.Sprintf("有 %d 个应用包", usage.Builds))
	}
	if usage.Members > 0 {
		parts = append(parts, fmt.Sprintf("有 %d 名成员", usage.Members))
	}
	if usage.Teams > 0 {
		parts = append(parts, fmt.Sprintf("被 %d 个团队关联", usage.Teams))
	}
	return "该项目" + strings.Join(parts, "、") + "，请先清理后再删除"
}

// hideProjectMeta 清空单人视图不应展示的角色字段，避免列表响应里仍带着敏感配置。
func hideProjectMeta(items []contract.Project) {
	for i := range items {
		items[i].RoleKey = ""
		items[i].RoleName = ""
	}
}

// normalize 收敛字段并校验；withKey 为 false 时忽略 key（编辑场景）。
func normalize(in contract.ProjectInput, withKey bool) (contract.ProjectInput, error) {
	out := contract.ProjectInput{
		Key:         strings.ToLower(strings.TrimSpace(in.Key)),
		Name:        strings.TrimSpace(in.Name),
		Description: strings.TrimSpace(in.Description),
		IconURL:     strings.TrimSpace(in.IconURL),
		RoleKey:     strings.TrimSpace(in.RoleKey),
	}

	if withKey && !contract.IsValidProjectKey(out.Key) {
		return contract.ProjectInput{}, &contract.ValidationError{
			Message: "项目标识只能用小写字母、数字与短横线，2-32 位且以字母或数字开头",
		}
	}
	if out.Name == "" {
		return contract.ProjectInput{}, &contract.ValidationError{Message: "请填写项目名称"}
	}
	if utf8.RuneCountInString(out.Name) > contract.MaxProjectNameLen {
		return contract.ProjectInput{}, &contract.ValidationError{Message: "项目名称不能超过 60 个字符"}
	}
	if utf8.RuneCountInString(out.Description) > contract.MaxProjectDescLen {
		return contract.ProjectInput{}, &contract.ValidationError{Message: "项目描述不能超过 200 个字符"}
	}

	if trimmed := strings.TrimSpace(in.RepoURL); trimmed != "" {
		if len(trimmed) > contract.MaxRepoURLLen {
			return contract.ProjectInput{}, &contract.ValidationError{Message: "项目库地址不能超过 300 个字符"}
		}
		normalized, err := contract.NormalizeHTTPURL(trimmed)
		if err != nil {
			return contract.ProjectInput{}, &contract.ValidationError{Message: "项目库地址" + err.Error()}
		}
		out.RepoURL = normalized
	}

	if out.IconURL != "" {
		if len(out.IconURL) > contract.MaxProjectIconURLLen ||
			!strings.HasPrefix(out.IconURL, "data:image/") ||
			!strings.Contains(out.IconURL, ";base64,") {
			return contract.ProjectInput{}, &contract.ValidationError{Message: "项目图标格式不正确或过大"}
		}
	}
	return out, nil
}
