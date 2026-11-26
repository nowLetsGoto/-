package service

import (
	"testing"

	"dandelion/server/internal/contract"
)

// 锁定 parseResult 的 warning 行为：重构解析入口后，前端看到的提示文案不能变。
func TestParseResultWarnings(t *testing.T) {
	complete := contract.ParsedMeta{
		AppName: "景田", PackageID: "uni.UNIeebddc7", Version: "26.9.1", BuildNumber: "1",
	}

	// 完整元数据：无 warning。
	if got := parseResult("tok", complete, ""); len(got.Warnings) != 0 {
		t.Fatalf("完整元数据不应有 warning: %v", got.Warnings)
	}
	// 字段缺失：通用提示。
	if got := parseResult("tok", contract.ParsedMeta{AppName: "景田"}, ""); len(got.Warnings) != 1 {
		t.Fatalf("缺字段应有 1 条 warning: %v", got.Warnings)
	}
	// 明确错误：用错误文案，不叠加通用提示。
	const explicit = "解析超时，请手动填写应用名、包名与版本号"
	got := parseResult("tok", contract.ParsedMeta{}, explicit)
	if len(got.Warnings) != 1 || got.Warnings[0] != explicit {
		t.Fatalf("明确错误应原样透传: %v", got.Warnings)
	}
	// token 必须原样回传，否则 Create 找不到暂存。
	if got.StageToken != "tok" {
		t.Fatalf("StageToken 丢失: %q", got.StageToken)
	}
}
