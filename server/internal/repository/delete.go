// delete.go · 删除操作共用的结果判定。
// 职责：把「DELETE 影响 0 行」翻译成统一的 contract.ErrNotFound。
// 输入：一条已执行的 DELETE 结果；输出：nil 或 ErrNotFound。
// 边界：只做结果判定，不拼 SQL、不开事务——条件怎么加由各仓储自己决定
//       （它们各自知道要不要带 tenant_id / owner_user_id）。
package repository

import (
	"database/sql"
	"fmt"

	"dandelion/server/internal/contract"
)

// EnsureDeleted 判断一条 DELETE 是否真的删到了东西。
//
// 为什么需要它：删除接口里「目标不存在」「不属于本公司」「已被别人删掉」
// 在 SQL 层面都是"影响 0 行"，三者对调用方应当是同一个答案——404。
// 若各仓储自己写 `if rows == 0 { return contract.ErrNotFound }`，
// 就会出现有的返回 404、有的静默返回 200 的不一致。
//
// entity 只用于错误信息里指明是哪张表的删除，方便排查。
//
// 注意：外键为 RESTRICT / NO ACTION 的表，删除会直接抛外键错误，
// 那种情况必须由调用方在删除**之前**拦掉并给人话提示（见各 service 的引用检查），
// 本函数只处理"本来就查不到这条记录"。
func EnsureDeleted(result sql.Result, entity string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取删除结果失败（%s）: %w", entity, err)
	}
	if affected == 0 {
		return contract.ErrNotFound
	}
	return nil
}
