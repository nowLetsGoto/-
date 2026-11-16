// Package audit · 审计日志的业务层。
// 职责：把「谁、从哪、对什么、做了什么、结果如何」组装成一条记录；提供分页查询。
// 输入：repository/audit、调用者身份、请求来源 IP。
// 输出：写入无返回（失败只记日志，绝不影响主流程）；查询返回列表与总数。
// 边界：不做权限判断（/admin/audit-logs 由中间件守）；不改历史记录。
package audit

import (
	"context"
	"encoding/json"
	"log"

	"dandelion/server/internal/contract"
	auditrepo "dandelion/server/internal/repository/audit"
)

// 结果只有两种：成功与失败。失败要带原因，写在 detail 里。
const (
	ResultSuccess = "success"
	ResultFailure = "failure"
)

// Service 是审计模块的唯一入口。
type Service struct {
	repo *auditrepo.Repository
}

func NewService(repo *auditrepo.Repository) *Service {
	return &Service{repo: repo}
}

// Record 写一条审计。**永远不返回错误**：审计失败不该让业务操作回滚或报错，
// 但会在服务端日志里留下痕迹，便于发现"日志写不进去"这种系统性问题。
func (s *Service) Record(ctx context.Context, actor contract.Identity, action, objectType, objectID string, detail map[string]any, ip string) {
	payload, err := json.Marshal(detail)
	if err != nil {
		log.Printf("[audit] 序列化 detail 失败 action=%s err=%v", action, err)
		payload = []byte("{}")
	}
	if err := s.repo.Create(ctx, auditrepo.Entry{
		ActorID:    actor.UserID,
		ActorName:  actor.Username,
		Action:     action,
		ObjectType: objectType,
		ObjectID:   objectID,
		Result:     ResultSuccess,
		DetailJSON: string(payload),
		IP:         ip,
	}); err != nil {
		log.Printf("[audit] 写入失败 action=%s actor=%s err=%v", action, actor.Username, err)
	}
}

// List 分页查询审计日志。
func (s *Service) List(ctx context.Context, page contract.PageQuery, filters contract.AuditQuery) (contract.AuditLogList, error) {
	items, total, err := s.repo.List(ctx, page, filters)
	if err != nil {
		return contract.AuditLogList{}, err
	}
	return contract.AuditLogList{
		Items: items,
		Page:  contract.Page{Page: page.Page, PageSize: page.PageSize, Total: total},
	}, nil
}
