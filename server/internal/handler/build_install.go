// 职责：提供 iOS 在线安装清单的 HTTP 响应。
// 输入：包 ID 与独立分享凭证；输出：禁止缓存的 XML plist，失败沿用统一 JSON 错误。
// 边界：不解析 IPA、不生成 URL、不接收登录令牌替代分享凭证、不增加下载次数。
package handler

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

func (h *BuildHandler) SharedIOSManifest(w http.ResponseWriter, r *http.Request) error {
	shareHeaders(w)
	body, err := h.svc.SharedIOSManifest(r.Context(), chi.URLParam(r, "id"), shareToken(r))
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="manifest.plist"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(body)
	return err
}
