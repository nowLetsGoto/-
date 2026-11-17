// Package openapi · 对外开放接口：给 CI 与脚本用的一步式上传。
// 职责：一次 multipart 请求完成「写 tmp → 解析 → 落盘写库 → 生成地址」，返回完整 Build。
// 输入：multipart 文件 + 环境 / 平台 + 可选文本字段；输出：201 + Build。
// 边界：不重复实现暂存与发布逻辑（复用 handler.ReadUpload 与 service 的 Stage/Create），
//
//	不做鉴权（在 middleware/apikey），不提供查询 / 删除能力。
package openapi

import (
	"net/http"
	"strings"

	"dandelion/server/internal/config"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	"dandelion/server/internal/service"
	"dandelion/server/internal/storage"
)

// Handler 复用管理端的两步能力，把 stage 与 create 合成一次请求。
type Handler struct {
	cfg   config.Config
	svc   *service.BuildService
	paths *storage.Paths
}

func NewHandler(cfg config.Config, svc *service.BuildService, paths *storage.Paths) *Handler {
	return &Handler{cfg: cfg, svc: svc, paths: paths}
}

// Upload 是外部系统唯一的写入口。
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) error {
	form, err := handler.ReadUpload(w, r, h.cfg.MaxUploadMB, h.paths)
	if err != nil {
		return err
	}

	// 先解析：外部调用方常常只传文件与环境，应用名 / 版本号由服务端补齐更省事。
	parsed := h.svc.Stage(r.Context(), form.Staged)

	in := contract.CommitInput{
		Project:     form.Fields["project"],
		StageToken:  form.Staged.Token,
		Environment: form.Fields["environment"],
		Platform:    form.Fields["platform"],
		// 优先用调用方给的值，其次用解析结果，最后才由 Create 用文件名兜底。
		AppName:     firstNonEmpty(form.Fields["appName"], parsed.Parsed.AppName),
		PackageID:   firstNonEmpty(form.Fields["packageId"], parsed.Parsed.PackageID),
		Version:     firstNonEmpty(form.Fields["version"], parsed.Parsed.Version),
		BuildNumber: firstNonEmpty(form.Fields["buildNumber"], parsed.Parsed.BuildNumber),
		Changelog:   form.Fields["changelog"],
	}
	if err := handler.ValidateCommitInput(in); err != nil {
		return err
	}
	handler.AttachLogFields(r, "source=openapi project="+in.Project+" environment="+in.Environment+" platform="+in.Platform)

	// 外部系统没有登录态：按团队模式的匿名身份处理（UserID 为空 → 包记为 system），
	// 因此只写 team 分区，永远进不了任何人的单人空间。
	build, err := h.svc.Create(r.Context(), in, contract.Identity{Mode: contract.ModeTeam})
	if err != nil {
		return err
	}
	return handler.OKWithStatus(w, http.StatusCreated, build)
}

// firstNonEmpty 返回第一个非空白值，空串表示三者都没给。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
