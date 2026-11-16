// Package teams · 团队的业务层。
// 职责：列出团队、新建与编辑团队（含关联人员 / 项目的多选结果）。
// 输入：repository/teams 的读写能力、contract 的字段上限。
// 输出：contract.TeamList / Team。
// 边界：不碰 HTTP、不做权限判断、不拼 SQL。
package teams

import (
	"context"
	"strings"
	"unicode/utf8"

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

// List 返回全部团队，已带上关联的人员与项目。
func (s *Service) List(ctx context.Context) (contract.TeamList, error) {
	items, err := s.repo.List(ctx)
	if err != nil {
		return contract.TeamList{}, err
	}
	return contract.TeamList{Items: items}, nil
}

// Get 按 id 取单个团队（含关联），供编辑页回填。
// id 不是 UUID 时直接按 404 处理：否则会被绑定到 uuid 列报 SQL 错误而变成 500。
func (s *Service) Get(ctx context.Context, id string) (contract.Team, error) {
	if !contract.IsValidUUID(id) {
		return contract.Team{}, contract.ErrNotFound
	}
	return s.repo.FindByID(ctx, id)
}

// Create 新建团队。
func (s *Service) Create(ctx context.Context, in contract.TeamInput) (contract.Team, error) {
	normalized, err := normalize(in)
	if err != nil {
		return contract.Team{}, err
	}
	item, err := s.repo.Create(ctx, normalized)
	if err != nil {
		return contract.Team{}, translate(err)
	}
	return item, nil
}

// Update 全量覆盖团队字段与关联。
func (s *Service) Update(ctx context.Context, id string, in contract.TeamInput) (contract.Team, error) {
	normalized, err := normalize(in)
	if err != nil {
		return contract.Team{}, err
	}
	item, err := s.repo.Update(ctx, id, normalized)
	if err != nil {
		return contract.Team{}, translate(err)
	}
	return item, nil
}

// translate 把外键冲突翻译成可上屏的中文提示。
func translate(err error) error {
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
