// Package people · 人员接口（含「当前登录人」投影）。
// 职责：列出人员、新建 / 编辑人员、返回 GET /me 的档案投影。
// 输入：HTTP 请求（GET / POST / PUT JSON）；输出：统一 JSON 响应。
// 边界：不做校验（在 service/people）、不判断权限；
//
//	/me 放在这里是因为它的数据源就是人员表，再开一个包只为转发一次不值得。
package people

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	auditsvc "dandelion/server/internal/service/audit"
	authzsvc "dandelion/server/internal/service/authz"
	peoplesvc "dandelion/server/internal/service/people"
)

// Handler 依赖人员业务层、授权服务（仅 /me）与审计。
// 审计是"谁改了人员档案"的唯一痕迹：人员变动直接影响权限归属，
// 公司管理员查审计时最常看的就是这一类。
type Handler struct {
	svc *peoplesvc.Service
	// authz 只用于 /me：把"全局权限集合"一并返回，前端据此裁剪菜单与按钮。
	authz *authzsvc.Service
	// audit 记录增删改；写入失败只记日志，不影响业务（见 service/audit.Record）。
	audit *auditsvc.Service
}

func NewHandler(svc *peoplesvc.Service, authz *authzsvc.Service, audit *auditsvc.Service) *Handler {
	return &Handler{svc: svc, authz: authz, audit: audit}
}

// List 返回全部人员，已带关联的团队与项目。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	data, err := h.svc.List(r.Context(), handler.ReadPage(r))
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// Get 返回单个人员（含关联），供编辑页回填。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	item, err := h.svc.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	return handler.OK(w, item)
}

// Create 新建人员；请求体 7 个字段（含两个多选 ID 数组）。
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	var in contract.PersonInput
	if err := handler.DecodeJSON(w, r, &in, "name/username/email/phone/title/teamIds/projectIds"); err != nil {
		return err
	}
	// 授权字段独立于普通资料编辑：directory.write 不等于可分配角色。
	// 分配角色是管理动作：由身份层级把关（团队管理员及以上），不再要求超管。
	// 具体能分配哪些角色键由 service 校验（必须是本公司可见、且不属于越权角色）。
	if in.RoleKey != "" && !canAssignRole(authctx.From(r.Context())) {
		return contract.ErrForbidden
	}
	item, err := h.svc.Create(r.Context(), in)
	if err != nil {
		return err
	}
	h.audit.Record(r.Context(), authctx.From(r.Context()), "person.created", "person", item.ID,
		map[string]any{"name": item.Name, "username": item.Username, "roleKey": item.RoleKey},
		handler.ClientIP(r))
	return handler.OKWithStatus(w, http.StatusCreated, item)
}

// Update 全量覆盖人员字段与关联。
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	var in contract.PersonInput
	if err := handler.DecodeJSON(w, r, &in, "name/username/email/phone/title/teamIds/projectIds"); err != nil {
		return err
	}
	if !authctx.From(r.Context()).IsSuperAdmin {
		current, err := h.svc.Get(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		// 登录名参与账号绑定，非超管不得借更换登录名接管权限——这条红线不变。
		if in.Username != current.Username {
			return contract.ErrForbidden
		}
		// 角色可以改，但改角色本身是管理动作，需要管理层级。
		if in.RoleKey != current.RoleKey && !canAssignRole(authctx.From(r.Context())) {
			return contract.ErrForbidden
		}
	}
	item, err := h.svc.Update(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		return err
	}
	// 记下角色键：改角色是本页影响权限最大的动作，事后必须能查到"谁把谁提成了什么"。
	h.audit.Record(r.Context(), authctx.From(r.Context()), "person.updated", "person", item.ID,
		map[string]any{"name": item.Name, "roleKey": item.RoleKey}, handler.ClientIP(r))
	return handler.OK(w, item)
}

// Delete 删除人员；只允许删没有任何引用（团队/项目/账号）的人。
// 「能不能删」由 service 判定并把原因写进 400 的 message，这里只做转发——
// HTTP 层不该知道"人员还挂在几个团队"这种业务事实。
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	if err := h.svc.Delete(r.Context(), id); err != nil {
		return err
	}
	h.audit.Record(r.Context(), authctx.From(r.Context()), "person.deleted", "person", id,
		nil, handler.ClientIP(r))
	return handler.OK(w, nil)
}

// canAssignRole 判断该身份能否给人员分配业务角色。
// 分配角色是「管人」的一部分：团队管理员及以上都该能做（范围由 scope 限定在本团队），
// 普通成员不行。角色定义本身能不能改是另一回事，走 /roles 接口（platform_admin 起）。
func canAssignRole(identity contract.Identity) bool {
	return contract.AccountLevelAtLeast(identity.Level(), contract.AccountLevelTeamAdmin)
}

// Me 返回当前登录人；登录名由鉴权中间件写入请求上下文。
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) error {
	identity := authctx.From(r.Context())
	// 档案与权限必须用同一个人：由 authz 统一按账号绑定解析。
	personID, err := h.authz.PersonID(r.Context(), identity)
	if err != nil {
		return err
	}
	data, err := h.svc.Me(r.Context(), identity, personID, authctx.ScopeFrom(r.Context()).TeamIDs)
	if err != nil {
		return err
	}
	// 平台级权限（不含项目）：项目内的权限可能被项目角色覆盖，那部分由各接口自己判定。
	permissions, err := h.authz.Permissions(r.Context(), identity, "")
	if err != nil {
		return err
	}
	data.Permissions = authzsvc.Sorted(permissions)
	return handler.OK(w, data)
}
