package repository

import (
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newTorrentRepositoryTestStore(t *testing.T) *Store {
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
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	statements := []string{
		`CREATE TABLE traffic_stats (
			stat_datetime TEXT NOT NULL,
			downloader_id TEXT NOT NULL,
			uploaded INTEGER DEFAULT 0,
			downloaded INTEGER DEFAULT 0,
			upload_speed INTEGER DEFAULT 0,
			download_speed INTEGER DEFAULT 0,
			cumulative_uploaded INTEGER NOT NULL DEFAULT 0,
			cumulative_downloaded INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (stat_datetime, downloader_id)
		)`,
	}
	for _, stmt := range statements {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("create test schema failed: %v", err)
		}
	}

	return &Store{DB: db, DBType: "sqlite"}
}

func insertTrafficStat(t *testing.T, db *gorm.DB, dt time.Time, downloaderID string, cumulativeUploaded, cumulativeDownloaded int64) {
	t.Helper()
	if err := db.Exec(
		`INSERT INTO traffic_stats (stat_datetime, downloader_id, uploaded, downloaded, upload_speed, download_speed, cumulative_uploaded, cumulative_downloaded)
		 VALUES (?, ?, 0, 0, 0, 0, ?, ?)`,
		dt.Format("2006-01-02 15:04:05"), downloaderID, cumulativeUploaded, cumulativeDownloaded,
	).Error; err != nil {
		t.Fatalf("insert traffic stat failed: %v", err)
	}
}

func TestTrafficTodayFiltersToCurrentLocalDay(t *testing.T) {
	store := newTorrentRepositoryTestStore(t)
	repo := NewTorrentRepository(store)

	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)

	insertTrafficStat(t, store.DB, now.Add(-2*time.Minute), "qb-1", 50, 100)
	insertTrafficStat(t, store.DB, now.Add(-1*time.Minute), "qb-1", 80, 150)
	insertTrafficStat(t, store.DB, yesterday, "qb-1", 999, 999)
	insertTrafficStat(t, store.DB, now.Add(-5*time.Minute), "tr-2", 2000, 1000)
	insertTrafficStat(t, store.DB, now.Add(-1*time.Minute), "tr-2", 2200, 1200)

	result, err := repo.TrafficToday()
	if err != nil {
		t.Fatalf("TrafficToday failed: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("unexpected downloader count: %d result=%#v", len(result), result)
	}
	qb := result["qb-1"]
	if qb == nil {
		t.Fatalf("qb-1 missing from result: %#v", result)
	}
	if qb["today_dl"] != 50 || qb["today_ul"] != 30 {
		t.Fatalf("qb-1 unexpected today traffic: %#v", qb)
	}
	tr := result["tr-2"]
	if tr == nil {
		t.Fatalf("tr-2 missing from result: %#v", result)
	}
	if tr["today_dl"] != 200 || tr["today_ul"] != 200 {
		t.Fatalf("tr-2 unexpected today traffic: %#v", tr)
	}
}

func TestTrafficTodayCachesWithinTTL(t *testing.T) {
	origTTL := trafficTodayCacheTTL
	trafficTodayCacheTTL = 50 * time.Millisecond
	defer func() { trafficTodayCacheTTL = origTTL }()

	store := newTorrentRepositoryTestStore(t)
	repo := NewTorrentRepository(store)

	now := time.Now()
	insertTrafficStat(t, store.DB, now.Add(-1*time.Minute), "qb-1", 100, 100)

	first, err := repo.TrafficToday()
	if err != nil {
		t.Fatalf("TrafficToday failed: %v", err)
	}
	if first["qb-1"]["today_dl"] != 0 || first["qb-1"]["today_ul"] != 0 {
		t.Fatalf("unexpected initial traffic: %#v", first["qb-1"])
	}

	// TTL 内新增数据后立即查询，应命中缓存并返回旧值。
	insertTrafficStat(t, store.DB, now.Add(-30*time.Second), "qb-1", 200, 300)
	cached, err := repo.TrafficToday()
	if err != nil {
		t.Fatalf("TrafficToday (cached) failed: %v", err)
	}
	if cached["qb-1"]["today_dl"] != 0 || cached["qb-1"]["today_ul"] != 0 {
		t.Fatalf("cache miss: expected stale traffic, got %#v", cached["qb-1"])
	}

	// TTL 过期后再次查询应拿到最新数据。
	time.Sleep(80 * time.Millisecond)
	fresh, err := repo.TrafficToday()
	if err != nil {
		t.Fatalf("TrafficToday (fresh) failed: %v", err)
	}
	if fresh["qb-1"]["today_dl"] != 200 || fresh["qb-1"]["today_ul"] != 100 {
		t.Fatalf("unexpected refreshed traffic: %#v", fresh["qb-1"])
	}
}
