// Package teams · 团队的业务层。
// 职责：列出团队、新建与编辑团队（含关联人员 / 项目的多选结果）。
// 输入：repository/teams 的读写能力、contract 的字段上限。
// 输出：contract.TeamList / Team。
// 边界：不碰 HTTP、不做权限判断、不拼 SQL。
package teams

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
	teamsrepo "dandelion/server/internal/repository/teams"
)

// Service 是团队模块的唯一业务入口。
type Service struct {
	repo *teamsrepo.Repository
}

func NewService(repo *teamsrepo.Repository) *Service {
	return &Service{repo: repo}
}

// List 返回指定页的团队（含关联名字）与总数；page.PageSize=0 时返回全量。
// scope 从 ctx 取（InjectScope 注入），仓储据此只返回本公司团队。
func (s *Service) List(ctx context.Context, page contract.PageQuery) (contract.TeamList, error) {
	items, total, err := s.repo.List(ctx, page, authctx.ScopeFrom(ctx))
	if err != nil {
		return contract.TeamList{}, err
	}
	return contract.TeamList{
		Items: items,
		Page:  contract.Page{Page: page.Page, PageSize: page.PageSize, Total: total},
	}, nil
}

// Get 按 id 取单个团队（含关联），供编辑页回填。
// id 不是 UUID 时直接按 404 处理：否则会被绑定到 uuid 列报 SQL 错误而变成 500。
func (s *Service) Get(ctx context.Context, id string) (contract.Team, error) {
	if !contract.IsValidUUID(id) {
		return contract.Team{}, contract.NotFound(contract.ResourceTeam)
	}
	return s.repo.FindByID(ctx, id, authctx.ScopeFrom(ctx))
}

// Create 新建团队。
func (s *Service) Create(ctx context.Context, in contract.TeamInput) (contract.Team, error) {
	normalized, err := normalize(in)
	if err != nil {
		return contract.Team{}, err
	}
	item, err := s.repo.Create(ctx, normalized, authctx.ScopeFrom(ctx))
	if err != nil {
		return contract.Team{}, constraintConflictToMessage(err)
	}
	return item, nil
}

// Update 全量覆盖团队字段与关联。
func (s *Service) Update(ctx context.Context, id string, in contract.TeamInput) (contract.Team, error) {
	normalized, err := normalize(in)
	if err != nil {
		return contract.Team{}, err
	}
	item, err := s.repo.Update(ctx, id, normalized, authctx.ScopeFrom(ctx))
	if err != nil {
		return contract.Team{}, constraintConflictToMessage(err)
	}
	return item, nil
}

// Delete 删除团队。
//
// 只允许删**没有任何引用**的团队：还有成员、还关联项目、还挂着管理员账号时一律拒绝。
// 拒绝而不是级联，是因为这三种关系都代表"有人正在用它"——静默断开会悄悄改变
// 别人的可见范围（团队成员会突然什么都看不到），而界面上看不出发生过什么。
func (s *Service) Delete(ctx context.Context, id string) error {
	// 先按 scope 取一次：既确认存在且属于本公司（越界得 404，而非 400），
	// 也让引用检查只针对真实存在的记录。
	if _, err := s.Get(ctx, id); err != nil {
		return err
	}
	usage, err := s.repo.CountUsage(ctx, id)
	if err != nil {
		return err
	}
	if !usage.Empty() {
		return &contract.ValidationError{Message: describeTeamUsage(usage)}
	}
	return constraintConflictToMessage(s.repo.Delete(ctx, id, authctx.ScopeFrom(ctx)))
}

// describeTeamUsage 把引用统计翻成一句能指导操作的话。
func describeTeamUsage(usage teamsrepo.Usage) string {
	parts := make([]string, 0, 3)
	if usage.Persons > 0 {
		parts = append(parts, fmt.Sprintf("有 %d 名成员", usage.Persons))
	}
	if usage.Projects > 0 {
		parts = append(parts, fmt.Sprintf("关联 %d 个项目", usage.Projects))
	}
	if usage.Admins > 0 {
		parts = append(parts, fmt.Sprintf("有 %d 个管理员", usage.Admins))
	}
	return "该团队" + strings.Join(parts, "、") + "，请先解除关联后再删除"
}

// constraintConflictToMessage 把团队的外键冲突翻译成可上屏的 400 提示。
//
// 删除前的引用检查已在 service 里给出更具体的说明（"该团队还被谁用着"），
// 这里只兜底并发删除等漏网情况，提示关联对象不存在。
func constraintConflictToMessage(err error) error {
	if repository.IsForeignKeyViolation(err) {
		return &contract.ValidationError{Message: "关联的人员或项目不存在，请刷新后重试"}
	}
	return err
}

// normalize 收敛字段：去空白、校验长度、去重关联 ID。
func normalize(in contract.TeamInput) (contract.TeamInput, error) {
	out := contract.TeamInput{
		Name:        strings.TrimSpace(in.Name),
		Description: strings.TrimSpace(in.Description),
		PersonIDs:   dedupe(in.PersonIDs),
		ProjectIDs:  dedupe(in.ProjectIDs),
	}

	if out.Name == "" {
		return contract.TeamInput{}, &contract.ValidationError{Message: "请填写团队名称"}
	}
	if utf8.RuneCountInString(out.Name) > contract.MaxTeamNameLen {
		return contract.TeamInput{}, &contract.ValidationError{Message: "团队名称不能超过 60 个字符"}
	}
	if utf8.RuneCountInString(out.Description) > contract.MaxTeamDescLen {
		return contract.TeamInput{}, &contract.ValidationError{Message: "团队描述不能超过 200 个字符"}
	}
	if len(out.PersonIDs) > contract.MaxRelationCount || len(out.ProjectIDs) > contract.MaxRelationCount {
		return contract.TeamInput{}, &contract.ValidationError{Message: "关联数量过多"}
	}
	return out, nil
}

// dedupe 去重并保持勾选顺序。
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
