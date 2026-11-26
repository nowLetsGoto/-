// build_resync_test.go · 「能否重推」这条规则的单测。
//
// 职责：锁定只有 evicted 才允许重推、synced 幂等放行、其余状态一律拒绝。
// 输入：构造的 Build（只填 r2State）。
// 输出：对 checkResyncable 返回值的断言。
// 边界：纯函数测试，不连库、不碰文件、不需要对象存储。
//
// 为什么值得单测：这条规则决定「什么时候会往 R2 传一次 119MB」。
// 判错方向有两个后果——放行 pending 会让后台镜像与人工重推互相覆盖状态；
// 拒绝 evicted 会让这个功能彻底失效。而它只是一个 switch，
// 有测试就能一眼看清允许哪些状态，不必去读上层那几十行装配代码。
package service

import (
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
