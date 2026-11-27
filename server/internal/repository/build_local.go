// build_local.go · 本机留底状态的持久化。
//
// 职责：读出待拉回的包快照，以及推进 local_state 的状态机。
// 输入：租户范围、包 id。
// 输出：待拉回快照；状态更新后无返回。
// 边界：**不碰文件系统**——文件是否存在、是否已落盘由 service 判断，
// 仓储只记录结论。这样「文件写一半崩了」这类问题只需在 service 处理一处。
package repository

import (
	"context"
	"fmt"
	"time"

	"dandelion/server/internal/contract"
)

// LocalPendingRow 是一个等待拉回本机的包。
//
// 字段只带「拉回所需」的信息：对象名、落盘位置、应有多少字节。
// 应用名、版本号等业务字段不参与搬运，带出来只会让每次巡检白读。
type LocalPendingRow struct {
	ID           string
	R2Key        string
	RelativePath string
	FileSize     int64
}

// LocalTarget 是「拉一个包」所需的最小信息。
//
// 与 LocalPendingRow 字段相同但用途不同：那个用于列出待办，
// 这个用于真正搬运。分成两个名字是为了让调用处一眼看出在读哪一类。
type LocalTarget = LocalPendingRow

// GetLocalTarget 读取单个包的搬运信息。
//
// 不复用 FindInProject：那个要 projectID 与 owner 才能定位，
// 而搬运是系统内部动作，调用方只持有包 id，不该为查一条记录去凑齐权限参数。
func (r *BuildRepository) GetLocalTarget(ctx context.Context, id string) (LocalTarget, error) {
	var row LocalTarget
	err := r.db.QueryRowContext(ctx,
		`SELECT id, r2_key, relative_path, file_size FROM `+r.table+` WHERE id = $1`, id).
		Scan(&row.ID, &row.R2Key, &row.RelativePath, &row.FileSize)
	if err != nil {
		return LocalTarget{}, fmt.Errorf("读取待拉回包失败: %w", err)
	}
	return row, nil
}

// localPullStaleAfter 是判定 pulling 已卡死的阈值。
//
// 给 30 分钟：119MB 的包在正常家宽上几分钟内完成，即便慢到 10 分钟也够。
// 超过这个时长几乎只可能是进程中途被杀（部署重启、崩溃），
// 而 pulling 是中间态，不主动回收就会永远停在那里（实测卡过 18 小时）。
const localPullStaleAfter = 30 * time.Minute

// ListLocalPending 读出一个租户下需要拉回本机的包，供巡检补拉。
//
// 筛选条件是「R2 有对象、本机还没有」：
//   - r2_state = synced：对象确实在桶里，拉得动；
//   - local_state 在 absent / failed：没拉过或上次失败；
//   - local_state 是 pulling 但已超时：上次拉取中途进程死了，需要重新捞起来。
//
// 不纳入「刚进 pulling」的包：那是正在拉，重复取出来会让同一对象被下载两份。
// 用时间区分「正在拉」与「卡死的拉」，比只看状态更接近事实。
func (r *BuildRepository) ListLocalPending(ctx context.Context, tenantID string) ([]LocalPendingRow, error) {
	query := `SELECT b.id, b.r2_key, b.relative_path, b.file_size
FROM ` + r.table + ` b
JOIN ` + r.project + ` p ON p.id = b.project_id
WHERE p.tenant_id = $1
  AND b.r2_state = $2
  AND (
        b.local_state = ANY($3)
     -- COALESCE 不能省：老记录的 local_pulling_at 是 NULL（字段是后加的），
     -- 而 NULL < 时间戳 结果是 NULL 而非 true，只写比较会把这类包永久漏掉。
     -- 实测正是这个原因，4 个包卡在 pulling 18 小时、巡检补拉一直是 0。
     OR (b.local_state = $4 AND COALESCE(b.local_pulling_at, to_timestamp(0)) < $5)
  )
ORDER BY b.created_at`

	staleBefore := time.Now().Add(-localPullStaleAfter)
	rows, err := r.db.QueryContext(ctx, query, tenantID,
		contract.R2StateSynced,
		[]string{contract.LocalAbsent, contract.LocalFailed},
		contract.LocalPulling, staleBefore)
	if err != nil {
		return nil, fmt.Errorf("查询待拉回包失败: %w", err)
	}
	defer rows.Close()

	var out []LocalPendingRow
	for rows.Next() {
		var row LocalPendingRow
		if err := rows.Scan(&row.ID, &row.R2Key, &row.RelativePath, &row.FileSize); err != nil {
			return nil, fmt.Errorf("读取待拉回包失败: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ClaimLocalPull 把包标记为「正在拉回」，返回是否抢到这次拉取。
//
// 用条件更新的影响行数做互斥，而不是先查再写：
// 先查后写之间存在窗口，两个 goroutine 会同时判定「该我拉」，
// 于是同一个对象被下载两份、并发写同一个文件。一条 UPDATE 天然是原子的，
// 影响 0 行说明别人已经抢到，本次直接放弃。
//
// 允许抢「卡死的 pulling」：这与 ListLocalPending 的筛选必须一致。
// 若这里只写 local_state <> 'pulling'，被捞出来的卡死记录会因为状态已是 pulling
// 而抢不到，于是「捞出来又立刻让路」，表现为巡检补拉数一直不为 0、状态却永远不动
// ——这个不一致实测导致 4 个包卡了 18 小时。
func (r *BuildRepository) ClaimLocalPull(ctx context.Context, id string) (bool, error) {
	staleBefore := time.Now().Add(-localPullStaleAfter)
	result, err := r.db.ExecContext(ctx,
		`UPDATE `+r.table+`
SET local_state = $2, local_pulling_at = now()
WHERE id = $1
  AND (local_state <> $2 OR COALESCE(local_pulling_at, to_timestamp(0)) < $3)`,
		id, contract.LocalPulling, staleBefore)
	if err != nil {
		return false, fmt.Errorf("抢占拉回失败: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("读取抢占结果失败: %w", err)
	}
	return affected > 0, nil
}

// SetLocalState 推进本机留底状态。
//
// 只写这一个字段：文件的存在与否是 service 的判断结果，
// 仓储不该顺带修改 r2_state 或字节数——那两个字段描述的是另一件事。
func (r *BuildRepository) SetLocalState(ctx context.Context, id, state string) error {
	// 一并清掉拉取时间戳：它只在拉取期间有意义。
	// 留着会让下一次被误判为「刚拉过」（或反之），也与事实不符。
	if _, err := r.db.ExecContext(ctx,
		`UPDATE `+r.table+` SET local_state = $2, local_pulling_at = NULL WHERE id = $1`,
		id, state); err != nil {
		return fmt.Errorf("更新本机状态失败: %w", err)
	}
	return nil
}

// SetLocalProgress 记录已下载字节数，供前端渲染进度条。
//
// 只写这一个字段：进度是纯展示信息，不该顺带改状态——
// 什么时候算「拉完了」由 PullLocal 统一判断，两个地方都能改状态就会打架。
func (r *BuildRepository) SetLocalProgress(ctx context.Context, id string, pulled int64) error {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE `+r.table+` SET local_pulled_bytes = $2 WHERE id = $1`, id, pulled); err != nil {
		return fmt.Errorf("更新下载进度失败: %w", err)
	}
	return nil
}

// SetLocalError 记录拉回失败的原因，供卡片显示红字提示。
//
// 单独一个方法而不塞进 SetLocalState：状态与原因是两件事，
// 成功时没有原因（传空串即可），失败时两者才一起写。
func (r *BuildRepository) SetLocalError(ctx context.Context, id, reason string) error {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE `+r.table+` SET local_error = $2 WHERE id = $1`, id, reason); err != nil {
		return fmt.Errorf("更新失败原因失败: %w", err)
	}
	return nil
}
