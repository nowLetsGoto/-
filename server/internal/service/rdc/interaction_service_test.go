// interaction_service_test.go · 交互层业务编排的安全底线测试。
// 职责：钉死三态门禁、风险取最大、回滚哈希两路、越界、provider 未就绪与状态机零写入。
// 输入：内存替身仓储 / provider、contract DTO 与构造的 scope / identity。
// 输出：合法路径断言写入参数与审计组装，非法路径断言 ValidationError / 404 / 409。
// 边界：不连数据库、不起 HTTP 服务、不写真实文件；只覆盖 service 自身分支与源码红线。
package rdc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dandelion/server/internal/contract"
)

// fakeInteractionRepo 是 InteractionRepository 的内存替身，记录所有写调用以便断言。
type fakeInteractionRepo struct {
	review      *contract.DiffReview
	plan        *contract.PlanConfirmation
	checkpoint  *contract.Checkpoint
	checkpoints []contract.Checkpoint
	evidence    []contract.ReviewRecord
	reviewByID  *contract.DiffReview
	planByID    *contract.PlanConfirmation

	savedDecisions []contract.HunkDecision
	submitted      bool
	upserted       []contract.HunkDecision
	confirmed      bool
	markedStatus   string
	appended       []string
	findErr        error
}

func (f *fakeInteractionRepo) FindLatestReview(
	_ context.Context, _, _ string, _ contract.Scope,
) (*contract.DiffReview, error) {
	return f.review, f.findErr
}
func (f *fakeInteractionRepo) FindReviewByID(
	_ context.Context, _ string, _ contract.Scope,
) (*contract.DiffReview, error) {
	if f.reviewByID != nil {
		return f.reviewByID, nil
	}
	return f.review, nil
}
func (f *fakeInteractionRepo) UpsertDecision(
	_ context.Context, _, hunkID, decision, _ string, _ contract.Scope,
) error {
	f.upserted = append(f.upserted, contract.HunkDecision{HunkID: hunkID, Decision: decision})
	return nil
}
func (f *fakeInteractionRepo) SaveDecisionsTx(
	_ context.Context, _ string, decisions []contract.HunkDecision, _ string, _ contract.Scope,
) error {
	f.savedDecisions = decisions
	f.submitted = true
	return nil
}
func (f *fakeInteractionRepo) FindPlan(
	_ context.Context, _, _ string, _ contract.Scope,
) (*contract.PlanConfirmation, error) {
	return f.plan, nil
}
func (f *fakeInteractionRepo) FindPlanByID(
	_ context.Context, _ string, _ contract.Scope,
) (*contract.PlanConfirmation, error) {
	if f.planByID != nil {
		return f.planByID, nil
	}
	return f.plan, nil
}
func (f *fakeInteractionRepo) ConfirmPlanTx(
	_ context.Context, _ string, _ contract.PlanConfirmInput, _ string, _ contract.Scope,
) (contract.PlanConfirmation, error) {
	f.confirmed = true
	result := *f.plan
	result.GateStatus = contract.GateConfirmed
	return result, nil
}
func (f *fakeInteractionRepo) ListCheckpoints(
	_ context.Context, _, _ string, _ contract.Scope,
) ([]contract.Checkpoint, error) {
	return f.checkpoints, nil
}
func (f *fakeInteractionRepo) FindCheckpoint(
	_ context.Context, _ string, _ contract.Scope,
) (*contract.Checkpoint, error) {
	return f.checkpoint, nil
}
func (f *fakeInteractionRepo) MarkRollbackTx(
	_ context.Context, _, status, _ string, _ contract.Scope,
) (contract.Checkpoint, error) {
	f.markedStatus = status
	result := *f.checkpoint
	result.Status = status
	return result, nil
}
func (f *fakeInteractionRepo) AppendEvidence(
	_ context.Context, _, _, action, _, _, _ string, _ map[string]any, _ contract.Scope,
) (contract.ReviewRecord, error) {
	f.appended = append(f.appended, action)
	return contract.ReviewRecord{Action: action}, nil
}
func (f *fakeInteractionRepo) FindEvidence(
	_ context.Context, _, _, _ string, _ contract.Scope,
) ([]contract.ReviewRecord, error) {
	return f.evidence, nil
}

// sampleReview 构造含两条 hunk 的审查：一个高风险、一个低风险，便于门禁与风险取最大同时验证。
func sampleReview() *contract.DiffReview {
	return &contract.DiffReview{
		ID: "review-1", TaskID: "task-1", SubtaskID: "subtask-1",
		Source: contract.ArtifactSourceAgent, SubmitStatus: contract.SubmitPending,
		Files: []contract.FileDiff{{
			FilePath: "src/main.go", FileStatus: contract.FileModified,
			Hunks: []contract.Hunk{
				{HunkID: "h1", OldStart: 1, RiskLabel: "delete_file"},
				{HunkID: "h2", OldStart: 10, RiskLabel: "format"},
			},
		}},
		Decisions: []contract.HunkDecision{},
	}
}

var testScope = contract.Scope{TenantID: "tenant-1"}
var testIdentity = contract.Identity{UserID: "user-1", Username: "tester", Mode: contract.ModeTeam}

// TestSubmitDiffReviewGate 钉死三态门禁：存在 pending 不写任何东西，全表态才写决策 + 一条证据。
func TestSubmitDiffReviewGate(t *testing.T) {
	ctx := context.Background()
	repo := &fakeInteractionRepo{review: sampleReview()}
	svc := NewInteractionService(repo, NewUnavailableProvider())

	_, _, err := svc.SubmitDiffReview(ctx, testScope, "review-1", contract.DiffReviewInput{
		Decisions: []contract.HunkDecision{{HunkID: "h1", Decision: contract.DecisionAccepted}},
	}, testIdentity)
	if err == nil || !strings.Contains(err.Error(), "1 个 hunk 未处理") {
		t.Fatalf("存在 pending 应拒绝提交，实际 err=%v", err)
	}
	if repo.submitted || len(repo.appended) != 0 {
		t.Fatal("门禁未通过时不得写决策或证据")
	}

	review, audit, err := svc.SubmitDiffReview(ctx, testScope, "review-1", contract.DiffReviewInput{
		Decisions: []contract.HunkDecision{
			{HunkID: "h1", Decision: contract.DecisionAccepted},
			{HunkID: "h2", Decision: contract.DecisionRejected},
		},
	}, testIdentity)
	if err != nil {
		t.Fatalf("全表态应放行: %v", err)
	}
	if !repo.submitted || len(repo.savedDecisions) != 2 {
		t.Fatalf("应写 2 条决策，实际 %d", len(repo.savedDecisions))
	}
	if len(repo.appended) != 1 || repo.appended[0] != contract.EvidenceDiffSubmitted {
		t.Fatalf("提交应只写一条 diff_submitted 证据，实际 %v", repo.appended)
	}
	if review.SubmitStatus != contract.SubmitSubmitted || audit.Action != "rdc.interaction.diff_submitted" {
		t.Fatalf("提交态或审计 action 不正确: %+v %+v", review.SubmitStatus, audit)
	}
}

// TestUnavailableProviderNeverFabricates 钉死 Group A 数据源：provider 全部真实失败，读路径返回真实空态。
func TestUnavailableProviderNeverFabricates(t *testing.T) {
	ctx := context.Background()
	provider := NewUnavailableProvider()
	if _, err := provider.LatestDiffReview(ctx, "task", "sub", testScope); !errors.Is(err, ErrArtifactUnavailable) {
		t.Fatalf("LatestDiffReview 应真实报未就绪，实际 %v", err)
	}
	if _, _, err := provider.LatestPlan(ctx, "task", "sub", testScope); !errors.Is(err, ErrArtifactUnavailable) {
		t.Fatalf("LatestPlan 应真实报未就绪，实际 %v", err)
	}
	if _, _, err := provider.Checkpoints(ctx, "task", "sub", testScope); !errors.Is(err, ErrArtifactUnavailable) {
		t.Fatalf("Checkpoints 应真实报未就绪，实际 %v", err)
	}
	if err := provider.ApplyRollback(ctx, contract.Checkpoint{}, testScope); !errors.Is(err, ErrArtifactUnavailable) {
		t.Fatalf("ApplyRollback 应真实报未就绪，实际 %v", err)
	}

	svc := NewInteractionService(&fakeInteractionRepo{}, provider)
	review, err := svc.GetDiffReview(ctx, testScope, "task", "sub")
	if err != nil || review != nil {
		t.Fatalf("无产物应返回真实空态 nil，实际 review=%v err=%v", review, err)
	}
	plan, err := svc.GetPlan(ctx, testScope, "task", "sub")
	if err != nil || plan != nil {
		t.Fatalf("无计划应返回真实空态 nil，实际 plan=%v err=%v", plan, err)
	}
	checkpoints, err := svc.ListCheckpoints(ctx, testScope, "task", "sub")
	if err != nil || len(checkpoints) != 0 {
		t.Fatalf("无检查点应返回空数组，实际 %v err=%v", checkpoints, err)
	}
	approval, err := svc.GetApproval(ctx, testScope, "task", "sub")
	if err != nil || approval != nil {
		t.Fatalf("无审批视图应返回 nil，实际 %v err=%v", approval, err)
	}
}

// TestRollbackHashGate 钉死回滚两路：哈希不匹配 409 + blocked 审计、无证据无状态写；匹配才写证据。
func TestRollbackHashGate(t *testing.T) {
	ctx := context.Background()
	cp := &contract.Checkpoint{
		ID: "cp-1", TaskID: "task-1", SubtaskID: "subtask-1",
		RollbackToken: "token-1", Status: contract.CheckpointActive,
		Files: []contract.FileSnapshot{{
			FilePath: "src/main.go", AfterHash: "hash-after", BeforeHash: "hash-before",
		}},
	}
	repo := &fakeInteractionRepo{checkpoint: cp}
	svc := NewInteractionService(repo, NewUnavailableProvider())

	_, audit, err := svc.Rollback(ctx, testScope, "cp-1", contract.RollbackInput{
		RollbackToken: "token-1", Scope: contract.RollbackCheckpoint,
		CurrentHashes: map[string]string{"src/main.go": "hash-changed"},
	}, testIdentity)
	var conflict *contract.InteractionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("哈希不匹配应返回 409 冲突错误，实际 %v", err)
	}
	if len(conflict.ConflictFiles()) != 1 || conflict.ConflictFiles()[0] != "src/main.go" {
		t.Fatalf("应列出冲突文件，实际 %v", conflict.ConflictFiles())
	}
	if audit.Action != "rdc.interaction.rollback_blocked" || len(repo.appended) != 0 || repo.markedStatus != "" {
		t.Fatalf("禁止回滚时只写 blocked 审计，不得写证据或改状态: %+v %v %q", audit, repo.appended, repo.markedStatus)
	}

	result, audit, err := svc.Rollback(ctx, testScope, "cp-1", contract.RollbackInput{
		RollbackToken: "token-1", Scope: contract.RollbackCheckpoint,
		CurrentHashes: map[string]string{"src/main.go": "hash-after"},
	}, testIdentity)
	if err != nil {
		t.Fatalf("哈希匹配应放行: %v", err)
	}
	if result.Status != contract.CheckpointRolledBack || repo.markedStatus != contract.CheckpointRolledBack {
		t.Fatalf("整检查点回滚状态应为 rolled_back，实际 %q", result.Status)
	}
	// 证据由仓储的 MarkRollbackTx 与状态更新同事务写入；service 这里只断言审计组装与状态标记。
	if audit.Action != "rdc.interaction.checkpoint_rolled_back" || len(repo.appended) != 0 {
		t.Fatalf("service 不应自己写审核记录，证据在同事务的 MarkRollbackTx 内，实际 %+v %v", audit, repo.appended)
	}
}

// TestRollbackTokenAndScope 钉死令牌与粒度校验：无效令牌 / 非 active / 非法粒度都不得写任何东西。
func TestRollbackTokenAndScope(t *testing.T) {
	ctx := context.Background()
	base := &contract.Checkpoint{
		ID: "cp-1", TaskID: "task-1", SubtaskID: "subtask-1",
		RollbackToken: "token-1", Status: contract.CheckpointActive,
	}
	for name, input := range map[string]contract.RollbackInput{
		"wrong token": {RollbackToken: "bad", Scope: contract.RollbackCheckpoint},
		"bad scope":   {RollbackToken: "token-1", Scope: "everything"},
	} {
		t.Run(name, func(t *testing.T) {
			repo := &fakeInteractionRepo{checkpoint: base}
			svc := NewInteractionService(repo, NewUnavailableProvider())
			if _, _, err := svc.Rollback(ctx, testScope, "cp-1", input, testIdentity); err == nil {
				t.Fatal("非法回滚应被拒绝")
			}
			if repo.markedStatus != "" || len(repo.appended) != 0 {
				t.Fatal("非法回滚不得写任何证据或状态")
			}
		})
	}
	rolled := *base
	rolled.Status = contract.CheckpointRolledBack
	repo := &fakeInteractionRepo{checkpoint: &rolled}
	svc := NewInteractionService(repo, NewUnavailableProvider())
	if _, _, err := svc.Rollback(ctx, testScope, "cp-1", contract.RollbackInput{
		RollbackToken: "token-1", Scope: contract.RollbackCheckpoint,
	}, testIdentity); err == nil {
		t.Fatal("已回滚检查点不得重复回滚")
	}
}

// TestApprovalRiskUsesMax 钉死整体风险取最大与介入强度映射，并验证审批证据只写一次。
func TestApprovalRiskUsesMax(t *testing.T) {
	ctx := context.Background()
	review := sampleReview()
	repo := &fakeInteractionRepo{
		review: review,
		checkpoint: &contract.Checkpoint{
			ID: "cp-1", TaskID: "task-1", SubtaskID: "subtask-1",
		},
	}
	svc := NewInteractionService(repo, NewUnavailableProvider())
	view, err := svc.GetApproval(ctx, testScope, "task-1", "subtask-1")
	if err != nil {
		t.Fatalf("读取审批徽标失败: %v", err)
	}
	if view.OverallRisk != contract.RiskHigh || view.Intervention != contract.InterventionStrict || view.Decided {
		t.Fatalf("含 delete_file 的整体风险应为 high/strict/未决定，实际 %+v", view)
	}
	decided, audit, err := svc.DecideApproval(ctx, testScope, "task-1", "subtask-1", contract.ApprovalDecideInput{
		CheckpointID: "cp-1", Decision: contract.ApprovalDecisionApprove,
	}, testIdentity)
	if err != nil {
		t.Fatalf("记录审批决定失败: %v", err)
	}
	if !decided.Decided || decided.Decision != contract.ApprovalDecisionApprove ||
		audit.Action != "rdc.interaction.approval_decided" {
		t.Fatalf("审批徽标或审计不正确: %+v %+v", decided, audit)
	}
	if len(repo.appended) != 1 || repo.appended[0] != contract.EvidenceApprovalDecided {
		t.Fatalf("审批应只写一条 approval_decided 证据，实际 %v", repo.appended)
	}
}

// TestSubmitDiffReviewByTaskUsesTaskSubtask 钉死契约路由口径：按关联键定位最新审查再提交。
func TestSubmitDiffReviewByTaskUsesTaskSubtask(t *testing.T) {
	ctx := context.Background()
	repo := &fakeInteractionRepo{review: sampleReview()}
	svc := NewInteractionService(repo, NewUnavailableProvider())
	review, audit, err := svc.SubmitDiffReviewByTask(ctx, testScope, "task-1", "subtask-1",
		contract.DiffReviewInput{Decisions: []contract.HunkDecision{
			{HunkID: "h1", Decision: contract.DecisionAccepted},
			{HunkID: "h2", Decision: contract.DecisionRejected},
		}}, testIdentity)
	if err != nil {
		t.Fatalf("按 task/subtask 提交失败: %v", err)
	}
	if review.ID != "review-1" || !repo.submitted || audit.Action != "rdc.interaction.diff_submitted" {
		t.Fatalf("提交未走最新审查或未写审计: %+v %v %+v", review, repo.submitted, audit)
	}
}

// TestRollbackRejectsUnknownTarget 钉死哈希绕过防护：目标不在快照里必须 400，且不写任何证据。
func TestRollbackRejectsUnknownTarget(t *testing.T) {
	ctx := context.Background()
	cp := &contract.Checkpoint{
		ID: "cp-1", TaskID: "task-1", SubtaskID: "subtask-1",
		RollbackToken: "token-1", Status: contract.CheckpointActive,
		Files: []contract.FileSnapshot{{FilePath: "src/main.go", AfterHash: "after"}},
	}
	repo := &fakeInteractionRepo{checkpoint: cp}
	svc := NewInteractionService(repo, NewUnavailableProvider())
	_, audit, err := svc.Rollback(ctx, testScope, "cp-1", contract.RollbackInput{
		RollbackToken: "token-1", Scope: contract.RollbackFile,
		Targets:       []contract.RollbackTarget{{FilePath: "bogus.go"}},
		CurrentHashes: map[string]string{},
	}, testIdentity)
	var invalid *contract.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("未知目标应 400 业务拒绝，实际 %v", err)
	}
	if audit.Action != "" || repo.markedStatus != "" || len(repo.appended) != 0 {
		t.Fatalf("非法目标不得写审计、证据或状态: %+v %q %v", audit, repo.markedStatus, repo.appended)
	}
}

// TestRollbackHunkScopeValidatesMapping 钉死按 hunk 回滚时 hunkId 必须属于目标文件且属于该检查点。
func TestRollbackHunkScopeValidatesMapping(t *testing.T) {
	ctx := context.Background()
	review := sampleReview()
	review.CheckpointID = "cp-1"
	cp := &contract.Checkpoint{
		ID: "cp-1", TaskID: "task-1", SubtaskID: "subtask-1",
		RollbackToken: "token-1", Status: contract.CheckpointActive,
		Files: []contract.FileSnapshot{{FilePath: "src/main.go", AfterHash: "after"}},
	}
	svc := NewInteractionService(&fakeInteractionRepo{review: review, checkpoint: cp}, NewUnavailableProvider())

	good := contract.RollbackInput{
		RollbackToken: "token-1", Scope: contract.RollbackHunk,
		Targets:       []contract.RollbackTarget{{FilePath: "src/main.go", HunkID: "h1"}},
		CurrentHashes: map[string]string{"src/main.go": "after"},
	}
	if _, _, err := svc.Rollback(ctx, testScope, "cp-1", good, testIdentity); err != nil {
		t.Fatalf("合法 hunk 映射应放行: %v", err)
	}
	for name, target := range map[string]contract.RollbackTarget{
		"wrong hunk": {FilePath: "src/main.go", HunkID: "unknown"},
		"empty hunk": {FilePath: "src/main.go"},
	} {
		t.Run(name, func(t *testing.T) {
			repo := &fakeInteractionRepo{review: review, checkpoint: cp}
			svc := NewInteractionService(repo, NewUnavailableProvider())
			input := good
			input.Targets = []contract.RollbackTarget{target}
			if _, _, err := svc.Rollback(ctx, testScope, "cp-1", input, testIdentity); err == nil {
				t.Fatal("非法 hunk 映射应被拒绝")
			}
			if repo.markedStatus != "" || len(repo.appended) != 0 {
				t.Fatal("非法 hunk 映射不得写状态或证据")
			}
		})
	}
}

// TestInteractionNeverWritesDevStatus 是安全底线的结构性断言：
// 本任务新增的全部交互源码都不允许出现状态写入 SQL 或 DevService 写状态方法调用。
func TestInteractionNeverWritesDevStatus(t *testing.T) {
	forbidden := []string{
		"rdc_dev_subtask.status", "UPDATE rdc_dev_subtask", "ApplyReviewAction",
		"Advance(", "Skip(", "Resubmit(", "SetStatus(",
	}
	files := []string{
		"artifact_provider.go", "interaction_service.go",
		filepath.Join("..", "..", "repository", "rdc", "interaction_repository.go"),
		filepath.Join("..", "..", "repository", "rdc", "interaction_repository_control.go"),
	}
	for _, name := range files {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("读取交互源码失败 %s: %v", name, err)
		}
		for _, token := range forbidden {
			if strings.Contains(string(data), token) {
				t.Fatalf("%s 出现状态写入红线 %q，交互层不得触碰状态唯一写入口", name, token)
			}
		}
	}
}
