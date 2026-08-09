package logger

// fix_log_rotation_test.go — 回归测试：checkLogRotation 之前只在真正触发轮转的
// 分支里把 logCount 归零，轮转在途（setupLogWorking==true）时计数会越过阈值无限
// 增长；而且 logCount/setupLogWorking 是无同步的普通变量，多个请求 goroutine 并发
// 读写它们本身就是数据竞争。

import (
	"sync"
	"testing"
)

// fixRotationReset 保存并恢复轮转相关的全局状态。
func fixRotationReset(t *testing.T) {
	t.Helper()
	prevCount, prevWorking := logCount.Load(), setupLogWorking.Load()
	t.Cleanup(func() {
		logCount.Store(prevCount)
		setupLogWorking.Store(prevWorking)
	})
}

// TestFixCheckLogRotation_CounterBoundedWhileRotating: 轮转在途时计数不得越过阈值
// 持续增长。修复前每次调用都是 logCount++ 且不归零，5 次调用后计数会是
// maxLogCount+5。
func TestFixCheckLogRotation_CounterBoundedWhileRotating(t *testing.T) {
	fixRotationReset(t)

	// 模拟“轮转已经在跑”：标志位被占用
	setupLogWorking.Store(true)
	logCount.Store(maxLogCount)

	for i := 0; i < 5; i++ {
		checkLogRotation()
	}

	if got := logCount.Load(); got > maxLogCount {
		t.Errorf("logCount = %d, must stay bounded by maxLogCount (%d) while a rotation is in flight", got, maxLogCount)
	}
	if !setupLogWorking.Load() {
		t.Error("an in-flight rotation flag must not be cleared by checkLogRotation")
	}
}

// TestFixCheckLogRotation_BelowThresholdJustCounts: 未到阈值时只计数，不触发轮转。
func TestFixCheckLogRotation_BelowThresholdJustCounts(t *testing.T) {
	fixRotationReset(t)

	setupLogWorking.Store(false)
	logCount.Store(0)

	for i := 0; i < 3; i++ {
		checkLogRotation()
	}
	if got := logCount.Load(); got != 3 {
		t.Errorf("logCount = %d, want 3", got)
	}
	if setupLogWorking.Load() {
		t.Error("rotation must not be triggered below the threshold")
	}
}

// TestFixCheckLogRotation_ConcurrentCallsAreSafe: 并发调用不得数据竞争，
// 计数仍然有界。修复前 logCount++ / setupLogWorking 是无保护的普通变量，
// 该用例在 -race 下会报竞争。
func TestFixCheckLogRotation_ConcurrentCallsAreSafe(t *testing.T) {
	fixRotationReset(t)

	// 占住标志位，避免测试真的去启动轮转 goroutine
	setupLogWorking.Store(true)
	logCount.Store(maxLogCount - 10)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				checkLogRotation()
			}
		}()
	}
	wg.Wait()

	if got := logCount.Load(); got > maxLogCount {
		t.Errorf("logCount = %d, must stay bounded by maxLogCount (%d)", got, maxLogCount)
	}
}
