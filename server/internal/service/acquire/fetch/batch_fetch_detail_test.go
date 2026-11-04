package fetch

import (
	"testing"
)

func TestBatchFetchStateDetailFlow(t *testing.T) {
	state := NewBatchFetchState()
	names := []string{"Seed.A", "Seed.B"}
	state.StartWithItems("task-1", names)

	task, ok := state.Snapshot("task-1")
	if !ok {
		t.Fatal("task not found")
	}
	if len(task.Items) != 2 || task.Items[0].Status != "queued" {
		t.Fatalf("expected 2 queued items, got %d", len(task.Items))
	}

	state.MarkItemStart("task-1", "Seed.A", []string{"我堡", "彩虹岛"})
	state.MarkItemAttemptStart("task-1", "Seed.A", "我堡", "12345")
	state.ApplyItemStage("task-1", "Seed.A", StageDetail, "running", "正在打开详情页")
	state.ApplyItemStage("task-1", "Seed.A", StageDetail, "failed", "http status 403")
	state.MarkItemAttemptEnd("task-1", "Seed.A", "我堡", false, "403")

	state.MarkItemAttemptStart("task-1", "Seed.A", "彩虹岛", "593793")
	state.ApplyItemStage("task-1", "Seed.A", StageTorrent, "running", "正在下载源站种子文件...")
	state.ApplyItemStage("task-1", "Seed.A", StageTorrent, "done", "种子文件下载完成")
	state.AppendItemLog("task-1", "Seed.A", "下载种子: 种子文件下载完成")

	task, _ = state.Snapshot("task-1")
	item := findBatchItem(&task, "Seed.A")
	if item == nil {
		t.Fatal("item missing")
	}
	if len(item.Attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(item.Attempts))
	}
	if item.Attempts[0].State != "failed" || item.Attempts[0].Reason != "403" {
		t.Fatalf("attempt 0 should be failed with reason, got %+v", item.Attempts[0])
	}
	detail := item.Attempts[0].Stages[StageDetail]
	if detail == nil || detail.State != "failed" {
		t.Fatalf("attempt0 detail stage missing: %+v", item.Attempts[0].Stages)
	}
	torrent := item.Attempts[1].Stages[StageTorrent]
	if torrent == nil || torrent.State != "done" {
		t.Fatalf("attempt1 torrent stage missing: %+v", item.Attempts[1].Stages)
	}
	if item.Stages[StageFetchTop] == nil || item.Stages[StageFetchTop].Site != "彩虹岛" {
		t.Fatalf("top fetch stage should track current site: %+v", item.Stages[StageFetchTop])
	}
	if len(item.LogTail) != 1 {
		t.Fatalf("expected log tail entry, got %v", item.LogTail)
	}

	state.MarkItemResult("task-1", "Seed.A", "success", "彩虹岛", "抓取成功")
	task, _ = state.Snapshot("task-1")
	item = findBatchItem(&task, "Seed.A")
	if item.Status != "success" || item.FinalSite != "彩虹岛" || item.DurationMs <= 0 {
		t.Fatalf("item terminal state wrong: %+v", item)
	}
	if task.CurrentName != "" {
		t.Fatalf("current name should clear after item end")
	}

	// Snapshot 深拷贝隔离：外部修改不得影响内部状态
	item.Stages[StageLocate].Message = "外部篡改"
	again, _ := state.Snapshot("task-1")
	inner := findBatchItem(&again, "Seed.A")
	if inner.Stages[StageLocate].Message == "外部篡改" {
		t.Fatal("snapshot must deep copy stages")
	}
}
