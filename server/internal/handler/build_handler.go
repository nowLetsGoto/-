// build_handler.go · 管理端业务接口的 HTTP 语义层。
// 职责：取参与校验、调用 service、设置下载响应头与状态码、下载成功后计数。
// 输入：HTTP 请求（multipart / JSON / query）；输出：统一 JSON 或二进制包流。
// 边界：不写 SQL、不拼存储路径、不做业务判断；
//
//	multipart 处理在 upload.go、提交字段校验在 commit.go，两者与 /open/builds 共用。
package handler

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/config"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/service"
	auditsvc "dandelion/server/internal/service/audit"
	authzsvc "dandelion/server/internal/service/authz"
	"dandelion/server/internal/storage"
)

// BuildHandler 持有配置、业务服务与路径对象，由 main.go 注入。
type BuildHandler struct {
	cfg   config.Config
	svc   *service.BuildService
	paths *storage.Paths
	audit *auditsvc.Service
	// authz 用于"项目在请求体里"的场景：中间件拿不到项目，只能在解码后判定。
	authz *authzsvc.Service
}

func NewBuildHandler(cfg config.Config, svc *service.BuildService, paths *storage.Paths, audit *auditsvc.Service, authz *authzsvc.Service) *BuildHandler {
	return &BuildHandler{cfg: cfg, svc: svc, paths: paths, audit: audit, authz: authz}
}

// Stage 接收唯一一次文件上传：流式写 storage/tmp/{token}.{ext}，再同步解析。
// 解析失败只体现在 warnings；只有缺文件 / 类型不符 / 超限 / 写 tmp 失败才返回错误。
func (h *BuildHandler) Stage(w http.ResponseWriter, r *http.Request) error {
	form, err := ReadUpload(w, r, h.cfg.MaxUploadMB, h.paths)
	if err != nil {
		return err
	}
	AttachLogFields(r, fmt.Sprintf("fileName=%s platformHint=%s", form.Staged.OriginalFileName, form.Staged.Ext))
	return OK(w, h.svc.Stage(r.Context(), form.Staged))
}

// Commit 只接受 application/json：恰好 9 个字段（项目 + token + 表单最终值），
// 绝不含文件、绝不再次解析；metaSource 由服务端派生，不接受前端传入。
func (h *BuildHandler) Commit(w http.ResponseWriter, r *http.Request) error {
	var in contract.CommitInput
	if err := DecodeJSON(w, r, &in, "project/stageToken/environment/platform/appName/packageId/version/buildNumber/changelog"); err != nil {
		return err
	}
	if !contract.IsValidUUID(in.StageToken) {
		return BadRequest(contract.ErrStageInvalid.Error())
	}
	if err := ValidateCommitInput(in); err != nil {
		return err
	}
	AttachLogFields(r, fmt.Sprintf("environment=%s platform=%s stageToken=%s", in.Environment, in.Platform, in.StageToken))

	// 项目在请求体里，中间件判定不了；这里按「项目角色 → 人员角色」判定发布权限。
	identity := authctx.From(r.Context())
	if err := h.authz.Check(r.Context(), identity, in.Project, "build.write"); err != nil {
		return err
	}

	build, err := h.svc.Create(r.Context(), in, identity)
	if err != nil {
		return err
	}
	// 发布是最值得追溯的动作：谁在什么时候往哪个环境发了哪个版本。
	h.audit.Record(r.Context(), identity, "build.created", "build", build.ID,
		map[string]any{
			"project": in.Project, "environment": build.Environment, "platform": build.Platform,
			"appName": build.AppName, "version": build.Version, "buildNumber": build.BuildNumber,
		}, ClientIP(r))
	return OKWithStatus(w, http.StatusCreated, build)
}

// List 按「项目 + 环境（+平台）」查询；空结果由 service 返回 items: []，不是 404。
func (h *BuildHandler) List(w http.ResponseWriter, r *http.Request) error {
	identity := authctx.From(r.Context())
	environment := r.URL.Query().Get("environment")
	if !contract.IsValidEnvironment(environment) {
		return BadRequest("环境参数不合法")
	}
	platform := r.URL.Query().Get("platform")
	if platform == "all" {
		platform = ""
	}
	if platform != "" && !contract.IsValidPlatform(platform) {
		return BadRequest("平台参数不合法")
	}
	query := contract.ListQuery{
		Project:     r.URL.Query().Get("project"),
		Environment: environment,
		Platform:    platform,
	}
	// 单人模式只可能看到自己分区里的包（solo.projects 按 owner 隔离，包随项目），
	// 不需要再加 owner 条件；团队模式看团队分区的全量。
	result, err := h.svc.List(r.Context(), query, identity)
	if err != nil {
		return err
	}
	return OK(w, result)
}

// Get 返回单个包详情，供独立下载页按 id 自行取数。
func (h *BuildHandler) Get(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	AttachLogFields(r, "id="+id)
	build, err := h.svc.Get(r.Context(), id)
	if err != nil {
		return err
	}
	return OK(w, build)
}

// DownloadFile 流式下载原始包。计数时点严格在 io.Copy 成功且 written == Content-Length 之后；
// 客户端中断 / copy 出错 / 字节不足一律不计数，且计数旁路失败绝不阻断已完成的下载。
func (h *BuildHandler) DownloadFile(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	AttachLogFields(r, "id="+id)
	ref, err := h.svc.ResolveFile(r.Context(), id)
	if err != nil {
		return err
	}
	file, err := os.Open(ref.AbsPath)
	if err != nil {
		return NotFound("包文件不存在或已被移除")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Internal("服务内部错误，请稍后重试")
	}
	size := info.Size()

	w.Header().Set("Content-Type", ref.ContentType)
	// 中文下载名双重编码：filename 兼容旧浏览器，filename* 遵循 RFC 5987。
	w.Header().Set("Content-Disposition", contentDisposition(ref.FileName))
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	written, copyErr := io.Copy(w, file)
	if copyErr == nil && written == size {
		h.svc.IncrDownloadCount(id)
	} else {
		log.Printf("下载未完成，不计数 id=%s written=%d size=%d err=%v", id, written, size, copyErr)
	}
	return nil
}

func contentDisposition(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r > 127 || r == '"' || r == '\\' || r == '\r' || r == '\n' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii, url.PathEscape(name))
}
