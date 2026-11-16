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
	authzsvc "dandelion/server/internal/service/authz"
	peoplesvc "dandelion/server/internal/service/people"
)

// Handler 只依赖人员业务层。
type Handler struct {
	svc *peoplesvc.Service
	// authz 只用于 /me：把"全局权限集合"一并返回，前端据此裁剪菜单与按钮。
	authz *authzsvc.Service
}

func NewHandler(svc *peoplesvc.Service, authz *authzsvc.Service) *Handler {
	return &Handler{svc: svc, authz: authz}
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
	item, err := h.svc.Create(r.Context(), in)
	if err != nil {
		return err
	}
	return handler.OKWithStatus(w, http.StatusCreated, item)
}

// Update 全量覆盖人员字段与关联。
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	var in contract.PersonInput
	if err := handler.DecodeJSON(w, r, &in, "name/username/email/phone/title/teamIds/projectIds"); err != nil {
		return err
	}
	item, err := h.svc.Update(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		return err
	}
	return handler.OK(w, item)
}

// Me 返回当前登录人；登录名由鉴权中间件写入请求上下文。
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) error {
	identity := authctx.From(r.Context())
	data, err := h.svc.Me(r.Context(), identity.Username, identity.IsSuperAdmin)
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
