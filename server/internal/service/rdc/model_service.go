// model_service.go · AI 模型配置业务层。
// 职责：超管代配的 scope 投影、字段/URL/扩展配置校验、Key 加密编排、默认模型联动与审计事实组装。
// 输入：handler 传入的契约 DTO、调用者 scope、查询参数 tenantId（仅超管有效）。
// 输出：只含掩码的 contract.AIModel / AIModelList，或可预期业务错误。
// 边界：不碰 *http.Request / ResponseWriter、不写 SQL、不调用 LLM；Key 明文只在 Seal 调用处短暂出现。
package rdc

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/modelkey"
)

// AIModelRepository 是配置业务与 Chat 共用的最小仓储契约，便于纯逻辑单测注入 fake。
// 所有方法都要求 scope 已投影到单一公司；跨公司由仓储再兜一层 404。
type AIModelRepository interface {
	List(ctx context.Context, query contract.AIModelQuery, scope contract.Scope) (contract.AIModelList, error)
	FindByID(ctx context.Context, id string, scope contract.Scope) (contract.AIModelRecord, error)
	Create(ctx context.Context, record contract.AIModelRecord, tenantID string) (contract.AIModelRecord, error)
	Update(ctx context.Context, id string, record contract.AIModelRecord, scope contract.Scope) (contract.AIModelRecord, error)
	UpdateKey(ctx context.Context, id, ciphertext, nonce, mask string, scope contract.Scope) error
	SetDefault(ctx context.Context, id string, scope contract.Scope) (contract.AIModelRecord, error)
	SetEnabled(ctx context.Context, id string, enabled bool, scope contract.Scope) (contract.AIModelRecord, error)
	SoftDelete(ctx context.Context, id string, scope contract.Scope) error
	FindDefaultOrFirstEnabled(ctx context.Context, scope contract.Scope) (contract.AIModelRecord, bool, error)
	// ListEnabled 供能力调度取候选全量（同租户 + enabled，已按 is_default DESC, created_at ASC 排序）。
	ListEnabled(ctx context.Context, scope contract.Scope) ([]contract.AIModelRecord, error)
	HasOtherEnabled(ctx context.Context, id string, scope contract.Scope) (bool, error)
	HasEnabled(ctx context.Context, scope contract.Scope) (bool, error)
	HasDefault(ctx context.Context, scope contract.Scope) (bool, error)
}

// ModelService 编排模型配置业务；codec 只负责加密，业务层看不到主密钥。
type ModelService struct {
	repo  AIModelRepository
	codec *modelkey.Codec
}

// NewModelService 构造配置服务；codec 必须由 main 用 config 主密钥构造成功后注入。
func NewModelService(repo AIModelRepository, codec *modelkey.Codec) *ModelService {
	return &ModelService{repo: repo, codec: codec}
}

// resolveModelScope 把超管的 ?tenantId= 投影为单一公司范围。
// 非超管永远忽略该参数；超管未指定或非法时返回 ErrAITenantRequired。
func resolveModelScope(scope contract.Scope, tenantID string) (contract.Scope, error) {
	if !scope.All {
		return scope, nil
	}
	if !contract.IsValidUUID(tenantID) {
		return scope, contract.ErrAITenantRequired
	}
	return contract.Scope{TenantID: tenantID}, nil
}

// ResolveScope 暴露给 handler 用于「实测」等不走配置读写方法的写操作：
// 与 List/Create 共用同一投影口径，避免超管 All 范围漏过 tenantId 直接落到仓储。
func (s *ModelService) ResolveScope(scope contract.Scope, tenantID string) (contract.Scope, error) {
	return resolveModelScope(scope, tenantID)
}

// validateAIModelInput 做字段级校验并补默认值，返回规范化后的入站 DTO（apiKey 原样保留）。
func validateAIModelInput(in contract.AIModelInput) (contract.AIModelInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Provider = strings.TrimSpace(in.Provider)
	in.Protocol = strings.TrimSpace(in.Protocol)
	in.ModelID = strings.TrimSpace(in.ModelID)
	if in.Name == "" || len([]rune(in.Name)) > contract.MaxAINameLen {
		return contract.AIModelInput{}, &contract.ValidationError{Message: "模型名称必填且不得超过 100 字"}
	}
	if !slices.Contains(contract.AIProviders, in.Provider) {
		return contract.AIModelInput{}, &contract.ValidationError{Message: "供应商不受支持"}
	}
	if !slices.Contains(contract.AIProtocols, in.Protocol) {
		return contract.AIModelInput{}, &contract.ValidationError{Message: "协议不在支持范围"}
	}
	baseURL, err := contract.NormalizeAIBaseURL(in.BaseURL)
	if err != nil {
		return contract.AIModelInput{}, err
	}
	in.BaseURL = baseURL
	if in.ModelID == "" || len(in.ModelID) > contract.MaxAIModelIDLen {
		return contract.AIModelInput{}, &contract.ValidationError{Message: "模型标识必填且不得超过 100 字"}
	}
	if in.ContextTokens == 0 {
		in.ContextTokens = contract.AIDefaultContextTokens
	}
	if in.ContextTokens < contract.AIMinContextTokens || in.ContextTokens > contract.AIMaxContextTokens {
		return contract.AIModelInput{}, &contract.ValidationError{Message: "上下文长度必须在 1024-393216 之间"}
	}
	extra, err := contract.ValidateAIExtraConfig(in.ExtraConfig, in.Protocol)
	if err != nil {
		return contract.AIModelInput{}, err
	}
	in.ExtraConfig = extra
	if in.Capabilities == nil {
		in.Capabilities = []string{}
	}
	if len(in.Capabilities) > contract.MaxAICapabilities {
		return contract.AIModelInput{}, &contract.ValidationError{Message: "能力标签最多 16 个"}
	}
	for _, capability := range in.Capabilities {
		if len([]rune(capability)) > 50 {
			return contract.AIModelInput{}, &contract.ValidationError{Message: "单个能力标签不得超过 50 字"}
		}
	}
	return in, nil
}

// sealKey 只在用户确实填了新 Key 时调用；返回的密文 / nonce / 掩码进入仓储，明文随即离开作用域。
func (s *ModelService) sealKey(plain string) (string, string, string, error) {
	if plain == "" {
		return "", "", "", nil
	}
	if s.codec == nil {
		return "", "", "", fmt.Errorf("模型加密器未初始化")
	}
	ciphertext, nonce, err := s.codec.Seal(plain)
	if err != nil {
		return "", "", "", fmt.Errorf("加密模型 Key 失败")
	}
	return ciphertext, nonce, modelkey.Mask(plain), nil
}

// normalizeAIModelExtra 把历史 / 外部写入的 extra_config 按读取口径规范化：
// 忽略未知键、补默认值。返回拷贝，不原地修改仓储持有的记录。
func normalizeAIModelExtra(model contract.AIModel) contract.AIModel {
	model.ExtraConfig = contract.NormalizeAIExtraConfig(model.ExtraConfig)
	return model
}

// List 返回 scope 内列表；超管未指定目标公司时返回空页，绝不默认展示某家公司的 Key 配置。
func (s *ModelService) List(
	ctx context.Context, scope contract.Scope, tenantID string, query contract.AIModelQuery,
) (contract.AIModelList, error) {
	resolved, err := resolveModelScope(scope, tenantID)
	if errors.Is(err, contract.ErrAITenantRequired) {
		return contract.AIModelList{
			Items: []contract.AIModel{},
			Page:  contract.Page{Page: query.Page.Page, PageSize: query.Page.PageSize},
		}, nil
	}
	if err != nil {
		return contract.AIModelList{}, err
	}
	data, err := s.repo.List(ctx, query, resolved)
	if err != nil {
		return contract.AIModelList{}, err
	}
	if data.Items == nil {
		data.Items = []contract.AIModel{}
	}
	// 仓储是 extra_config::text 原样映射；读路径必须在这里补默认值、丢未知键。
	items := make([]contract.AIModel, len(data.Items))
	for index, item := range data.Items {
		items[index] = normalizeAIModelExtra(item)
	}
	data.Items = items
	return data, nil
}

// Get 按 id 读取一条；跨公司与不存在同为 404。
func (s *ModelService) Get(ctx context.Context, id string, scope contract.Scope, tenantID string) (contract.AIModel, error) {
	resolved, err := resolveModelScope(scope, tenantID)
	if err != nil {
		return contract.AIModel{}, err
	}
	record, err := s.repo.FindByID(ctx, id, resolved)
	if err != nil {
		return contract.AIModel{}, err
	}
	return normalizeAIModelExtra(record.AIModel), nil
}

// Create 新建模型；该公司首个启用模型自动成为默认，Key 留空则三列保持空。
func (s *ModelService) Create(
	ctx context.Context, scope contract.Scope, tenantID string, in contract.AIModelInput,
) (contract.AIModel, error) {
	resolved, err := resolveModelScope(scope, tenantID)
	if err != nil {
		return contract.AIModel{}, err
	}
	validated, err := validateAIModelInput(in)
	if err != nil {
		return contract.AIModel{}, err
	}
	ciphertext, nonce, mask, err := s.sealKey(in.APIKey)
	if err != nil {
		return contract.AIModel{}, err
	}
	hasEnabled, err := s.repo.HasEnabled(ctx, resolved)
	if err != nil {
		return contract.AIModel{}, err
	}
	record := contract.AIModelRecord{
		AIModel: contract.AIModel{
			Name: validated.Name, Provider: validated.Provider, Protocol: validated.Protocol,
			BaseURL: validated.BaseURL, ModelID: validated.ModelID, APIKeyMask: mask,
			IsDefault: !hasEnabled, ThinkingEnabled: validated.ThinkingEnabled,
			ContextTokens: validated.ContextTokens,
			ExtraConfig:   contract.NormalizeAIExtraConfig(validated.ExtraConfig),
			Capabilities:  validated.Capabilities, Enabled: true,
		},
		Ciphertext: ciphertext, Nonce: nonce,
	}
	created, err := s.repo.Create(ctx, record, resolved.TenantID)
	if err != nil {
		return contract.AIModel{}, err
	}
	return created.AIModel, nil
}

// Update 改资料；APIKey 为空时沿用原密文 / nonce / 掩码，绝不把掩码当 Key 回写。
func (s *ModelService) Update(
	ctx context.Context, id string, scope contract.Scope, tenantID string, in contract.AIModelInput,
) (contract.AIModel, error) {
	resolved, err := resolveModelScope(scope, tenantID)
	if err != nil {
		return contract.AIModel{}, err
	}
	existing, err := s.repo.FindByID(ctx, id, resolved)
	if err != nil {
		return contract.AIModel{}, err
	}
	validated, err := validateAIModelInput(in)
	if err != nil {
		return contract.AIModel{}, err
	}
	ciphertext, nonce, mask := existing.Ciphertext, existing.Nonce, existing.APIKeyMask
	if in.APIKey != "" {
		ciphertext, nonce, mask, err = s.sealKey(in.APIKey)
		if err != nil {
			return contract.AIModel{}, err
		}
	}
	record := existing
	record.Name = validated.Name
	record.Provider = validated.Provider
	record.Protocol = validated.Protocol
	record.BaseURL = validated.BaseURL
	record.ModelID = validated.ModelID
	record.ThinkingEnabled = validated.ThinkingEnabled
	record.ContextTokens = validated.ContextTokens
	record.ExtraConfig = contract.NormalizeAIExtraConfig(validated.ExtraConfig)
	record.Capabilities = validated.Capabilities
	record.APIKeyMask = mask
	record.Ciphertext = ciphertext
	record.Nonce = nonce
	updated, err := s.repo.Update(ctx, id, record, resolved)
	if err != nil {
		return contract.AIModel{}, err
	}
	return updated.AIModel, nil
}

// SetDefault 事务互斥切换默认模型，目标必须启用且可见。
func (s *ModelService) SetDefault(
	ctx context.Context, id string, scope contract.Scope, tenantID string,
) (contract.AIModel, error) {
	resolved, err := resolveModelScope(scope, tenantID)
	if err != nil {
		return contract.AIModel{}, err
	}
	record, err := s.repo.SetDefault(ctx, id, resolved)
	if err != nil {
		return contract.AIModel{}, err
	}
	return record.AIModel, nil
}

// SetEnabled 启停模型：停用默认且另有启用行时拒绝；启用后若该公司无默认则自动补默认。
func (s *ModelService) SetEnabled(
	ctx context.Context, id string, enabled bool, scope contract.Scope, tenantID string,
) (contract.AIModel, error) {
	resolved, err := resolveModelScope(scope, tenantID)
	if err != nil {
		return contract.AIModel{}, err
	}
	record, err := s.repo.FindByID(ctx, id, resolved)
	if err != nil {
		return contract.AIModel{}, err
	}
	if record.IsDefault && !enabled {
		other, err := s.repo.HasOtherEnabled(ctx, id, resolved)
		if err != nil {
			return contract.AIModel{}, err
		}
		if other {
			return contract.AIModel{}, &contract.ValidationError{Message: "请先指定新的默认模型"}
		}
	}
	updated, err := s.repo.SetEnabled(ctx, id, enabled, resolved)
	if err != nil {
		return contract.AIModel{}, err
	}
	if enabled && !updated.IsDefault {
		hasDefault, err := s.repo.HasDefault(ctx, resolved)
		if err != nil {
			return contract.AIModel{}, err
		}
		if !hasDefault {
			updated, err = s.repo.SetDefault(ctx, id, resolved)
			if err != nil {
				return contract.AIModel{}, err
			}
		}
	}
	return updated.AIModel, nil
}

// SoftDelete 是可预期的"删除"：enabled=false，默认联动与停用一致；行仍可用于审计追溯。
func (s *ModelService) SoftDelete(ctx context.Context, id string, scope contract.Scope, tenantID string) error {
	resolved, err := resolveModelScope(scope, tenantID)
	if err != nil {
		return err
	}
	record, err := s.repo.FindByID(ctx, id, resolved)
	if err != nil {
		return err
	}
	if record.IsDefault {
		other, err := s.repo.HasOtherEnabled(ctx, id, resolved)
		if err != nil {
			return err
		}
		if other {
			return &contract.ValidationError{Message: "请先指定新的默认模型"}
		}
	}
	return s.repo.SoftDelete(ctx, id, resolved)
}
