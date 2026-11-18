// middleware_test.go · 验证 API Key 中间件把「归属公司」正确写进 context。
// 关键点：空串（平台兜底 Key）必须与「没走中间件」严格区分——
// 若两者混淆，一个未鉴权的请求会被当成兜底 Key 放行。
package apikey

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"dandelion/server/internal/contract"
)

func TestRequireWritesTenant(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		resolved   string
		resolveErr error
		wantStatus int
		wantTenant string
		wantOK     bool
	}{
		{
			name:       "公司 Key 写入公司 id",
			header:     "company-key",
			resolved:   "tenant-1",
			wantStatus: http.StatusOK,
			wantTenant: "tenant-1",
			wantOK:     true,
		},
		{
			name:       "平台兜底 Key 写入空串但标记为已鉴权",
			header:     "fallback-key",
			resolved:   "",
			wantStatus: http.StatusOK,
			wantTenant: "",
			wantOK:     true,
		},
		{
			name:       "Key 无效回 401",
			header:     "bad",
			resolveErr: contract.ErrAPIKeyInvalid,
			wantStatus: http.StatusUnauthorized,
			wantOK:     false,
		},
		{
			name:       "缺少 Key 头回 401",
			header:     "",
			wantStatus: http.StatusUnauthorized,
			wantOK:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotTenant string
			var gotOK bool
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotTenant, gotOK = TenantFrom(r.Context())
			})
			resolver := func(_ context.Context, provided string) (string, error) {
				if tt.resolveErr != nil {
					return "", tt.resolveErr
				}
				if provided != tt.header {
					return "", errors.New("unexpected key")
				}
				return tt.resolved, nil
			}

			req := httptest.NewRequest(http.MethodPost, "/open/builds", nil)
			if tt.header != "" {
				req.Header.Set(headerName, tt.header)
			}
			rec := httptest.NewRecorder()
			Require(resolver)(next).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("状态码不符：want %d, got %d", tt.wantStatus, rec.Code)
			}
			if tt.wantStatus != http.StatusOK {
				return
			}
			if gotTenant != tt.wantTenant {
				t.Fatalf("归属公司不符：want %q, got %q", tt.wantTenant, gotTenant)
			}
			if gotOK != tt.wantOK {
				t.Fatalf("已鉴权标记不符：want %v, got %v", tt.wantOK, gotOK)
			}
		})
	}
}

func TestTenantFromWithoutMiddleware(t *testing.T) {
	// 没有中间件时必须是 ("", false)，不能退化成像兜底 Key 那样的 ("", true)。
	if _, ok := TenantFrom(context.Background()); ok {
		t.Fatal("未走中间件时不应报告已鉴权")
	}
}
