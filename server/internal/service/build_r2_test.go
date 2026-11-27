// build_r2_test.go · R2 下载地址选择的单测。
//
// 职责：锁定「哪些状态才能把 CDN 直链交给分享页」这条规则。
// 输入：构造的 Build 与本包内的 R2 客户端。
// 输出：对 SharedDirectURL 返回值的断言。
// 边界：不连对象存储、不查数据库；只测纯地址选择，网络行为由 storage 包自己覆盖。
package service

import (
	"context"
	"testing"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/storage"
)

func TestSharedDirectURL(t *testing.T) {
	r2, err := storage.NewR2(context.Background(), storage.R2Config{
		AccountID: "acct", AccessKeyID: "key", SecretKey: "secret",
		Bucket: "bucket", PublicBaseURL: "https://cdn.example.com/download",
	})
	if err != nil {
		t.Fatalf("构造 R2 失败: %v", err)
	}
	svc := &BuildService{r2: storage.NewR2Ref(r2)}

	cases := []struct {
		name  string
		build contract.Build
		want  string
	}{
		{
			name:  "已同步才有直链，对象名中的目录要保留",
			build: contract.Build{R2State: contract.R2StateSynced, R2Key: "tenant/project/direct/a.apk"},
			want:  "https://cdn.example.com/download/tenant/project/direct/a.apk",
		},
		{
			name:  "已淘汰的对象不在桶里，不能给直链",
			build: contract.Build{R2State: contract.R2StateEvicted, R2Key: "tenant/project/direct/a.apk"},
			want:  "",
		},
		{
			name:  "缺少对象名时不能拼 URL",
			build: contract.Build{R2State: contract.R2StateSynced},
			want:  "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := svc.SharedDirectURL(c.build); got != c.want {
				t.Fatalf("got=%q want=%q", got, c.want)
			}
		})
	}
}

func TestSharedDirectURLWithoutR2(t *testing.T) {
	svc := &BuildService{}
	if got := svc.SharedDirectURL(contract.Build{
		R2State: contract.R2StateSynced, R2Key: "a.apk",
	}); got != "" {
		t.Fatalf("未配置 R2 时不能给直链，got=%q", got)
	}
}
