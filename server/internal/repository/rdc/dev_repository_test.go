// dev_repository_test.go · 研发任务仓储的口径一致性测试。
// 职责：把列表筛选 SQL 的 CASE 序号与 Go 聚合逐组合比对，钉死级联删除 DDL 与条件更新语义。
// 输入：仓储内的真实 SQL CASE 片段、schema.sql、模拟的 sql.Result。
// 输出：两处聚合一致、级联索引齐全、影响 0 行被判为陈旧更新。
// 边界：不连数据库，不启动服务；SQL 片段从生产代码常量解析，避免测试里另抄一份映射。
package rdc

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"dandelion/server/internal/contract"
)

// devStatusCasePattern 只解析生产 SQL 里的 WHEN 'x' THEN n，映射表不在测试里手写。
var devStatusCasePattern = regexp.MustCompile(`WHEN '([a-z_]+)' THEN (-?\d+)`)

// parseDevStatusOrdinals 从真实 SQL CASE 提取状态 → 序号映射。
func parseDevStatusOrdinals(t *testing.T, statement string) map[string]int {
	t.Helper()
	result := make(map[string]int)
	for _, match := range devStatusCasePattern.FindAllStringSubmatch(statement, -1) {
		ordinal, err := strconv.Atoi(match[2])
		if err != nil {
			t.Fatalf("解析 SQL 序号失败 %q: %v", match[2], err)
		}
		result[match[1]] = ordinal
	}
	return result
}

// sqlAggregateOrdinal 模拟 SQL：skipped 无 CASE 分支被 MIN 忽略；
// 两条都被忽略时 min_ord 为 NULL，COALESCE(min_ord, 7) 收敛成 completed。
func sqlAggregateOrdinal(ordinals map[string]int, statuses ...string) int {
	// rejected 的序号是 -1（合法值），不能用 -1 兼作「未找到」哨兵，改用显式 found。
	minOrdinal, found := 0, false
	for _, status := range statuses {
		ordinal, exists := ordinals[status]
		if !exists {
			continue
		}
		if !found || ordinal < minOrdinal {
			minOrdinal, found = ordinal, true
		}
	}
	if !found {
		return 7 // 全 skipped → COALESCE(min_ord, 7) = completed
	}
	return minOrdinal
}

// TestAggregateSQLMatchesGo 遍历 10×10 全部子任务组合，断言 SQL 筛选序号 ==
// contract.AggregateDevStatus 的 Go 序号。任何一处顺序漂移都会被钉死。
func TestAggregateSQLMatchesGo(t *testing.T) {
	ordinals := parseDevStatusOrdinals(t, devStatusOrdinalCase)
	if len(ordinals) != 9 {
		t.Fatalf("SQL CASE 必须覆盖 9 个参与序号的状态（8 进度 + rejected），实际 %d: %v", len(ordinals), ordinals)
	}
	if _, exists := ordinals[contract.DevStatusSkipped]; exists {
		t.Fatal("skipped 不得出现在 SQL CASE 中（必须返回 NULL 被 MIN 忽略）")
	}
	if ordinals[contract.DevStatusRejected] != -1 {
		t.Fatalf("rejected 在 SQL CASE 中必须为 -1（阻断优先），实际 %d", ordinals[contract.DevStatusRejected])
	}
	for _, status := range contract.DevSubtaskStatuses[:8] {
		if ordinals[status] != contract.DevStatusOrdinal(status) {
			t.Fatalf("SQL 序号 %q=%d 与 Go 序号 %d 不一致",
				status, ordinals[status], contract.DevStatusOrdinal(status))
		}
	}

	for _, frontend := range contract.DevSubtaskStatuses {
		for _, backend := range contract.DevSubtaskStatuses {
			sqlOrdinal := sqlAggregateOrdinal(ordinals, frontend, backend)
			overall := contract.AggregateDevStatus([]string{frontend, backend})
			goOrdinal := contract.DevStatusOrdinal(overall)
			if sqlOrdinal != goOrdinal {
				t.Fatalf("组合(%s,%s): SQL 序号=%d，Go 聚合=%s 序号=%d",
					frontend, backend, sqlOrdinal, overall, goOrdinal)
			}
		}
	}
}

// TestCascadeDDLExists 断言子任务外键带 ON DELETE CASCADE 且成对唯一索引存在。
func TestCascadeDDLExists(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "schema.sql"))
	if err != nil {
		t.Fatalf("读取 schema.sql 失败: %v", err)
	}
	ddl := string(data)
	for _, fragment := range []string{
		"CREATE TABLE IF NOT EXISTS team.rdc_dev_task",
		"CREATE TABLE IF NOT EXISTS team.rdc_dev_subtask",
		"task_id    UUID        NOT NULL REFERENCES team.rdc_dev_task(id) ON DELETE CASCADE",
		"uq_rdc_dev_subtask_task_kind",
		"idx_rdc_dev_task_tenant_requirement",
	} {
		if !strings.Contains(ddl, fragment) {
			t.Fatalf("schema.sql 缺少片段: %s", fragment)
		}
	}
}

// fakeResult 是可控的 sql.Result，只提供 RowsAffected 供条件更新测试。
type fakeResult struct {
	affected int64
	err      error
}

func (r fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (r fakeResult) RowsAffected() (int64, error) { return r.affected, r.err }

// TestStatusUpdateAffected 钉死"陈旧 from 影响 0 行"不会被误判为成功。
func TestStatusUpdateAffected(t *testing.T) {
	updated, err := statusUpdateAffected(fakeResult{affected: 1})
	if err != nil || !updated {
		t.Fatalf("影响 1 行应判为成功: updated=%v err=%v", updated, err)
	}
	updated, err = statusUpdateAffected(fakeResult{affected: 0})
	if err != nil || updated {
		t.Fatalf("影响 0 行应判为陈旧更新: updated=%v err=%v", updated, err)
	}
	updated, err = statusUpdateAffected(fakeResult{affected: 1, err: errors.New("boom")})
	if err == nil || updated {
		t.Fatalf("读取影响行数失败时应返回错误: updated=%v err=%v", updated, err)
	}
}

// TestEditStatementsNeverWriteStatus 是编辑安全底线之一：仓储的编辑 SET 子句不含 status。
func TestEditStatementsNeverWriteStatus(t *testing.T) {
	for name, statement := range map[string]string{
		"任务资料":  devTaskUpdateSet,
		"子任务备注": devSubtaskNoteUpdateSet,
	} {
		if strings.Contains(statement, "status") {
			t.Fatalf("%s编辑语句不得写 status: %s", name, statement)
		}
	}
}

// 保证 sql 包在本测试被真实使用（fakeResult 实现其接口）。
var _ sql.Result = fakeResult{}
