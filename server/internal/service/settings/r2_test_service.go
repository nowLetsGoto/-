// r2_test_service.go · R2 配置的连通性测试。
// 职责：用已保存的配置验证「凭据可用」「桶可达」「自定义域名可取到对象」。
// 输入：scope（决定测哪家公司的配置）、HTTP 上下文（用于超时与取消）。
// 输出：contract.R2TestResult，三项布尔 + 一句中文说明。
// 边界：只读探测，不上传、不删除；不改配置；不缓存结果。
//
// 为什么独立成文件：这是本模块唯一发起网络请求的地方，
// 与配置的读写校验属于不同职责（前者面对外部服务，后者面对数据库）。
// 混在一起会让"保存配置"这条路径也带上网络依赖，测试时无处下手。
package settings

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/storage"
)

// testProbeKey 是探测对象名的固定前缀。
//
// 不复用真实包的对象名：测试不该依赖桶里恰好有什么。
// 用一个几乎不可能存在的名字，能区分「凭据错」与「对象不存在」——
// 后者返回 404 但凭据有效，正是我们要的结果。
const testProbeKey = "__dandelion_probe__/connectivity-check"

// probeTimeout 是单个探测步骤的时限。
//
// 给 10 秒：跨公网的 HEAD 请求正常在 1 秒内返回，
// 超过 10 秒通常意味着网络不可达或账号被限流，早失败比让用户干等好。
const probeTimeout = 10 * time.Second

// Test 用已保存的配置做连通性测试。
//
// 顺序执行三步而不是并发：后一步依赖前一步的结论——
// 凭据都错了，再去验桶或域名只会得到一串误导性的失败。
func (s *R2Service) Test(ctx context.Context, scope contract.Scope) (contract.R2TestResult, error) {
	tenantID, err := settingsTenant(scope)
	if err != nil {
		return contract.R2TestResult{}, err
	}

	runtimeCfg, err := s.Runtime(ctx, tenantID)
	if err != nil {
		// 解密失败等配置层错误要如实返回：这不是"连接失败"，
		// 而是凭据本身不可用，页面应提示重新填写而不是重试。
		return contract.R2TestResult{}, err
	}
	if !runtimeCfg.Enabled() {
		return contract.R2TestResult{Message: "配置不完整：四项必填（Account ID / Access Key / Secret / Bucket）都需要填写"}, nil
	}

	client, err := storage.NewR2(ctx, runtimeCfg)
	if err != nil {
		return contract.R2TestResult{Message: "无法创建客户端：" + err.Error()}, nil
	}

	result := contract.R2TestResult{}

	// 第一步：凭据与桶合并验证。
	//
	// 无法把两者分开：S3 的 HeadObject 对「凭据错」和「桶不存在」
	// 都返回 403（为安全考虑不区分）。因此一次探测同时得出两个结论——
	// 拿到 404 说明凭据有效且桶存在（只是对象没有），这是最强信号。
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	_, exists, statErr := client.StatObject(probeCtx, testProbeKey)
	switch {
	case statErr != nil:
		// 区分 403 与其他错误：403 才是凭据/权限问题，网络错误要说网络。
		result.Message = describeProbeError(statErr)
	case exists:
		// 探测对象竟真的存在：说明凭据与桶都可用，只是这个 key 被占用了。
		// 不影响结论，但对排查有意义，如实说出。
		result.CredentialsOK = true
		result.BucketOK = true
		result.Message = "凭据与桶均可访问（探测对象名已被占用，可忽略）"
	default:
		result.CredentialsOK = true
		result.BucketOK = true
		result.Message = "凭据与桶均可访问"
	}

	// 第二步：自定义域名只在配置了才测。
	//
	// 未配置不是错误：此时下载走 R2 签名直链，功能完整。
	// 但配了就必须能取到东西——这是"页面正常、下载 404"的唯一防线。
	if result.CredentialsOK && runtimeCfg.PublicBaseURL != "" {
		urlOK, urlMsg := probePublicURL(ctx, runtimeCfg, client)
		result.PublicURLOK = urlOK
		if urlMsg != "" {
			result.Message += "；" + urlMsg
		}
	}

	return result, nil
}

// probePublicURL 验证自定义域名能否真的取到对象。
//
// 探测方式：请求一个确定不存在的对象，期望得到 404。
// 为什么不是期望 200：我们无法保证桶里有任何对象，而 404 恰恰证明
// 「域名已解析到 R2 且桶已开启公共访问」——这两件事才是要验证的。
// 若返回 200，说明域名指向了别处（比如某个静态站点），那是错误配置。
func probePublicURL(ctx context.Context, cfg storage.R2Config, client *storage.R2) (bool, string) {
	url, err := client.PublicURL(testProbeKey)
	if err != nil {
		return false, "自定义域名不可用：" + err.Error()
	}

	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodHead, url, nil)
	if err != nil {
		return false, "自定义域名请求构造失败：" + err.Error()
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, "自定义域名无法访问（检查 DNS 是否解析、R2 是否开启公共访问）"
	}
	defer func() {
		// 需要读完并关闭，否则连接无法复用。
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	switch resp.StatusCode {
	case http.StatusNotFound:
		return true, ""
	case http.StatusOK:
		// 指向了别的东西：域名可能被别的服务占用。
		return false, "自定义域名返回 200 而非 404，可能指向了其他服务"
	default:
		return false, fmt.Sprintf("自定义域名返回 HTTP %d，检查是否已开启公共访问", resp.StatusCode)
	}
}

// describeProbeError 把底层错误翻译成管理员能据此行动的提示。
//
// 不直接透传 SDK 错误：AWS SDK 的报错形如
// "operation error S3: HeadObject, https response error StatusCode: 403, ..."，
// 页面上放这么一段，用户只知道"失败了"，不知道该改哪一项。
func describeProbeError(err error) string {
	text := err.Error()
	switch {
	case strings.Contains(text, "403"), strings.Contains(text, "Forbidden"),
		strings.Contains(text, "AccessDenied"):
		return "凭据无效或无权访问该桶：检查 Access Key / Secret 与桶名是否匹配"
	case strings.Contains(text, "404"), strings.Contains(text, "NotFound"):
		// 桶不存在时 S3 也可能返回 404 而非 403。
		return "桶不存在：检查 Bucket 名称是否拼写正确"
	case strings.Contains(text, "timeout"), strings.Contains(text, "deadline exceeded"):
		return "连接超时：检查 Account ID 是否与账号匹配、网络是否可达"
	default:
		return "连接失败：" + text
	}
}
