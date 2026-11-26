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
	"os"

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

	// 逐个删对象：删之前必须确认本机有副本，否则包会彻底消失。
	evicted := make([]string, 0, len(plan.Evict))
	for _, candidate := range plan.Evict {
		row := byID[candidate.ID]

		// 安全闸：R2 对象一旦删除就不可恢复。若本机也没有副本，
		// 这个包将既无法下载、也无处重建，而数据库里还留着一条指向空文件的记录。
		// 因此缺副本时先把它拉回本机，拉不回来就跳过本轮——
		// 宁可让它继续占着 R2 空间，也不能让它变成永久丢失的数据。
		if !s.ensureLocalCopy(ctx, mode, row) {
			continue
		}

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

// ensureLocalCopy 确认包在本机有副本；没有则尝试拉回，返回是否可安全删除 R2 对象。
//
// 这是淘汰的安全闸。判据是「文件确实存在且大小相符」，而不是看 local_state：
// 状态可能因为上次中途崩溃而停在 pulling，也可能因手工清理磁盘而与事实不符。
// 只有真正 stat 到文件，才敢删掉 R2 那份唯一的远程副本。
//
// 拉回失败返回 false，调用方据此跳过本轮淘汰：那个包会继续占着 R2 空间，
// 但这远好过把它变成一份谁也拿不到的数据。
func (s *BuildService) ensureLocalCopy(ctx context.Context, mode contract.SessionMode, row repository.RetentionRow) bool {
	abs, err := s.paths.SafeResolve(row.RelativePath)
	if err != nil {
		log.Printf("淘汰跳过：落盘路径不合法 id=%s rel=%s", row.ID, row.RelativePath)
		return false
	}
	if info, statErr := os.Stat(abs); statErr == nil && info.Size() == row.FileSize {
		return true // 已有合格副本，可以直接删 R2
	}

	log.Printf("淘汰前需先留底 id=%s（本机无合格副本，开始拉回）", row.ID)
	s.PullLocal(ctx, mode, row.ID)

	// 拉回是否成功要看文件，不看状态：PullLocal 内部已把失败写进 local_state，
	// 这里再查一次数据库只会多一次往返，且仍不如 stat 直接。
	if info, statErr := os.Stat(abs); statErr == nil && info.Size() == row.FileSize {
		return true
	}
	log.Printf("淘汰跳过：留底未完成，保留 R2 对象 id=%s（下一轮重试）", row.ID)
	return false
}
