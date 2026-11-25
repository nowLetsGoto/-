// chat_transport.go · 唯一传输层：超时夹紧、出站、body 限量读取、状态码分类。
// 职责：把「一次 HTTP POST」的全部工程细节收敛成一份实现，三协议共用同一个出口。
// 输入：已构造好的 endpoint / header / body 字节、per-call 超时；出站事实写进 upstreamCall。
// 输出：upstreamCall（状态码 / 是否出站 / 耗时）与非 2xx 时的分类错误；2xx 的原文交解析器。
// 边界：**不构造请求体、不认识 protocol 枚举、不解析任何响应字段、不做 Redact**。
// 协议差异只能出现在 protocol_*.go；本文件一旦出现 if protocol == ... 就是抽象漏了。
package rdc

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"dandelion/server/internal/contract"
)

// maxUpstreamBody 是上游响应体读取上限。超限按 response_error 处理而不是截断解析：
// 截断的 JSON 必然解析失败，还会让 body 大小成为内存攻击面。
const maxUpstreamBody = 1 << 20

// upstreamCall 是一次出站的事实账本，也是 used_upstream 语义的唯一载体。
//
// 为什么用可变出参而不是返回值：请求可能成功也可能失败，但"有没有真的打出去"在两条
// 路径上都要被记录下来（失败同样计入限流窗口），返回 error 时不能被顺手丢掉。
type upstreamCall struct {
	statusCode   int   // 上游 HTTP 状态码；未出站或网络错误时为 0
	latencyMS    int64 // 从出站发起到返回的耗时（不含解析与落库）
	usedUpstream bool  // 是否真的发出了上游请求；限流窗口只数这个为 true 的行
}

// transport 执行唯一一次 HTTP 出站，并把"出站已发生"写进 call。
// 返回的 raw 仅在 2xx 时有效；非 2xx 直接返回分类后的 *ChatError，
// "上游说了什么"永远不出本函数（不读错误体、不留原文、不回显）。
func transport(
	ctx context.Context, client *http.Client, endpoint string, header http.Header, body []byte,
	timeoutMS int, call *upstreamCall,
) ([]byte, error) {
	// 软超时（extra_config.timeoutMs）不得越过硬超时：硬超时是进程级兜底，
	// 任何配置错误都不该让单次调用无限期占着连接。
	timeout := time.Duration(timeoutMS) * time.Millisecond
	if timeout <= 0 || timeout > contract.ModelCallHardTimeout {
		timeout = contract.ModelCallHardTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		// endpoint 来自配置期已校验的 base_url，构造失败只能是历史脏数据。
		return nil, newChatError(contract.AIChatBadParameter, 0)
	}
	for key, values := range header {
		for _, value := range values {
			request.Header.Set(key, value)
		}
	}
	// Content-Type 是"发送 JSON"这一件事，不属于任何单一协议，故由传输层补；
	// 认证头（可能含明文 Key）一律由 protocol_*.go 的 build 提供。
	request.Header.Set("Content-Type", "application/json")

	started := time.Now()
	response, doErr := client.Do(request)
	// ★ 出站事实的唯一赋值点：出站调用一旦返回，请求体就已经写出去了（含超时与网络错误），
	// 所以 used_upstream 在这里无条件置 true，与 doErr 是否为 nil 无关。
	call.usedUpstream = true
	call.latencyMS = time.Since(started).Milliseconds()
	if doErr != nil {
		return nil, newChatError(contract.AIChatNetwork, 0)
	}
	defer func() { _ = response.Body.Close() }()

	call.statusCode = response.StatusCode
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxUpstreamBody+1))
	if readErr != nil || len(raw) > maxUpstreamBody {
		return nil, newChatError(contract.AIChatBadResponse, response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, newChatError(classifyUpstreamStatus(response.StatusCode), response.StatusCode)
	}
	return raw, nil
}

// classifyUpstreamStatus 是错误分类的唯一入口（三协议共用）；上游 body 一律不读原文、不回显。
// 为什么统一成"状态码 → 类别"而不是让各协议各自映射：同一状态码在不同协议下语义一致
// （401 鉴权、429 限流、5xx 上游故障），分裂映射必然让用户看到前后不一致的提示。
func classifyUpstreamStatus(status int) contract.AIChatCategory {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return contract.AIChatAuthFailed
	case status == http.StatusTooManyRequests || status >= 500:
		return contract.AIChatRateLimited
	case status >= 400 && status < 500:
		// 含 400 / 422 与其余 4xx：请求被拒即"参数或模型不被支持"，文案引导检查配置。
		return contract.AIChatBadParameter
	default:
		return contract.AIChatBadResponse
	}
}
