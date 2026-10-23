package fetch

import (
	"testing"
	"time"
)

// TestBatchFetchStateCancelAndTimestamps 验证取消标志与时间字段：
// 任务监控的「终止」靠 Cancel 置位、执行侧靠 IsCancelled 协作退出；
// StartedAt/UpdatedAt 用于面板判断任务是否卡住（此前恒为空）。
func TestBatchFetchStateCancelAndTimestamps(t *testing.T) {
	state := NewBatchFetchState()
	state.Start("fetch-1", 3)

	if state.IsCancelled("fetch-1") {
		t.Fatalf("新任务不应处于取消状态")
	}
	task, ok := state.Snapshot("fetch-1")
	if !ok {
		t.Fatalf("snapshot failed")
	}
	if task.StartedAt == "" || task.UpdatedAt == "" {
		t.Fatalf("Start 应写入时间字段, got started=%q updated=%q", task.StartedAt, task.UpdatedAt)
	}

	if !state.Cancel("fetch-1") {
		t.Fatalf("Cancel 应返回 true")
	}
	if !state.IsCancelled("fetch-1") {
		t.Fatalf("Cancel 后 IsCancelled 应为 true")
	}

	// 不存在的任务：Cancel 返回 false，IsCancelled 返回 true（避免执行侧空转）。
	if state.Cancel("missing") {
		t.Fatalf("不存在的任务 Cancel 应为 false")
	}
	if !state.IsCancelled("missing") {
		t.Fatalf("不存在的任务 IsCancelled 应为 true")
	}

	if !state.Finish("fetch-1") {
		t.Fatalf("Finish 应返回 true")
	}
	finished, _ := state.Snapshot("fetch-1")
	if finished.IsRunning {
		t.Fatalf("Finish 后不应仍在运行")
	}
	if !finished.Cancelled {
		t.Fatalf("Finish 不应清掉 Cancelled 标志")
	}
}

// TestRunBatchFetchStopsOnCancel 验证协作式取消确实会停止剩余条目，
// 而不是只把面板状态改掉、后台照样跑完。
func TestRunBatchFetchStopsOnCancel(t *testing.T) {
	names := []string{"a", "b", "c"}
	emitted := 0
	visited := 0

	RunBatchFetch(names, []string{"hdsky"}, nil, BatchFetchRunnerDeps{
		OnResult: func(bool, map[string]any) { emitted++ },
		Sleep:    func(time.Duration) {},
		ShouldCancel: func() bool {
			visited++
			return visited > 1
		},
	})

	if emitted != 1 {
		t.Fatalf("取消后应只处理 1 项, got emitted=%d visited=%d", emitted, visited)
	}
}
