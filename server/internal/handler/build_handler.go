// build_handler.go · 五个业务接口的 HTTP 语义层。
// 职责：取参与校验、multipart 流式写 tmp、调用 service、设置下载响应头与状态码、成功后计数。
// 输入：HTTP 请求（multipart / JSON / query）；输出：统一 JSON 或二进制包流。
// 边界：不写 SQL、不拼存储路径（只向 storage.Paths 要路径）、不做业务判断、零 try/catch；
//       stage 解析失败恒 200；下载只在 io.Copy 成功且字节完整之后才计数。
package handler

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"dandelion/server/internal/config"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/service"
	"dandelion/server/internal/storage"
)

// BuildHandler 持有配置、业务服务与路径对象，由 main.go 注入。
type BuildHandler struct {
	cfg   config.Config
	svc   *service.BuildService
	paths *storage.Paths
}

func NewBuildHandler(cfg config.Config, svc *service.BuildService, paths *storage.Paths) *BuildHandler {
	return &BuildHandler{cfg: cfg, svc: svc, paths: paths}
}

// Stage 接收唯一一次文件上传：流式写 storage/tmp/{token}.{ext}，再同步解析。
// 解析失败只体现在 warnings；只有缺文件 / 类型不符 / 超限 / 写 tmp 失败才返回错误。
func (h *BuildHandler) Stage(w http.ResponseWriter, r *http.Request) error {
	limit := h.cfg.MaxUploadMB * 1024 * 1024
	// 给 multipart 边界与字段头留 1MB 余量，文件体本身仍受 limit 约束。
	r.Body = http.MaxBytesReader(w, r.Body, limit+1<<20)

	reader, err := r.MultipartReader()
	if err != nil {
		return BadRequest("请选择 .apk 或 .ipa 文件")
	}
	var part io.Reader
	var fileName string
	for {
		next, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return tooLargeOrBadRequest(err, h.cfg.MaxUploadMB)
		}
		if next.FormName() == "file" {
			part = next
			fileName = next.FileName()
			break
		}
		_ = next.Close()
	}
	if part == nil {
		return BadRequest("请选择 .apk 或 .ipa 文件")
	}

	ext := strings.ToLower(strings.TrimPrefix(pathExt(fileName), "."))
	if ext != "apk" && ext != "ipa" {
		return BadRequest("请选择 .apk 或 .ipa 文件")
	}
	token, err := contract.NewStageToken()
	if err != nil {
		return Internal("服务内部错误，请稍后重试")
	}
	if err := os.MkdirAll(h.paths.TmpDir(), 0o755); err != nil {
		return Internal("服务内部错误，请稍后重试")
	}
	tmpPath := h.paths.StagePath(token, ext)
	namePath := h.paths.StageNamePath(token)
	// 先登记到请求上下文：此后任何失败，错误中间件都会删掉刚写入的 tmp 文件。
	AttachStageTmp(r, tmpPath, namePath)

	dst, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		log.Printf("创建暂存文件失败 path=%s err=%v", tmpPath, err)
		return Internal("服务内部错误，请稍后重试")
	}
	written, copyErr := io.Copy(dst, part)
	if closeErr := dst.Close(); closeErr != nil && copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return tooLargeOrBadRequest(copyErr, h.cfg.MaxUploadMB)
	}
	if written > limit {
		return TooLarge(fmt.Sprintf("文件超过 %d MB 上限", h.cfg.MaxUploadMB))
	}

	// stage 与 commit 是两个无状态请求，清洗后的原始文件名随 .name 伴生文件留存。
	cleanName := storage.CleanFileName(fileName)
	if err := os.WriteFile(namePath, []byte(cleanName), 0o600); err != nil {
		log.Printf("写入暂存文件名失败 path=%s err=%v", namePath, err)
		return Internal("服务内部错误，请稍后重试")
	}
	AttachLogFields(r, fmt.Sprintf("fileName=%s platformHint=%s", cleanName, ext))

	result := h.svc.Stage(r.Context(), service.StagedFile{
		Token:            token,
		AbsPath:          tmpPath,
		Ext:              ext,
		OriginalFileName: cleanName,
	})
	return OK(w, result)
}

func tooLargeOrBadRequest(err error, maxUploadMB int64) error {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) || strings.Contains(err.Error(), "request body too large") {
		return TooLarge(fmt.Sprintf("文件超过 %d MB 上限", maxUploadMB))
	}
	return BadRequest("上传内容不合法，请重新选择文件")
}

func pathExt(name string) string {
	if index := strings.LastIndex(name, "."); index >= 0 {
		return name[index:]
	}
	return ""
}

// Commit 只接受 application/json：恰好 8 个字段（token + 表单最终值），
// 绝不含文件、绝不再次解析；metaSource 由服务端派生，不接受前端传入。
func (h *BuildHandler) Commit(w http.ResponseWriter, r *http.Request) error {
	var in contract.CommitInput
	if err := DecodeJSON(w, r, &in, "stageToken/environment/platform/appName/packageId/version/buildNumber/changelog"); err != nil {
		return err
	}
	if !contract.IsValidUUID(in.StageToken) {
		return BadRequest(contract.ErrStageInvalid.Error())
	}
	if !contract.IsValidEnvironment(in.Environment) {
		return BadRequest("环境参数不合法")
	}
	if !contract.IsValidPlatform(in.Platform) {
		return BadRequest("平台参数不合法")
	}
	for _, field := range []struct {
		label string
		value string
		max   int
	}{
		{"应用名", in.AppName, contract.MaxAppNameLen},
		{"包名", in.PackageID, contract.MaxPackageIDLen},
		{"版本号", in.Version, contract.MaxVersionLen},
		{"构建号", in.BuildNumber, contract.MaxBuildNumberLen},
		{"更新说明", in.Changelog, contract.MaxChangelogLen},
	} {
		if utf8.RuneCountInString(field.value) > field.max {
			return BadRequest(fmt.Sprintf("%s超过 %d 个字符", field.label, field.max))
		}
	}
	AttachLogFields(r, fmt.Sprintf("environment=%s platform=%s fileName=%s", in.Environment, in.Platform, in.StageToken))

	build, err := h.svc.Create(r.Context(), in)
	if err != nil {
		return err
	}
	return OKWithStatus(w, http.StatusCreated, build)
}

// List 按环境（+平台）查询；空结果由 service 返回 items: []，不是 404。
func (h *BuildHandler) List(w http.ResponseWriter, r *http.Request) error {
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
	result, err := h.svc.List(r.Context(), contract.ListQuery{Environment: environment, Platform: platform})
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
