package repository

import (
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newTaskRunTestStore 用真实的 EnsureSchema 建库，顺带验证 task_runs/task_run_items
// 的建表语句与索引声明在 sqlite 上确实可执行（MySQL/PG 由 CI 构建覆盖）。
func newTaskRunTestStore(t *testing.T) *Store {
	t.Helper()

	dsn := "file:" + strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(t.Name()) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB failed: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	store := &Store{DB: db, DBType: "sqlite"}
	if err := NewSchemaManager(store).EnsureSchema(); err != nil {
		t.Fatalf("EnsureSchema failed: %v", err)
	}
	return store
}

// TestTaskRunLifecycleAndResume 覆盖批次登记 → 条目终态 → 聚合进度 → 续跑候选 → 收尾 → 清理。
func TestTaskRunLifecycleAndResume(t *testing.T) {
	store := newTaskRunTestStore(t)
	repo := NewTaskRunRepository(store)

	if err := repo.CreateRun("batch-1", TaskRunKindPublishBatch, `{"targetSite":"mteam"}`, 2, []string{"mteam", "hdsky", "pter"}); err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	items, err := repo.ListItems("batch-1")
	if err != nil {
		t.Fatalf("ListItems failed: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("期望 3 个条目, got %d", len(items))
	}
	for _, item := range items {
		if item.Status != TaskItemStatusPending {
			t.Fatalf("新建条目应为 pending, got %s", item.Status)
		}
	}

	// 重启后 running 条目要退回 pending 才能重放。
	if err := repo.MarkItemRunning("batch-1", "mteam"); err != nil {
		t.Fatalf("MarkItemRunning failed: %v", err)
	}
	if n, err := repo.ResetRunningItems("batch-1"); err != nil || n != 1 {
		t.Fatalf("ResetRunningItems got n=%d err=%v", n, err)
	}

	if err := repo.FinishItem("batch-1", "mteam", TaskItemStatusSucceeded, "ok"); err != nil {
		t.Fatalf("FinishItem failed: %v", err)
	}
	if err := repo.FinishItem("batch-1", "hdsky", TaskItemStatusFailed, "站点拒绝"); err != nil {
		t.Fatalf("FinishItem failed: %v", err)
	}

	resumable, err := repo.ListResumableRuns()
	if err != nil {
		t.Fatalf("ListResumableRuns failed: %v", err)
	}
	if len(resumable) != 1 || resumable[0].RunID != "batch-1" {
		t.Fatalf("running 批次应可被续跑捞出, got %+v", resumable)
	}

	summaries, err := repo.ListRecentRunSummaries(10)
	if err != nil {
		t.Fatalf("ListRecentRunSummaries failed: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("期望 1 条批次摘要, got %d", len(summaries))
	}
	summary := summaries[0]
	// 进度全部由条目聚合得出：2 已处理（含失败）、1 成功、1 失败，剩余 1 条待执行。
	if summary.Total != 3 || summary.Processed != 2 || summary.Success != 1 || summary.Failed != 1 {
		t.Fatalf("聚合进度不正确: total=%d processed=%d success=%d failed=%d", summary.Total, summary.Processed, summary.Success, summary.Failed)
	}
	if summary.Concurrency != 2 {
		t.Fatalf("并发度应随批次持久化, got %d", summary.Concurrency)
	}
	if strings.TrimSpace(summary.PayloadJSON) == "" {
		t.Fatalf("续跑入参必须落库")
	}

	if err := repo.FinishRun("batch-1", TaskRunStatusCancelled, "用户手动终止"); err != nil {
		t.Fatalf("FinishRun failed: %v", err)
	}
	// 终止后未执行条目被置为 skipped，不会被再次续跑。
	items, _ = repo.ListItems("batch-1")
	for _, item := range items {
		if item.Target == "pter" && item.Status != TaskItemStatusSkipped {
			t.Fatalf("终止批次后未执行条目应为 skipped, got %s", item.Status)
		}
	}
	resumable, _ = repo.ListResumableRuns()
	if len(resumable) != 0 {
		t.Fatalf("已结束批次不应再出现在续跑列表, got %d", len(resumable))
	}

	deleted, err := repo.DeleteFinishedRunsBefore(time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("DeleteFinishedRunsBefore failed: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("应清理 1 个历史批次, got %d", deleted)
	}
	if left, _ := repo.ListItems("batch-1"); len(left) != 0 {
		t.Fatalf("条目的级联删除未生效, got %d", len(left))
	}
}

// TestTaskRunIllegalStatusRejected 验证终态枚举校验，避免脏状态写进表里污染续跑判断。
func TestTaskRunIllegalStatusRejected(t *testing.T) {
	store := newTaskRunTestStore(t)
	repo := NewTaskRunRepository(store)

	if err := repo.CreateRun("batch-2", TaskRunKindBatchFetch, "{}", 1, []string{"a"}); err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}
	if err := repo.FinishItem("batch-2", "a", "whatever", ""); err == nil {
		t.Fatalf("非法条目终态应被拒绝")
	}
	if err := repo.FinishRun("batch-2", TaskRunStatusRunning, ""); err == nil {
		t.Fatalf("FinishRun 不应接受 running 作为终态")
	}
	if err := repo.CreateRun("  ", TaskRunKindBatchFetch, "{}", 1, []string{"a"}); err == nil {
		t.Fatalf("空 run_id 应被拒绝")
	}
}
