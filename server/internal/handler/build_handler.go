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
	"os"
	"strconv"

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
	// all 与平台筛选的 all 同义：不按该维度收敛，仓储侧省略对应 WHERE 条件。
	if environment == "all" {
		environment = ""
	} else if !contract.IsValidEnvironment(environment) {
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
	shareHeaders(w)
	id := chi.URLParam(r, "id")
	AttachLogFields(r, "id="+id)
	build, err := h.svc.Get(r.Context(), id, authctx.From(r.Context()))
	if err != nil {
		return err
	}
	return OK(w, build)
}

// Versions 返回登录用户可访问项目内的其它版本。
func (h *BuildHandler) Versions(w http.ResponseWriter, r *http.Request) error {
	shareHeaders(w)
	id := chi.URLParam(r, "id")
	AttachLogFields(r, "id="+id)
	includePlatforms := false
	if value := r.URL.Query().Get("includePlatforms"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return BadRequest("includePlatforms 必须是布尔值")
		}
		includePlatforms = parsed
	}
	versions, err := h.svc.Versions(r.Context(), id, includePlatforms, authctx.From(r.Context()))
	if err != nil {
		return err
	}
	return OK(w, versions)
}

// DownloadFile 流式下载原始包。计数时点严格在 io.Copy 成功且 written == Content-Length 之后；
// 客户端中断 / copy 出错 / 字节不足一律不计数，且计数旁路失败绝不阻断已完成的下载。
func (h *BuildHandler) DownloadFile(w http.ResponseWriter, r *http.Request) error {
	shareHeaders(w)
	id := chi.URLParam(r, "id")
	AttachLogFields(r, "id="+id)
	ref, err := h.svc.ResolveFile(r.Context(), id, authctx.From(r.Context()))
	if err != nil {
		return err
	}
	return h.streamFile(w, r, id, authctx.From(r.Context()).Mode, ref)
}

// SharedGet 不消费登录态，只接受独立分享凭证；错误也禁止缓存。
func (h *BuildHandler) SharedGet(w http.ResponseWriter, r *http.Request) error {
	shareHeaders(w)
	build, err := h.svc.GetShared(r.Context(), chi.URLParam(r, "id"), shareToken(r))
	if err != nil {
		return err
	}
	return OK(w, build)
}

// SharedVersions 仅使用来源分享凭证，返回同应用可切换的有效分享版本。
func (h *BuildHandler) SharedVersions(w http.ResponseWriter, r *http.Request) error {
	shareHeaders(w)
	versions, err := h.svc.SharedVersions(r.Context(), chi.URLParam(r, "id"), shareToken(r))
	if err != nil {
		return err
	}
	return OK(w, versions)
}

func (h *BuildHandler) SharedDownloadFile(w http.ResponseWriter, r *http.Request) error {
	shareHeaders(w)
	id := chi.URLParam(r, "id")
	ref, mode, err := h.svc.ResolveSharedFile(r.Context(), id, shareToken(r))
	if err != nil {
		return err
	}
	return h.streamFile(w, r, id, mode, ref)
}

// SharedDownloadCount 给绕过本站中转的 CDN 直链补记一次下载。
func (h *BuildHandler) SharedDownloadCount(w http.ResponseWriter, r *http.Request) error {
	shareHeaders(w)
	if err := h.svc.RecordSharedDownload(r.Context(), chi.URLParam(r, "id"), shareToken(r)); err != nil {
		return err
	}
	return OK(w, nil)
}

// 详情使用请求头，原生文件下载使用 query；同时出现不一致凭证时失败关闭。
func shareToken(r *http.Request) string {
	header := r.Header.Get("X-Share-Token")
	query := r.URL.Query().Get("shareToken")
	if header != "" && query != "" && header != query {
		return ""
	}
	if header != "" {
		return header
	}
	return query
}

func shareHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (h *BuildHandler) streamFile(w http.ResponseWriter, r *http.Request, id string, mode contract.SessionMode, ref contract.FileRef) error {
	// 已镜像到 R2 就 302 跳转到签名直链：文件字节直接由 R2 下发，
	// 不占用本机带宽（家宽 / 小带宽云主机的主要瓶颈）。
	// 跳转后浏览器不再回源，因此这里主动计数——真正的传输结果由 R2 负责。
	if signed := h.svc.R2SignedURL(r.Context(), ref); signed != "" {
		h.svc.RecordDownload(id, mode)
		shareHeaders(w)
		http.Redirect(w, r, signed, http.StatusFound)
		return nil
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
	shareHeaders(w)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	written, copyErr := io.Copy(w, file)
	if copyErr == nil && written == size {
		h.svc.RecordDownload(id, mode)
	} else {
		log.Printf("下载未完成，不计数 id=%s written=%d size=%d err=%v", id, written, size, copyErr)
	}
	return nil
}

// contentDisposition 委托 storage 实现：本机下载与 R2 对象元数据共用同一套编码，
// 保证两条下载路径返回的文件名一致。
func contentDisposition(name string) string {
	return storage.ContentDisposition(name)
}
