package torrentdata

import (
	"context"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/pt-nexus/server/internal/repository"
	processingpersist "github.com/pt-nexus/server/internal/service/processing/persist"
	"github.com/pt-nexus/server/internal/service/reversemapping"
	"gorm.io/gorm"
)

// TestGetDataOnlyPublishableShowsReviewedGroups 复现生产"一种多站全空"：
// 用与生产一致的形态造数据（reviewed=1 的副本 + 标准全角空格简介 + 完整
// MediaInfo），跑 GetData only_publishable 全链，验证分组被正确保留。
func TestGetDataOnlyPublishableShowsReviewedGroups(t *testing.T) {
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

	mediainfo := "General\nUnique ID : 1234 (0x4D2)\nComplete name : A.mkv\nFormat : Matroska\nFile size : 5.87 GiB\nDuration : 33 min\nOverall bit rate : 24.7 Mb/s\nVideo\nFormat : HEVC\nWidth : 3840 pixels\nHeight : 2160 pixels\nAudio\nFormat : E-AC-3\nLanguage : English\nText\nFormat : UTF-8"
	body := "◎译　　名　成年人/Snowflakes\n◎片　　名　Adults\n◎年　　代　2025\n◎产　　地　美国\n◎类　　别　喜剧\n◎语　　言　英语\n◎简介　 adults comedy"

	statements := []string{
		`CREATE TABLE torrents (
			hash TEXT, name TEXT NOT NULL, save_path TEXT, size INTEGER, progress REAL,
			state TEXT, sites TEXT, "group" TEXT, details TEXT, downloader_id TEXT,
			last_seen TEXT, iyuu_last_check TEXT, seeders INTEGER DEFAULT 0, is_hidden INTEGER DEFAULT 0
		)`,
		`CREATE TABLE seed_parameters (
			hash TEXT, torrent_id TEXT, site_name TEXT, nickname TEXT, title TEXT, subtitle TEXT,
			type TEXT, medium TEXT, video_codec TEXT, audio_codec TEXT, resolution TEXT, team TEXT,
			source TEXT, tags TEXT, title_components TEXT, mediainfo TEXT, body TEXT,
			screenshot_review_status TEXT, is_reviewed INTEGER DEFAULT 0, created_at TEXT, updated_at TEXT
		)`,
		`CREATE TABLE sites (nickname TEXT, migration INTEGER, cookie TEXT, passkey TEXT, base_url TEXT, site TEXT, special_tracker_domain TEXT)`,
		`CREATE TABLE torrent_upload_stats (hash TEXT, uploaded INTEGER, is_hidden INTEGER DEFAULT 0)`,
		`CREATE TABLE publish_queue_tasks (id INTEGER)`,
		`CREATE TABLE publish_logs (id INTEGER)`,
	}
	for _, stmt := range statements {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("create schema failed: %v", err)
		}
	}
	if err := db.Exec(`INSERT INTO sites (nickname, migration, cookie, base_url, site) VALUES ('憨憨', 1, 'cookie', 'hhan.example', 'hhanclub')`).Error; err != nil {
		t.Fatal(err)
	}

	// 两个组：组A（hhan 副本 + reviewed 参数行）应显示；组B（无任何 reviewed 副本）应被过滤。
	if err := db.Exec(`INSERT INTO torrents (hash, name, save_path, size, progress, state, sites, downloader_id, last_seen, seeders, is_hidden) VALUES
		('hash-a', 'Adults.S01.2025', '/m', 1000, 100, '做种中', '憨憨', 'qb', '2026-11-12 00:00:00', 1, 0),
		('hash-b', 'NoParams.Show.2025', '/m', 2000, 100, '做种中', '憨憨', 'qb', '2026-11-12 00:00:00', 1, 0)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO seed_parameters (hash, torrent_id, site_name, nickname, title, type, medium, video_codec, audio_codec, resolution, team, source, tags, title_components, mediainfo, body, screenshot_review_status, is_reviewed, created_at, updated_at) VALUES
		('hash-a', '1001', 'hhanclub', '憨憨', 'Adults', 'category.movie', 'medium.webdl', 'video.h264', 'audio.ddp', 'resolution.r1080p', 'team.hhweb', 'source.western', '[]', '[]', ?, ?, 'confirmed', 1, '2026-11-12 00:00:00', '2026-11-12 00:00:00')`,
		mediainfo, body).Error; err != nil {
		t.Fatal(err)
	}

	service := NewTorrentDataService(repository.NewTorrentDataRepository(&repository.Store{DB: db, DBType: "sqlite"}), nil)
	result, err := service.GetData(context.Background(), TorrentsDataParams{
		Page:            1,
		PageSize:        20,
		OnlyPublishable: true,
	})
	if err != nil {
		t.Fatalf("GetData returned error: %v", err)
	}
	// 二分第一步：分组键查询层（不再有 SQL 预筛 join，所有分组都应返回）。
	repo := repository.NewTorrentDataRepository(&repository.Store{DB: db, DBType: "sqlite"})
	groupKeys, groupErr := repo.ListTorrentGroupKeysWithFilters(context.Background(), repository.TorrentListFilters{})
	if groupErr != nil {
		t.Fatalf("ListTorrentGroupKeysWithFilters failed: %v", groupErr)
	}
	if len(groupKeys) != 2 {
		t.Fatalf("expected 2 group keys (prefilter removed), got %#v", groupKeys)
	}

	// 二分第二步：Go 精确判定层（reviewedParams + IsRowPublishableData）。
	reviewed, revErr := repo.ListReviewedSeedParamsByHashes(context.Background(), []string{"hash-a"})
	if revErr != nil {
		t.Fatalf("ListReviewedSeedParamsByHashes failed: %v", revErr)
	}
	if len(reviewed["hash-a"]) != 1 {
		t.Fatalf("expected 1 reviewed param row for hash-a, got %d", len(reviewed["hash-a"]))
	}

	// 二分第三步：判定函数对 fixture 行是否放行。
	reverseMappings := reversemapping.Build(nil)
	if !processingpersist.IsRowPublishableData(reviewed["hash-a"][0], reverseMappings) {
		t.Fatalf("IsRowPublishableData rejected fixture row: %#v", reviewed["hash-a"][0])
	}

	total := result["total"].(int)
	if total != 1 {
		t.Fatalf("expected total 1 (only reviewed group), got %d; data=%#v", total, result["data"])
	}
	rows := result["data"].([]map[string]any)
	if rows[0]["name"] != "Adults.S01.2025" {
		t.Fatalf("expected reviewed group kept, got %#v", rows[0]["name"])
	}
	sites := rows[0]["sites"].(map[string]any)
	hhan := sites["憨憨"].(map[string]any)
	if reviewed, ok := hhan["reviewed"].(bool); !ok || !reviewed {
		t.Fatalf("expected hhan site reviewed=true, got %#v", hhan)
	}
}
