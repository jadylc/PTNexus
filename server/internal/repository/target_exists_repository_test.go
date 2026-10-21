package repository

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newTargetExistsTestStore 构建目标站已存在检测所需的最小表结构：
// sites / torrents（快照）/ publish_queue_tasks / publish_logs。
func newTargetExistsTestStore(t *testing.T) *Store {
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
		`CREATE TABLE sites (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			site TEXT UNIQUE,
			nickname TEXT
		)`,
		`CREATE TABLE torrents (
			hash TEXT,
			name TEXT NOT NULL,
			size INTEGER,
			sites TEXT,
			downloader_id TEXT NOT NULL,
			last_seen TEXT NOT NULL,
			is_hidden INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE publish_queue_tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			group_id TEXT NOT NULL,
			status TEXT NOT NULL,
			publish_trigger TEXT NOT NULL DEFAULT 'queue',
			source_site TEXT,
			torrent_id TEXT,
			target_site TEXT,
			title TEXT,
			payload_json TEXT NOT NULL,
			upload_data_json TEXT NOT NULL,
			context_json TEXT NOT NULL,
			attempt_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE publish_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			publish_trigger TEXT NOT NULL,
			source_site TEXT,
			torrent_id TEXT,
			target_site TEXT,
			title TEXT,
			status TEXT NOT NULL,
			cost_ms INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
	}

	for _, stmt := range statements {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("create test schema failed: %v", err)
		}
	}

	return &Store{DB: db, DBType: "sqlite"}
}

func mustExecTargetExistsTest(t *testing.T, db *gorm.DB, query string, args ...any) {
	t.Helper()
	if err := db.Exec(query, args...).Error; err != nil {
		t.Fatalf("exec %q failed: %v", query, err)
	}
}

// TestCheckTargetExistsSnapshotMatchesCommaSeparatedSites 验证快照来源兼容 "siteA,siteB" 历史格式。
func TestCheckTargetExistsSnapshotMatchesCommaSeparatedSites(t *testing.T) {
	store := newTargetExistsTestStore(t)
	repo := NewMigrateRepository(store)

	mustExecTargetExistsTest(t, store.DB,
		`INSERT INTO sites (site, nickname) VALUES (?, ?)`, "mteam", "M-Team")
	mustExecTargetExistsTest(t, store.DB,
		`INSERT INTO torrents (hash, name, size, sites, downloader_id, last_seen, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-abc", "Release.One", 1024, "hdc,mteam", "qb", "2026-10-01 00:00:00")

	source, detail, err := repo.CheckTargetExists(TargetExistsQuery{
		Hash:       "hash-abc",
		Name:       "Release.One",
		Size:       1024,
		TargetSite: "M-Team",
	})
	if err != nil {
		t.Fatalf("CheckTargetExists returned error: %v", err)
	}
	if source != "snapshot" {
		t.Fatalf("expected snapshot hit, got source=%q detail=%q", source, detail)
	}
}

// TestCheckTargetExistsSnapshotMatchesAliasSiteCode 验证别名解析：目标站给昵称，快照存站点代码也能命中。
func TestCheckTargetExistsSnapshotMatchesAliasSiteCode(t *testing.T) {
	store := newTargetExistsTestStore(t)
	repo := NewMigrateRepository(store)

	mustExecTargetExistsTest(t, store.DB,
		`INSERT INTO sites (site, nickname) VALUES (?, ?)`, "hdt", "HD 天空")
	mustExecTargetExistsTest(t, store.DB,
		`INSERT INTO torrents (hash, name, size, sites, downloader_id, last_seen, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-abc", "Release.One", 1024, "hdt", "qb", "2026-10-01 00:00:00")

	source, _, err := repo.CheckTargetExists(TargetExistsQuery{
		Name:       "Release.One",
		Size:       1024,
		TargetSite: "HD 天空",
	})
	if err != nil {
		t.Fatalf("CheckTargetExists returned error: %v", err)
	}
	if source != "snapshot" {
		t.Fatalf("expected snapshot hit via alias resolution, got source=%q", source)
	}
}

// TestCheckTargetExistsSnapshotMatchesByHashOnly 验证仅 hash 指纹即可命中快照。
func TestCheckTargetExistsSnapshotMatchesByHashOnly(t *testing.T) {
	store := newTargetExistsTestStore(t)
	repo := NewMigrateRepository(store)

	mustExecTargetExistsTest(t, store.DB,
		`INSERT INTO torrents (hash, name, size, sites, downloader_id, last_seen, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-abc", "Release.Other.Name", 4096, "mteam", "qb", "2026-10-01 00:00:00")

	source, _, err := repo.CheckTargetExists(TargetExistsQuery{
		Hash:       "hash-abc",
		TargetSite: "mteam",
	})
	if err != nil {
		t.Fatalf("CheckTargetExists returned error: %v", err)
	}
	if source != "snapshot" {
		t.Fatalf("expected snapshot hit by hash, got source=%q", source)
	}
}

// TestCheckTargetExistsQueueMatchesByTitleWithoutTorrentID 验证队列来源在 torrent_id 缺失时按 title 兜底命中。
func TestCheckTargetExistsQueueMatchesByTitleWithoutTorrentID(t *testing.T) {
	store := newTargetExistsTestStore(t)
	repo := NewMigrateRepository(store)

	mustExecTargetExistsTest(t, store.DB,
		`INSERT INTO publish_queue_tasks (group_id, status, source_site, torrent_id, target_site, title, payload_json, upload_data_json, context_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"g1", PublishQueueStatusQueued, "hdc", "", "mteam", "Release.One", "{}", "{}", "{}", "2026-10-01 00:00:00", "2026-10-01 00:00:00")

	source, _, err := repo.CheckTargetExists(TargetExistsQuery{
		Name:       "Release.One",
		TargetSite: "mteam",
	})
	if err != nil {
		t.Fatalf("CheckTargetExists returned error: %v", err)
	}
	if source != "queue" {
		t.Fatalf("expected queue hit by title, got source=%q", source)
	}
}

// TestCheckTargetExistsQueueMatchesByHashInPayload 验证队列来源按 upload_data_json 内 hash 兜底命中（大小写不敏感存储方小写）。
func TestCheckTargetExistsQueueMatchesByHashInPayload(t *testing.T) {
	store := newTargetExistsTestStore(t)
	repo := NewMigrateRepository(store)

	mustExecTargetExistsTest(t, store.DB,
		`INSERT INTO publish_queue_tasks (group_id, status, source_site, torrent_id, target_site, title, payload_json, upload_data_json, context_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"g1", PublishQueueStatusRunning, "hdc", "999", "mteam", "其他标题", "{}",
		`{"hash":"deadbeef","name":"Release.One"}`, `{"context":"x"}`, "2026-10-01 00:00:00", "2026-10-01 00:00:00")

	source, _, err := repo.CheckTargetExists(TargetExistsQuery{
		Hash:       "DEADBEEF",
		TargetSite: "mteam",
	})
	if err != nil {
		t.Fatalf("CheckTargetExists returned error: %v", err)
	}
	if source != "queue" {
		t.Fatalf("expected queue hit by payload hash, got source=%q", source)
	}
}

// TestCheckTargetExistsQueueSkipsFailedStatus 验证队列中 failed 状态不视为“已存在”。
func TestCheckTargetExistsQueueSkipsFailedStatus(t *testing.T) {
	store := newTargetExistsTestStore(t)
	repo := NewMigrateRepository(store)

	mustExecTargetExistsTest(t, store.DB,
		`INSERT INTO publish_queue_tasks (group_id, status, source_site, torrent_id, target_site, title, payload_json, upload_data_json, context_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"g1", PublishQueueStatusFailed, "hdc", "111", "mteam", "Release.One", "{}", "{}", "{}", "2026-10-01 00:00:00", "2026-10-01 00:00:00")

	source, _, err := repo.CheckTargetExists(TargetExistsQuery{
		Name:       "Release.One",
		TargetSite: "mteam",
	})
	if err != nil {
		t.Fatalf("CheckTargetExists returned error: %v", err)
	}
	if source != "" {
		t.Fatalf("expected no queue hit for failed status, got source=%q", source)
	}
}

// TestCheckTargetExistsPublishLogMatchesByTitle 验证发种日志来源按 title 命中（status success）。
func TestCheckTargetExistsPublishLogMatchesByTitle(t *testing.T) {
	store := newTargetExistsTestStore(t)
	repo := NewMigrateRepository(store)

	mustExecTargetExistsTest(t, store.DB,
		`INSERT INTO publish_logs (publish_trigger, source_site, torrent_id, target_site, title, status, cost_ms, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"queue", "hdc", "222", "mteam", "Release.One", PublishQueueStatusSuccess, 100, "2026-10-01 00:00:00", "2026-10-01 00:00:00")

	source, _, err := repo.CheckTargetExists(TargetExistsQuery{
		Name:       "Release.One",
		TargetSite: "mteam",
	})
	if err != nil {
		t.Fatalf("CheckTargetExists returned error: %v", err)
	}
	if source != "publish_log" {
		t.Fatalf("expected publish_log hit by title, got source=%q", source)
	}
}

// TestCheckTargetExistsPublishLogMatchesExistsStatus 验证发种日志中 status=exists 也视为已存在。
func TestCheckTargetExistsPublishLogMatchesExistsStatus(t *testing.T) {
	store := newTargetExistsTestStore(t)
	repo := NewMigrateRepository(store)

	mustExecTargetExistsTest(t, store.DB,
		`INSERT INTO publish_logs (publish_trigger, source_site, torrent_id, target_site, title, status, cost_ms, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"queue", "hdc", "333", "mteam", "Release.One", "exists", 100, "2026-10-01 00:00:00", "2026-10-01 00:00:00")

	source, _, err := repo.CheckTargetExists(TargetExistsQuery{
		Name:       "Release.One",
		TargetSite: "mteam",
	})
	if err != nil {
		t.Fatalf("CheckTargetExists returned error: %v", err)
	}
	if source != "publish_log" {
		t.Fatalf("expected publish_log hit with exists status, got source=%q", source)
	}
}

// TestCheckTargetExistsNoMatchAcrossAllSources 验证三个来源均未命中时返回空 source。
func TestCheckTargetExistsNoMatchAcrossAllSources(t *testing.T) {
	store := newTargetExistsTestStore(t)
	repo := NewMigrateRepository(store)

	mustExecTargetExistsTest(t, store.DB,
		`INSERT INTO torrents (hash, name, size, sites, downloader_id, last_seen, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-other", "Other.Release", 2048, "hdc", "qb", "2026-10-01 00:00:00")
	mustExecTargetExistsTest(t, store.DB,
		`INSERT INTO publish_queue_tasks (group_id, status, source_site, torrent_id, target_site, title, payload_json, upload_data_json, context_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"g1", PublishQueueStatusQueued, "hdc", "444", "hdc", "Other.Release", "{}", "{}", "{}", "2026-10-01 00:00:00", "2026-10-01 00:00:00")
	mustExecTargetExistsTest(t, store.DB,
		`INSERT INTO publish_logs (publish_trigger, source_site, torrent_id, target_site, title, status, cost_ms, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"queue", "hdc", "555", "hdc", "Other.Release", PublishQueueStatusSuccess, 100, "2026-10-01 00:00:00", "2026-10-01 00:00:00")

	source, detail, err := repo.CheckTargetExists(TargetExistsQuery{
		Name:       "Release.One",
		TargetSite: "mteam",
	})
	if err != nil {
		t.Fatalf("CheckTargetExists returned error: %v", err)
	}
	if source != "" {
		t.Fatalf("expected no hit, got source=%q detail=%q", source, detail)
	}
}

// TestCheckTargetExistsWithAliasesSkipsSiteLookup 验证带预解析别名的入口行为一致（批量场景复用）。
func TestCheckTargetExistsWithAliasesSkipsSiteLookup(t *testing.T) {
	store := newTargetExistsTestStore(t)
	repo := NewMigrateRepository(store)

	mustExecTargetExistsTest(t, store.DB,
		`INSERT INTO torrents (hash, name, size, sites, downloader_id, last_seen, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-abc", "Release.One", 1024, "mteam", "qb", "2026-10-01 00:00:00")

	source, _, err := repo.CheckTargetExistsWithAliases(TargetExistsQuery{
		Hash:       "hash-abc",
		Name:       "Release.One",
		Size:       1024,
		TargetSite: "anything",
	}, []string{"mteam", "M-Team"})
	if err != nil {
		t.Fatalf("CheckTargetExistsWithAliases returned error: %v", err)
	}
	if source != "snapshot" {
		t.Fatalf("expected snapshot hit with pre-resolved aliases, got source=%q", source)
	}
}

// TestResolveSiteAliasesReturnsNicknameAndCode 验证别名解析返回昵称+站点代码去重列表。
func TestResolveSiteAliasesReturnsNicknameAndCode(t *testing.T) {
	store := newTargetExistsTestStore(t)
	repo := NewMigrateRepository(store)

	mustExecTargetExistsTest(t, store.DB,
		`INSERT INTO sites (site, nickname) VALUES (?, ?)`, "mteam", "M-Team")

	aliases, err := repo.ResolveSiteAliases("M-Team")
	if err != nil {
		t.Fatalf("ResolveSiteAliases returned error: %v", err)
	}
	if len(aliases) != 2 {
		t.Fatalf("expected 2 aliases [M-Team, mteam], got %#v", aliases)
	}
	seen := map[string]bool{}
	for _, alias := range aliases {
		seen[strings.ToLower(alias)] = true
	}
	if !seen["m-team"] || !seen["mteam"] {
		t.Fatalf("expected both nickname and site code in aliases, got %#v", aliases)
	}
}
