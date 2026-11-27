// build_resync.go · 把已淘汰的包重新推回对象存储。
//
// 职责：校验前置条件、立即受理请求、在后台完成上传并回写状态，同时暴露实时进度。
// 输入：包的 id 与请求者身份。
// 输出：受理结果、进度快照；真正的上传结果写日志与数据库状态。
// 边界：只处理「本机有副本、R2 没有」这一种情况，不负责拉回本机（那是 PullLocal）。
//
// 为什么必须异步：整包上传可能超过 Cloudflare Tunnel 的 100 秒代理超时，
// 同步等待必然换来 524；而且请求断开会取消 ctx，把上传到一半的对象一起打断。
// 受理与执行分离后，前端轮询进度接口画进度条，上传不受连接存活影响。
//
// 与 syncToR2 的区别：那个是普通上传后的后台镜像，失败只记日志；
// 这个是用户显式触发的重推，前置错误必须当场返回，后台失败则如实记日志。
package service

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"dandelion/server/internal/contract"
)

// syncProgressTTL 是终态保留时长：让卡片有时间显示绿色/红色，
// 又不至于让历史进度在进程里无限堆积。
const syncProgressTTL = 10 * time.Minute

type resyncState struct {
	state   string
	bytes   int64
	total   int64
	err     string
	updated time.Time
}

// resyncTracker 记录正在后台重推的包及其进度，防止重复点击把同一对象传两遍。
//
// 用进程内状态而不是新增 r2_state 枚举：重推是分钟级的进程内任务，
// 进程重启后自然清空；为它改枚举会让下载、统计、淘汰全部跟着改，得不偿失。
// 零值可用，所以 BuildService 不需要在构造函数里额外初始化。
type resyncTracker struct {
	mu    sync.Mutex
	items map[string]*resyncState
}

// tryAdd 尝试占住一个包；返回 false 表示已有任务在跑。
func (t *resyncTracker) tryAdd(id string, total int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.items == nil {
		t.items = make(map[string]*resyncState)
	}
	if _, busy := t.items[id]; busy {
		return false
	}
	t.items[id] = &resyncState{state: contract.SyncStateRunning, total: total, updated: time.Now()}
	return true
}

// setBytes 更新已读字节数。进度读取来自文件流，反映真实上传进度。
func (t *resyncTracker) setBytes(id string, n int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if st, ok := t.items[id]; ok {
		st.bytes = n
		st.updated = time.Now()
	}
}

// done 结束占用并保留终态，供卡片显示成功或失败。
func (t *resyncTracker) done(id string, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	st, ok := t.items[id]
	if !ok {
		return
	}
	if err != nil {
		st.state, st.err = contract.SyncStateFailed, err.Error()
	} else {
		st.state, st.bytes = contract.SyncStateSucceeded, st.total
	}
	st.updated = time.Now()
}

// progress 返回进度快照；终态超过 TTL 后回收，避免长期占用内存。
func (t *resyncTracker) progress(id string) contract.SyncProgress {
	t.mu.Lock()
	defer t.mu.Unlock()
	st, ok := t.items[id]
	if !ok {
		return contract.SyncProgress{State: contract.SyncStateIdle}
	}
	if st.state != contract.SyncStateRunning && time.Since(st.updated) > syncProgressTTL {
		delete(t.items, id)
		return contract.SyncProgress{State: contract.SyncStateIdle}
	}
	return contract.SyncProgress{State: st.state, Bytes: st.bytes, Total: st.total, Error: st.err}
}

// progressReader 包装文件流，每读一块就把累计字节数交给 tracker。
func (t *resyncTracker) progressReader(id string, src io.Reader) io.Reader {
	return &countingReader{src: src, onRead: func(n int64) { t.setBytes(id, n) }}
}

type countingReader struct {
	src    io.Reader
	onRead func(int64)
	read   int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	if n > 0 {
		r.read += int64(n)
		r.onRead(r.read)
	}
	return n, err
}

// SyncToR2 受理一次重推请求，上传在后台进行。
//
// 只允许 evicted：synced 的包对象还在桶里，重推纯属浪费带宽；
// pending 的包从未同步过，属于上传流程的中间态，不该由这个入口介入。
// 保留原有 r2_key 而不重新生成：那个 key 里带着 {租户}/{项目}/direct/ 前缀，
// 是按归属组织对象的唯一线索，重新生成会让它成为无法追溯的孤儿。
//
// 前置校验（状态、本机副本、对象名）留在请求内：这些是用户能立刻修正的错误，
// 不能等到后台日志里才被发现。校验通过就返回 202，前端轮询进度看结果。
func (s *BuildService) SyncToR2(ctx context.Context, id string, identity contract.Identity) (contract.Build, error) {
	build, err := s.authorizedBuild(ctx, id, identity)
	if err != nil {
		return contract.Build{}, err
	}
	// 先取包再判断：包带着归属公司，才知道该查哪家公司的通道。
	if !s.r2.Enabled(build.TenantID) {
		return contract.Build{}, &contract.ValidationError{Message: "对象存储未配置，无法同步"}
	}
	if err := checkResyncable(build); err != nil {
		return contract.Build{}, err
	}
	if build.R2State == contract.R2StateSynced {
		return build, nil // 幂等：已在桶里，直接回当前状态
	}

	// 本机副本缺失是唯一无法自行恢复的前置条件：这个接口不做拉回，也没有别的字节来源。
	abs, err := s.paths.SafeResolve(build.RelativePath)
	if err != nil {
		return contract.Build{}, contract.NotFound(contract.ResourceBuild)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return contract.Build{}, &contract.ValidationError{Message: "本机副本已丢失，无法同步到对象存储"}
	}
	if build.R2Key == "" {
		return contract.Build{}, &contract.ValidationError{Message: "该版本缺少对象名，无法同步"}
	}

	if !s.resyncs.tryAdd(id, info.Size()) {
		return build, nil // 已在后台同步，重复点击直接受理成功，不重复传字节
	}

	// 独立 ctx：Cloudflare 超时或用户关页面都不能中断上传。
	go s.resyncInBackground(build, identity, abs, info.Size())

	return build, nil
}

// SyncProgress 返回一次重推的实时进度；先校验可见性，避免跨租户探测。
func (s *BuildService) SyncProgress(ctx context.Context, id string, identity contract.Identity) (contract.SyncProgress, error) {
	if _, err := s.authorizedBuild(ctx, id, identity); err != nil {
		return contract.SyncProgress{}, err
	}
	return s.resyncs.progress(id), nil
}

// resyncInBackground 后台完成一次重推，结果只进日志与数据库，进度留在 tracker。
func (s *BuildService) resyncInBackground(build contract.Build, identity contract.Identity, abs string, size int64) {
	ctx, cancel := context.WithTimeout(context.Background(), r2SyncTimeout)
	defer cancel()

	err := s.uploadResync(ctx, build, identity, abs, size)
	s.resyncs.done(build.ID, err)
	if err != nil {
		log.Printf("后台同步到 R2 失败 id=%s err=%v", build.ID, err)
		return
	}
	log.Printf("后台同步到 R2 完成 id=%s", build.ID)
}

// uploadResync 真正搬字节，是后台任务里唯一会失败的部分。
func (s *BuildService) uploadResync(ctx context.Context, build contract.Build, identity contract.Identity, abs string, size int64) error {
	file, err := os.Open(abs)
	if err != nil {
		return fmt.Errorf("打开本机副本失败: %w", err)
	}
	defer file.Close()

	contentType, err := s.contentTypeOf(build.Platform)
	if err != nil {
		return err
	}
	body := s.resyncs.progressReader(build.ID, file)
	r2 := s.r2.Get(build.TenantID)
	if r2 == nil {
		return fmt.Errorf("该公司未配置对象存储，无法重推")
	}
	if err := r2.Put(ctx, build.R2Key, body, size, contentType, build.OriginalFileName); err != nil {
		return fmt.Errorf("上传到对象存储失败: %w", err)
	}
	if err := s.repos.For(identity.Mode).MarkR2Synced(ctx, build.ID, build.R2Key, size); err != nil {
		// 对象已上传成功、只是状态没写上：下载仍会走 CDN，不应再传一遍。
		return fmt.Errorf("同步已完成但状态更新失败: %w", err)
	}

	// 预热 CDN：让首个下载者命中边缘缓存，而不是等待回源。
	s.prewarmCDNAsync(build.TenantID, build.R2Key)
	return nil
}

// checkResyncable 判断一个包当前是否允许重推，返回 nil 表示可以。
//
// 抽成纯函数是为了能被直接测到：它只依赖 r2State 这一个输入，
// 若混在 SyncToR2 里，任何验证都得先备好数据库、对象存储与完整权限链，
// 成本高到没人愿意写，这条规则就会长期无人看管。
//
// 三种状态的去留各有理由：
//   - synced：对象还在桶里，重推纯属浪费一次整包上传，但也不该报错
//     （用户可能连点两次），由调用方直接返回当前状态。
//   - evicted：唯一允许重推的状态，本机副本是字节的唯一来源。
//   - pending / failed / 其他：上传流程的中间态，由后台镜像负责，
//     不该由这个人工入口介入，否则两条路径会互相覆盖状态。
func checkResyncable(build contract.Build) error {
	switch build.R2State {
	case contract.R2StateSynced, contract.R2StateEvicted:
		return nil
	default:
		return &contract.ValidationError{Message: "该版本尚未同步到对象存储，无需重推"}
	}
}
