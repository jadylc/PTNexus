package repository

import (
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newPublishRecoveryTestStore 建立只包含本次测试所需两张表的内存库。
func newPublishRecoveryTestStore(t *testing.T) *Store {
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

	statements := []string{
		`CREATE TABLE publish_queue_tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			group_id TEXT NOT NULL,
			status TEXT NOT NULL,
			torrent_id TEXT,
			source_site TEXT,
			target_site TEXT,
			attempt_count INTEGER NOT NULL DEFAULT 0,
			next_run_at DATETIME,
			started_at DATETIME,
			finished_at DATETIME,
			last_error TEXT,
			last_result TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE publish_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			publish_trigger TEXT NOT NULL,
			queue_task_id INTEGER,
			torrent_id TEXT,
			source_site TEXT,
			target_site TEXT,
			status TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
	}
	for _, stmt := range statements {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("create test schema failed: %v", err)
		}
	}

	return &Store{DB: db, DBType: "sqlite"}
}

func insertQueueTaskForRecovery(t *testing.T, db *gorm.DB, status string, startedAt *time.Time) {
	t.Helper()
	now := time.Now().Format(PublishQueueTimeLayout)
	startedText := any(nil)
	if startedAt != nil {
		startedText = startedAt.Format(PublishQueueTimeLayout)
	}
	if err := db.Exec(
		`INSERT INTO publish_queue_tasks (group_id, status, started_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		"group-1", status, startedText, now, now,
	).Error; err != nil {
		t.Fatalf("insert queue task failed: %v", err)
	}
}

// TestRecoverOrphanRunningTasks 验证僵尸 running 任务能被回收：
// 启动语义（olderThan<=0 全量回收）与运行期超时兜底（只回收超过阈值且无 started_at 的）。
func TestRecoverOrphanRunningTasks(t *testing.T) {
	store := newPublishRecoveryTestStore(t)
	repo := NewPublishQueueRepository(store)

	recent := time.Now()
	stale := time.Now().Add(-2 * time.Hour)
	insertQueueTaskForRecovery(t, store.DB, PublishQueueStatusRunning, &recent)
	insertQueueTaskForRecovery(t, store.DB, PublishQueueStatusRunning, &stale)
	insertQueueTaskForRecovery(t, store.DB, PublishQueueStatusQueued, nil)
	insertQueueTaskForRecovery(t, store.DB, PublishQueueStatusSuccess, &recent)

	// 超时模式：只回收 started_at 早于阈值的那条。
	recovered, err := repo.RecoverOrphanRunningTasks(time.Now(), 30*time.Minute)
	if err != nil {
		t.Fatalf("recover failed: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("期望超时回收 1 条, got %d", recovered)
	}

	// 启动模式：剩下这条 running 也全部回收。
	recovered, err = repo.RecoverOrphanRunningTasks(time.Now(), 0)
	if err != nil {
		t.Fatalf("recover failed: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("期望启动回收 1 条, got %d", recovered)
	}

	var runningCount int64
	if err := store.DB.Table("publish_queue_tasks").Where("status = ?", PublishQueueStatusRunning).Count(&runningCount).Error; err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if runningCount != 0 {
		t.Fatalf("回收后不应残留 running, got %d", runningCount)
	}

	var queuedCount int64
	if err := store.DB.Table("publish_queue_tasks").Where("status = ?", PublishQueueStatusQueued).Count(&queuedCount).Error; err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if queuedCount != 3 {
		t.Fatalf("两条回收 + 原本一条 queued 应为 3, got %d", queuedCount)
	}

	// 幂等：再次调用不应改动已结束任务，也不应重复回收。
	recovered, err = repo.RecoverOrphanRunningTasks(time.Now(), 0)
	if err != nil {
		t.Fatalf("recover failed: %v", err)
	}
	if recovered != 0 {
		t.Fatalf("重复调用应回收 0 条, got %d", recovered)
	}
}

// TestExistsSucceededPublish 验证重复发布幂等判定：按 source+torrent+target 精确匹配，
// 且排除本任务自己写入的成功记录，避免"刚成功又被判已发布"。
func TestExistsSucceededPublish(t *testing.T) {
	store := newPublishRecoveryTestStore(t)
	repo := NewPublishLogRepository(store)

	now := time.Now().Format("2006-01-02 15:04:05")
	if err := store.DB.Exec(
		`INSERT INTO publish_logs (publish_trigger, queue_task_id, torrent_id, source_site, target_site, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"queue", 7, "1001", "hdsky", "mteam", "success", now, now,
	).Error; err != nil {
		t.Fatalf("insert log failed: %v", err)
	}

	existed, err := repo.ExistsSucceededPublish("hdsky", "1001", "mteam", 0)
	if err != nil {
		t.Fatalf("exists failed: %v", err)
	}
	if !existed {
		t.Fatalf("已有成功记录时应判为已发布")
	}

	existed, err = repo.ExistsSucceededPublish("hdsky", "1001", "mteam", 7)
	if err != nil {
		t.Fatalf("exists failed: %v", err)
	}
	if existed {
		t.Fatalf("排除本任务后不应判为已发布")
	}

	for _, probe := range []struct{ source, torrent, target string }{
		{"hdsky", "1001", "pter"},
		{"pter", "1001", "mteam"},
		{"hdsky", "9999", "mteam"},
		{"", "1001", "mteam"},
	} {
		got, err := repo.ExistsSucceededPublish(probe.source, probe.torrent, probe.target, 0)
		if err != nil {
			t.Fatalf("exists failed: %v", err)
		}
		if got {
			t.Fatalf("定位字段不匹配却判为已发布: %+v", probe)
		}
	}
}
