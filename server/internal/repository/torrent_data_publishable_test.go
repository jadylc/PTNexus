package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestListReviewedSeedParamsByHashesInClause 复现生产"两个转种页面全空"：
// 验证 ListReviewedSeedParamsByHashes 的 IN (?) 传 []string 在 Raw SQL 中
// 是否正确展开并命中行。
func TestListReviewedSeedParamsByHashesInClause(t *testing.T) {
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
		`CREATE TABLE seed_parameters (
			hash TEXT,
			type TEXT,
			medium TEXT,
			video_codec TEXT,
			audio_codec TEXT,
			resolution TEXT,
			team TEXT,
			source TEXT,
			tags TEXT,
			title_components TEXT,
			mediainfo TEXT,
			body TEXT,
			is_reviewed INTEGER
		)`,
		`INSERT INTO seed_parameters VALUES (
			'hash-a', 'category.movie', 'medium.bluray', 'video.h264', 'audio.dts',
			'resolution.r1080p', 'team.x', 'source.china', '[]', '[]', 'General MI', 'body text', 1
		)`,
	}
	for _, stmt := range statements {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("prepare test data failed: %v", err)
		}
	}

	store := &Store{DB: db, DBType: "sqlite"}
	repo := NewTorrentDataRepository(store)

	rows, err := repo.ListReviewedSeedParamsByHashes(context.Background(), []string{"hash-a"})
	if err != nil {
		t.Fatalf("ListReviewedSeedParamsByHashes returned error: %v", err)
	}
	if len(rows["hash-a"]) != 1 {
		t.Fatalf("expected 1 reviewed row for hash-a, got %d (rows=%#v)", len(rows["hash-a"]), rows)
	}
}
