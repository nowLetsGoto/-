// Package rdc · 研发中心需求池的 HTTP 语义层。
// 职责：解析分页/筛选参数、解析 multipart 请求、调用业务层、记录创建审计、写统一响应；
//
//	图片访问以二进制流输出（受保护，非公开）。
//
// 输入：HTTP 请求（GET 列表/详情/图片、POST 新建）与上下文中的身份、scope。
// 输出：{code,data,message} JSON 外壳，或受保护图片的 image/* 流；创建成功 201。
// 边界：不写 SQL、不做字段业务校验（在 service）、不判权限（在路由中间件）；
//
//	身份与归属只从上下文取，multipart 文本字段严格白名单，未知字段一律 400。
package rdc

import (
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	auditsvc "dandelion/server/internal/service/audit"
	rdcsvc "dandelion/server/internal/service/rdc"
	"dandelion/server/internal/storage"
)

// Handler 依赖需求业务层、审计服务与存储路径；创建动作的审计写在这里，与 role / user 一致。
type Handler struct {
	svc   *rdcsvc.Service
	audit *auditsvc.Service
	paths *storage.Paths
}

// NewHandler 构造需求 HTTP 处理器。
func NewHandler(svc *rdcsvc.Service, audit *auditsvc.Service, paths *storage.Paths) *Handler {
	return &Handler{svc: svc, audit: audit, paths: paths}
}

// List 返回需求分页列表；page/pageSize/status/keyword/projectId 全部来自查询参数，
// 数据范围由 InjectScope 写入上下文的 scope 决定，前端无法通过参数扩大范围。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	query := contract.RequirementQuery{
		Page:      handler.ReadPage(r),
		Status:    strings.TrimSpace(r.URL.Query().Get("status")),
		Keyword:   strings.TrimSpace(r.URL.Query().Get("keyword")),
		ProjectID: strings.TrimSpace(r.URL.Query().Get("projectId")),
	}
	data, err := h.svc.List(r.Context(), query, authctx.ScopeFrom(r.Context()))
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// Get 按路径参数 reqKey 返回需求详情（含项目名与附件）；越界或不存在的编号由 service 统一转 404。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	reqKey := chi.URLParam(r, "reqKey")
	data, err := h.svc.Get(r.Context(), reqKey, authctx.ScopeFrom(r.Context()))
	if err != nil {
		return err
	}
	return handler.OK(w, data)
}

// Create 新建需求：multipart 文本字段严格白名单 + 多图流式 stage；
// service 成功后 tmp 已全部搬走，失败则由 service 补偿、再由这里兜底清理剩余 staged 图片。
// 审计写失败不返回错误——audit.Record 本身只记日志，绝不让已成功的需求创建回滚。
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	form, err := parseMultipart(r, h.paths)
	if err != nil {
		return err
	}
	identity := authctx.From(r.Context())
	item, err := h.svc.Create(r.Context(), identity, authctx.ScopeFrom(r.Context()), form.Fields, form.Images)
	if err != nil {
		// 幂等兜底：service 校验失败未搬文件时清 tmp；搬文件失败 service 已补偿，重复删除无害。
		cleanupStagedImages(form.Images, h.paths)
		return err
	}
	h.audit.Record(r.Context(), identity, "rdc.requirement.created", "rdc_requirement", item.ReqKey,
		map[string]any{
			"title": form.Fields.Title, "status": item.Status,
			"projectId": form.Fields.ProjectID, "imageCount": len(form.Images),
		}, handler.ClientIP(r))
	return handler.OKWithStatus(w, http.StatusCreated, item)
}

// Attachment 按附件 id 输出原图二进制流。
// 前置中间件已保证登录 + team 模式 + rdc.requirement.read；service 再按 scope 与 SafeResolve 兜底。
// 跨租户 / 不存在 / 路径越界一律 404；响应带 nosniff / inline / private，不缓存到共享代理。
func (h *Handler) Attachment(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	ref, err := h.svc.ResolveAttachment(r.Context(), id, authctx.ScopeFrom(r.Context()))
	if err != nil {
		return err
	}
	file, err := os.Open(ref.AbsPath)
	if err != nil {
		return contract.NotFound(contract.ResourceRequirementAttachment)
	}
	defer func() { _ = file.Close() }()

	w.Header().Set("Content-Type", ref.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cache-Control", "private")
	w.Header().Set("Content-Length", strconv.FormatInt(ref.Size, 10))
	if r.Method == http.MethodHead {
		return nil
	}
	// 响应头已发出，流复制失败无法再改成 JSON 外壳：只记日志，不返回错误。
	if _, copyErr := io.Copy(w, file); copyErr != nil {
		log.Printf("输出需求图片失败 id=%s err=%v", id, copyErr)
	}
	return nil
}
