// commit.go · 提交发布请求的统一字段校验。
// 职责：校验环境 / 平台枚举与五个文本字段长度。
// 输入：contract.CommitInput；输出：可直接上屏的 AppError，或 nil。
// 边界：不校验 stageToken（service 负责）、不写库、不碰 HTTP 对象；
//       管理端提交与 /open/builds 共用同一份规则，避免两条入口校验口径漂移。
package handler

import (
	"fmt"
	"unicode/utf8"

	"dandelion/server/internal/contract"
)

// ValidateCommitInput 是提交发布的唯一字段校验入口。
func ValidateCommitInput(in contract.CommitInput) error {
	if !contract.IsValidEnvironment(in.Environment) {
		return BadRequest("环境参数不合法")
	}
	if !contract.IsValidPlatform(in.Platform) {
		return BadRequest("平台参数不合法")
	}
	for _, field := range []struct {
		label string
		value string
		max   int
	}{
		{"应用名", in.AppName, contract.MaxAppNameLen},
		{"包名", in.PackageID, contract.MaxPackageIDLen},
		{"版本号", in.Version, contract.MaxVersionLen},
		{"构建号", in.BuildNumber, contract.MaxBuildNumberLen},
		{"更新说明", in.Changelog, contract.MaxChangelogLen},
	} {
		if utf8.RuneCountInString(field.value) > field.max {
			return BadRequest(fmt.Sprintf("%s超过 %d 个字符", field.label, field.max))
		}
	}
	return nil
}
