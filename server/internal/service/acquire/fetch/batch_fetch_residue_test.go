package fetch

import "testing"

// TestBatchFetchStateNoRunningResidueAfterSuccess 种子进入终态后，
// 所有阶段（含 B 顶层与 attempt 子步）不得残留 running 状态（阶段条永远橙色的 bug）。
func TestBatchFetchStateNoRunningResidueAfterSuccess(t *testing.T) {
	state := NewBatchFetchState()
	state.StartWithItems("task-x", []string{"Seed.X"})

	state.MarkItemStart("task-x", "Seed.X", []string{"彩虹岛"})
	state.MarkItemAttemptStart("task-x", "Seed.X", "彩虹岛", "593793")
	state.ApplyItemStage("task-x", "Seed.X", StageRepair, "running", "正在并发校验并纠偏抓取结果...")
	state.ApplyItemStage("task-x", "Seed.X", StageRepair, "warning", "媒体信息正在后台提取")
	state.ApplyItemStage("task-x", "Seed.X", StagePersist, "running", "正在写入 seed_parameters...")
	// 不发送 C done / E done，直接终态——模拟修复链最后一步就是写库、由终态收口
	state.MarkItemResult("task-x", "Seed.X", "success", "彩虹岛", "抓取成功")

	task, _ := state.Snapshot("task-x")
	item := findBatchItem(&task, "Seed.X")
	if item == nil {
		t.Fatal("item missing")
	}
	for stageID, record := range item.Stages {
		if record.State == "running" {
			t.Fatalf("stage %s still running after success: %+v", stageID, record)
		}
	}
	if item.Stages[StagePersist].State != "done" {
		t.Fatalf("persist should close as done, got %s", item.Stages[StagePersist].State)
	}
	if item.Stages[StageFetchTop].State != "done" {
		t.Fatalf("fetch top should close as done, got %s", item.Stages[StageFetchTop].State)
	}
	if len(item.Attempts) != 1 || item.Attempts[0].State != "success" {
		t.Fatalf("attempt should be closed success: %+v", item.Attempts)
	}
}
