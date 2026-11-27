// Package openapi · 对外开放接口：给 CI 与脚本用的上传。
//
// 职责：暴露两种入口——一步式 multipart（小包兼容）与三步直传 R2（大包推荐），
//
//	两者共用 service 的 Stage / StageFromR2 / Create，不在本层实现任何业务规则。
//
// 输入：multipart 或直传票据 + JSON 元信息；输出：201 + Build。
// 边界：鉴权在 middleware/apikey；本层只做解码、字段合并与身份构造，
//	不写 SQL、不拼对象名、不实现第二套发布流程。
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

	in := buildCommitInput(
		form.Fields["project"], form.Fields["environment"], form.Fields["platform"],
		form.Fields["appName"], form.Fields["packageId"], form.Fields["version"],
		form.Fields["buildNumber"], form.Fields["changelog"], parsed.Parsed,
	)
	in.StageToken = form.Staged.Token
	if err := handler.ValidateCommitInput(in); err != nil {
		return err
	}
	handler.AttachLogFields(r, "source=openapi project="+in.Project+" environment="+in.Environment+" platform="+in.Platform)

	ctx, err := keyScopedContext(r)
	if err != nil {
		return err
	}

	build, err := h.svc.Create(ctx, in, externalIdentity())
	if err != nil {
		return err
	}
	return handler.OKWithStatus(w, http.StatusCreated, build)
}

// UploadTicket 给外部系统签发 R2 预签名 PUT 票据。
//
// 与页面上传共用 IssueUploadTicket：对象 key、签名头、有效期、归属校验完全一致。
// 调用方拿到票据后自行 PUT 字节到 R2，再调 StageFromR2 完成发布。
func (h *Handler) UploadTicket(w http.ResponseWriter, r *http.Request) error {
	var in contract.UploadTicketInput
	if err := handler.DecodeJSON(w, r, &in, "project/fileName/size/contentType"); err != nil {
		return err
	}
	ctx, err := keyScopedContext(r)
	if err != nil {
		return err
	}
	ticket, err := h.svc.IssueUploadTicket(ctx, in, externalIdentity())
	if err != nil {
		return err
	}
	return handler.OK(w, ticket)
}

// StageFromR2 收尾外部直传：校验对象 → 解析 → 写库 → 返回 Build。
//
// 与一步式上传的唯一区别是字节来源：这里 uploadToken 已指向 R2 上校验过的对象，
// 所以不需要 multipart，也无需本层再搬一次文件。
func (h *Handler) StageFromR2(w http.ResponseWriter, r *http.Request) error {
	var in contract.OpenStageFromR2Input
	if err := handler.DecodeJSON(w, r, &in, "uploadToken/project/environment/platform/appName/packageId/version/buildNumber/changelog"); err != nil {
		return err
	}
	if strings.TrimSpace(in.Project) == "" {
		return handler.BadRequest("project 不能为空")
	}

	// 先校验字段长度再消费 uploadToken：输入不合法时不该删掉仍有效的上传会话。
	if err := handler.ValidateCommitInput(buildCommitInput(
		in.Project, in.Environment, in.Platform,
		in.AppName, in.PackageID, in.Version, in.BuildNumber, in.Changelog, contract.ParsedMeta{},
	)); err != nil {
		return err
	}

	ctx, err := keyScopedContext(r)
	if err != nil {
		return err
	}
	parsed, err := h.svc.StageFromR2(ctx, in.UploadToken)
	if err != nil {
		return err
	}

	commit := buildCommitInput(
		in.Project, in.Environment, in.Platform,
		in.AppName, in.PackageID, in.Version, in.BuildNumber, in.Changelog, parsed.Parsed,
	)
	commit.StageToken = parsed.StageToken
	handler.AttachLogFields(r, "source=openapi direct project="+commit.Project+" environment="+commit.Environment+" platform="+commit.Platform)

	build, err := h.svc.Create(ctx, commit, externalIdentity())
	if err != nil {
		return err
	}
	return handler.OKWithStatus(w, http.StatusCreated, build)
}

// externalIdentity 表示外部系统写入：团队模式的匿名账号。
//
// UserID 为空会让包记为 system；只写 team 分区，永远进不了任何人的单人空间。
func externalIdentity() contract.Identity {
	return contract.Identity{Mode: contract.ModeTeam}
}

// buildCommitInput 把「调用方字段 + 解析结果」合成发布请求体。
//
// 优先级：调用方给的值 > 解析值 > Create 内部的文件名兜底。
// 一步式和直传式都必须走这里，避免两条入口对同一份表单产生不同解释。
func buildCommitInput(
	project, environment, platform, appName, packageID, version, buildNumber, changelog string,
	parsed contract.ParsedMeta,
) contract.CommitInput {
	return contract.CommitInput{
		Project:     project,
		Environment: environment,
		Platform:    platform,
		AppName:     firstNonEmpty(appName, parsed.AppName),
		PackageID:   firstNonEmpty(packageID, parsed.PackageID),
		Version:     firstNonEmpty(version, parsed.Version),
		BuildNumber: firstNonEmpty(buildNumber, parsed.BuildNumber),
		Changelog:   changelog,
	}
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
