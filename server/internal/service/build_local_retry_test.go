// build_local_retry_test.go · 「能否重试下载」这条规则的单测。
//
// 职责：锁定哪些状态允许重试、哪些必须拒绝及拒绝原因。
// 输入：构造的 Build（只填 r2State 与 localState）。
// 输出：对 checkPullable 返回值的断言。
// 边界：纯函数测试，不连库、不碰文件、不需要对象存储。
//
// 为什么值得单测：判错方向有两个后果——放行「正在下载」会让同一对象被下载两份，
// 白耗一次上百 MB 的带宽；拒绝「失败态」会让重试按钮彻底失效。
// 而它只是一个 switch，有测试就能一眼看清规则，不必读上层几十行装配代码。
package service

import (
	"strings"
	"testing"

	"dandelion/server/internal/contract"
)

func TestCheckPullable(t *testing.T) {
	cases := []struct {
		name       string
		r2State    string
		localState string
		wantErr    string // 期望的错误片段；空表示应放行
	}{
		{"失败后可重试", contract.R2StateSynced, contract.LocalFailed, ""},
		{"从未下载可重试", contract.R2StateSynced, contract.LocalAbsent, ""},
		{"已有副本无需重试", contract.R2StateSynced, contract.LocalPresent, "已有该版本的副本"},
		{"正在下载不可重复触发", contract.R2StateSynced, contract.LocalPulling, "正在下载中"},
		{"对象不在桶里无法下载", contract.R2StatePending, contract.LocalAbsent, "不在对象存储"},
		{"已淘汰的对象也不在桶里", contract.R2StateEvicted, contract.LocalAbsent, "不在对象存储"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkPullable(contract.Build{R2State: c.r2State, LocalState: c.localState})
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("应放行，got err=%v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("应拒绝（%s），却放行了", c.wantErr)
			}
			// 断言具体原因：只判 err != nil 会让「装配错误导致的拒绝」也算通过。
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("拒绝原因应为 %q，got=%v", c.wantErr, err)
			}
		})
	}
}
