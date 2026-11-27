// watch_stall_test.go · 停滞检测的单测。
//
// 职责：验证「有进展不取消、停滞超阈值才取消」这条判据。
// 输入：可控的原子计数器与短阈值。
// 输出：对上送 ctx 是否被取消的断言。
// 边界：不连库、不碰文件、不需要对象存储。
//
// 为什么必须测：判错方向各有代价——
// 太敏感会在慢速链路上误杀正常下载，导致反复重试、永远下不完；
// 太迟钝则等于没有守护，卡住的进度条又是一动不动。
package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestWatchStallCancelsWhenNoProgress(t *testing.T) {
	// 阈值传 60 毫秒把测试压到秒级：判据本身与数值无关，缩短只是让测试快。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var pulled atomic.Int64

	done := make(chan struct{})
	go func() { watchStall(ctx, cancel, "test", &pulled, 60*time.Millisecond); close(done) }()

	select {
	case <-ctx.Done():
		// 期望：完全没有进展 → 被取消。
	case <-time.After(2 * time.Second):
		t.Fatal("进度始终为 0 却未被取消，守护失效——卡住的下载会永远挂着")
	}
	<-done
}

func TestWatchStallKeepsGoingWhileProgressing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var pulled atomic.Int64

	go watchStall(ctx, cancel, "test", &pulled, 120*time.Millisecond)

	// 持续喂进度，跨过多个阈值周期。慢但在动的下载不该被取消。
	for i := 1; i <= 6; i++ {
		time.Sleep(40 * time.Millisecond)
		pulled.Add(1024)
	}

	if ctx.Err() != nil {
		t.Fatal("下载一直有进展却被取消——慢速链路上的正常下载会被反复误杀")
	}
	cancel()
}
