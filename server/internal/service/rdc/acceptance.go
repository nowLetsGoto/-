// Package rdc 负责需求交付的业务规则。
// 职责：同构拆分验收标准并验证逐条结果完整性与结论一致性。
// 输入：数据库中的标准原文、客户端逐条结果；输出：有序条目或 400 错误。
// 边界：纯函数，不信任客户端提供的标准文本，不读写库。
package rdc

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"dandelion/server/internal/contract"
)

type AcceptanceCriterion struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
}

var criterionMarker = regexp.MustCompile(`(?:^|\s)(?:\d+[.、)]|[-•])\s*`)

// SplitAcceptanceCriteria 将换行及列表标记拆成连续编号的条目。
func SplitAcceptanceCriteria(text string) []AcceptanceCriterion {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	result := []AcceptanceCriterion{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		locations := criterionMarker.FindAllStringIndex(line, -1)
		if len(locations) == 0 {
			result = append(result, AcceptanceCriterion{Index: len(result), Text: line})
			continue
		}
		if locations[0][0] > 0 {
			prefix := strings.TrimSpace(line[:locations[0][0]])
			if prefix != "" {
				result = append(result, AcceptanceCriterion{Index: len(result), Text: prefix})
			}
		}
		for i, loc := range locations {
			end := len(line)
			if i+1 < len(locations) {
				end = locations[i+1][0]
			}
			item := strings.TrimSpace(line[loc[1]:end])
			if item != "" {
				result = append(result, AcceptanceCriterion{Index: len(result), Text: item})
			}
		}
	}
	return result
}

// ValidateAcceptanceItems 以当前库中标准重算 N，并拒绝缺项、重复、越界或伪通过。
func ValidateAcceptanceItems(criteria, result string, items []contract.AcceptanceItemResult) (string, error) {
	count := len(SplitAcceptanceCriteria(criteria))
	if count == 0 {
		return "", &contract.ValidationError{Message: "需求尚未填写验收标准"}
	}
	if count > contract.MaxAcceptanceItems || len(items) != count {
		return "", &contract.ValidationError{Message: "验收结果必须覆盖全部标准条目"}
	}
	if result != contract.AcceptPassed && result != contract.AcceptFailed {
		return "", &contract.ValidationError{Message: "验收结论无效"}
	}
	seen := make([]bool, count)
	failed := false
	for _, item := range items {
		if item.Index < 0 || item.Index >= count || seen[item.Index] || item.Result != contract.AcceptPassed && item.Result != contract.AcceptFailed {
			return "", &contract.ValidationError{Message: "验收条目索引或结果无效"}
		}
		seen[item.Index] = true
		failed = failed || item.Result == contract.AcceptFailed
	}
	if result == contract.AcceptPassed && failed || result == contract.AcceptFailed && !failed {
		return "", &contract.ValidationError{Message: "验收结论与逐条结果不一致"}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Index < items[j].Index })
	raw, err := json.Marshal(items)
	return string(raw), err
}
