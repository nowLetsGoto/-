// Package rdc · 研发中心需求池的业务层。
// 职责：校验新建字段与项目归属、复校图片约束、生成附件 id/相对路径、编排事务与
//
//	「搬文件 + 失败补偿」，组装详情与受保护图片引用。
//
// 输入：已解析的业务输入与 staged 图片、调用者身份、数据范围 scope、列表筛选条件。
// 输出：contract.Requirement / RequirementDetail / RequirementList / FileRef；
//
//	校验失败返回 ValidationError，越界返回 NotFoundError。
// 边界：不碰 *http.Request / ResponseWriter、不写 SQL、不判权限；
//
//	归属公司从 scope 取、附件相对路径只由 storage.Paths 生成，请求体不接受任何归属字段。
package rdc

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"dandelion/server/internal/contract"
	rdcrepo "dandelion/server/internal/repository/rdc"
	"dandelion/server/internal/storage"
)

// reqKeyDayLayout 是需求编号里的日期格式；固定取 UTC，避免跨时区用户当天编号不一致。
const reqKeyDayLayout = "20060102"

// ProjectValidator 把「项目存在且属于当前租户」的校验从具体仓储解耦。
// 由 repository/project 实现；service 只认接口，便于纯逻辑测试替身。
type ProjectValidator interface {
	ExistsVisibleByID(ctx context.Context, id string, scope contract.Scope) bool
}

// Service 是需求池的业务入口。
type Service struct {
	repo     *rdcrepo.Repository
	projects ProjectValidator
	paths    *storage.Paths
}

// NewService 构造需求业务层；仓储、项目校验器与存储路径全部由装配层注入。
func NewService(repo *rdcrepo.Repository, projects ProjectValidator, paths *storage.Paths) *Service {
	return &Service{repo: repo, projects: projects, paths: paths}
}

// Create 校验并归一化输入，校验项目归属，编排单事务落库，提交后把图片从 tmp 搬到 uploads。
// 任一搬文件失败都补偿删除刚建的需求（级联附件）与已搬文件，确保无孤儿需求 / 孤儿文件。
func (s *Service) Create(
	ctx context.Context,
	caller contract.Identity,
	scope contract.Scope,
	in contract.RequirementInput,
	images []contract.StagedImage,
) (contract.Requirement, error) {
	normalized, err := normalize(in)
	if err != nil {
		return contract.Requirement{}, err
	}
	if err := validateImages(images); err != nil {
		return contract.Requirement{}, err
	}
	if s.projects == nil || !s.projects.ExistsVisibleByID(ctx, normalized.ProjectID, scope) {
		return contract.Requirement{}, &contract.ValidationError{Message: "所属项目无效，请重新选择"}
	}

	rows, err := s.attachmentRows(images)
	if err != nil {
		return contract.Requirement{}, err
	}
	day := time.Now().UTC().Format(reqKeyDayLayout)
	item, err := s.repo.CreateWithAttachments(ctx, normalized, caller.UserID, scope.TenantID,
		normalized.ProjectID, day, rows)
	if err != nil {
		return contract.Requirement{}, err
	}

	if err := s.stageToFinal(ctx, item.ID, images, rows); err != nil {
		return contract.Requirement{}, err
	}
	return item, nil
}

// List 返回 scope 范围内按筛选条件过滤的需求列表。
func (s *Service) List(ctx context.Context, query contract.RequirementQuery, scope contract.Scope) (contract.RequirementList, error) {
	return s.repo.List(ctx, query, scope)
}

// Get 按需求编号查详情（含附件）；格式非法的编号与不存在的编号同为 404。
func (s *Service) Get(ctx context.Context, reqKey string, scope contract.Scope) (contract.RequirementDetail, error) {
	if !contract.IsValidReqKey(reqKey) {
		return contract.RequirementDetail{}, contract.NotFound(contract.ResourceRequirement)
	}
	return s.repo.FindByReqKey(ctx, reqKey, scope)
}

// ResolveAttachment 按附件 id 解析出受保护图片的已校验文件引用。
// id 格式非法、跨租户、不存在或路径越界一律 404，不泄漏存在性。
func (s *Service) ResolveAttachment(ctx context.Context, id string, scope contract.Scope) (contract.FileRef, error) {
	if !contract.IsValidUUID(id) {
		return contract.FileRef{}, contract.NotFound(contract.ResourceRequirementAttachment)
	}
	attachment, err := s.repo.FindAttachmentByID(ctx, id, scope)
	if err != nil {
		return contract.FileRef{}, err
	}
	absPath, err := s.paths.SafeResolve(attachment.RelativePath)
	if err != nil {
		return contract.FileRef{}, contract.NotFound(contract.ResourceRequirementAttachment)
	}
	info, err := os.Stat(absPath)
	if err != nil || !info.Mode().IsRegular() {
		return contract.FileRef{}, contract.NotFound(contract.ResourceRequirementAttachment)
	}
	return contract.FileRef{
		AbsPath:     absPath,
		ContentType: attachment.ContentType,
		FileName:    attachment.FileName,
		Size:        info.Size(),
	}, nil
}

// attachmentRows 为每张 staged 图片生成 UUID 与经 storage 计算的相对路径。
// 扩展名已由 handler 嗅探白名单得到，这里再复校一次，杜绝绕过 handler 的直接调用。
func (s *Service) attachmentRows(images []contract.StagedImage) ([]contract.AttachmentRow, error) {
	rows := make([]contract.AttachmentRow, 0, len(images))
	for _, image := range images {
		if _, ok := contract.RDCImageExts[image.Ext]; !ok || !contract.IsRDCImageContentType(image.ContentType) {
			return nil, &contract.ValidationError{Message: "图片格式不支持，请重新选择"}
		}
		id, err := contract.NewStageToken()
		if err != nil {
			return nil, fmt.Errorf("生成图片标识失败: %w", err)
		}
		rows = append(rows, contract.AttachmentRow{
			ID:           id,
			FileName:     image.CleanName,
			ContentType:  image.ContentType,
			RelativePath: s.paths.RelativePath(contract.RDCImageDir, contract.RDCImageRequirementSeg, id, image.Ext),
			FileSize:     image.FileSize,
		})
	}
	return rows, nil
}

// stageToFinal 在 DB 提交后逐张将 tmp 搬到 uploads（文件系统无事务，故顺序为「先库后文件」）。
// 任一步失败都补偿：删刚建的需求（级联附件行）+ 删已搬的最终文件 + 清剩余临时文件。
func (s *Service) stageToFinal(ctx context.Context, reqID string, images []contract.StagedImage, rows []contract.AttachmentRow) error {
	moved := make([]string, 0, len(images))
	for i, image := range images {
		tmpPath, err := s.paths.SafeResolveStage(image.AbsPath)
		if err != nil {
			return s.compensate(ctx, reqID, moved, images, err)
		}
		finalPath := s.paths.FinalPath(contract.RDCImageDir, contract.RDCImageRequirementSeg, rows[i].ID, image.Ext)
		if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
			return s.compensate(ctx, reqID, moved, images, err)
		}
		if err := os.Rename(tmpPath, finalPath); err != nil {
			return s.compensate(ctx, reqID, moved, images, err)
		}
		moved = append(moved, finalPath)
	}
	return nil
}

// compensate 回滚「已提交的需求 + 已搬文件 + 剩余 tmp」。
// DB 删除失败只记日志：需求可能已不存在，继续清文件比留下孤儿文件更重要。
func (s *Service) compensate(
	ctx context.Context,
	reqID string,
	moved []string,
	images []contract.StagedImage,
	cause error,
) error {
	// 补偿必须跑完，即使调用方已断开连接：WithoutCancel 保留 scope 等 context 值，
	// 但去掉请求取消与超时，避免删库删文件半途中断留下孤儿。
	cleanupCtx := context.WithoutCancel(ctx)
	if err := s.repo.DeleteByID(cleanupCtx, reqID); err != nil {
		log.Printf("补偿删除需求失败 id=%s err=%v", reqID, err)
	}
	for _, finalPath := range moved {
		if err := os.Remove(finalPath); err != nil && !os.IsNotExist(err) {
			log.Printf("补偿删除已搬图片失败 path=%s err=%v", finalPath, err)
		}
	}
	cleanupStaged(images, s.paths)
	return fmt.Errorf("保存需求图片失败: %w", cause)
}

// cleanupStaged 遍历清理 staged 临时文件；路径先经 SafeResolveStage 校验，非法路径直接跳过。
// 多图不能用 AttachStageTmp（只存单个路径），所有失败路径都由持有者显式调用本函数。
func cleanupStaged(images []contract.StagedImage, paths *storage.Paths) {
	if paths == nil {
		return
	}
	for _, image := range images {
		absPath, err := paths.SafeResolveStage(image.AbsPath)
		if err != nil {
			continue
		}
		if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
			log.Printf("清理暂存图片失败 path=%s err=%v", absPath, err)
		}
	}
}

// normalize 做字段校验与缺省兜底，返回可直接落库的输入。
// projectId 必填（存量可空只针对历史数据，新写入一律必填），其余字段沿用 V1.0 规则。
func normalize(in contract.RequirementInput) (contract.RequirementInput, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return contract.RequirementInput{}, &contract.ValidationError{Message: "标题不能为空"}
	}
	if utf8.RuneCountInString(title) > contract.MaxReqTitleLen {
		return contract.RequirementInput{}, &contract.ValidationError{Message: "标题不能超过 100 字"}
	}

	description := strings.TrimSpace(in.Description)
	if description == "" {
		return contract.RequirementInput{}, &contract.ValidationError{Message: "描述不能为空"}
	}
	if utf8.RuneCountInString(description) > contract.MaxReqDescLen {
		return contract.RequirementInput{}, &contract.ValidationError{Message: "描述不能超过 4000 字"}
	}

	acceptance := strings.TrimSpace(in.AcceptanceCriteria)
	if utf8.RuneCountInString(acceptance) > contract.MaxReqAcceptanceLen {
		return contract.RequirementInput{}, &contract.ValidationError{Message: "验收标准不能超过 2000 字"}
	}

	status := strings.TrimSpace(in.Status)
	if status == "" {
		status = contract.ReqStatusPendingReview
	}
	if !contract.IsValidRequirementStatus(status) {
		return contract.RequirementInput{}, &contract.ValidationError{Message: "需求状态不合法"}
	}

	projectID := strings.TrimSpace(in.ProjectID)
	if projectID == "" {
		return contract.RequirementInput{}, &contract.ValidationError{Message: "请选择所属项目"}
	}
	if !contract.IsValidUUID(projectID) {
		return contract.RequirementInput{}, &contract.ValidationError{Message: "所属项目无效，请重新选择"}
	}

	return contract.RequirementInput{
		Title:              title,
		Description:        description,
		Status:             status,
		AcceptanceCriteria: acceptance,
		ProjectID:          projectID,
	}, nil
}

// validateImages 复校图片数量、单张体积、扩展名与 MIME 白名单（与 handler / 前端一一对应）。
// 纯函数，不依赖文件系统；测试可直接构造 staged 行覆盖全部拒绝分支。
func validateImages(images []contract.StagedImage) error {
	if len(images) > contract.MaxRDCImages {
		return &contract.ValidationError{Message: "每个需求最多上传 9 张图片"}
	}
	for _, image := range images {
		if image.FileSize <= 0 || image.FileSize > contract.MaxRDCImageBytes {
			return &contract.ValidationError{Message: "单张图片不能超过 5MB"}
		}
		if _, ok := contract.RDCImageExts[image.Ext]; !ok {
			return &contract.ValidationError{Message: "图片格式不支持，请重新选择"}
		}
		if !contract.IsRDCImageContentType(image.ContentType) {
			return &contract.ValidationError{Message: "图片格式不支持，请重新选择"}
		}
	}
	return nil
}
