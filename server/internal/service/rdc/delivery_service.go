// Package rdc 负责需求交付的业务编排。
// 职责：基于锁内最新需求/快照验证测试、验收与归档；沙箱就绪前阻断 Git 刷新。
// 输入：scope、需求 ID、当前身份及人工登记输入；输出：追加后的交付快照。
// 边界：不碰 HTTP/SQL/Git argv；归档状态与快照由同一仓储事务提交。
package rdc

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/gitreader"
	rdcrepo "dandelion/server/internal/repository/rdc"
	settingsrepo "dandelion/server/internal/repository/settings"
)

type gitRepoSettings interface {
	GitRepoPath(context.Context, string) (string, error)
}

type gitSnapshotReader interface {
	Read(context.Context, string) contract.GitSnapshot
}

type DeliveryService struct {
	repo     *rdcrepo.DeliveryRepository
	visible  func(context.Context, string, contract.Scope) error
	settings gitRepoSettings
	reader   gitSnapshotReader
}

func NewDeliveryService(repo *rdcrepo.DeliveryRepository, settings *settingsrepo.Repository, reader *gitreader.Reader) *DeliveryService {
	return &DeliveryService{repo: repo, visible: repo.RequirementVisible, settings: settings, reader: reader}
}
func (s *DeliveryService) List(ctx context.Context, q contract.DeliveryQuery, scope contract.Scope) (contract.DeliveryRecordList, error) {
	return s.repo.ListCurrent(ctx, q, scope)
}
func (s *DeliveryService) Detail(ctx context.Context, id string, scope contract.Scope) (contract.DeliveryDetail, error) {
	return s.repo.FindDetail(ctx, id, scope)
}
func (s *DeliveryService) RefreshGit(ctx context.Context, id string, scope contract.Scope) (contract.GitSnapshot, error) {
	if err := s.visible(ctx, id, scope); err != nil {
		return contract.GitSnapshot{}, err
	}
	// 未建立可验证的子进程沙箱前，任何配置路径都不能进入 Git 执行器。
	return contract.GitSnapshot{}, &contract.ValidationError{Message: "Git 读取暂不可用，请联系管理员完成安全隔离配置"}
}
func assertWritable(state *rdcrepo.LockedDelivery) error {
	if state.Status == contract.ReqStatusArchived || state.Current.Archived {
		return &contract.ValidationError{Message: "需求已归档，不可追加交付记录"}
	}
	return nil
}
func (s *DeliveryService) RegisterTest(ctx context.Context, id string, scope contract.Scope, actor contract.Identity, in contract.TestRegisterInput) (contract.DeliveryRecord, error) {
	if in.Result != contract.TestPassed && in.Result != contract.TestFailed {
		return contract.DeliveryRecord{}, &contract.ValidationError{Message: "测试结论无效"}
	}
	if len([]rune(in.Summary)) > contract.MaxTestSummaryLen || strings.TrimSpace(in.Summary) == "" || len([]rune(in.ReportRef)) > contract.MaxTestReportRefLen {
		return contract.DeliveryRecord{}, &contract.ValidationError{Message: "请填写测试摘要，且不要超过字段上限"}
	}
	if in.ReportRef != "" {
		if _, err := contract.NormalizeHTTPURL(in.ReportRef); err != nil {
			return contract.DeliveryRecord{}, &contract.ValidationError{Message: "报告链接需为 http(s) 地址"}
		}
	}
	return s.repo.WithLockedRequirement(ctx, scope, id, func(state *rdcrepo.LockedDelivery) (contract.DeliveryRecord, error) {
		if err := assertWritable(state); err != nil {
			return contract.DeliveryRecord{}, err
		}
		row := state.Current
		row.TestResult = in.Result
		row.TestSummary = strings.TrimSpace(in.Summary)
		row.TestReportRef = strings.TrimSpace(in.ReportRef)
		row.TestRegisteredBy = actor.UserID
		row.TestRegisteredAt = time.Now().UTC().Format(time.RFC3339Nano)
		return state.Append(ctx, scope, actor.UserID, row)
	})
}
func (s *DeliveryService) Accept(ctx context.Context, id string, scope contract.Scope, actor contract.Identity, in contract.DeliveryAcceptInput) (contract.DeliveryRecord, error) {
	if len([]rune(in.Note)) > contract.MaxAcceptanceNoteLen {
		return contract.DeliveryRecord{}, &contract.ValidationError{Message: "验收备注超过上限"}
	}
	return s.repo.WithLockedRequirement(ctx, scope, id, func(state *rdcrepo.LockedDelivery) (contract.DeliveryRecord, error) {
		if err := assertWritable(state); err != nil {
			return contract.DeliveryRecord{}, err
		}
		encoded, err := ValidateAcceptanceItems(state.Criteria, in.Result, in.Items)
		if err != nil {
			return contract.DeliveryRecord{}, err
		}
		row := state.Current
		row.AcceptanceStatus = in.Result
		row.AcceptanceItems = encoded
		row.AcceptanceNote = strings.TrimSpace(in.Note)
		row.AcceptedBy = actor.UserID
		row.AcceptedAt = time.Now().UTC().Format(time.RFC3339Nano)
		return state.Append(ctx, scope, actor.UserID, row)
	})
}
func (s *DeliveryService) Archive(ctx context.Context, id string, scope contract.Scope, actor contract.Identity) (contract.DeliveryRecord, error) {
	return s.repo.WithLockedRequirement(ctx, scope, id, func(state *rdcrepo.LockedDelivery) (contract.DeliveryRecord, error) {
		if err := assertWritable(state); err != nil {
			return contract.DeliveryRecord{}, err
		}
		if state.Current.AcceptanceStatus != contract.AcceptPassed {
			return contract.DeliveryRecord{}, &contract.ValidationError{Message: "验收尚未通过，不能归档"}
		}
		var items []contract.AcceptanceItemResult
		if json.Unmarshal([]byte(state.Current.AcceptanceItems), &items) != nil {
			return contract.DeliveryRecord{}, &contract.ValidationError{Message: "验收条目已变化，请重新验收"}
		}
		if _, err := ValidateAcceptanceItems(state.Criteria, contract.AcceptPassed, items); err != nil {
			return contract.DeliveryRecord{}, err
		}
		row := state.Current
		row.Archived = true
		row.ArchivedBy = actor.UserID
		row.ArchivedAt = time.Now().UTC().Format(time.RFC3339Nano)
		row.RequirementStatus = contract.ReqStatusArchived
		result, err := state.Append(ctx, scope, actor.UserID, row)
		if err != nil {
			return contract.DeliveryRecord{}, err
		}
		if err = state.MarkArchived(ctx, scope); err != nil {
			return contract.DeliveryRecord{}, err
		}
		result.RequirementStatus = contract.ReqStatusArchived
		return result, nil
	})
}
