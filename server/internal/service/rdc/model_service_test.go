// model_service_test.go · AI 模型配置业务层的纯逻辑测试。
// 职责：钉住超管代配投影、Key 留空不更新、默认停用拦截、扩展配置越界与跨公司 404。
// 输入：内存 fake 仓储、测试主密钥与本地测试字面量（非真实 Key）。
// 输出：业务规则符合设计；不连数据库、不发网络请求。
// 边界：只覆盖 service 判定；仓储的 SQL 隔离另由仓储测试锁死。
package rdc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/modelkey"
)

const aiTestMasterKey = "01234567890123456789012345678901"

type fakeAIRepo struct {
	items      map[string]contract.AIModelRecord
	listItems  []contract.AIModel
	findScope  contract.Scope
	findErr    error
	other      bool
	enabled    bool
	hasDefault bool
	defaultRow contract.AIModelRecord
	fallback   bool
	created    contract.AIModelRecord
	updated    contract.AIModelRecord
	// enabledItems 供能力调度用例直接给出候选集；为 nil 时回落到包含 defaultRow 的单元素集。
	enabledItems []contract.AIModelRecord
}

func newFakeAIRepo() *fakeAIRepo {
	return &fakeAIRepo{items: map[string]contract.AIModelRecord{}}
}

func (f *fakeAIRepo) List(context.Context, contract.AIModelQuery, contract.Scope) (contract.AIModelList, error) {
	if f.listItems == nil {
		return contract.AIModelList{Items: []contract.AIModel{}}, nil
	}
	return contract.AIModelList{Items: f.listItems}, nil
}
func (f *fakeAIRepo) FindByID(_ context.Context, id string, scope contract.Scope) (contract.AIModelRecord, error) {
	f.findScope = scope
	if f.findErr != nil {
		return contract.AIModelRecord{}, f.findErr
	}
	record, ok := f.items[id]
	if !ok {
		return contract.AIModelRecord{}, contract.NotFound(contract.ResourceAIModel)
	}
	return record, nil
}
func (f *fakeAIRepo) Create(_ context.Context, record contract.AIModelRecord, _ string) (contract.AIModelRecord, error) {
	f.created = record
	return record, nil
}
func (f *fakeAIRepo) Update(_ context.Context, _ string, record contract.AIModelRecord, _ contract.Scope) (contract.AIModelRecord, error) {
	f.updated = record
	return record, nil
}
func (f *fakeAIRepo) UpdateKey(context.Context, string, string, string, string, contract.Scope) error {
	return nil
}
func (f *fakeAIRepo) SetDefault(_ context.Context, _ string, _ contract.Scope) (contract.AIModelRecord, error) {
	return contract.AIModelRecord{}, nil
}
func (f *fakeAIRepo) SetEnabled(_ context.Context, _ string, _ bool, _ contract.Scope) (contract.AIModelRecord, error) {
	return contract.AIModelRecord{}, nil
}
func (f *fakeAIRepo) SoftDelete(context.Context, string, contract.Scope) error { return nil }
func (f *fakeAIRepo) FindDefaultOrFirstEnabled(context.Context, contract.Scope) (contract.AIModelRecord, bool, error) {
	if f.findErr != nil {
		return contract.AIModelRecord{}, false, f.findErr
	}
	return f.defaultRow, f.fallback, nil
}
func (f *fakeAIRepo) ListEnabled(context.Context, contract.Scope) ([]contract.AIModelRecord, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	if f.enabledItems != nil {
		return f.enabledItems, nil
	}
	return []contract.AIModelRecord{f.defaultRow}, nil
}
func (f *fakeAIRepo) HasOtherEnabled(context.Context, string, contract.Scope) (bool, error) {
	return f.other, nil
}
func (f *fakeAIRepo) HasEnabled(context.Context, contract.Scope) (bool, error) { return f.enabled, nil }
func (f *fakeAIRepo) HasDefault(context.Context, contract.Scope) (bool, error) {
	return f.hasDefault, nil
}

func aiService(t *testing.T, repo *fakeAIRepo) *ModelService {
	t.Helper()
	codec, err := modelkey.New([]byte(aiTestMasterKey))
	if err != nil {
		t.Fatal(err)
	}
	return NewModelService(repo, codec)
}

func aiInput() contract.AIModelInput {
	return contract.AIModelInput{
		Name: "测试模型", Provider: contract.AIProviderSCNet, Protocol: contract.AIProtocolOpenAI,
		BaseURL: "https://api.scnet.cn/api/llm/v1", ModelID: "model-x", ContextTokens: 8192,
		ExtraConfig: json.RawMessage(`{}`), Capabilities: []string{},
	}
}

const superScope = "11111111-1111-1111-1111-111111111111"

func TestResolveModelScope(t *testing.T) {
	delegated, err := resolveModelScope(contract.Scope{All: true}, superScope)
	if err != nil || delegated.All || delegated.TenantID != superScope {
		t.Fatalf("超管带合法 tenantId 应投影到该公司: %+v err=%v", delegated, err)
	}
	if _, err := resolveModelScope(contract.Scope{All: true}, ""); !errors.Is(err, contract.ErrAITenantRequired) {
		t.Fatal("超管未指定公司必须返回 ErrAITenantRequired")
	}
	own := contract.Scope{TenantID: superScope, TeamScoped: true}
	got, err := resolveModelScope(own, "22222222-2222-2222-2222-222222222222")
	if err != nil || got.TenantID != superScope {
		t.Fatalf("非超管必须忽略 tenantId: %+v err=%v", got, err)
	}

	// handler 的实测端点走导出方法，口径必须与私有函数完全一致。
	svc := aiService(t, newFakeAIRepo())
	exported, err := svc.ResolveScope(contract.Scope{All: true}, superScope)
	if err != nil || exported.All || exported.TenantID != superScope {
		t.Fatalf("ResolveScope 投影错误: %+v err=%v", exported, err)
	}
	if _, err := svc.ResolveScope(contract.Scope{All: true}, ""); !errors.Is(err, contract.ErrAITenantRequired) {
		t.Fatal("ResolveScope 未指定公司必须返回 ErrAITenantRequired")
	}
}

func TestSuperAdminListWithoutTenantIsEmpty(t *testing.T) {
	svc := aiService(t, newFakeAIRepo())
	data, err := svc.List(context.Background(), contract.Scope{All: true}, "", contract.AIModelQuery{})
	if err != nil || len(data.Items) != 0 || data.Items == nil {
		t.Fatalf("超管未选公司应返回空数组: %+v err=%v", data, err)
	}
}

func TestCreateFirstEnabledBecomesDefaultAndKeyMasked(t *testing.T) {
	repo := newFakeAIRepo()
	svc := aiService(t, repo)
	in := aiInput()
	in.APIKey = "sk-test-not-a-real-key-1234"
	created, err := svc.Create(context.Background(), contract.Scope{TenantID: superScope}, "", in)
	if err != nil {
		t.Fatal(err)
	}
	if !repo.created.IsDefault || repo.created.Ciphertext == "" || repo.created.Nonce == "" {
		t.Fatalf("首个启用模型应成为默认并写入密文: %+v", repo.created)
	}
	if created.APIKeyMask != modelkey.Mask(in.APIKey) || created.APIKeyMask == in.APIKey {
		t.Fatalf("掩码错误或明文回显: %q", created.APIKeyMask)
	}
}

func TestUpdateEmptyKeyKeepsCipherAndMask(t *testing.T) {
	repo := newFakeAIRepo()
	codec, err := modelkey.New([]byte(aiTestMasterKey))
	if err != nil {
		t.Fatal(err)
	}
	cipher, nonce, err := codec.Seal("sk-old-not-a-real-key-1234")
	if err != nil {
		t.Fatal(err)
	}
	repo.items["id"] = contract.AIModelRecord{
		AIModel: contract.AIModel{
			ID: "id", Name: "旧名", Provider: contract.AIProviderSCNet, Protocol: contract.AIProtocolOpenAI,
			BaseURL: "https://api.scnet.cn/api/llm/v1", ModelID: "model-x", APIKeyMask: "sk-****1234",
			ContextTokens: 8192, ExtraConfig: json.RawMessage(`{}`), Enabled: true,
		},
		Ciphertext: cipher, Nonce: nonce,
	}
	svc := aiService(t, repo)
	in := aiInput()
	in.APIKey = ""
	updated, err := svc.Update(context.Background(), "id", contract.Scope{TenantID: superScope}, "", in)
	if err != nil {
		t.Fatal(err)
	}
	if repo.updated.Ciphertext != cipher || repo.updated.Nonce != nonce || updated.APIKeyMask != "sk-****1234" {
		t.Fatalf("留空 Key 不得改写三列: %+v", repo.updated)
	}
}

func TestDisableDefaultWithOtherEnabledRejected(t *testing.T) {
	repo := newFakeAIRepo()
	repo.items["id"] = contract.AIModelRecord{
		AIModel: contract.AIModel{ID: "id", IsDefault: true, Enabled: true},
	}
	repo.other = true
	svc := aiService(t, repo)
	_, err := svc.SetEnabled(context.Background(), "id", false, contract.Scope{TenantID: superScope}, "")
	if err == nil || !strings.Contains(err.Error(), "请先指定新的默认模型") {
		t.Fatalf("默认模型另有启用项时必须拦截: %v", err)
	}
}

func TestInvalidContextAndExtraRejected(t *testing.T) {
	svc := aiService(t, newFakeAIRepo())
	in := aiInput()
	in.ContextTokens = 999999
	if _, err := svc.Create(context.Background(), contract.Scope{TenantID: superScope}, "", in); err == nil {
		t.Fatal("上下文越界必须被拒")
	}
	in = aiInput()
	in.ExtraConfig = json.RawMessage(`{"unknown":1}`)
	if _, err := svc.Create(context.Background(), contract.Scope{TenantID: superScope}, "", in); err == nil {
		t.Fatal("未知扩展字段必须被拒")
	}
}

func TestCrossTenantNotFound(t *testing.T) {
	repo := newFakeAIRepo()
	repo.findErr = contract.NotFound(contract.ResourceAIModel)
	svc := aiService(t, repo)
	_, err := svc.Get(context.Background(), "id", contract.Scope{TenantID: superScope}, "")
	if !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("越界应转 404: %v", err)
	}
	if repo.findScope.TenantID != superScope {
		t.Fatalf("查询必须先落到 scope 的公司: %+v", repo.findScope)
	}
}

// TestReadPathNormalizesLegacyExtraConfig 钉死历史行的读取口径：未知键忽略、缺键补默认，
// 且不原地修改仓储返回的切片 / 记录（避免污染后续调用）。
func TestReadPathNormalizesLegacyExtraConfig(t *testing.T) {
	const legacy = `{"temperature":0.7,"legacy":true}`
	scope := contract.Scope{TenantID: superScope}

	repo := newFakeAIRepo()
	repo.listItems = []contract.AIModel{{
		ID: "list-id", Name: "历史行", ExtraConfig: json.RawMessage(legacy),
	}}
	svc := aiService(t, repo)
	data, err := svc.List(context.Background(), scope, "", contract.AIModelQuery{})
	if err != nil || len(data.Items) != 1 {
		t.Fatalf("列表读取失败: %+v err=%v", data, err)
	}
	assertNormalizedAIExtra(t, data.Items[0].ExtraConfig, 0.7)
	if string(repo.listItems[0].ExtraConfig) != legacy {
		t.Fatal("列表规范化不得原地修改仓储记录")
	}

	repo.items["detail-id"] = contract.AIModelRecord{AIModel: contract.AIModel{
		ID: "detail-id", Name: "历史行", ExtraConfig: json.RawMessage(legacy),
	}}
	detail, err := svc.Get(context.Background(), "detail-id", scope, "")
	if err != nil {
		t.Fatal(err)
	}
	assertNormalizedAIExtra(t, detail.ExtraConfig, 0.7)
	if string(repo.items["detail-id"].ExtraConfig) != legacy {
		t.Fatal("详情规范化不得原地修改仓储记录")
	}
}

func assertNormalizedAIExtra(t *testing.T, raw json.RawMessage, wantTemperature float64) {
	t.Helper()
	var extra map[string]any
	if err := json.Unmarshal(raw, &extra); err != nil {
		t.Fatal(err)
	}
	if _, ok := extra["legacy"]; ok {
		t.Fatalf("未知键必须忽略: %s", raw)
	}
	if extra["temperature"] != wantTemperature || extra["topP"] != float64(1) ||
		extra["reasoningEffort"] != "high" || extra["timeoutMs"] != float64(30000) {
		t.Fatalf("缺省值补全错误: %s", raw)
	}
}
