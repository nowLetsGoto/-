// Package rdc · 研发中心需求池的 multipart 解析。
// 职责：严格白名单读取文本字段，流式把 images[] 暂存到 storage/tmp，嗅探 MIME 派生扩展名，
//
//	执行 5MB / 9 张限制，并在所有失败路径清理已暂存的多张图片。
//
// 输入：*http.Request（multipart/form-data）与 storage.Paths。
// 输出：MultipartForm{Fields, Images}；失败返回 handler 的 400/413/500 错误。
// 边界：不写业务校验（在 service）、不写 SQL、不碰响应体；
//
//	临时文件一律经 paths.StagePath 生成，扩展名只由 contract.RDCImageExtOf 派生，禁止取用户文件名。
package rdc

import (
	"errors"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	"dandelion/server/internal/storage"
)

// maxTextFieldBytes 是单个文本字段的读取上限，与 DecodeJSON 的 1MB 请求体上限同口径。
const maxTextFieldBytes = 1 << 20

// MultipartForm 是解析结果：文本字段进严格白名单，图片已在 tmp 落盘待 service 搬移。
type MultipartForm struct {
	Fields contract.RequirementInput
	Images []contract.StagedImage
}

// allowedTextFields 是 multipart 文本字段的唯一白名单（与 DecodeJSON 的严格性一致）。
// images 是文件部分名，单独处理，不在这里。
var allowedTextFields = map[string]bool{
	"title": true, "description": true, "status": true,
	"acceptanceCriteria": true, "projectId": true,
}

// parseMultipart 解析一次性提交的表单。
// 命名返回值 + defer 保证任一错误路径（未知字段 / 非图片 / 超限 / 超量 / IO 故障）
// 都会遍历清理已 stage 的图片；service 成功后由 service 负责搬移与补偿。
func parseMultipart(r *http.Request, paths *storage.Paths) (form MultipartForm, err error) {
	form = MultipartForm{Images: make([]contract.StagedImage, 0)}
	defer func() {
		if err != nil {
			cleanupStagedImages(form.Images, paths)
		}
	}()

	reader, readerErr := r.MultipartReader()
	if readerErr != nil {
		return form, handler.BadRequest("请求必须是 multipart/form-data")
	}
	for {
		part, partErr := reader.NextPart()
		if errors.Is(partErr, io.EOF) {
			break
		}
		if partErr != nil {
			return form, handler.BadRequest("读取上传内容失败")
		}

		name := part.FormName()
		if name == "images" && part.FileName() != "" {
			if len(form.Images) >= contract.MaxRDCImages {
				_ = part.Close()
				return form, handler.BadRequest("每个需求最多上传 9 张图片")
			}
			image, stageErr := stageImage(part, paths)
			_ = part.Close()
			if stageErr != nil {
				return form, stageErr
			}
			form.Images = append(form.Images, image)
			continue
		}

		if !allowedTextFields[name] {
			_ = part.Close()
			return form, handler.BadRequest("请求包含未知字段（仅允许 title/description/status/acceptanceCriteria/projectId/images）")
		}
		data, readErr := io.ReadAll(io.LimitReader(part, maxTextFieldBytes+1))
		_ = part.Close()
		if readErr != nil {
			return form, handler.BadRequest("读取表单字段失败")
		}
		if int64(len(data)) > maxTextFieldBytes {
			return form, handler.BadRequest("表单字段内容过长")
		}
		applyTextField(&form.Fields, name, string(data))
	}
	return form, nil
}

// applyTextField 把白名单内的字段值写入输入结构体；重复字段以后出现者为准。
func applyTextField(fields *contract.RequirementInput, name string, value string) {
	switch name {
	case "title":
		fields.Title = value
	case "description":
		fields.Description = value
	case "status":
		fields.Status = value
	case "acceptanceCriteria":
		fields.AcceptanceCriteria = value
	case "projectId":
		fields.ProjectID = value
	}
}

// stageImage 流式把一张图片写入 tmp：嗅探前 512 字节得 MIME → 由白名单派生扩展名 →
// 生成 StagePath → 写入并统计字节数。超出上限立即返回 413 并删掉半成品。
func stageImage(part *multipart.Part, paths *storage.Paths) (contract.StagedImage, error) {
	var image contract.StagedImage
	// LimitReader 比 MaxBytesReader 更可控：多读 1 字节即可判超限，且不依赖 ResponseWriter。
	limited := io.LimitReader(part, contract.MaxRDCImageBytes+1)
	var head [512]byte
	headLen, readErr := io.ReadFull(limited, head[:])
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return image, handler.BadRequest("读取图片内容失败")
	}
	contentType := http.DetectContentType(head[:headLen])
	ext, ok := contract.RDCImageExtOf(contentType)
	if !ok {
		return image, handler.BadRequest("图片仅支持 png / jpg / jpeg / webp / gif 格式")
	}

	token, tokenErr := contract.NewStageToken()
	if tokenErr != nil {
		return image, handler.Internal("生成暂存标识失败")
	}
	absPath := paths.StagePath(token, ext)
	file, createErr := os.Create(absPath)
	if createErr != nil {
		return image, handler.Internal("暂存图片失败")
	}
	if _, writeErr := file.Write(head[:headLen]); writeErr != nil {
		_ = file.Close()
		_ = os.Remove(absPath)
		return image, handler.Internal("写入暂存图片失败")
	}
	copied, copyErr := io.Copy(file, limited)
	if closeErr := file.Close(); copyErr == nil && closeErr != nil {
		copyErr = closeErr
	}
	size := int64(headLen) + copied
	if copyErr != nil || size > contract.MaxRDCImageBytes {
		_ = os.Remove(absPath)
		return image, handler.TooLarge("单张图片不能超过 5MB")
	}
	if size == 0 {
		_ = os.Remove(absPath)
		return image, handler.BadRequest("图片内容为空")
	}

	return contract.StagedImage{
		Token:       token,
		AbsPath:     absPath,
		Ext:         ext,
		ContentType: contentType,
		CleanName:   storage.CleanFileName(part.FileName()),
		FileSize:    size,
	}, nil
}

// cleanupStagedImages 遍历清理暂存图片（经 SafeResolveStage 校验后删除）。
// handler 与 service 各自持有一份 []StagedImage，分别在不同失败阶段清理；os.Remove 幂等。
func cleanupStagedImages(images []contract.StagedImage, paths *storage.Paths) {
	if paths == nil {
		return
	}
	for _, image := range images {
		absPath, err := paths.SafeResolveStage(image.AbsPath)
		if err != nil {
			continue
		}
		if err := os.Remove(absPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("清理暂存需求图片失败 path=%s err=%v", absPath, err)
		}
	}
}
