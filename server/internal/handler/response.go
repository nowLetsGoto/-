// Package handler 负责 HTTP 语义：取参、校验、调用 service、设置响应头与状态码。
// response.go 集中定义统一响应外壳、唯一错误类型 AppError 与唯一错误格式化函数 WriteError。
// 职责：构造 {code,data,message}；把 AppError / 404 / 未知错误统一成中文可读响应；失败清理 tmp 残留。
// 输入：ResponseWriter、Request、业务数据或 error。
// 输出：JSON 响应（成功 code=0，失败 code=HTTP 状态码且 data=null）。
// 边界：错误码只有 400/401/404/405/413/500；handler 层零 try/catch；不写 SQL、不拼磁盘路径。
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"dandelion/server/internal/contract"
)

// AppError 是业务错误的唯一类型，Status 与 Code 恒等且只允许 5 个取值。
type AppError struct {
	Status  int
	Code    int
	Message string
}

func (e *AppError) Error() string { return e.Message }

func BadRequest(message string) error { return &AppError{Status: 400, Code: 400, Message: message} }
func NotFound(message string) error   { return &AppError{Status: 404, Code: 404, Message: message} }
func MethodNotAllowed(message string) error {
	return &AppError{Status: 405, Code: 405, Message: message}
}
func TooLarge(message string) error { return &AppError{Status: 413, Code: 413, Message: message} }
func Internal(message string) error { return &AppError{Status: 500, Code: 500, Message: message} }

type apiEnvelope struct {
	Code    int    `json:"code"`
	Data    any    `json:"data"`
	Message string `json:"message"`
}

// OK 返回 200 成功响应；返回 nil 让 handler 可以写成 return OK(w, data)。
func OK(w http.ResponseWriter, data any) error {
	writeJSON(w, http.StatusOK, 0, data, "ok")
	return nil
}

// OKWithStatus 返回指定状态码的成功响应（提交发布用 201）。
func OKWithStatus(w http.ResponseWriter, status int, data any) error {
	writeJSON(w, status, 0, data, "ok")
	return nil
}

func writeJSON(w http.ResponseWriter, status int, code int, data any, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiEnvelope{Code: code, Data: data, Message: message})
}

type contextKey string

const (
	stageTmpKey  contextKey = "stage-tmp-path"
	stageNameKey contextKey = "stage-name-path"
	logFieldsKey contextKey = "log-fields"
)

// AttachStageTmp 就地记录本请求新建的暂存文件，失败时由 WriteError 统一删除。
// 就地修改是为了让外层 Wrap 持有的同一个 *http.Request 也能看到这份上下文。
func AttachStageTmp(r *http.Request, tmpPath string, namePath string) {
	ctx := context.WithValue(r.Context(), stageTmpKey, tmpPath)
	ctx = context.WithValue(ctx, stageNameKey, namePath)
	*r = *r.WithContext(ctx)
}

// AttachLogFields 就地记录 500 日志需要的 id / 环境 / 平台 / 文件名，不泄漏到响应。
func AttachLogFields(r *http.Request, fields string) {
	*r = *r.WithContext(context.WithValue(r.Context(), logFieldsKey, fields))
}

func logFields(r *http.Request) string {
	if value, ok := r.Context().Value(logFieldsKey).(string); ok {
		return value
	}
	return ""
}

// WriteError 是唯一的错误响应出口：未知错误保留日志上下文，上屏只给通用中文文案。
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var appErr *AppError
	if !errors.As(err, &appErr) {
		// 业务层的校验拒绝（如 Webhook 地址非法）直接透传中文文案。
		var invalid *contract.ValidationError
		// 资源缺失的场景化说法：文案由调用方给定（账号 / 人员 / 包各不相同）。
		var missing *contract.NotFoundError
		switch {
		case errors.As(err, &invalid):
			appErr = &AppError{Status: 400, Code: 400, Message: invalid.Message}
		case errors.As(err, &missing):
			appErr = &AppError{Status: 404, Code: 404, Message: missing.Message}
		case errors.Is(err, contract.ErrNotFound):
			appErr = &AppError{Status: 404, Code: 404, Message: contract.ErrNotFound.Error()}
		// 项目 key 不存在属于「请求参数不对」，不是资源缺失，故 400 而非 404。
		case errors.Is(err, contract.ErrProjectInvalid):
			appErr = &AppError{Status: 400, Code: 400, Message: contract.ErrProjectInvalid.Error()}
		case errors.Is(err, contract.ErrStageInvalid):
			appErr = &AppError{Status: 400, Code: 400, Message: contract.ErrStageInvalid.Error()}
		case errors.Is(err, contract.ErrPlatformMismatch):
			appErr = &AppError{Status: 400, Code: 400, Message: contract.ErrPlatformMismatch.Error()}
		case errors.Is(err, contract.ErrInvalidCredentials):
			appErr = &AppError{Status: 400, Code: 400, Message: contract.ErrInvalidCredentials.Error()}
		case errors.Is(err, contract.ErrTooManyAttempts):
			// 429 而不是 400：让外部监控能一眼区分「输错密码」与「被限流」。
			appErr = &AppError{Status: 429, Code: 429, Message: contract.ErrTooManyAttempts.Error()}
		case errors.Is(err, contract.ErrUnauthorized):
			appErr = &AppError{Status: 401, Code: 401, Message: contract.ErrUnauthorized.Error()}
		case errors.Is(err, contract.ErrAPIKeyInvalid):
			appErr = &AppError{Status: 401, Code: 401, Message: contract.ErrAPIKeyInvalid.Error()}
		case errors.Is(err, contract.ErrForbidden):
			appErr = &AppError{Status: 403, Code: 403, Message: contract.ErrForbidden.Error()}
		default:
			log.Printf("[500] method=%s path=%s query=%s %s err=%v",
				r.Method, r.URL.Path, r.URL.RawQuery, logFields(r), err)
			appErr = &AppError{Status: 500, Code: 500, Message: "服务内部错误，请稍后重试"}
		}
	}
	if appErr.Status >= 500 {
		// 日志保留可定位信息（含 id / 环境 / 平台 / 文件名），响应绝不携带堆栈。
		log.Printf("[%d] method=%s path=%s query=%s %s err=%v",
			appErr.Status, r.Method, r.URL.Path, r.URL.RawQuery, logFields(r), appErr.Message)
	}
	// 失败请求可能在 tmp 留下刚写入的暂存文件，集中在这里清理。
	cleanupStageFiles(r)
	writeJSON(w, appErr.Status, appErr.Code, nil, appErr.Message)
}

func cleanupStageFiles(r *http.Request) {
	for _, key := range []contextKey{stageTmpKey, stageNameKey} {
		if value, ok := r.Context().Value(key).(string); ok && value != "" {
			if err := os.Remove(value); err != nil && !errors.Is(err, os.ErrNotExist) {
				log.Printf("清理暂存文件失败 path=%s err=%v", value, err)
			}
		}
	}
}

// DecodeJSON 限制请求体大小、拒绝未知字段并解码。
// allowed 是该接口契约允许的字段名（如 "username/password"），只用于把 DisallowUnknownFields
// 的英文错误翻译成可上屏的中文；未知字段一律 400，而不是被静默忽略后照常成功。
func DecodeJSON(w http.ResponseWriter, r *http.Request, target any, allowed string) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			return BadRequest(fmt.Sprintf("请求体包含未知字段（仅允许 %s）", allowed))
		}
		return BadRequest("请求体不是合法的 JSON")
	}
	return nil
}
