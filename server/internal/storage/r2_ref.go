// Package storage · R2 客户端的可替换持有者。
// 职责：以原子方式持有当前生效的 R2 客户端，支持运行时整体替换。
// 输入：构造好的 *R2（由装配层或配置保存流程提供）。
// 输出：Get 返回当前客户端的快照；nil 表示未启用。
// 边界：不构造客户端、不读配置、不判断业务语义；只做「持有与替换」。
//
// 为什么需要它：R2 凭据可以在设置页修改并立即生效，而 BuildService 里
// 有 30 余处直接使用客户端。若每处都去读数据库，一次下载会查几十次库；
// 若允许客户端字段被直接替换，正在进行的下载会在中途换到新凭据而失败。
//
// 关键约束：调用方必须在函数入口取一次快照，全程使用该局部变量。
// 每次使用时重新 Get，等于放弃了「一次操作使用同一套凭据」的保证——
// 切换恰好发生在两次 Get 之间时，同一次上传会前半段用旧密钥、后半段用新密钥。
package storage

import "sync/atomic"

// R2Ref 是可原子替换的 R2 客户端持有者，并发安全。
type R2Ref struct {
	ptr atomic.Pointer[R2]
}

// NewR2Ref 用初始客户端创建持有者；传入 nil 表示未启用。
func NewR2Ref(client *R2) *R2Ref {
	ref := &R2Ref{}
	ref.ptr.Store(client)
	return ref
}

// Get 返回当前生效的客户端；nil 表示未启用。
//
// 返回值在本次调用后仍然有效：即使随后发生替换，旧客户端对象不会被回收或修改，
// 已持有它的操作可以安全地跑完。这是「在途任务不受切换影响」的实现基础。
func (r *R2Ref) Get() *R2 {
	if r == nil {
		return nil
	}
	return r.ptr.Load()
}

// Enabled 报告当前是否已配置 R2。
func (r *R2Ref) Enabled() bool { return r.Get() != nil }

// Swap 用新客户端整体替换，返回被替换掉的旧客户端（便于调用方决定是否清理）。
//
// 传 nil 表示关闭 R2：此后新发起的操作全部降级为本地行为。
func (r *R2Ref) Swap(next *R2) *R2 {
	if r == nil {
		return nil
	}
	return r.ptr.Swap(next)
}
