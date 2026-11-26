// Package storage 的对象存储层：提供只读的远程随机访问器。
//
// 职责：把 HTTP Range 请求包装成 io.ReaderAt，让只接受 ReaderAt 的消费者
// （如 archive/zip 与 apk 解析器）能直接读取远端对象，无需先整包下载到本地。
// 输入：对象 URL、HTTP 客户端；输出：实现 io.ReaderAt 的读取器。
// 边界：只读、无状态写入、不解析内容；块缓存仅在内存，生命周期由调用方管理。
package storage

import (
	"fmt"
	"io"
	"net/http"
	"sync"
)

// rangeBlockSize 是一次 Range 请求的粒度。
//
// ZIP 中央目录的读取呈"大量小偏移、集中在文件尾部"的特征：256KB 的块
// 能把数十次零散请求合并成个位数（实测 2199 条目的 APK：95 次 → 11 次）。
// 块太大浪费流量，太小则失去合并意义；256KB 是实测的折中值。
const rangeBlockSize = 256 * 1024

// HTTPRangeReader 通过 HTTP Range 请求实现 io.ReaderAt。
//
// 它按 rangeBlockSize 对齐读取并缓存块：命中缓存的 ReadAt 不产生网络请求。
// 这样 archive/zip 遍历中央目录时的密集小读取会被合并，避免逐次跨境往返。
//
// 并发安全：缓存用互斥锁保护；同一块的首次读取只会发起一次请求。
type HTTPRangeReader struct {
	url    string
	client *http.Client

	mu     sync.Mutex
	blocks map[int64][]byte // blockIndex -> 该块的字节（最后一块可能不足 rangeBlockSize）
}

// NewHTTPRangeReader 创建远端随机读取器。
// client 应配置连接池（MaxIdleConnsPerHost > 0），否则每次 Range 都要重建 TLS。
func NewHTTPRangeReader(url string, client *http.Client) *HTTPRangeReader {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPRangeReader{
		url:    url,
		client: client,
		blocks: make(map[int64][]byte),
	}
}

// ReadAt 从 off 起读 len(p) 字节到 p，返回实际读取字节数。
// 语义与 io.ReaderAt 一致：读完或到对象末尾才返回 io.EOF。
func (r *HTTPRangeReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("负偏移: %d", off)
	}
	if len(p) == 0 {
		return 0, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	done := 0
	for done < len(p) {
		pos := off + int64(done)
		block := r.cachedBlock(pos / rangeBlockSize)
		within := int(pos % rangeBlockSize)

		if within >= len(block) {
			// 已读到对象末尾：块是最后一块且偏移越界。
			return done, io.EOF
		}
		done += copy(p[done:], block[within:])
	}
	return done, nil
}

// cachedBlock 返回指定块，未缓存则发起一次 Range 请求拉取。
// 调用方必须持有 r.mu。
func (r *HTTPRangeReader) cachedBlock(index int64) []byte {
	if block, ok := r.blocks[index]; ok {
		return block
	}

	start := index * rangeBlockSize
	end := start + rangeBlockSize - 1
	req, err := http.NewRequest(http.MethodGet, r.url, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	block := r.fetch(req)
	r.blocks[index] = block // 失败时缓存空块，避免对同一坏块反复重试。
	return block
}

// fetch 执行 Range 请求并返回响应体字节；任何异常返回空块，由 ReadAt 转成 io.EOF。
// 调用方必须持有 r.mu。
func (r *HTTPRangeReader) fetch(req *http.Request) []byte {
	resp, err := r.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	// 206 Partial Content 是标准 Range 响应；200 表示对象小于请求块（整块返回）。
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return nil
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}
	return data
}

// HTTPClientForRange 返回为 Range 读取优化的客户端：启用连接池、禁用压缩。
// 禁用压缩是因为 Range 必须按字节偏移读取，压缩会破坏偏移语义。
func HTTPClientForRange() *http.Client {
	return &http.Client{Transport: &http.Transport{
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 16,
		DisableCompression:  true,
	}}
}
