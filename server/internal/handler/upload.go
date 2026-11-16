// upload.go · multipart 上传的唯一实现。
// 职责：流式把 file 部分写入 tmp，同时收集其余文本字段，返回暂存描述。
// 输入：ResponseWriter（供 MaxBytesReader 记录超限）、Request、单包上限、路径对象。
// 输出：UploadForm（暂存文件 + 文本字段）或可直接上屏的错误。
// 边界：不调用业务层、不碰正式目录、不解析包内容；
//       管理端 stage 与 /open/builds 共用这一份，避免两套 multipart 逻辑各自演化。
package handler

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/service"
	"dandelion/server/internal/storage"
)

// maxFieldBytes 限制单个文本字段的读取上限；字段都是短文本，超长部分交给长度校验拒绝。
const maxFieldBytes = 8 << 10

// UploadForm 是一次 multipart 上传的解析结果。
type UploadForm struct {
	Staged service.StagedFile
	Fields map[string]string
}

// ReadUpload 读取 multipart：file 部分直接落 tmp，其余字段收进 Fields。
// 必须先消费完所有 part，否则 multipart reader 会丢弃未读部分。
func ReadUpload(w http.ResponseWriter, r *http.Request, maxUploadMB int64, paths *storage.Paths) (UploadForm, error) {
	limit := maxUploadMB * 1024 * 1024
	// 给 multipart 边界与字段头留 1MB 余量，文件体本身仍受 limit 约束。
	r.Body = http.MaxBytesReader(w, r.Body, limit+1<<20)

	reader, err := r.MultipartReader()
	if err != nil {
		return UploadForm{}, BadRequest("请选择受支持的安装包（.apk / .ipa / .hap）")
	}

	form := UploadForm{Fields: make(map[string]string)}
	foundFile := false
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return UploadForm{}, tooLargeOrBadRequest(err, maxUploadMB)
		}

		// 只取第一个 file 部分；其余部分无论叫什么，都按文本字段读掉。
		if part.FormName() == "file" && !foundFile {
			foundFile = true
			staged, err := persistUpload(part, part.FileName(), limit, paths, r)
			_ = part.Close()
			if err != nil {
				return UploadForm{}, err
			}
			form.Staged = staged
			continue
		}

		payload, err := io.ReadAll(io.LimitReader(part, maxFieldBytes))
		_ = part.Close()
		if err != nil {
			return UploadForm{}, BadRequest("上传内容不合法，请重新选择文件")
		}
		if name := part.FormName(); name != "" {
			form.Fields[name] = strings.TrimSpace(string(payload))
		}
	}

	if !foundFile {
		return UploadForm{}, BadRequest("请选择受支持的安装包（.apk / .ipa / .hap）")
	}
	return form, nil
}

// persistUpload 校验扩展名、生成 token 并把文件流写入 tmp；
// 同时把路径登记到请求上下文，后续任何失败都会由错误中间件清理。
func persistUpload(part io.Reader, fileName string, limit int64, paths *storage.Paths, r *http.Request) (service.StagedFile, error) {
	ext := strings.ToLower(strings.TrimPrefix(pathExt(fileName), "."))
	// 扩展名白名单来自契约的 x-platforms：新增平台不需要改这里。
	if _, ok := contract.PlatformOfExt(ext); !ok {
		return service.StagedFile{}, BadRequest("请选择受支持的安装包（.apk / .ipa / .hap）")
	}

	token, err := contract.NewStageToken()
	if err != nil {
		return service.StagedFile{}, Internal("服务内部错误，请稍后重试")
	}
	if err := os.MkdirAll(paths.TmpDir(), 0o755); err != nil {
		return service.StagedFile{}, Internal("服务内部错误，请稍后重试")
	}

	tmpPath := paths.StagePath(token, ext)
	namePath := paths.StageNamePath(token)
	AttachStageTmp(r, tmpPath, namePath)

	dst, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		log.Printf("创建暂存文件失败 path=%s err=%v", tmpPath, err)
		return service.StagedFile{}, Internal("服务内部错误，请稍后重试")
	}
	written, copyErr := io.Copy(dst, part)
	if closeErr := dst.Close(); closeErr != nil && copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return service.StagedFile{}, tooLargeOrBadRequest(copyErr, limit/1024/1024)
	}
	if written > limit {
		return service.StagedFile{}, TooLarge(fmt.Sprintf("文件超过 %d MB 上限", limit/1024/1024))
	}

	// stage 与 commit 是两个无状态请求，清洗后的原始文件名随 .name 伴生文件留存。
	cleanName := storage.CleanFileName(fileName)
	if err := os.WriteFile(namePath, []byte(cleanName), 0o600); err != nil {
		log.Printf("写入暂存文件名失败 path=%s err=%v", namePath, err)
		return service.StagedFile{}, Internal("服务内部错误，请稍后重试")
	}
	return service.StagedFile{Token: token, AbsPath: tmpPath, Ext: ext, OriginalFileName: cleanName}, nil
}

// tooLargeOrBadRequest 把「超过上限」与「multipart 损坏」区分成 413 / 400。
func tooLargeOrBadRequest(err error, maxUploadMB int64) error {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) || strings.Contains(err.Error(), "request body too large") {
		return TooLarge(fmt.Sprintf("文件超过 %d MB 上限", maxUploadMB))
	}
	return BadRequest("上传内容不合法，请重新选择文件")
}

// pathExt 取最后一个点之后的部分（含点），用于判断扩展名。
func pathExt(name string) string {
	if index := strings.LastIndex(name, "."); index >= 0 {
		return name[index:]
	}
	return ""
}
