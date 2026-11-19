// response_test.go · 错误出口的回归护栏。
// 职责：断言每个 contract sentinel（以及 ValidationError / AppError / 未知错误）在 WriteError 里
//       都映射成预期的 status/code/message，防止「契约新增了 sentinel、WriteError 忘了加分支」
//       这类问题以 500 的形式漏到线上（曾经发生过：ErrProjectInvalid → 500 而非 400）。
// 输入：一组 error 与期望的 HTTP 状态码 / code / 中文文案。
// 输出：测试结论；不落盘、不连库、不发网络。
// 边界：只测错误出口这一个函数；新增 sentinel 时**必须**在本表补一行，否则视为漏改。
package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"dandelion/server/internal/contract"
)

// envelope 是响应外壳的测试侧镜像，只取断言需要的字段。
type envelope struct {
	Code    int    `json:"code"`
	Data    any    `json:"data"`
	Message string `json:"message"`
}

func TestWriteErrorMapsContractSentinels(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		status  int
		code    int
		message string
	}{
		{
			name:    "ValidationError 透传中文文案",
			err:     &contract.ValidationError{Message: "Webhook 地址不合法"},
			status:  http.StatusBadRequest,
			code:    http.StatusBadRequest,
			message: "Webhook 地址不合法",
		},
		{
			name:    "通用 ErrNotFound → 404 且用中性文案",
			err:     contract.ErrNotFound,
			status:  http.StatusNotFound,
			code:    http.StatusNotFound,
			message: contract.ErrNotFound.Error(),
		},
		// 资源化的错误必须映射到同一状态码，但文案要准确到具体资源。
		// 这是本次改造的核心：团队/人员的 404 不再说"包不存在"。
		{
			name:    "团队不存在 → 404 且文案指名团队",
			err:     contract.NotFound(contract.ResourceTeam),
			status:  http.StatusNotFound,
			code:    http.StatusNotFound,
			message: "团队不存在或已被移除",
		},
		{
			name:    "人员不存在 → 404 且文案指名人员",
			err:     contract.NotFound(contract.ResourcePerson),
			status:  http.StatusNotFound,
			code:    http.StatusNotFound,
			message: "人员不存在或已被移除",
		},
		{
			name:    "包装后的资源错误仍走 404（errors.Is 穿透）",
			err:     fmt.Errorf("update team: %w", contract.NotFound(contract.ResourceTeam)),
			status:  http.StatusNotFound,
			code:    http.StatusNotFound,
			message: "团队不存在或已被移除",
		},
		{
			name:    "ErrProjectInvalid → 400 而不是 500",
			err:     contract.ErrProjectInvalid,
			status:  http.StatusBadRequest,
			code:    http.StatusBadRequest,
			message: contract.ErrProjectInvalid.Error(),
		},
		{
			// service 会把仓储错误往上包一层，errors.Is 必须能穿透。
			name:    "包装后的 ErrProjectInvalid 仍是 400",
			err:     fmt.Errorf("resolve project: %w", contract.ErrProjectInvalid),
			status:  http.StatusBadRequest,
			code:    http.StatusBadRequest,
			message: contract.ErrProjectInvalid.Error(),
		},
		{
			name:    "ErrStageInvalid → 400",
			err:     contract.ErrStageInvalid,
			status:  http.StatusBadRequest,
			code:    http.StatusBadRequest,
			message: contract.ErrStageInvalid.Error(),
		},
		{
			name:    "ErrPlatformMismatch → 400",
			err:     contract.ErrPlatformMismatch,
			status:  http.StatusBadRequest,
			code:    http.StatusBadRequest,
			message: contract.ErrPlatformMismatch.Error(),
		},
		{
			name:    "ErrInvalidCredentials → 400",
			err:     contract.ErrInvalidCredentials,
			status:  http.StatusBadRequest,
			code:    http.StatusBadRequest,
			message: contract.ErrInvalidCredentials.Error(),
		},
		{
			name:    "ErrUnauthorized → 401",
			err:     contract.ErrUnauthorized,
			status:  http.StatusUnauthorized,
			code:    http.StatusUnauthorized,
			message: contract.ErrUnauthorized.Error(),
		},
		{
			name:    "ErrAPIKeyInvalid → 401",
			err:     contract.ErrAPIKeyInvalid,
			status:  http.StatusUnauthorized,
			code:    http.StatusUnauthorized,
			message: contract.ErrAPIKeyInvalid.Error(),
		},
		{
			name:    "AppError 原样保留状态码",
			err:     BadRequest("环境只能是 test/hotfix/pre/prd"),
			status:  http.StatusBadRequest,
			code:    http.StatusBadRequest,
			message: "环境只能是 test/hotfix/pre/prd",
		},
		{
			name:    "未知错误兜底 500",
			err:     errors.New("pq: connection refused"),
			status:  http.StatusInternalServerError,
			code:    http.StatusInternalServerError,
			message: "服务内部错误，请稍后重试",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			// 带上 project 查询参数，贴近真实触发场景（项目 key 不存在）。
			request := httptest.NewRequest(http.MethodGet, "/api/v1/builds?project=nonexist-key&environment=test", nil)

			WriteError(recorder, request, tt.err)

			if recorder.Code != tt.status {
				t.Fatalf("HTTP 状态码 = %d，期望 %d", recorder.Code, tt.status)
			}
			var got envelope
			if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
				t.Fatalf("响应不是合法 JSON: %v（body=%s）", err, recorder.Body.String())
			}
			if got.Code != tt.code {
				t.Fatalf("envelope.code = %d，期望 %d", got.Code, tt.code)
			}
			if got.Message != tt.message {
				t.Fatalf("envelope.message = %q，期望 %q", got.Message, tt.message)
			}
			if got.Data != nil {
				t.Fatalf("失败响应必须 data=null，实际 %v", got.Data)
			}
		})
	}
}

// TestWriteErrorProjectInvalidIsNotServerError 是对上面「曾经踩过的坑」的显式断言：
// 项目 key 不存在绝不能被当成服务端故障（500），否则前端会显示"服务内部错误"而不是"项目不存在"。
func TestWriteErrorProjectInvalidIsNotServerError(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/builds?project=nonexist-key", nil)

	WriteError(recorder, request, contract.ErrProjectInvalid)

	if recorder.Code >= http.StatusInternalServerError {
		t.Fatalf("项目不存在被判成服务端错误：HTTP %d", recorder.Code)
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("项目不存在的 HTTP 状态码 = %d，期望 400", recorder.Code)
	}
}
