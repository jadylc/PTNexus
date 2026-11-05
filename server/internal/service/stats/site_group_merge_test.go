package stats

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/pt-nexus/server/internal/config"
	"github.com/pt-nexus/server/internal/repository"
	"gorm.io/gorm"
)

func newGroupStatsTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE sites (site TEXT, nickname TEXT, "group" TEXT, base_url TEXT)`,
		`CREATE TABLE torrents (hash TEXT, name TEXT, size INTEGER, "group" TEXT, is_hidden INTEGER DEFAULT 0)`,
		`CREATE TABLE seed_parameters (hash TEXT, name TEXT, title_components TEXT)`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("create schema: %v", err)
		}
	}
	store := &repository.Store{DB: db, DBType: "sqlite"}
	return New(repository.NewStatsRepository(store), &config.Manager{}), db
}

// TestGetGroupStatsMergesDuplicateSiteGroupRows 官组统计与未归属补归可能产生
// "同站点 + 同组名"两行（干净 -FRDS 与带引号污染 '-FRDS 各一批），必须合并计数，
// 避免前端展开出现两个 FRDS 明细行。
func TestGetGroupStatsMergesDuplicateSiteGroupRows(t *testing.T) {
	svc, db := newGroupStatsTestService(t)
	mustExec := func(q string, args ...any) {
		t.Helper()
		if err := db.Exec(q, args...).Error; err != nil {
			t.Fatalf("exec: %v", err)
		}
	}

	mustExec(`INSERT INTO sites (site, nickname, "group") VALUES (?, ?, ?)`, "keepfrds", "月月", "-FRDS")
	// 一批干净 -FRDS：被 QueryGroupStats 统计
	mustExec(`INSERT INTO torrents (hash, name, size, "group", is_hidden) VALUES (?, ?, ?, ?, 0)`,
		"h-clean", "Clean.Release-FRDS", 100, "-FRDS")
	// 一批带引号污染：官组统计 JOIN 不上，由 title_components 制作组补归
	mustExec(`INSERT INTO torrents (hash, name, size, "group", is_hidden) VALUES (?, ?, ?, ?, 0)`,
		"h-quoted", "Quoted.Release-FRDS", 200, "'-FRDS")
	mustExec(`INSERT INTO seed_parameters (hash, name, title_components) VALUES (?, ?, ?)`,
		"h-quoted", "Quoted.Release-FRDS", `[{"key":"制作组","value":"FRDS"}]`)

	rows, err := svc.GetGroupStats("")
	if err != nil {
		t.Fatalf("GetGroupStats: %v", err)
	}

	frdsRows := 0
	var totalCount, totalSize int64
	for _, row := range rows {
		if row["site_name"] == "月月" && row["group_suffix"] == "FRDS" {
			frdsRows++
			totalCount += toInt64(row["torrent_count"])
			totalSize += toInt64(row["total_size"])
		}
	}
	if frdsRows != 1 {
		t.Fatalf("expected 1 merged 月月/FRDS row, got %d: %#v", frdsRows, rows)
	}
	if totalCount != 2 || totalSize != 300 {
		t.Fatalf("expected merged count=2 size=300, got count=%d size=%d: %#v", totalCount, totalSize, rows)
	}
}
