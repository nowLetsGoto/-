// build_resync_test.go · 「能否重推」这条规则的单测。
//
// 职责：锁定只有 evicted 才允许重推、同一包不会被重复受理，以及进度能推进到终态。
// 输入：构造的 Build（只填 r2State）与包 id。
// 输出：对 checkResyncable 与 resyncTracker 的断言。
// 边界：纯内存测试，不连库、不碰文件、不需要对象存储。
//
// 为什么值得单测：这条规则决定「什么时候会往 R2 传一次 119MB」。
// 判错方向有两个后果——放行 pending 会让后台镜像与人工重推互相覆盖状态；
// 拒绝 evicted 会让这个功能彻底失效。而它只是一个 switch，
// 有测试就能一眼看清允许哪些状态，不必去读上层那几十行装配代码。
package service

import (
	"fmt"
	"testing"

	"dandelion/server/internal/contract"
)

func TestCheckResyncable(t *testing.T) {
	cases := []struct {
		name    string
		r2State string
		wantErr bool
	}{
		{"已淘汰可重推", contract.R2StateEvicted, false},
		{"已同步幂等放行（上传流程会再判一次）", contract.R2StateSynced, false},
		{"尚未同步应拒绝", contract.R2StatePending, true},
		{"同步失败应拒绝（后台镜像负责重试）", contract.R2StateFailed, true},
		{"未知状态应拒绝（失败关闭）", "some-new-state", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkResyncable(contract.Build{R2State: c.r2State})
			if c.wantErr && err == nil {
				t.Fatalf("r2State=%q 应被拒绝", c.r2State)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("r2State=%q 应放行，got err=%v", c.r2State, err)
			}
		})
	}
}

// TestResyncTracker 验证同一个包不会被重复受理，且进度能正确推进到终态。
//
// 后台上传一跑就是几十秒到几分钟；若重复点击能再起一个 goroutine，
// 同一对象会被传两遍，还可能两次回写状态互相覆盖。
func TestResyncTracker(t *testing.T) {
	var tracker resyncTracker // 零值必须可用：BuildService 的其它测试用结构体字面量构造。

	if got := tracker.progress("a").State; got != contract.SyncStateIdle {
		t.Fatalf("未开始时应为 idle，got=%s", got)
	}
	if !tracker.tryAdd("a", 100) {
		t.Fatal("首次占用应成功")
	}
	if tracker.tryAdd("a", 100) {
		t.Fatal("同一包重复点击应被拒绝")
	}
	if !tracker.tryAdd("b", 200) {
		t.Fatal("不同包应能同时上传")
	}

	tracker.setBytes("a", 40)
	if got := tracker.progress("a").Bytes; got != 40 {
		t.Fatalf("进度字节应为 40，got=%d", got)
	}

	tracker.done("a", nil)
	p := tracker.progress("a")
	if p.State != contract.SyncStateSucceeded || p.Bytes != p.Total {
		t.Fatalf("成功后应满格，got state=%s bytes=%d total=%d", p.State, p.Bytes, p.Total)
	}

	tracker.done("b", fmt.Errorf("上传失败"))
	if got := tracker.progress("b").State; got != contract.SyncStateFailed {
		t.Fatalf("失败应为 failed，got=%s", got)
	}
}
