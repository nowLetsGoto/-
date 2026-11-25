// Package gitreader 负责受限本机 Git 只读查询。
// 职责：唯一净化出口，隐藏提交作者、主题和分支中的凭据。
// 输入：Git 自由文本；输出：可回显文本。
// 边界：不净化结构化 SHA；stderr 和文件名永不回显。
package gitreader

import "regexp"

var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)Authorization\s*:\s*[^\s]+(?:\s+[^\s]+)?`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9_]{10,}\b`),
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),
	regexp.MustCompile(`\bAKIA[A-Z0-9]{16}\b`),
	regexp.MustCompile(`(?i)\b(?:token|password|secret|api[_-]?key)\s*[:=]\s*[^\s]+`),
	regexp.MustCompile(`\b[0-9a-fA-F]{32,}\b`),
}

// Sanitize 逐模式替换敏感片段，移除控制字符。
func Sanitize(text string) string {
	for _, pattern := range sensitivePatterns {
		text = pattern.ReplaceAllString(text, "[已隐藏]")
	}
	var runes []rune
	for _, r := range text {
		if r >= 32 && r != 127 {
			runes = append(runes, r)
		}
	}
	return string(runes)
}
