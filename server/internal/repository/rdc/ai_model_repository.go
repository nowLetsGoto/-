// ai_model_repository.go · AI 模型配置的唯一 SQL 出入口。
// 职责：team 分区内 rdc_ai_model 的分页列表、详情、新增、资料/Key 更新、设默认事务、启停软删与取用选模。
// 输入：service 已校验的 AIModelRecord / id / 分页筛选 / 已投影到单一公司的 scope。
// 输出：contract.AIModelRecord（只含掩码与密文，无明文）与错误（越界统一 404）。
// 边界：固定绑定 team 分区；不做业务校验、不解密、不发 HTTP、不写审计；
//
//	所有查询经 aiModelFilter（ScopeFilter + 空范围失败关闭）后再叠业务条件，Key 三列不进列表 DTO。
package rdc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
)

// AIModelRepository 固定绑定 team 分区；不为用不到的 solo 造第二份仓储。
type AIModelRepository struct {
	db    *sql.DB
	table string
}

// NewAIModelRepository 构造唯一一个 AI 模型仓储，表名只从 PartitionTeam.Table 生成。
func NewAIModelRepository(db *sql.DB) *AIModelRepository {
	return &AIModelRepository{db: db, table: repository.PartitionTeam.Table("rdc_ai_model")}
}

// aiModelColumns 是列表、详情与选模共用的列序；末两列是密文与 nonce，绝不进入 DTO。
const aiModelColumns = `m.id::text, m.name, m.provider, m.protocol, m.base_url, m.model_id,` +
	` m.api_key_mask, m.is_default, m.thinking_enabled, m.context_tokens,` +
	` coalesce(m.extra_config::text, '{}'), coalesce(m.capabilities::text, '[]'),` +
	` m.enabled, m.created_at, m.updated_at, m.api_key_ciphertext, m.api_key_nonce`

type aiScanner interface {
	Scan(dest ...any) error
}

// aiModelFilter 先走 ScopeFilter，再对超管 / 空 tenant 追加恒假条件。
// AI 模型是租户私有配置，即便调用方误传 All，也绝不允许跨公司读写。
func aiModelFilter(base int, scope contract.Scope) *repository.Filter {
	filter := repository.ScopeFilter(base, "m", scope)
	if filter.Cond() == "" {
		filter.AddRaw(`1 = 0`)
	}
	return filter
}

// scanAIModel 把一行映射到内部记录：JSONB 转 RawMessage / 字符串数组，时间统一 UTC RFC3339。
func scanAIModel(scanner aiScanner) (contract.AIModelRecord, error) {
	var record contract.AIModelRecord
	var extraText, capabilitiesText string
	var createdAt, updatedAt time.Time
	err := scanner.Scan(
		&record.ID, &record.Name, &record.Provider, &record.Protocol, &record.BaseURL, &record.ModelID,
		&record.APIKeyMask, &record.IsDefault, &record.ThinkingEnabled, &record.ContextTokens,
		&extraText, &capabilitiesText, &record.Enabled, &createdAt, &updatedAt,
		&record.Ciphertext, &record.Nonce,
	)
	if err != nil {
		return contract.AIModelRecord{}, err
	}
	record.ExtraConfig = json.RawMessage(extraText)
	record.Capabilities = []string{}
	if capabilitiesText != "" {
		_ = json.Unmarshal([]byte(capabilitiesText), &record.Capabilities)
	}
	record.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	record.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return record, nil
}

func aiNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
		return contract.NotFound(contract.ResourceAIModel)
	}
	return err
}

// List 返回 scope 内按启用态筛选的模型分页；默认模型置顶、创建时间倒序。
func (r *AIModelRepository) List(
	ctx context.Context, query contract.AIModelQuery, scope contract.Scope,
) (contract.AIModelList, error) {
	filter := aiModelFilter(0, scope)
	filter.AddIf(query.Enabled != nil, `m.enabled = ?`, query.Enabled != nil && *query.Enabled)
	filter.AddIf(query.Protocol != "", `m.protocol = ?`, query.Protocol)

	var total int
	countSQL := `SELECT count(*) FROM ` + r.table + ` m` + filter.Where()
	if err := r.db.QueryRowContext(ctx, countSQL, filter.Args()...).Scan(&total); err != nil {
		return contract.AIModelList{}, fmt.Errorf("统计 AI 模型数量失败: %w", err)
	}

	args := append(filter.Args(), query.Page.Limit(), query.Page.Offset())
	statement := `SELECT ` + aiModelColumns + ` FROM ` + r.table + ` m` + filter.Where() +
		` ORDER BY m.is_default DESC, m.created_at DESC` +
		fmt.Sprintf(` LIMIT %s OFFSET %s`, filter.Next(0), filter.Next(1))
	rows, err := r.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return contract.AIModelList{}, fmt.Errorf("查询 AI 模型列表失败: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]contract.AIModel, 0)
	for rows.Next() {
		record, err := scanAIModel(rows)
		if err != nil {
			return contract.AIModelList{}, err
		}
		items = append(items, record.AIModel)
	}
	if err := rows.Err(); err != nil {
		return contract.AIModelList{}, err
	}
	return contract.AIModelList{Items: items, Page: contract.Page{
		Page: query.Page.Page, PageSize: query.Page.PageSize, Total: total,
	}}, nil
}

// FindByID 按 id 查一条；越界与不存在同口径 404。
func (r *AIModelRepository) FindByID(ctx context.Context, id string, scope contract.Scope) (contract.AIModelRecord, error) {
	filter := aiModelFilter(0, scope).Add(`m.id::text = ?`, id)
	record, err := scanAIModel(r.db.QueryRowContext(ctx,
		`SELECT `+aiModelColumns+` FROM `+r.table+` m`+filter.Where(), filter.Args()...))
	if err != nil {
		return contract.AIModelRecord{}, aiNotFound(err)
	}
	return record, nil
}

// Create 在同一事务内插行；若 record.IsDefault 为真，紧接着清除本公司旧默认并置新行。
func (r *AIModelRepository) Create(
	ctx context.Context, record contract.AIModelRecord, tenantID string,
) (contract.AIModelRecord, error) {
	if !contract.IsValidUUID(tenantID) {
		return contract.AIModelRecord{}, &contract.ValidationError{Message: "目标公司标识不合法"}
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return contract.AIModelRecord{}, fmt.Errorf("开启 AI 模型创建事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	insert := `INSERT INTO ` + r.table +
		` (tenant_id, name, provider, protocol, base_url, model_id, api_key_ciphertext, api_key_nonce,` +
		` api_key_mask, thinking_enabled, context_tokens, extra_config, capabilities, enabled)` +
		` VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::jsonb, $13::jsonb, true)` +
		` RETURNING id::text`
	var id string
	err = tx.QueryRowContext(ctx, insert, tenantID, record.Name, record.Provider, record.Protocol,
		record.BaseURL, record.ModelID, record.Ciphertext, record.Nonce, record.APIKeyMask,
		record.ThinkingEnabled, record.ContextTokens, string(record.ExtraConfig), string(aiCapabilitiesJSON(record))).Scan(&id)
	if err != nil {
		return contract.AIModelRecord{}, fmt.Errorf("写入 AI 模型失败: %w", err)
	}
	if record.IsDefault {
		if err := r.setDefaultTx(ctx, tx, id, contract.Scope{TenantID: tenantID}); err != nil {
			return contract.AIModelRecord{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return contract.AIModelRecord{}, fmt.Errorf("提交 AI 模型创建事务失败: %w", err)
	}
	return r.FindByID(ctx, id, contract.Scope{TenantID: tenantID})
}

// aiCapabilitiesJSON 把能力标签转成落库 JSON 文本；仓储不修改业务字段，只负责序列化。
func aiCapabilitiesJSON(record contract.AIModelRecord) json.RawMessage {
	encoded, _ := json.Marshal(record.Capabilities)
	return encoded
}

// aiUpdateSet 的 $1..$11 由 SET 占用，ScopeFilter 的 base 必须为 11。
const aiUpdateSet = ` m SET name = $1, provider = $2, protocol = $3, base_url = $4, model_id = $5,` +
	` api_key_ciphertext = $6, api_key_nonce = $7, api_key_mask = $8, thinking_enabled = $9,` +
	` context_tokens = $10, extra_config = $11::jsonb, capabilities = $12::jsonb, updated_at = now()`

// Update 改资料与 Key 三列；is_default / enabled / tenant_id 不在 SET 中，无法经此接口越权。
func (r *AIModelRepository) Update(
	ctx context.Context, id string, record contract.AIModelRecord, scope contract.Scope,
) (contract.AIModelRecord, error) {
	filter := aiModelFilter(12, scope).Add(`m.id::text = ?`, id)
	statement := `UPDATE ` + r.table + aiUpdateSet + ` WHERE ` + filter.Cond() + ` RETURNING m.id::text`
	args := []any{
		record.Name, record.Provider, record.Protocol, record.BaseURL, record.ModelID,
		record.Ciphertext, record.Nonce, record.APIKeyMask, record.ThinkingEnabled,
		record.ContextTokens, string(record.ExtraConfig), string(aiCapabilitiesJSON(record)),
	}
	args = append(args, filter.Args()...)
	var updatedID string
	if err := r.db.QueryRowContext(ctx, statement, args...).Scan(&updatedID); err != nil {
		return contract.AIModelRecord{}, aiNotFound(err)
	}
	return r.FindByID(ctx, updatedID, scope)
}

// UpdateKey 单独更新 Key 三列；保留为明确的 Key 写入口，资料编辑由 Update 一并覆盖同三列。
func (r *AIModelRepository) UpdateKey(
	ctx context.Context, id, ciphertext, nonce, mask string, scope contract.Scope,
) error {
	filter := aiModelFilter(3, scope).Add(`m.id::text = ?`, id)
	statement := `UPDATE ` + r.table +
		` m SET api_key_ciphertext = $1, api_key_nonce = $2, api_key_mask = $3, updated_at = now()` +
		` WHERE ` + filter.Cond() + ` RETURNING m.id::text`
	args := append([]any{ciphertext, nonce, mask}, filter.Args()...)
	var updatedID string
	if err := r.db.QueryRowContext(ctx, statement, args...).Scan(&updatedID); err != nil {
		return aiNotFound(err)
	}
	return nil
}

// setDefaultTx 在同一事务里先清本公司全部默认，再置目标行；目标必须启用且在 scope 内。
func (r *AIModelRepository) setDefaultTx(ctx context.Context, tx *sql.Tx, id string, scope contract.Scope) error {
	clear := aiModelFilter(0, scope)
	clearStatement := `UPDATE ` + r.table + ` m SET is_default = false, updated_at = now() WHERE ` + clear.Cond()
	if _, err := tx.ExecContext(ctx, clearStatement, clear.Args()...); err != nil {
		return fmt.Errorf("清除默认 AI 模型失败: %w", err)
	}
	set := aiModelFilter(0, scope).Add(`m.id::text = ?`, id).AddRaw(`m.enabled = true`)
	setStatement := `UPDATE ` + r.table + ` m SET is_default = true, updated_at = now() WHERE ` +
		set.Cond() + ` RETURNING m.id::text`
	var updatedID string
	if err := tx.QueryRowContext(ctx, setStatement, set.Args()...).Scan(&updatedID); err != nil {
		return aiNotFound(err)
	}
	return nil
}

// SetDefault 事务互斥切换默认模型；影响 0 行（不存在 / 已停用 / 越界）统一 404。
func (r *AIModelRepository) SetDefault(ctx context.Context, id string, scope contract.Scope) (contract.AIModelRecord, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return contract.AIModelRecord{}, fmt.Errorf("开启设默认事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.setDefaultTx(ctx, tx, id, scope); err != nil {
		return contract.AIModelRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return contract.AIModelRecord{}, fmt.Errorf("提交设默认事务失败: %w", err)
	}
	return r.FindByID(ctx, id, scope)
}

// SetEnabled 启停模型；停用时同步清 is_default，保证禁用行恒不为默认。
func (r *AIModelRepository) SetEnabled(
	ctx context.Context, id string, enabled bool, scope contract.Scope,
) (contract.AIModelRecord, error) {
	filter := aiModelFilter(1, scope).Add(`m.id::text = ?`, id)
	statement := `UPDATE ` + r.table +
		` m SET enabled = $1::boolean, is_default = CASE WHEN $1::boolean THEN m.is_default ELSE false END,` +
		` updated_at = now() WHERE ` + filter.Cond() + ` RETURNING m.id::text`
	var updatedID string
	if err := r.db.QueryRowContext(ctx, statement, append([]any{enabled}, filter.Args()...)...).Scan(&updatedID); err != nil {
		return contract.AIModelRecord{}, aiNotFound(err)
	}
	return r.FindByID(ctx, updatedID, scope)
}

// SoftDelete 是 enabled=false 的软删，不物理删行。
func (r *AIModelRepository) SoftDelete(ctx context.Context, id string, scope contract.Scope) error {
	_, err := r.SetEnabled(ctx, id, false, scope)
	return err
}

// FindDefaultOrFirstEnabled 优先取默认，否则取第一个启用模型；usedFallback 标记兜底。
// 与 ListEnabled 共用 aiEnabledOrder，保证"取首个"与"取全量后排序取首个"永远同一个答案。
func (r *AIModelRepository) FindDefaultOrFirstEnabled(
	ctx context.Context, scope contract.Scope,
) (contract.AIModelRecord, bool, error) {
	filter := aiModelFilter(0, scope).AddRaw(`m.enabled = true`)
	statement := `SELECT ` + aiModelColumns + ` FROM ` + r.table + ` m` + filter.Where() + aiEnabledOrder + ` LIMIT 1`
	record, err := scanAIModel(r.db.QueryRowContext(ctx, statement, filter.Args()...))
	if err != nil {
		return contract.AIModelRecord{}, false, aiNotFound(err)
	}
	return record, !record.IsDefault, nil
}

// aiEnabledOrder 是选模排序的唯一 SQL 表达：默认优先，其次最早创建。
// 与 chat_select.go 的 sort.SliceStable 谓词逐字对应——SQL 下推与纯函数必须同序，
// 否则"P0 走 SQL、P1 走纯函数"两条路径会选出不同模型。
const aiEnabledOrder = ` ORDER BY m.is_default DESC, m.created_at ASC`

// ListEnabled 取 scope 内全部启用模型，供能力调度做硬筛。
// 刻意不在 SQL 里下推 context_tokens / capabilities / effort 的匹配条件：
// 筛选规则（`>=` 与 AND 的语义、无匹配时生成的提示文案）必须能脱离数据库单测，
// 且这些规则一旦下推到 SQL 就会与纯函数形成两份实现。这里是本模块唯一故意不优化处，
// 代价是每公司每次 Chat 多取几十行（模型配置天然是几十条量级），换来的是规则只有一份。
func (r *AIModelRepository) ListEnabled(ctx context.Context, scope contract.Scope) ([]contract.AIModelRecord, error) {
	filter := aiModelFilter(0, scope).AddRaw(`m.enabled = true`)
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+aiModelColumns+` FROM `+r.table+` m`+filter.Where()+aiEnabledOrder, filter.Args()...)
	if err != nil {
		return nil, fmt.Errorf("查询启用 AI 模型失败: %w", err)
	}
	defer func() { _ = rows.Close() }()

	records := make([]contract.AIModelRecord, 0)
	for rows.Next() {
		record, scanErr := scanAIModel(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

// HasOtherEnabled 判断除 id 外是否还有启用模型，供停用默认前拦截。
func (r *AIModelRepository) HasOtherEnabled(ctx context.Context, id string, scope contract.Scope) (bool, error) {
	filter := aiModelFilter(0, scope).AddRaw(`m.enabled = true`).Add(`m.id::text <> ?`, id)
	var total int
	if err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM `+r.table+` m`+filter.Where(), filter.Args()...).Scan(&total); err != nil {
		return false, fmt.Errorf("统计其它启用模型失败: %w", err)
	}
	return total > 0, nil
}

// HasEnabled / HasDefault 供首个启用自动设默认与启用后补默认使用。
func (r *AIModelRepository) HasEnabled(ctx context.Context, scope contract.Scope) (bool, error) {
	return r.aiModelCount(ctx, scope, `m.enabled = true`)
}

func (r *AIModelRepository) HasDefault(ctx context.Context, scope contract.Scope) (bool, error) {
	return r.aiModelCount(ctx, scope, `m.is_default = true`)
}

func (r *AIModelRepository) aiModelCount(ctx context.Context, scope contract.Scope, condition string) (bool, error) {
	filter := aiModelFilter(0, scope).AddRaw(condition)
	var total int
	if err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM `+r.table+` m`+filter.Where(), filter.Args()...).Scan(&total); err != nil {
		return false, fmt.Errorf("统计 AI 模型状态失败: %w", err)
	}
	return total > 0, nil
}
