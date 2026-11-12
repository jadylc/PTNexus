package torrentdata

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/pt-nexus/server/internal/repository"
	"github.com/pt-nexus/server/internal/service/reversemapping"
	"gorm.io/gorm"
)

// TestDiagnoseOnlyPublishableChain 复现生产"一种多站 total=0"：
// 用真实 global_mappings.yaml + 与生产一致的多组数据，逐环节打印
// rows/reviewedKeys/reverseTypeKeys/判定通过数，定位全灭环节。
func TestDiagnoseOnlyPublishableChain(t *testing.T) {
	dsn := "file:" + strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(t.Name()) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })

	stmts := []string{
		`CREATE TABLE torrents (hash TEXT, name TEXT NOT NULL, save_path TEXT, size INTEGER, progress REAL,
		 state TEXT, sites TEXT, "group" TEXT, details TEXT, downloader_id TEXT,
		 last_seen TEXT, iyuu_last_check TEXT, seeders INTEGER DEFAULT 0, is_hidden INTEGER DEFAULT 0)`,
		`CREATE TABLE seed_parameters (hash TEXT, torrent_id TEXT, site_name TEXT, nickname TEXT, title TEXT,
		 subtitle TEXT, type TEXT, medium TEXT, video_codec TEXT, audio_codec TEXT, resolution TEXT,
		 team TEXT, source TEXT, tags TEXT, title_components TEXT, mediainfo TEXT, body TEXT,
		 screenshot_review_status TEXT, is_reviewed INTEGER DEFAULT 0, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE sites (nickname TEXT, migration INTEGER, cookie TEXT, passkey TEXT, base_url TEXT, site TEXT, special_tracker_domain TEXT)`,
		`CREATE TABLE torrent_upload_stats (hash TEXT, uploaded INTEGER, is_hidden INTEGER DEFAULT 0)`,
	}
	for _, s := range stmts {
		if err := db.Exec(s).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`INSERT INTO sites (nickname, migration, cookie, site) VALUES ('憨憨', 1, 'c', 'hhanclub')`).Error; err != nil {
		t.Fatal(err)
	}

	mi := "General\nUnique ID : 1234\nComplete name : A.mkv\nFormat : Matroska\nFile size : 5.87 GiB\nDuration : 33 min\nOverall bit rate : 24.7 Mb/s\nVideo\nFormat : HEVC\nWidth : 3840 pixels\nHeight : 2160 pixels\nAudio\nFormat : E-AC-3\nLanguage : English"
	body := "◎译　　名　成年人\n◎片　　名　Adults\n◎年　　代　2025\n◎产　　地　美国\n◎简介　test synopsis"

	// 3 组：组A（reviewed）、组B（reviewed）、组C（未抓参数）
	tx := db.Begin()
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("Movie.%d.2025", i)
		hash := fmt.Sprintf("hash-%d", i)
		if err := tx.Exec(`INSERT INTO torrents (hash, name, save_path, size, progress, state, sites, downloader_id, last_seen, seeders, is_hidden) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			hash, name, "/k", int64(1000+i), 100, "做种中", "憨憨", "qb", "2026-11-12 00:00:00", 1, 0).Error; err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			if err := tx.Exec(`INSERT INTO seed_parameters (hash, torrent_id, site_name, nickname, title, type, medium, video_codec, audio_codec, resolution, team, source, tags, title_components, mediainfo, body, screenshot_review_status, is_reviewed, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				hash, fmt.Sprintf("t%d", i), "hhanclub", "憨憨", name,
				"category.animation", "medium.webdl", "video.h264", "audio.ddp", "resolution.r1080p",
				"team.hhweb", "source.china", `["tag.动画","tag.完结"]`, `[]`, mi, body,
				"confirmed", 1, "2026-11-12 00:00:00", "2026-11-12 00:00:00").Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	tx.Commit()

	store := &repository.Store{DB: db, DBType: "sqlite"}
	repo := repository.NewTorrentDataRepository(store)
	service := NewTorrentDataService(repo, nil)

	rows, err := repo.ListTorrentsWithFilters(context.Background(), repository.TorrentListFilters{})
	if err != nil {
		t.Fatal(err)
	}
	reviewed, err := repo.ListReviewedSeedParamsByHashes(context.Background(), torrentRecordHashes(rows))
	if err != nil {
		t.Fatal(err)
	}
	reverse := reversemapping.Build(nil)
	rt, _ := reverse["type"].(map[string]any)
	t.Logf("rows=%d reviewedKeys=%d reverseTypeKeys=%d", len(rows), len(reviewed), len(rt))

	revPass := 0
	for h, paramRows := range reviewed {
		for _, r := range paramRows {
			_ = h
			// 判定使用的字段形态与 MariaDB 一致（此处 sqlite 为 string）
			_ = r
			revPass++
		}
	}
	t.Logf("reviewed param rows=%d", revPass)

	result, err := service.GetData(context.Background(), TorrentsDataParams{Page: 1, PageSize: 50, OnlyPublishable: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("GetData total=%v pageSize=%v", result["total"], result["pageSize"])
	if result["total"].(int) != 2 {
		t.Fatalf("expected total 2 (reviewed groups), got %v", result["total"])
	}
}
