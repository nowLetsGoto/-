// interaction_repository_test.go · 交互层仓储的静态契约测试。
// 职责：钉死 team 专属 DDL、审计表复用、ScopeFilter 优先与「无状态写入」的 SQL 边界。
// 输入：schema.sql 与 interaction_repository*.go 的真实源码文本。
// 输出：缺表 / 缺列 / 越权 SQL / 第二套审核表 / 状态写入都会失败。
// 边界：不连数据库、不启动服务；测试解析生产源码，不在测试里另抄一份 SQL 口径。
package rdc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dandelion/server/internal/contract"
)

// readProjectFile 从 repository/rdc 目录定位仓库内文件。
func readProjectFile(t *testing.T, relative string) string {
	t.Helper()
	path := filepath.Join("..", "..", "..", relative)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取文件失败 %s: %v", path, err)
	}
	return string(data)
}

// TestInteractionDDLIsTeamOnlyAndIdempotent 钉死 4 张 team 表、detail 列与幂等语法。
func TestInteractionDDLIsTeamOnlyAndIdempotent(t *testing.T) {
	ddl := readProjectFile(t, "internal/repository/schema.sql")
	for _, table := range []string{
		"team.rdc_diff_review", "team.rdc_hunk_decision",
		"team.rdc_plan_confirmation", "team.rdc_checkpoint",
	} {
		if !strings.Contains(ddl, "CREATE TABLE IF NOT EXISTS "+table) {
			t.Fatalf("DDL 缺少幂等建表 %s", table)
		}
	}
	if !strings.Contains(ddl, "ALTER TABLE team.rdc_review_record ADD COLUMN IF NOT EXISTS detail JSONB") {
		t.Fatal("rdc_review_record 必须以幂等 ALTER 扩 detail JSONB")
	}
	if strings.Contains(ddl, "CREATE TABLE IF NOT EXISTS solo.rdc_") {
		t.Fatal("交互层是 team 专属，不得为 solo 造表")
	}
	// 建表段必须在 FOREACH 循环之外：检查最后一段循环结束标记出现在所有交互表之前。
	loopEnd := strings.Index(ddl, "END LOOP;")
	firstTable := strings.Index(ddl, "CREATE TABLE IF NOT EXISTS team.rdc_diff_review")
	if loopEnd < 0 || firstTable < loopEnd {
		t.Fatal("交互表必须写在 FOREACH 循环之外")
	}
	for _, index := range []string{"idx_rdc_diff_review_subtask", "idx_rdc_hunk_decision_review",
		"idx_rdc_plan_conf_subtask", "idx_rdc_checkpoint_subtask"} {
		if !strings.Contains(ddl, index) {
			t.Fatalf("DDL 缺少索引 %s", index)
		}
	}
	// DDL 不加 CHECK：枚举唯一事实来源是 openapi。
	checkpointStart := strings.Index(ddl, "CREATE TABLE IF NOT EXISTS team.rdc_checkpoint")
	checkpointEnd := strings.Index(ddl[checkpointStart:], "CREATE INDEX")
	if strings.Contains(ddl[checkpointStart:checkpointStart+checkpointEnd], "CHECK (") {
		t.Fatal("交互表不得加 CHECK 约束，枚举唯一来源在 openapi")
	}
}

// TestInteractionRepositoryHasNoStatusWrite 钉死仓储层不写状态、不建第二套审核历史。
func TestInteractionRepositoryHasNoStatusWrite(t *testing.T) {
	sources := []string{
		readProjectFile(t, "internal/repository/rdc/interaction_repository.go"),
		readProjectFile(t, "internal/repository/rdc/interaction_repository_control.go"),
	}
	forbidden := []string{
		"UPDATE rdc_dev_subtask", "rdc_dev_subtask SET status",
		"CREATE TABLE", "rdc_interaction_review_record",
	}
	for _, source := range sources {
		for _, token := range forbidden {
			if strings.Contains(source, token) {
				t.Fatalf("交互仓储出现红线 %q", token)
			}
		}
		// 每条语句都必须先 ScopeFilter 再叠条件；至少每条读路径出现一次。
		if !strings.Contains(source, "repository.ScopeFilter(") {
			t.Fatal("交互仓储查询必须先 ScopeFilter")
		}
	}
}

// TestInteractionEvidenceActionsAreDistinct 钉死证据 action 与状态动作集合完全不重叠。
func TestInteractionEvidenceActionsAreDistinct(t *testing.T) {
	for _, action := range contract.InteractionEvidenceActions {
		if contract.IsValidReviewAction(action) {
			t.Fatalf("证据动作 %s 不得同时是状态动作", action)
		}
	}
	_ = contract.DevAdvance // 状态机仍是 contract 内部实现，本测试只断言集合不重叠。
}

// TestInteractionTablesUsePartitionHelper 钉死表名只从 PartitionTeam.Table 生成。
func TestInteractionTablesUsePartitionHelper(t *testing.T) {
	for _, source := range []string{
		readProjectFile(t, "internal/repository/rdc/interaction_repository.go"),
	} {
		for _, table := range []string{"rdc_diff_review", "rdc_hunk_decision", "rdc_plan_confirmation", "rdc_checkpoint"} {
			if !strings.Contains(source, `partition.Table("`+table+`")`) {
				t.Fatalf("表名 %s 必须由 PartitionTeam.Table 生成", table)
			}
		}
	}
}
