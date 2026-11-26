// build_evict.go · 淘汰的执行器。
//
// 职责：把 PlanRetention 的决策落到 R2 与数据库——删对象、清键、留痕。
// 输入：一个租户的 id。
// 输出：本次淘汰的条数；失败逐条记日志，不中断整批。
// 边界：只删 R2 对象，不删数据库记录——历史版本、下载次数、分享记录全部保留，
//
//	用户仍能看到"曾有过这个版本"，只是文件不再占用对象存储。
//
// 与 deploy 的关系：淘汰是自动的、按容量规则进行的空间回收；
// 人工删除（BuildService.Delete）才是真正抹掉记录。两者不可混用。
package service

import (
	"context"
	"log"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
)

// EvictTenant 对单个租户执行一次淘汰，返回实际淘汰的条数。
//
// 流程刻意分成"算" "删" "标" 三步，且顺序不可交换：
//  1. 用纯函数算出该淘汰谁（可单测、无副作用）；
//  2. 逐个删 R2 对象——只有删除成功的才进入第 3 步，
//     否则标记了却删不掉，会留下"数据库说没有、桶里还在"的幽灵对象；
//  3. 把删除成功的批量标记为已淘汰。
//
// 单条删除失败只跳过该条：一个对象删不掉不该让其余包继续占着空间。
func (s *BuildService) EvictTenant(ctx context.Context, mode contract.SessionMode, tenantID string) (int, error) {
	if s.r2 == nil || tenantID == "" {
		return 0, nil
	}
	repo := s.repos.For(mode)
	rows, err := repo.ListRetentionCandidates(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}

	// 快照转成算法输入；顺带建 id → 行 的索引，供删除时取 r2_key。
	byID := make(map[string]repository.RetentionRow, len(rows))
	candidates := make([]RetentionCandidate, 0, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
		candidates = append(candidates, RetentionCandidate{
			ID:             row.ID,
			Bytes:          row.R2Bytes,
			Pinned:         row.PinKind != "",
			CreatedAt:      row.CreatedAt,
			LastAccessedAt: row.LastAccessedAt,
		})
	}

	plan := PlanRetention(candidates)
	if len(plan.Evict) == 0 {
		return 0, nil
	}

	// 逐个删对象：失败的跳过，成功的收集起来统一标记。
	evicted := make([]string, 0, len(plan.Evict))
	for _, candidate := range plan.Evict {
		row := byID[candidate.ID]
		if err := s.r2.DeleteObject(ctx, row.R2Key); err != nil {
			log.Printf("淘汰对象失败，跳过 id=%s key=%s err=%v", row.ID, row.R2Key, err)
			continue
		}
		evicted = append(evicted, row.ID)
	}
	if len(evicted) == 0 {
		return 0, nil
	}

	if err := repo.MarkEvicted(ctx, evicted); err != nil {
		// 对象已删但标记失败：下一次巡检会因 r2_bytes 仍大于 0 而重试删除，
		// 而 DeleteObject 对不存在的对象视为成功，因此可自愈。
		return 0, err
	}
	log.Printf("淘汰完成 tenant=%s 释放 %d 个对象，共 %.1f MB",
		tenantID, len(evicted), float64(bytesOf(plan.Evict))/(1024*1024))
	return len(evicted), nil
}

// bytesOf 汇总待淘汰对象的字节数，仅用于日志。
func bytesOf(candidates []RetentionCandidate) int64 {
	var total int64
	for _, c := range candidates {
		total += c.Bytes
	}
	return total
}
