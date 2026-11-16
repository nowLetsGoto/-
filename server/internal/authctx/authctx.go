// Package authctx · 调用者身份在请求上下文里的存取。
// 职责：定义上下文键与读写函数，供鉴权中间件写入、各 handler 读取。
// 输入：context.Context 与 contract.Identity；输出：写入后的 ctx / 读出的身份。
// 边界：**单独成包是为了打断循环依赖**——中间件要用 handler.WriteError，handler 也要读身份，
//       两边都依赖它就不再有环；本包只做存取，不含任何判定逻辑。
package authctx

import (
	"context"

	"dandelion/server/internal/contract"
)

type identityKey struct{}

// With 把调用者身份写进上下文。
func With(ctx context.Context, identity contract.Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, identity)
}

// From 读出调用者身份；未经鉴权中间件时得到零值（非超管、空用户名）。
func From(ctx context.Context) contract.Identity {
	identity, _ := ctx.Value(identityKey{}).(contract.Identity)
	return identity
}

// Username 是 From(ctx).Username 的简写，调用点更短。
func Username(ctx context.Context) string {
	return From(ctx).Username
}
