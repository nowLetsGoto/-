// Package storage · R2 客户端的按公司持有者。
// 职责：按公司维度持有 R2 客户端，支持运行时替换。
// 输入：构造好的 *R2 与所属公司 id。
// 输出：Get(tenantID) 返回该公司的客户端；nil 表示该公司未启用 R2。
// 边界：不构造客户端、不读配置、不判断业务语义；只做「持有与替换」。
//
// 为什么按公司而不是全局一份：配置存在 system_settings 的每公司行里，
// 各公司用自己的桶。全局一份会让「A 公司配的桶」被 B 公司也写进去。
//
// 为什么没有全局兜底：产品决策是严格隔离——某公司没配即视为未启用 R2，
// 下载走本机磁盘。若回落到平台兜底配置，该公司的文件会静默进入平台账号的桶，
// 那是比「没有加速」严重得多的问题。
//
// 关键约束：调用方必须在函数入口取一次快照（r2 := s.r2.Get(tenantID)），
// 全程使用该局部变量。每次使用时重新 Get，会放弃
// 「一次操作使用同一套凭据」的保证——替换恰好落在两次 Get 之间时，
// 同一次上传会前半段用旧密钥、后半段用新密钥。
package storage

import "sync"

// R2Ref 是按公司隔离的 R2 客户端持有者，并发安全。
type R2Ref struct {
	// mu 保护 clients。用 RWMutex 而不是原子指针：
	// 读取只在每次操作入口发生一次（不是逐字节），读锁之间不互斥，
	// 而配置变更极少，写锁几乎不会成为瓶颈。简单性优先。
	mu      sync.RWMutex
	clients map[string]*R2
}

// NewR2Ref 创建不含任何公司客户端的持有者。
func NewR2Ref() *R2Ref {
	return &R2Ref{clients: make(map[string]*R2)}
}

// Get 返回指定公司当前生效的客户端；nil 表示该公司未启用 R2。
//
// 返回值在本次调用后仍然有效：即使随后被替换，旧客户端对象不会被修改，
// 已持有它的操作可以安全跑完。这是「在途任务不受切换影响」的实现基础。
func (r *R2Ref) Get(tenantID string) *R2 {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.clients[tenantID]
}

// Enabled 报告指定公司是否已启用 R2。
func (r *R2Ref) Enabled(tenantID string) bool { return r.Get(tenantID) != nil }

// Set 为指定公司设置客户端；client 为 nil 表示关闭该公司的 R2。
func (r *R2Ref) Set(tenantID string, client *R2) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if client == nil {
		delete(r.clients, tenantID)
		return
	}
	r.clients[tenantID] = client
}

// TenantCount 返回已启用 R2 的公司数，供启动日志与诊断使用。
func (r *R2Ref) TenantCount() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.clients)
}
