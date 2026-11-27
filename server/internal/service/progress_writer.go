// progress_writer.go · 下载进度上报。
//
// 职责：包装 io.Writer，把「已写入多少字节」按固定间隔报给回调。
// 输入：目标 Writer、总字节数、上报回调。
// 输出：满足 io.Writer，写入量与包装的 Writer 完全一致。
// 边界：只计数与上报，不做缓冲、不改数据、不认识 HTTP 或数据库。
//
// 为什么需要节流：io.Copy 用小缓冲逐块写，一个 119MB 的包会产生上千次 Write。
// 每次都写数据库会让进度上报本身成为瓶颈，把磁盘 IO 挤到一边。
// 按「总量百分比」节流而不是按次数：大包小包都能得到一致的进度粒度。
package service

import "io"

// progressStep 是两次上报之间的进度间隔（百分比）。
//
// 取 2%：119MB 的包约每 2.4MB 报一次，前端刷新足够顺滑；
// 若取 1%，写库次数翻倍却看不出差别。
const progressStep = 2

// progressWriter 把写入字节数折算成百分比，跨过每个 progressStep 就上报一次。
//
// 不用原子操作或锁：它只在单次下载的 goroutine 里被使用，
// 并发安全由调用方（PullLocal 的串行流程）保证，加锁反而制造误解。
type progressWriter struct {
	dst      io.Writer
	total    int64
	written  int64
	reported int // 上次上报时的百分比，用于判断是否跨过下一个台阶
	report   func(pulled int64)
}

func newProgressWriter(dst io.Writer, total int64, report func(pulled int64)) *progressWriter {
	return &progressWriter{dst: dst, total: total, report: report}
}

// Write 透传写入并累计进度，跨过台阶时上报。
//
// 返回的 n 必须是底层 Writer 真实写入的字节数：io.Copy 依赖它判断短写，
// 这里若擅自改成 len(p) 会让拷贝逻辑以为写成功，数据静默丢失。
func (w *progressWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	w.written += int64(n)

	// total 未知（<=0）时只计数不上报：算不出百分比，报了也没有意义。
	if w.total > 0 {
		if percent := int(w.written * 100 / w.total); percent >= w.reported+progressStep {
			w.reported = percent
			w.report(w.written)
		}
	}
	return n, err
}