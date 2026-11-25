// model_chat.go · 统一 LLM 调用的**编排层**：需求校验 → 限流 → 选模 → 取 Key → 出站 → 落统计。
// 职责：Chat 与 Test 两条入口的流程串联；协议差异交 protocol_*.go，传输交 chat_transport.go，
// 筛选/阈值/记录归一交 chat_select.go / chat_quota.go / chat_record.go 三个纯函数文件。
// 输入：ChatRequest（TenantID / Scene / Messages / 三个可选能力条件）或实测指定的模型 id 与 scope。
// 输出：ChatResult / AIModelTestResult / ChatError；所有对外文本均经 Redact，无 Key、密文与上游原文。
// 边界：不构造报文、不发 HTTP、不解析响应字段、不写 SQL、不做筛选与阈值判定；
// 本文件出现 json.Marshal 或直接的 HTTP 出站调用即越界（唯一出口在 chat_transport.go）。
package rdc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/modelkey"
)

// maxPreviewRunes 是实测响应预览的字数上限：预览是给人扫一眼的，不是全文导出。
const maxPreviewRunes = 300

// ChatMessage 是对话最小单元；role 取 system / user / assistant。
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest 是内部模块唯一入参：Key / base_url / 思考 / 上下文全部由服务端配置决定。
// 三个能力字段全为可选：全零值 == 未声明 == 完全走 P0 原 SQL 与排序（见 chat_select.go）。
type ChatRequest struct {
	TenantID string
	Scene    string
	Messages []ChatMessage

	Capabilities     []string // AND 语义；空 = 不约束
	MinContextTokens int      // 0 = 不约束；要求模型 context_tokens >= 此值
	ReasoningEffort  string   // "" = 不约束；要求模型可提供档位 >= 请求档位
}

// ChatResult 是调用成功后的结果；Content 与 ReasoningContent 均已经过脱敏。
// TokensReported=false 表示上游未上报 usage（此时两个 token 数为 0，不是"真的用了 0"）。
type ChatResult struct {
	Content          string
	ReasoningContent string
	ModelID          string
	UsedDefault      bool
	UsedFallback     bool
	LatencyMS        int64
	PromptTokens     int
	CompletionTokens int
	TokensReported   bool
}

// ChatError 是分类后的业务错误，Message 中文可读且绝不含 Key / 密文 / 完整请求体。
type ChatError struct {
	Category   contract.AIChatCategory
	Message    string
	StatusCode int
	Cause      error
}

// Error 返回可直接上屏的中文文案。
func (e *ChatError) Error() string {
	if e == nil || e.Message == "" {
		return contract.AIChatMessage(contract.AIChatBadResponse)
	}
	return e.Message
}

// newChatError 只装分类事实：Cause 绝不含 http 错误或上游 body。
func newChatError(category contract.AIChatCategory, status int) *ChatError {
	return &ChatError{
		Category: category, Message: contract.AIChatMessage(category), StatusCode: status,
		Cause: errors.New(string(category)),
	}
}

// AIModelCallRepository 是调用记录的消费侧端口：写记录、数窗口、聚合统计。
// 声明成接口只为让编排与统计可脱离数据库单测；生产实现是 repository/rdc.AIModelCallRepository。
type AIModelCallRepository interface {
	Insert(ctx context.Context, record contract.AIModelCallRecord) error
	CountInWindow(ctx context.Context, scope contract.Scope, since time.Time) (int, error)
	Aggregate(ctx context.Context, query contract.AIModelCallQuery, scope contract.Scope) (
		contract.AIModelCallStats, error)
}

// AIQuotaRepository 是阈值配置的消费侧端口：读写都要求 scope 已投影到单一公司。
type AIQuotaRepository interface {
	Find(ctx context.Context, scope contract.Scope) (contract.AIQuotaConfig, error)
	Upsert(ctx context.Context, scope contract.Scope, input contract.AIQuotaInput, extra json.RawMessage) (
		contract.AIQuotaConfig, error)
}

// ModelChatService 由 main 注入共享 http.Client（Timeout=120s）、加密 Codec 与两个记录仓储。
type ModelChatService struct {
	repo   AIModelRepository
	calls  AIModelCallRepository
	quotas AIQuotaRepository
	codec  *modelkey.Codec
	client *http.Client
}

// NewModelChatService 构造统一调用服务；client 为空时使用同规格硬超时客户端兜底。
// calls / quotas 允许为 nil（只关心编排的单测可省略），此时限流与统计退化为"不做"。
func NewModelChatService(
	repo AIModelRepository, calls AIModelCallRepository, quotas AIQuotaRepository,
	codec *modelkey.Codec, client *http.Client,
) *ModelChatService {
	if client == nil {
		client = &http.Client{Timeout: contract.ModelCallHardTimeout}
	}
	return &ModelChatService{repo: repo, calls: calls, quotas: quotas, codec: codec, client: client}
}

// Chat 按 §4.2 的六步顺序执行：校验 → 限流 → 取候选选模 → 取 Key 出站 → 落统计。
// 顺序不可调换：限流必须先于选模，否则被拒请求会走完选模流程并污染统计。
func (s *ModelChatService) Chat(ctx context.Context, in ChatRequest) (ChatResult, error) {
	if in.TenantID == "" {
		return ChatResult{}, newChatError(contract.AIChatConfigMissing, 0)
	}
	needs, err := normalizeNeeds(in)
	if err != nil {
		// 调用方 bug（白名单外能力 / 非四档 / 负上下文）：不落统计、不出站 —— 它不算"一次调用"。
		return ChatResult{}, err
	}
	scope := contract.Scope{TenantID: in.TenantID}
	facts := callFacts{TenantID: in.TenantID, Scene: in.Scene, Needs: needs}
	if gateErr := s.quotaGate(ctx, &facts, scope); gateErr != nil {
		return ChatResult{}, gateErr
	}
	record, fallback, err := s.pickModel(ctx, scope, needs)
	if err != nil {
		// 无启用模型 / 无匹配：没出站，但仍是"一次被拒的调用"，要留痕供排查。
		facts.Err = err
		s.recordCall(ctx, facts)
		return ChatResult{}, err
	}
	facts.Record = record
	result, call, callErr := s.invoke(ctx, record, fallback, in.Messages)
	facts.Call, facts.Result, facts.Err = call, result, callErr
	s.recordCall(ctx, facts)
	return result, callErr
}

// Test 对指定模型实测；与 Chat 共用 invoke/chatOnce，确保"实测通过 = 接口可用"。
// scope 必须由 handler 先经 ModelService 的超管代配投影，这里不再读请求参数。
// 实测是一次真实出站调用（消耗上游额度），因此与 Chat 同样受软限流约束并计入统计。
func (s *ModelChatService) Test(
	ctx context.Context, scope contract.Scope, modelID string, probe []ChatMessage,
) (contract.AIModelTestResult, error) {
	record, err := s.repo.FindByID(ctx, modelID, scope)
	if err != nil {
		return contract.AIModelTestResult{}, err
	}
	facts := callFacts{TenantID: scope.TenantID, Record: record}
	if gateErr := s.quotaGate(ctx, &facts, scope); gateErr != nil {
		return testOutcome(record, ChatResult{}, gateErr), nil
	}
	result, call, callErr := s.invoke(ctx, record, false, probe)
	facts.Call, facts.Result, facts.Err = call, result, callErr
	s.recordCall(ctx, facts)
	return testOutcome(record, result, callErr), nil
}

// quotaGate 是软限流的唯一执行点：nil = 放行；非 nil = 已拒绝并落好一条未出站记录。
// facts 用指针传入，让"被拒"这次调用带着 tenant / scene / 需求快照一起落库，
// 调用方无需为拒绝分支重复组装记录。
func (s *ModelChatService) quotaGate(ctx context.Context, facts *callFacts, scope contract.Scope) error {
	if s.calls == nil || s.quotas == nil {
		return nil // 未装配限流端口：默认关闭本就等于放行
	}
	cfg, err := s.quotas.Find(ctx, scope)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		// 读配置失败 → fail-open 放行。限流是保护性措施，读不到时宁可放行也不能让功能挂掉；
		// 这与仓储层"隔离失败关闭（1=0）"方向相反但不冲突：那个防越权，这个保可用性。
		log.Printf("读取 AI 模型调用阈值失败，本次放行（fail-open）: %v", err)
		return nil
	}
	cfg = normalizeQuota(cfg) // 无行 / 零值 → 契约默认值（默认不启用）
	if !cfg.Enabled {
		return nil
	}
	// 窗口是滑动区间 [now-window, now)：窗口起止只有这里算，纯函数不取时钟。
	since := time.Now().Add(-time.Duration(cfg.WindowMinutes) * time.Minute)
	used, countErr := s.calls.CountInWindow(ctx, scope, since)
	if countErr != nil {
		log.Printf("统计窗口内 AI 调用次数失败，本次放行（fail-open）: %v", countErr)
		return nil
	}
	decision := decideQuota(cfg, used)
	if decision.Warn {
		// warn 模式：照常调用，只留一条可追溯的告警（不含 Key）。
		log.Printf("AI 模型调用达到软限流阈值，按 warn 动作放行: %s", decision.Reason)
	}
	if decision.Allow {
		return nil
	}
	refusal := newChatError(contract.AIChatRateLimited, 0)
	refusal.Message = decision.Reason + "，请稍后再试或联系管理员调整阈值"
	facts.Err = refusal
	s.recordCall(ctx, *facts) // 上游零请求，但仍要落一条 used_upstream=false 记录
	return refusal
}

// pickModel 取候选并选模：未声明需求走 P0 原方法（逐字节不变），声明后才加载全量做硬筛。
// 这条分叉是全模块唯一"新旧路径"开关，理由见 chat_select.go 的 ChatNeeds.Declared。
func (s *ModelChatService) pickModel(
	ctx context.Context, scope contract.Scope, needs ChatNeeds,
) (contract.AIModelRecord, bool, error) {
	if !needs.Declared() {
		record, fallback, err := s.repo.FindDefaultOrFirstEnabled(ctx, scope)
		if err != nil {
			return contract.AIModelRecord{}, false, newChatError(contract.AIChatConfigMissing, 0)
		}
		return record, fallback, nil
	}
	candidates, err := s.repo.ListEnabled(ctx, scope)
	if err != nil {
		return contract.AIModelRecord{}, false, newChatError(contract.AIChatConfigMissing, 0)
	}
	record, selectErr := selectModel(candidates, needs)
	if selectErr != nil {
		return contract.AIModelRecord{}, false, selectErr
	}
	// 声明了需求就不存在"兜底"语义：命中的模型本身就是最优解。
	return record, false, nil
}

// recordCall 落一条调用事实；写失败只记日志，永不影响调用结果（统计是旁路）。
func (s *ModelChatService) recordCall(ctx context.Context, facts callFacts) {
	if s.calls == nil {
		return
	}
	if err := s.calls.Insert(ctx, buildCallRecord(facts)); err != nil {
		// 统计是旁路：不能因为记账失败让用户丢掉已经花钱拿到的回答。
		log.Printf("写入 AI 模型调用记录失败（不影响本次调用结果）: %v", err)
	}
}

// testOutcome 把一次调用折成实测响应：失败也返回分类结果，成功补摘要与脱敏预览。
func testOutcome(record contract.AIModelRecord, result ChatResult, callErr error) contract.AIModelTestResult {
	out := contract.AIModelTestResult{
		OK: callErr == nil, Category: contract.AIChatBadResponse, ModelID: record.ModelID,
		PromptTokens: result.PromptTokens, CompletionTokens: result.CompletionTokens, LatencyMS: result.LatencyMS,
	}
	var chatErr *ChatError
	if errors.As(callErr, &chatErr) {
		out.Category, out.Message, out.StatusCode = chatErr.Category, chatErr.Message, chatErr.StatusCode
		return out
	}
	if callErr != nil {
		return out // 非 ChatError：保持 response_error 兜底，不猜测更具体的分类
	}
	out.Category, out.Message, out.StatusCode = "ok", "调用成功", http.StatusOK
	out.RequestSummary = requestSummary(record.AIModel)
	out.ResponsePreview = preview(result.Content)
	return out
}

// invoke 负责"取 Key → 解密 → 调 chatOnce"，并把出站账本一并回传供落统计。
// 明文只在本次函数栈内存在：协议层只拿到 string、拿不到 Codec，因而不可能写进日志或 DTO。
func (s *ModelChatService) invoke(
	ctx context.Context, record contract.AIModelRecord, fallback bool, messages []ChatMessage,
) (ChatResult, upstreamCall, error) {
	if record.Ciphertext == "" || record.Nonce == "" || s.codec == nil {
		return ChatResult{}, upstreamCall{}, newChatError(contract.AIChatConfigMissing, 0)
	}
	plain, err := s.codec.Open(record.Ciphertext, record.Nonce)
	if err != nil {
		// 解密失败只说明存量 Key 不可读，不回显密文或底层错误。
		return ChatResult{}, upstreamCall{}, newChatError(contract.AIChatConfigMissing, 0)
	}
	result, call, callErr := chatOnce(ctx, s.client, record, plain, withDefaultProbe(messages))
	result.UsedDefault = record.IsDefault
	result.UsedFallback = fallback
	return result, call, callErr
}

// chatOnce 是全仓唯一 LLM HTTP 出口：按协议构造 → 唯一传输 → 按协议解析。
// 只做四步编排，三个分支各承担一件事，本函数不认识任何协议字段名。
func chatOnce(
	ctx context.Context, client *http.Client, record contract.AIModelRecord, plainKey string,
	messages []ChatMessage,
) (ChatResult, upstreamCall, error) {
	spec := protocolSpecFor(record.Protocol)
	if spec == nil {
		// 历史脏数据里的未知协议值：不 panic、不回显原值，按参数错误上屏。
		return ChatResult{}, upstreamCall{}, newChatError(contract.AIChatBadParameter, 0)
	}
	params := contract.AIModelRequestParams(record.AIModel)
	endpoint, header, body, buildErr := spec.build(record, params, messages, plainKey)
	if buildErr != nil {
		return ChatResult{}, upstreamCall{}, buildErr
	}
	call := upstreamCall{}
	raw, transportErr := transport(ctx, client, endpoint, header, body, params.TimeoutMS, &call)
	if transportErr != nil {
		return ChatResult{}, call, transportErr
	}
	result, parseErr := spec.parse(raw, call, record, plainKey)
	return result, call, parseErr
}

// requestSummary 是实测请求摘要：只列参数，绝不含 Key 与请求体原文。
func requestSummary(model contract.AIModel) string {
	params := contract.AIModelRequestParams(model)
	return fmt.Sprintf("protocol=%s model=%s effort=%s max_tokens=%d temperature=%v top_p=%v timeout_ms=%d",
		model.Protocol, model.ModelID, params.ReasoningEffort, params.MaxTokens,
		params.Temperature, params.TopP, params.TimeoutMS)
}

// preview 截断响应正文，供实测界面展示"上游确实回了话"。
// 脱敏不在这里做：协议层统一经 buildChatResult → modelkey.Redact 处理过，
// 再调一次 Redact 只是重复计算，且会让"脱敏只有一处"这条保证出现第二个入口。
func preview(text string) string {
	runes := []rune(text)
	if len(runes) > maxPreviewRunes {
		return string(runes[:maxPreviewRunes]) + "…"
	}
	return string(runes)
}
