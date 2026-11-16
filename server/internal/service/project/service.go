// Package project · 项目的业务层。
// 职责：列出项目、查看详情（含关联人员/团队）、新建与编辑项目（含库地址）。
// 输入：repository/project 的读写能力、contract 的字段上限与 URL 校验。
// 输出：contract.ProjectList / Project / ProjectDetail。
// 边界：不碰 HTTP、不做权限判断（P2 再做成员角色）、不拼 SQL。
package project

import (
	"context"
	"strings"
	"unicode/utf8"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
	projectrepo "dandelion/server/internal/repository/project"
)

// Service 是项目模块的唯一业务入口。
type Service struct {
	repo *projectrepo.Repository
}

func NewService(repo *projectrepo.Repository) *Service {
	return &Service{repo: repo}
}

// List 返回全部项目，供下拉筛选、人员/团队关联选择使用。
func (s *Service) List(ctx context.Context) (contract.ProjectList, error) {
	items, err := s.repo.List(ctx)
	if err != nil {
		return contract.ProjectList{}, err
	}
	return contract.ProjectList{Items: items}, nil
}

// Detail 返回项目详情：项目本身 + 关联的人员与团队（带名字）。
func (s *Service) Detail(ctx context.Context, key string) (contract.ProjectDetail, error) {
	item, err := s.repo.FindByKey(ctx, key)
	if err != nil {
		return contract.ProjectDetail{}, err
	}
	persons, err := s.repo.ListPersons(ctx, item.ID)
	if err != nil {
		return contract.ProjectDetail{}, err
	}
	teams, err := s.repo.ListTeams(ctx, item.ID)
	if err != nil {
		return contract.ProjectDetail{}, err
	}
	return contract.ProjectDetail{Project: item, Persons: persons, Teams: teams}, nil
}

// Create 新建项目：key 全仓唯一且创建后不可改（它是 URL 段）。
func (s *Service) Create(ctx context.Context, in contract.ProjectInput) (contract.Project, error) {
	normalized, err := normalize(in, true)
	if err != nil {
		return contract.Project{}, err
	}
	item, err := s.repo.Create(ctx, normalized)
	if err != nil {
		if repository.IsUniqueViolation(err) {
			return contract.Project{}, &contract.ValidationError{Message: "项目标识已被占用，换一个试试"}
		}
		return contract.Project{}, err
	}
	return item, nil
}

// Update 按 key 编辑名称 / 描述 / 库地址；key 不在可编辑范围内。
func (s *Service) Update(ctx context.Context, key string, in contract.ProjectInput) (contract.Project, error) {
	normalized, err := normalize(in, false)
	if err != nil {
		return contract.Project{}, err
	}
	return s.repo.Update(ctx, key, normalized)
}

// normalize 收敛字段并校验；withKey 为 false 时忽略 key（编辑场景）。
func normalize(in contract.ProjectInput, withKey bool) (contract.ProjectInput, error) {
	out := contract.ProjectInput{
		Key:         strings.ToLower(strings.TrimSpace(in.Key)),
		Name:        strings.TrimSpace(in.Name),
		Description: strings.TrimSpace(in.Description),
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
	return out, nil
}
