// query.go · 列表分页参数的解析。
// 职责：把 page / pageSize 查询参数归一化成 contract.PageQuery，非法值回落到安全默认。
// 输入：*http.Request；输出：contract.PageQuery。
// 边界：只做解析与夹紧，不查库、不构造错误响应——分页永远不该让列表 400。
package handler

import (
	"net/http"
	"strconv"

	"dandelion/server/internal/contract"
)

// ReadPage 解析分页参数：
//   - page 缺省 / 非法 / < 1 → 1
//   - pageSize 缺省 / 非法 → DefaultPageSize；显式传 0 → 不分页（全量）；超过上限 → MaxPageSize
func ReadPage(r *http.Request) contract.PageQuery {
	query := r.URL.Query()
	return contract.PageQuery{
		Page:     atLeastOne(query.Get("page")),
		PageSize: boundedPageSize(query.Get("pageSize")),
	}
}

func atLeastOne(raw string) int {
	if raw == "" {
		return 1
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 1
	}
	return value
}

func boundedPageSize(raw string) int {
	if raw == "" {
		return contract.DefaultPageSize
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return contract.DefaultPageSize
	}
	if value > contract.MaxPageSize {
		return contract.MaxPageSize
	}
	return value
}
