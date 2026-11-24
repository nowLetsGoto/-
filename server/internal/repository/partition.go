// partition.go · 业务数据的物理分区。
// 职责：定义分区名、把会话模式翻译成分区、按分区持有同构仓储。
// 输入：contract.SessionMode 或显式分区；输出：分区名 / 选中的仓储实例。
// 边界：**只做路由，不做权限判定**——越权由路由中间件与业务层负责，
//
//	这里的价值是让"单人数据落在 solo、团队数据落在 team"成为编译期就固定的结构。
package repository

import "dandelion/server/internal/contract"

// Partition 是业务数据所在的 schema 名。
// 取值只有两个编译期常量，因此可以安全地拼进 SQL 表名（表名无法参数化）。
type Partition string

const (
	PartitionTeam Partition = "team"
	PartitionSolo Partition = "solo"
)

// PartitionFor 把会话模式翻译成分区。
// 只有明确声明为 solo 的会话进入单人分区；其余（含公开链路里的零值）都按团队处理，
// 避免"忘记带模式"把请求悄悄路由到更宽松的单人空间。
func PartitionFor(mode contract.SessionMode) Partition {
	if mode == contract.ModeSolo {
		return PartitionSolo
	}
	return PartitionTeam
}

// Mode 是 PartitionFor 的逆映射：读出来的数据要知道自己属于哪种会话模式。
// 与 PartitionFor 保持同一条默认规则——不是 solo 的一律视为 team。
func (p Partition) Mode() contract.SessionMode {
	if p == PartitionSolo {
		return contract.ModeSolo
	}
	return contract.ModeTeam
}

// Table 返回分区内的限定表名，例如 team.builds。
// 显式写全限定名而不依赖 search_path：同一份查询要能在两个 schema 里跑。
func (p Partition) Table(name string) string {
	return string(p) + "." + name
}

// Partitioned 按分区持有两份同构实现，是"分表"在代码里的唯一投影。
// 上层只需 repos.For(identity.Mode)，不必认识 team / solo 这两个名字。
type Partitioned[T any] struct {
	team T
	solo T
}

// NewPartitioned 装配两份同构仓储；两份的类型相同，因此上层调用点无需分支。
func NewPartitioned[T any](team, solo T) Partitioned[T] {
	return Partitioned[T]{team: team, solo: solo}
}

// For 返回该模式对应的仓储。
func (p Partitioned[T]) For(mode contract.SessionMode) T {
	if PartitionFor(mode) == PartitionSolo {
		return p.solo
	}
	return p.team
}

// All 按团队、单人的顺序返回两份实现，供可信的跨分区维护使用；分享访问必须验签后用 For。
func (p Partitioned[T]) All() []T {
	return []T{p.team, p.solo}
}

// Schema 是分区名的另一个称呼，让"仓储属于哪个分区"在读代码时更直白。
// 与 Partition 完全等价，只是用在结构体字段上可读性更好。
type Schema = Partition
