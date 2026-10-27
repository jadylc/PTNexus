package repository

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newSiteIDTestStore 构建 attachSiteTorrentIDs 所需的最小表结构：torrents / seed_parameters。
func newSiteIDTestStore(t *testing.T) *Store {
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
		`CREATE TABLE torrents (
			hash TEXT,
			name TEXT NOT NULL,
			size INTEGER,
			save_path TEXT,
			sites TEXT,
			downloader_id TEXT NOT NULL,
			last_seen TEXT NOT NULL,
			details TEXT,
			iyuu_last_check TEXT,
			state TEXT NOT NULL DEFAULT '做种中',
			is_hidden INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE seed_parameters (
			hash TEXT NOT NULL,
			torrent_id TEXT NOT NULL,
			site_name TEXT NOT NULL,
			nickname TEXT,
			name TEXT,
			PRIMARY KEY (hash, torrent_id, site_name)
		)`,
	}

	for _, stmt := range statements {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("create test schema failed: %v", err)
		}
	}

	return &Store{DB: db, DBType: "sqlite"}
}

func mustExecSiteIDTest(t *testing.T, db *gorm.DB, query string, args ...any) {
	t.Helper()
	if err := db.Exec(query, args...).Error; err != nil {
		t.Fatalf("exec %q failed: %v", query, err)
	}
}

// TestAttachSiteTorrentIDsBuildsPerSiteMap 验证从 seed_parameters 为 torrent 行补充站点内部 ID。
func TestAttachSiteTorrentIDsBuildsPerSiteMap(t *testing.T) {
	store := newSiteIDTestStore(t)
	repo := NewMigrateRepository(store)

	mustExecSiteIDTest(t, store.DB,
		`INSERT INTO torrents (hash, name, size, sites, downloader_id, last_seen, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-abc", "Release.One", 1024, "彩虹岛", "qb", "2026-10-01 00:00:00")
	mustExecSiteIDTest(t, store.DB,
		`INSERT INTO seed_parameters (hash, torrent_id, site_name, nickname) VALUES (?, ?, ?, ?)`,
		"hash-abc", "593793", "chdbits", "彩虹岛")

	rows, err := repo.ListTorrentsByNames([]string{"Release.One"})
	if err != nil {
		t.Fatalf("ListTorrentsByNames failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	ids, ok := rows[0]["site_torrent_ids"].(map[string]string)
	if !ok {
		t.Fatalf("expected site_torrent_ids map, got %#v", rows[0]["site_torrent_ids"])
	}
	if ids["彩虹岛"] != "593793" || ids["chdbits"] != "593793" {
		t.Fatalf("expected numeric site id for 彩虹岛/chdbits, got %#v", ids)
	}
}

// TestAttachSiteTorrentIDsNoSeedParameterLeavesEmpty 验证无 seed_parameters 时不写入站点 ID。
func TestAttachSiteTorrentIDsNoSeedParameterLeavesEmpty(t *testing.T) {
	store := newSiteIDTestStore(t)
	repo := NewMigrateRepository(store)

	mustExecSiteIDTest(t, store.DB,
		`INSERT INTO torrents (hash, name, size, sites, downloader_id, last_seen, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-empty", "Release.Two", 1024, "彩虹岛", "qb", "2026-10-01 00:00:00")

	rows, err := repo.ListTorrentsByNames([]string{"Release.Two"})
	if err != nil {
		t.Fatalf("ListTorrentsByNames failed: %v", err)
	}
	if _, exists := rows[0]["site_torrent_ids"]; exists {
		t.Fatalf("expected no site_torrent_ids when no seed_parameters, got %#v", rows[0])
	}
}

// TestAttachSiteTorrentIDsFallsBackToName 验证 hash 不匹配时按名称回退匹配 seed_parameters。
func TestAttachSiteTorrentIDsFallsBackToName(t *testing.T) {
	store := newSiteIDTestStore(t)
	repo := NewMigrateRepository(store)

	mustExecSiteIDTest(t, store.DB,
		`INSERT INTO torrents (hash, name, size, sites, downloader_id, last_seen, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-synthetic", "Release.Five", 1024, "彩虹岛", "qb", "2026-10-01 00:00:00")
	mustExecSiteIDTest(t, store.DB,
		`INSERT INTO seed_parameters (hash, name, torrent_id, site_name, nickname) VALUES (?, ?, ?, ?, ?)`,
		"real-infohash", "Release.Five", "593793", "chdbits", "彩虹岛")

	rows, err := repo.ListTorrentsByNames([]string{"Release.Five"})
	if err != nil {
		t.Fatalf("ListTorrentsByNames failed: %v", err)
	}
	ids, ok := rows[0]["site_torrent_ids"].(map[string]string)
	if !ok {
		t.Fatalf("expected site_torrent_ids map via name fallback, got %#v", rows[0]["site_torrent_ids"])
	}
	if ids["彩虹岛"] != "593793" {
		t.Fatalf("expected numeric site id via name fallback, got %#v", ids)
	}
}

// TestUpdateIYUUCheckAndFillDetailsMatchesCommaSeparatedSites 验证多站点逗号串也能回填 details。
func TestUpdateIYUUCheckAndFillDetailsMatchesCommaSeparatedSites(t *testing.T) {
	store := newSiteIDTestStore(t)
	repo := NewTorrentDataRepository(store)

	mustExecSiteIDTest(t, store.DB,
		`INSERT INTO torrents (hash, name, size, sites, downloader_id, last_seen, is_hidden, details) VALUES (?, ?, ?, ?, ?, ?, 0, '')`,
		"hash-multi", "Release.Three", 2048, "ssd,彩虹岛", "qb", "2026-10-01 00:00:00")

	filled, err := repo.UpdateIYUUCheckAndFillDetails("Release.Three", 2048, map[string]string{
		"彩虹岛": "https://ptchdbits.co/details.php?id=593793&hit=1",
	}, "2026-10-02 00:00:00")
	if err != nil {
		t.Fatalf("UpdateIYUUCheckAndFillDetails failed: %v", err)
	}
	if filled != 1 {
		t.Fatalf("expected filled=1, got %d", filled)
	}

	var details string
	if err := store.DB.Table("torrents").Where("name = ?", "Release.Three").Pluck("details", &details).Error; err != nil {
		t.Fatalf("query details failed: %v", err)
	}
	if !strings.Contains(details, "id=593793") {
		t.Fatalf("expected details filled with numeric id, got %q", details)
	}
}

// TestUpdateIYUUCheckAndFillDetailsDoesNotOverwriteExistingDetails 验证已有 details 不被覆盖。
func TestUpdateIYUUCheckAndFillDetailsDoesNotOverwriteExistingDetails(t *testing.T) {
	store := newSiteIDTestStore(t)
	repo := NewTorrentDataRepository(store)

	mustExecSiteIDTest(t, store.DB,
		`INSERT INTO torrents (hash, name, size, sites, downloader_id, last_seen, is_hidden, details) VALUES (?, ?, ?, ?, ?, ?, 0, 'https://ptchdbits.co/details.php?id=111')`,
		"hash-existing", "Release.Four", 2048, "彩虹岛", "qb", "2026-10-01 00:00:00")

	filled, err := repo.UpdateIYUUCheckAndFillDetails("Release.Four", 2048, map[string]string{
		"彩虹岛": "https://ptchdbits.co/details.php?id=593793&hit=1",
	}, "2026-10-02 00:00:00")
	if err != nil {
		t.Fatalf("UpdateIYUUCheckAndFillDetails failed: %v", err)
	}
	if filled != 0 {
		t.Fatalf("expected filled=0, got %d", filled)
	}

	var details string
	if err := store.DB.Table("torrents").Where("name = ?", "Release.Four").Pluck("details", &details).Error; err != nil {
		t.Fatalf("query details failed: %v", err)
	}
	if !strings.Contains(details, "id=111") || strings.Contains(details, "id=593793") {
		t.Fatalf("expected existing details preserved, got %q", details)
	}
}
