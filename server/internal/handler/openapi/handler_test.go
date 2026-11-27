// handler_test.go · 外部上传的租户边界。
// 职责：钉住"API Key 必须绑定公司"这条安全不变量——平台兜底 Key 一律拒绝。
// 输入：请求上下文里由中间件写入的归属公司；输出：是否放行，以及 scope 是否被限死。
// 边界：只测租户判定这一层，不触发文件解析与落库，因此不需要真实 DB 与配置。
package openapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/middleware/apikey"
)

// scopedRequest 造一个"中间件已写入归属公司"的请求。
// tenantID 传空串表示平台兜底 Key。
func scopedRequest(tenantID string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/open/builds", nil)
	ctx := apikey.WithTenant(context.Background(), tenantID)
	return req.WithContext(ctx)
}

// TestKeyScopedContext 覆盖三种归属情形。
//
// 核心是第 2 条：平台兜底 Key（空串）必须被拒绝。
// 它以前被放行成 scope.All，等于一把 Key 能往全平台任意公司的任意项目投包——
// App 包会被终端用户下载安装，那就是一条供应链投毒通道。
func TestKeyScopedContext(t *testing.T) {
	tests := []struct {
		name        string
		tenantID    string
		wantErr     error
		wantTenant  string
		wantAllTrue bool
	}{
		{
			name:       "公司 Key 放行且 scope 锁定该公司",
			tenantID:   "tenant-1",
			wantTenant: "tenant-1",
		},
		{
			name:     "平台兜底 Key 被拒绝",
			tenantID: "",
			wantErr:  contract.ErrAPIKeyUnbound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, err := keyScopedContext(scopedRequest(test.tenantID))
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("应返回 %v，实际 %v", test.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("不应报错: %v", err)
			}
			scope := authctx.ScopeFrom(ctx)
			if scope.TenantID != test.wantTenant {
				t.Fatalf("TenantID 应为 %q，实际 %q", test.wantTenant, scope.TenantID)
			}
			// 这条是安全底线：外部接口永远不该拿到跨公司视角。
			if scope.All {
				t.Fatal("外部上传的 scope 绝不能是 All")
			}
		})
	}
}

// TestKeyScopedContextMissingTenant 覆盖"路由没挂中间件"的情形：
// 上下文里根本没有归属信息，必须按 Key 无效处理，不能误当成兜底 Key。
func TestKeyScopedContextMissingTenant(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/open/builds", nil)
	if _, err := keyScopedContext(req); !errors.Is(err, contract.ErrAPIKeyInvalid) {
		t.Fatalf("缺少中间件信息应返回 ErrAPIKeyInvalid，实际 %v", err)
	}
}

// TestUnboundKeyIsDistinctError 确认两种失败原因可区分。
// 用 401 会让 CI 一直去核对 Key 是否抄错；403 + 明确文案才指向真正的修法。
func TestUnboundKeyIsDistinctError(t *testing.T) {
	if errors.Is(contract.ErrAPIKeyUnbound, contract.ErrAPIKeyInvalid) {
		t.Fatal("ErrAPIKeyUnbound 不应与 ErrAPIKeyInvalid 混为一谈")
	}
	if contract.ErrAPIKeyUnbound.Error() == "" {
		t.Fatal("错误文案不能为空：CI 日志靠它定位问题")
	}
}

// TestBuildCommitInput 锁定「调用方字段 > 解析结果」的合并规则。
// 一步式 multipart 与三步直传都走这里，规则分叉会让同一份 CI 脚本在不同入口得到不同包信息。
func TestBuildCommitInput(t *testing.T) {
	parsed := contract.ParsedMeta{
		AppName: "解析应用名", PackageID: "com.parsed", Version: "1.2.3", BuildNumber: "12",
	}
	got := buildCommitInput(
		"world2-1", "test", "android",
		"用户指定名", "", "", "13", "本次更新", parsed,
	)

	if got.Project != "world2-1" || got.Environment != "test" || got.Platform != "android" {
		t.Fatalf("项目/环境/平台应原样传出，got=%+v", got)
	}
	if got.AppName != "用户指定名" {
		t.Fatalf("调用方给的应用名优先，got=%q", got.AppName)
	}
	if got.PackageID != "com.parsed" || got.Version != "1.2.3" {
		t.Fatalf("空字段应用解析结果，got packageID=%q version=%q", got.PackageID, got.Version)
	}
	if got.BuildNumber != "13" || got.Changelog != "本次更新" {
		t.Fatalf("构建号与更新说明应以调用方为准，got=%+v", got)
	}
}

// TestExternalIdentity 确认外部上传只写团队分区的匿名身份。
func TestExternalIdentity(t *testing.T) {
	identity := externalIdentity()
	if identity.Mode != contract.ModeTeam {
		t.Fatalf("外部上传必须写 team 分区，got=%q", identity.Mode)
	}
	if identity.UserID != "" {
		t.Fatalf("外部上传没有账号，UserID 必须为空（入库兜底为 system），got=%q", identity.UserID)
	}
}
