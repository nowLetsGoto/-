// Package dandelion 是 Go 模块根包，只负责一件事：把前端构建产物绑定进单二进制。
// 职责：静态资源编译期绑定，供 app.NewRouter 通过 fs.Sub 取出 public 子目录并做 SPA fallback。
// 输入：server/public/ 下由 npm run build 生成的文件（仓库内保留一份兜底 index.html 保证可编译）。
// 输出：导出的 PublicFiles（embed.FS）。
// 边界：不含业务逻辑、不读环境变量、不在运行时访问磁盘；embed 指令只能写在与 public/ 同级的包内。
package dandelion

import "embed"

// PublicFiles 内嵌 server/public/ 下的全部前端产物。
//go:embed public/*
var PublicFiles embed.FS
