// Package openapi · 对外开放接口：给 CI 与脚本用的一步式上传。
// 职责：一次 multipart 请求完成「写 tmp → 解析 → 落盘写库 → 生成地址」，返回完整 Build。
// 输入：multipart 文件 + 环境 / 平台 + 可选文本字段；输出：201 + Build。
// 边界：不重复实现暂存与发布逻辑（复用 handler.ReadUpload 与 service 的 Stage/Create），
//
//	不做鉴权（在 middleware/apikey），不提供查询 / 删除能力。
package openapi

import (
	"context"
	"net/http"
	"strings"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/config"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/handler"
	"dandelion/server/internal/middleware/apikey"
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

	ctx, err := keyScopedContext(r)
	if err != nil {
		return err
	}

	// 外部系统没有登录态：按团队模式的匿名身份处理（UserID 为空 → 包记为 system），
	// 因此只写 team 分区，永远进不了任何人的单人空间。
	build, err := h.svc.Create(ctx, in, contract.Identity{Mode: contract.ModeTeam})
	if err != nil {
		return err
	}
	return handler.OKWithStatus(w, http.StatusCreated, build)
}

// keyScopedContext 把 API Key 的归属公司变成**唯一**可信的租户范围。
//
// 三条规则，缺一不可：
//
//  1. 没有归属信息 → 401。这只能说明路由没挂 API Key 中间件，
//     绝不能当成"兜底 Key"放行（那会让未鉴权请求直接写库）。
//
//  2. 归属是空串 → 403。空串代表平台兜底 Key，它不属于任何公司。
//     以前这种情况被放行成 scope.All，意味着这一把 Key 能往全平台任意公司的
//     任意项目投包；App 包会被终端用户下载安装，等于一条供应链投毒通道。
//     现在失败关闭：要外部上传，就为那家公司生成自己的 Key。
//
//  3. 有归属 → scope 只含该 TenantID，永不含 All。
//     项目解析走这个 scope，因此跨公司投包在 SQL 条件上就不可能发生。
//
// 独立成函数是为了让这条安全不变量能被单测直接钉住，不必拉起整个上传流程。
func keyScopedContext(r *http.Request) (context.Context, error) {
	tenantID, ok := apikey.TenantFrom(r.Context())
	if !ok {
		return nil, contract.ErrAPIKeyInvalid
	}
	if tenantID == "" {
		return nil, contract.ErrAPIKeyUnbound
	}
	return authctx.WithScope(r.Context(), contract.Scope{TenantID: tenantID}), nil
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
