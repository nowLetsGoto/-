// dev_service_test.go · 研发任务业务层的纯逻辑测试。
// 职责：钉死状态机唯一写入口、SKIPPED 不可恢复、陈旧更新、聚合口径、需求换绑越界与字段校验。
// 输入：contract DTO、测试替身仓储 / 需求校验器与构造的 scope。
// 输出：合法路径断言写入参数，非法路径断言 ValidationError / NotFoundError。
// 边界：不连数据库、不起 HTTP 服务、不写审计；只覆盖 service 自身分支。
package rdc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dandelion/server/internal/contract"
)

const (
	devTaskID        = "11111111-1111-4111-8111-111111111111"
	devSubtaskID     = "22222222-2222-4222-8222-222222222222"
	devRequirementID = "33333333-3333-4333-8333-333333333333"
)

// fakeDevRepository 记录状态写入参数，替身的全部行为由字段直接控制。
type fakeDevRepository struct {
	subtask       contract.DevSubtask
	afterSubtask  contract.DevSubtask
	reReadSubtask *contract.DevSubtask
	findCalls     int
	detail        contract.DevTaskDetail
	list          contract.DevTaskList
	writeOK       bool
	updateCalls   int
	writtenFrom   string
	writtenTo     string
	deleted       bool
}

func (f *fakeDevRepository) CreateWithSubtasks(
	_ context.Context, in contract.DevTaskInput, _, _ string,
) (contract.DevTaskDetail, error) {
	return f.detail, nil
}

func (f *fakeDevRepository) List(
	_ context.Context, _ contract.DevTaskQuery, _ contract.Scope,
) (contract.DevTaskList, error) {
	return f.list, nil
}

func (f *fakeDevRepository) FindByID(
	_ context.Context, _ string, _ contract.Scope,
) (contract.DevTaskDetail, error) {
	return f.detail, nil
}

func (f *fakeDevRepository) FindSubtask(
	_ context.Context, _, _ string, _ contract.Scope,
) (contract.DevSubtask, error) {
	f.findCalls++
	// 第二次读取用于回读；模拟另一个请求在此期间把状态推进得更远。
	if f.findCalls >= 2 && f.reReadSubtask != nil {
		return *f.reReadSubtask, nil
	}
	return f.subtask, nil
}

func (f *fakeDevRepository) UpdateTask(
	_ context.Context, _ string, in contract.DevTaskUpdate, _ contract.Scope,
) (contract.DevTaskDetail, error) {
	f.detail.Title = in.Title
	f.detail.RequirementID = in.RequirementID
	return f.detail, nil
}

func (f *fakeDevRepository) UpdateSubtaskNote(
	_ context.Context, _, _, _ string, _ contract.Scope,
) (bool, error) {
	return true, nil
}

func (f *fakeDevRepository) UpdateStatusIf(
	_ context.Context, _, _, from, to string, _ contract.Scope,
) (bool, error) {
	f.updateCalls++
	f.writtenFrom = from
	f.writtenTo = to
	if !f.writeOK {
		return false, nil
	}
	f.subtask = f.afterSubtask
	return true, nil
}

func (f *fakeDevRepository) Delete(_ context.Context, _ string, _ contract.Scope) error {
	f.deleted = true
	return nil
}

// fakeRequirementValidator 用 exists 控制需求可见性，calls 校验是否真的复校过。
type fakeRequirementValidator struct {
	exists bool
	calls  int
}

func (v *fakeRequirementValidator) ExistsVisibleByID(
	_ context.Context, _ string, _ contract.Scope,
) bool {
	v.calls++
	return v.exists
}

func newDevFixture() (*DevService, *fakeDevRepository, *fakeRequirementValidator) {
	repo := &fakeDevRepository{writeOK: true}
	requirements := &fakeRequirementValidator{exists: true}
	return NewDevService(repo, requirements), repo, requirements
}

func TestAdvanceAcceptsEveryLegalEdge(t *testing.T) {
	for from, wantTo := range contract.DevAdvance {
		t.Run(from, func(t *testing.T) {
			svc, repo, _ := newDevFixture()
			repo.subtask = contract.DevSubtask{ID: devSubtaskID, Kind: contract.DevKindFrontend, Status: from}
			repo.afterSubtask = contract.DevSubtask{ID: devSubtaskID, Kind: contract.DevKindFrontend, Status: wantTo}
			got, returnedFrom, err := svc.Advance(context.Background(), devTaskID, devSubtaskID, contract.Scope{})
			if err != nil {
				t.Fatalf("合法转换 %s→%s 不应失败: %v", from, wantTo, err)
			}
			if got.Status != wantTo || returnedFrom != from ||
				repo.writtenFrom != from || repo.writtenTo != wantTo {
				t.Fatalf("推进参数错误: got=%s returnedFrom=%s from=%s to=%s",
					got.Status, returnedFrom, repo.writtenFrom, repo.writtenTo)
			}
			if contract.DevAdvance[returnedFrom] != got.Status {
				t.Fatalf("审计 from→to 必须是相邻边: %s→%s", returnedFrom, got.Status)
			}
		})
	}
}

func TestAdvanceRejectsTerminalAndUnknown(t *testing.T) {
	for _, from := range []string{contract.DevStatusCompleted, contract.DevStatusSkipped, "unknown", ""} {
		svc, repo, _ := newDevFixture()
		repo.subtask = contract.DevSubtask{ID: devSubtaskID, Status: from}
		_, returnedFrom, err := svc.Advance(context.Background(), devTaskID, devSubtaskID, contract.Scope{})
		if returnedFrom != "" {
			t.Fatalf("非法转换不得返回审计 from，实际 %q", returnedFrom)
		}
		var invalid *contract.ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("状态 %q 推进应被拒绝，实际 %T: %v", from, err, err)
		}
		if repo.updateCalls != 0 {
			t.Fatalf("非法转换不得触发状态写入: from=%s", from)
		}
	}
}

func TestSkipAllowedOnlyBeforeTerminal(t *testing.T) {
	for _, status := range contract.DevSubtaskStatuses[:7] {
		svc, repo, _ := newDevFixture()
		repo.subtask = contract.DevSubtask{ID: devSubtaskID, Status: status}
		repo.afterSubtask = contract.DevSubtask{ID: devSubtaskID, Status: contract.DevStatusSkipped}
		got, returnedFrom, err := svc.Skip(context.Background(), devTaskID, devSubtaskID, contract.Scope{})
		if err != nil || got.Status != contract.DevStatusSkipped || repo.writtenTo != contract.DevStatusSkipped ||
			returnedFrom != status {
			t.Fatalf("未完成态 %q 应可标跳过: got=%+v from=%s err=%v", status, got, returnedFrom, err)
		}
	}
	for _, status := range []string{contract.DevStatusCompleted, contract.DevStatusSkipped} {
		svc, repo, _ := newDevFixture()
		repo.subtask = contract.DevSubtask{ID: devSubtaskID, Status: status}
		_, returnedFrom, err := svc.Skip(context.Background(), devTaskID, devSubtaskID, contract.Scope{})
		if returnedFrom != "" {
			t.Fatalf("终态跳过不得返回审计 from，实际 %q", returnedFrom)
		}
		var invalid *contract.ValidationError
		if !errors.As(err, &invalid) || repo.updateCalls != 0 {
			t.Fatalf("终态 %q 不得再标跳过: err=%v calls=%d", status, err, repo.updateCalls)
		}
	}
}

// TestAdvanceAuditPairStaysAdjacentUnderConcurrentReread 是并发审计回归：
// 即使条件更新成功后，另一个请求在回读前把状态推进到更远态，
// 本次返回的 from→to 仍必须是本次实际写入的相邻边，不能拼出状态机里不存在的跳级对。
func TestAdvanceAuditPairStaysAdjacentUnderConcurrentReread(t *testing.T) {
	svc, repo, _ := newDevFixture()
	repo.subtask = contract.DevSubtask{
		ID: devSubtaskID, Kind: contract.DevKindFrontend, Status: contract.DevStatusPending,
	}
	repo.afterSubtask = contract.DevSubtask{
		ID: devSubtaskID, Kind: contract.DevKindFrontend, Status: contract.DevStatusAIRunning,
	}
	later := contract.DevSubtask{
		ID: devSubtaskID, Kind: contract.DevKindFrontend, Status: contract.DevStatusReviewing,
	}
	repo.reReadSubtask = &later

	got, from, err := svc.Advance(context.Background(), devTaskID, devSubtaskID, contract.Scope{})
	if err != nil {
		t.Fatalf("本次条件更新应成功: %v", err)
	}
	if from != contract.DevStatusPending || got.Status != contract.DevStatusAIRunning {
		t.Fatalf("响应应描述本次写入的 from→to，实际 %s→%s", from, got.Status)
	}
	if contract.DevAdvance[from] != got.Status {
		t.Fatalf("审计 from→to 必须是相邻边，实际 %s→%s", from, got.Status)
	}
}

func TestWriteStatusRejectsStaleUpdate(t *testing.T) {
	svc, repo, _ := newDevFixture()
	repo.writeOK = false
	repo.subtask = contract.DevSubtask{ID: devSubtaskID, Status: contract.DevStatusPending}
	_, returnedFrom, err := svc.Advance(context.Background(), devTaskID, devSubtaskID, contract.Scope{})
	if returnedFrom != "" {
		t.Fatalf("陈旧更新不得返回审计 from，实际 %q", returnedFrom)
	}
	var invalid *contract.ValidationError
	if !errors.As(err, &invalid) || !strings.Contains(invalid.Message, "刷新") {
		t.Fatalf("陈旧更新应要求刷新后重试，实际 %v", err)
	}
}

func TestAggregateAllSubtaskCombinations(t *testing.T) {
	for _, frontend := range contract.DevSubtaskStatuses {
		for _, backend := range contract.DevSubtaskStatuses {
			got := overallStatus(frontend, backend)
			want := contract.AggregateDevStatus([]string{frontend, backend})
			if got != want {
				t.Fatalf("组合(%s,%s) 聚合=%s 期望=%s", frontend, backend, got, want)
			}
		}
	}
	if overallStatus(contract.DevStatusSkipped, contract.DevStatusSkipped) != contract.DevStatusCompleted {
		t.Fatal("两条都跳过应视为 completed")
	}
}

func TestListFillsOverallStatusForEveryRow(t *testing.T) {
	svc, repo, _ := newDevFixture()
	repo.list = contract.DevTaskList{Items: []contract.DevTask{
		{ID: devTaskID, FrontendStatus: contract.DevStatusSkipped, BackendStatus: contract.DevStatusValidated},
		{ID: devSubtaskID, FrontendStatus: contract.DevStatusSkipped, BackendStatus: contract.DevStatusSkipped},
	}}
	got, err := svc.List(context.Background(), contract.DevTaskQuery{}, contract.Scope{})
	if err != nil {
		t.Fatalf("列表不应失败: %v", err)
	}
	if got.Items[0].OverallStatus != contract.DevStatusValidated ||
		got.Items[1].OverallStatus != contract.DevStatusCompleted {
		t.Fatalf("列表整体状态未正确聚合: %+v", got.Items)
	}
}

func TestListRejectsSkippedFilter(t *testing.T) {
	svc, _, _ := newDevFixture()
	_, err := svc.List(context.Background(),
		contract.DevTaskQuery{Status: contract.DevStatusSkipped}, contract.Scope{})
	var invalid *contract.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("skipped 不是合法整体态，应返回 ValidationError，实际 %v", err)
	}
}

func TestRequirementRebindOutOfScope(t *testing.T) {
	svc, _, requirements := newDevFixture()
	requirements.exists = false
	_, err := svc.Update(context.Background(), devTaskID, contract.DevTaskUpdate{
		Title: "合法标题", RequirementID: devRequirementID,
	}, contract.Scope{TenantID: "company-a"})
	var invalid *contract.ValidationError
	if !errors.As(err, &invalid) || requirements.calls != 1 {
		t.Fatalf("换绑越界必须拒绝且复校一次: err=%v calls=%d", err, requirements.calls)
	}
}

func TestCreateValidatesRequirementAndFields(t *testing.T) {
	svc, _, requirements := newDevFixture()
	requirements.exists = false
	caller := contract.Identity{UserID: "user-1"}
	_, err := svc.Create(context.Background(), caller, contract.Scope{}, contract.DevTaskInput{
		Title: "任务", RequirementID: devRequirementID,
	})
	var invalid *contract.ValidationError
	if !errors.As(err, &invalid) || !strings.Contains(invalid.Message, "需求") {
		t.Fatalf("来源需求不可见应拒绝: err=%v", err)
	}

	requirements.exists = true
	for _, input := range []contract.DevTaskInput{
		{Title: "", RequirementID: devRequirementID},
		{Title: strings.Repeat("字", contract.MaxDevTitleLen+1), RequirementID: devRequirementID},
		{Title: "任务", RequirementID: "not-a-uuid"},
	} {
		if _, err := svc.Create(context.Background(), caller, contract.Scope{}, input); err == nil {
			t.Fatalf("非法输入应被拒绝: %+v", input)
		}
	}
}

func TestGetAndDeleteRejectInvalidID(t *testing.T) {
	svc, repo, _ := newDevFixture()
	_, err := svc.Get(context.Background(), "bad-id", contract.Scope{})
	var missing *contract.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("非法 id 详情应 404，实际 %v", err)
	}
	if err := svc.Delete(context.Background(), "bad-id", contract.Scope{}); !errors.As(err, &missing) || repo.deleted {
		t.Fatalf("非法 id 删除应 404 且不得写库，实际 err=%v deleted=%v", err, repo.deleted)
	}
}

func TestNormalizeDevNotesRejectsOversize(t *testing.T) {
	_, err := normalizeDevNotes([]contract.DevSubtaskNoteUpdate{{
		ID: devSubtaskID, Note: strings.Repeat("字", contract.MaxDevNoteLen+1),
	}})
	var invalid *contract.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("超长备注应被拒绝，实际 %v", err)
	}
	notes, err := normalizeDevNotes([]contract.DevSubtaskNoteUpdate{{
		ID: devSubtaskID, Note: "  ",
	}})
	if err != nil || len(notes) != 1 || notes[0].Note != "" {
		t.Fatalf("空白备注应归一成空串: notes=%+v err=%v", notes, err)
	}
}
