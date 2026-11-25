// ai_model_repository_test.go · AI 模型仓储的隔离与 DDL 口径测试。
// 职责：钉死 team 专属表与唯一默认索引、空 scope 失败关闭、列表不读明文、编辑 SET 不含归属列。
// 输入：schema.sql、仓储内的真实 SQL 常量、repository.ScopeFilter 与内存行数据。
// 输出：结构断言全部通过；不连数据库也能发现危险漂移。
// 边界：不启动 PostgreSQL、不解密、不写任何真实 Key。
package rdc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"dandelion/server/internal/contract"
)

func TestAIModelDDLIsTeamOnly(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	ddl := string(data)
	for _, fragment := range []string{
		"CREATE TABLE IF NOT EXISTS team.rdc_ai_model",
		"tenant_id           UUID        REFERENCES public.tenants(id)",
		"api_key_ciphertext  TEXT        NOT NULL DEFAULT ''",
		"idx_rdc_ai_model_tenant_enabled_created",
		"uq_rdc_ai_model_default",
		"ON team.rdc_ai_model (tenant_id) WHERE is_default",
	} {
		if !strings.Contains(ddl, fragment) {
			t.Fatalf("schema.sql 缺少片段: %s", fragment)
		}
	}
	if strings.Contains(strings.Split(ddl, "CREATE TABLE IF NOT EXISTS team.rdc_ai_model")[0], "solo.rdc_ai_model") {
		t.Fatal("AI 模型表不得进 solo 分区")
	}
}

func TestAIModelFilterFailsClosed(t *testing.T) {
	for _, scope := range []contract.Scope{{}, {All: true}} {
		filter := aiModelFilter(0, scope)
		if filter.Cond() != "1 = 0" || len(filter.Args()) != 0 {
			t.Fatalf("空范围必须失败关闭，实际 %q args=%v", filter.Cond(), filter.Args())
		}
	}
	filter := aiModelFilter(0, contract.Scope{TenantID: "11111111-1111-1111-1111-111111111111"})
	if !strings.Contains(filter.Cond(), "m.tenant_id = $1") || len(filter.Args()) != 1 {
		t.Fatalf("租户条件必须最先叠加，实际 %q args=%v", filter.Cond(), filter.Args())
	}
}

func TestAIModelListNeverSelectsPlainKey(t *testing.T) {
	// 列清单里所有 api_key_* 必须属于允许集合：密文、nonce、掩码。
	for _, match := range regexp.MustCompile(`api_key_[a-z_]+`).FindAllString(aiModelColumns, -1) {
		switch match {
		case "api_key_ciphertext", "api_key_nonce", "api_key_mask":
		default:
			t.Fatalf("列表列出现不允许的 Key 字段 %s", match)
		}
	}
	if !strings.Contains(aiModelColumns, "m.api_key_mask") {
		t.Fatal("列表必须返回预算好的掩码列")
	}
}

func TestAIModelUpdateSetExcludesOwnership(t *testing.T) {
	for _, forbidden := range []string{"tenant_id =", "is_default =", " enabled ="} {
		if strings.Contains(aiUpdateSet, forbidden) {
			t.Fatalf("资料编辑 SET 不得包含 %s", forbidden)
		}
	}
	if !strings.Contains(aiUpdateSet, "api_key_ciphertext") || !strings.Contains(aiUpdateSet, "api_key_nonce") {
		t.Fatal("编辑需要能写入新 Key 的三列")
	}
	filter := aiModelFilter(12, contract.Scope{TenantID: "11111111-1111-1111-1111-111111111111"}).Add(`m.id::text = ?`, "x")
	if !strings.Contains(filter.Cond(), "$13") || !strings.Contains(filter.Cond(), "$14") {
		t.Fatalf("SET 占用 12 个参数后 WHERE 必须从 $13 起: %s", filter.Cond())
	}
}

type fakeAIScanner struct {
	values []any
	err    error
}

func (s fakeAIScanner) Scan(dest ...any) error {
	if s.err != nil {
		return s.err
	}
	for i, target := range dest {
		if i >= len(s.values) {
			return os.ErrInvalid
		}
		switch out := target.(type) {
		case *string:
			*out = s.values[i].(string)
		case *bool:
			*out = s.values[i].(bool)
		case *int:
			*out = s.values[i].(int)
		case *time.Time:
			*out = s.values[i].(time.Time)
		}
	}
	return nil
}

func TestScanAIModelMapsMaskOnly(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	scanner := fakeAIScanner{values: []any{
		"id", "name", "scnet", "openai", "https://api.scnet.cn/api/llm/v1", "model-id",
		"sk-****wxyz", true, true, 8192, `{"temperature":1}`, `["文本生成"]`, true, now, now,
		"CIPHER", "NONCE",
	}}
	record, err := scanAIModel(scanner)
	if err != nil {
		t.Fatal(err)
	}
	if record.APIKeyMask != "sk-****wxyz" || record.Ciphertext != "CIPHER" || record.Nonce != "NONCE" {
		t.Fatalf("内部行映射错误: %+v", record)
	}
	if string(record.ExtraConfig) != `{"temperature":1}` || len(record.Capabilities) != 1 {
		t.Fatalf("JSONB 映射错误: %+v", record)
	}
	if record.CreatedAt != "2026-01-02T03:04:05Z" {
		t.Fatalf("时间格式错误: %s", record.CreatedAt)
	}
	encoded, err := json.Marshal(record)
	if err != nil || strings.Contains(string(encoded), "CIPHER") || strings.Contains(string(encoded), "NONCE") {
		t.Fatalf("内部行序列化不得带出密文: %s err=%v", encoded, err)
	}
}
