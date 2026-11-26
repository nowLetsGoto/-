// build_upload_handler.go · 浏览器直传 R2 的三个接口。
//
// 职责：签发预签名 PUT 票据、回拉 R2 对象并解析、放弃上传时清理。
// 输入：JSON（文件名/大小/类型 或 uploadToken）。
// 输出：票据（含 putUrl 与必需请求头）、StageResult、空响应。
//
// 为什么单独成文件：这三个接口是"绕开 Tunnel 100MB 限制"的完整闭环，
// 与 upload.go 的 multipart 路径是并列的两条上传通道，分开便于对照阅读。
// 两条通道互不影响：R2 未配置时前端会自动回落到 multipart。
package handler

import (
	"net/http"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
)

// UploadTicket 申请直传票据。响应里带预签名 URL，前端据此直接 PUT 到 R2。
//
// 请求需带 project：对象 key 由"租户/项目"构成，缺失就无法为对象定归属。
func (h *BuildHandler) UploadTicket(w http.ResponseWriter, r *http.Request) error {
	var in contract.UploadTicketInput
	if err := DecodeJSON(w, r, &in, "project/fileName/size/contentType"); err != nil {
		return err
	}
	AttachLogFields(r, "fileName="+in.FileName)
	ticket, err := h.svc.IssueUploadTicket(r.Context(), in, authctx.From(r.Context()))
	if err != nil {
		return err
	}
	return OK(w, ticket)
}

// StageFromR2 让后端把已直传的对象回拉本地并解析元信息。
// 成功后返回与 multipart stage 完全一致的 StageResult，前端可以无缝接 commit。
func (h *BuildHandler) StageFromR2(w http.ResponseWriter, r *http.Request) error {
	var in contract.StageFromR2Input
	if err := DecodeJSON(w, r, &in, "uploadToken"); err != nil {
		return err
	}
	AttachLogFields(r, "uploadToken="+in.UploadToken)
	result, err := h.svc.StageFromR2(r.Context(), in.UploadToken)
	if err != nil {
		return err
	}
	return OK(w, result)
}

// UploadAbort 放弃直传：删会话与 R2 临时对象。
// 前端在改选文件、关闭页面或 PUT 失败时调用；不调用也会由过期清理兜底。
func (h *BuildHandler) UploadAbort(w http.ResponseWriter, r *http.Request) error {
	var in contract.StageFromR2Input
	if err := DecodeJSON(w, r, &in, "uploadToken"); err != nil {
		return err
	}
	AttachLogFields(r, "uploadToken="+in.UploadToken)
	if err := h.svc.AbortUpload(r.Context(), in.UploadToken); err != nil {
		return err
	}
	return OK(w, map[string]bool{"aborted": true})
}
