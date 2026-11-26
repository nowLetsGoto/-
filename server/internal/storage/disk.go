// disk.go · 查询文件系统的可用空间。
//
// 职责：回答「还有多少字节可写」这一个问题。
// 输入：任意路径。
// 输出：该路径所在文件系统的可用字节数。
// 边界：只查询，不创建目录、不写文件；不支持 Statfs 的平台返回 0，
// 调用方据此跳过空间预检（宁可尝试写失败，也不要因为查不到就拒绝服务）。
//
// 为什么单独成文件而不是塞进 paths.go：paths 的边界写明「只做词法路径运算，
// 不访问文件系统」，把 syscall 混进去会破坏那条约定。
package storage

import "syscall"

// DiskFree 返回 path 所在文件系统的可用字节数。
//
// 用 Bavail 而非 Bfree：后者包含仅 root 可用的保留块，
// 对以普通用户运行的服务来说那部分写不进去，按它判断会高估可用空间。
func DiskFree(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}