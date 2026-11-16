// Package people · 人员的业务层。
// 职责：列出人员、新建与编辑人员（含关联团队 / 项目的多选结果）、提供「当前登录人」投影。
// 输入：repository/people 的读写能力、contract 的字段上限。
// 输出：contract.PersonList / Person / Me。
// 边界：不碰 HTTP、不做权限判断（P2 才用 person 建账号与角色）、不拼 SQL。
package people

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
	peoplerepo "dandelion/server/internal/repository/people"
)

// Service 是人员模块的唯一业务入口。
type Service struct {
	repo *peoplerepo.Repository
}

func NewService(repo *peoplerepo.Repository) *Service {
	return &Service{repo: repo}
}

// List 返回指定页的人员（含关联名字）与总数；page.PageSize=0 时返回全量。
func (s *Service) List(ctx context.Context, page contract.PageQuery) (contract.PersonList, error) {
	items, total, err := s.repo.List(ctx, page)
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
		return contract.Person{}, contract.ErrNotFound
	}
	return s.repo.FindByID(ctx, id)
}

// Create 新建人员。
func (s *Service) Create(ctx context.Context, in contract.PersonInput) (contract.Person, error) {
	normalized, err := normalize(in)
	if err != nil {
		return contract.Person{}, err
	}
	item, err := s.repo.Create(ctx, normalized)
	if err != nil {
		return contract.Person{}, translate(err)
	}
	return item, nil
}

// Update 全量覆盖人员字段与关联。
func (s *Service) Update(ctx context.Context, id string, in contract.PersonInput) (contract.Person, error) {
	normalized, err := normalize(in)
	if err != nil {
		return contract.Person{}, err
	}
	item, err := s.repo.Update(ctx, id, normalized)
	if err != nil {
		return contract.Person{}, translate(err)
	}
	return item, nil
}

// Me 返回当前登录人的档案投影。
// 登录账号还没有对应人员时不算错误：返回空档案，前端据此回退到「第一个项目」。
// isSuperAdmin 由鉴权中间件判定的身份传入，前端只认这一个布尔值。
func (s *Service) Me(ctx context.Context, username string, isSuperAdmin bool) (contract.Me, error) {
	person, err := s.repo.FindByUsername(ctx, username)
	if errors.Is(err, contract.ErrNotFound) {
		return contract.Me{Username: username, ProjectKeys: []string{}, IsSuperAdmin: isSuperAdmin}, nil
	}
	if err != nil {
		return contract.Me{}, err
	}
	keys, err := s.repo.ListProjectKeys(ctx, person.ID)
	if err != nil {
		return contract.Me{}, err
	}
	return contract.Me{
		Username: username, PersonID: person.ID, Name: person.Name,
		ProjectKeys: keys, IsSuperAdmin: isSuperAdmin,
	}, nil
}

// translate 把数据库约束冲突翻译成可上屏的中文提示。
func translate(err error) error {
	switch {
	case repository.IsUniqueViolation(err):
		return &contract.ValidationError{Message: "登录名已被其他人员占用"}
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
