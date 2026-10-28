package crossseed

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/pt-nexus/server/internal/repository"
	"github.com/pt-nexus/server/internal/service/reversemapping"
	"gorm.io/gorm"
)

func newCrossSeedTestStore(t testing.TB) *repository.Store {
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
			site TEXT,
			nickname TEXT,
			migration INTEGER DEFAULT 1
		)`,
		`CREATE TABLE seed_parameters (
			hash TEXT NOT NULL,
			torrent_id TEXT NOT NULL,
			site_name TEXT NOT NULL,
			nickname TEXT,
			title TEXT,
			subtitle TEXT,
			type TEXT,
			medium TEXT,
			video_codec TEXT,
			audio_codec TEXT,
			resolution TEXT,
			team TEXT,
			source TEXT,
			tags TEXT,
			title_components TEXT,
			screenshot_review_status TEXT,
			is_reviewed INTEGER DEFAULT 0,
			created_at TEXT,
			updated_at TEXT
		)`,
		`CREATE TABLE torrents (
			hash TEXT NOT NULL,
			name TEXT NOT NULL,
			size INTEGER,
			save_path TEXT,
			downloader_id TEXT,
			state TEXT,
			last_seen TEXT,
			sites TEXT,
			is_hidden INTEGER DEFAULT 0
		)`,
	}

	for _, stmt := range statements {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("create test schema failed: %v", err)
		}
	}

	return &repository.Store{DB: db, DBType: "sqlite"}
}

func mustExecCrossSeedTest(t *testing.T, db *gorm.DB, query string, args ...any) {
	t.Helper()
	if err := db.Exec(query, args...).Error; err != nil {
		t.Fatalf("exec %q failed: %v", query, err)
	}
}

func TestQueryDataExcludeTargetSitesMatchesSiteAliases(t *testing.T) {
	store := newCrossSeedTestStore(t)
	repo := repository.NewCrossSeedRepository(store)
	service := NewCrossSeedService(repo)

	mustExecCrossSeedTest(t, store.DB, `INSERT INTO sites (site, nickname, migration) VALUES (?, ?, ?)`, "mteam", "M-Team", 2)
	mustExecCrossSeedTest(t, store.DB, `INSERT INTO sites (site, nickname, migration) VALUES (?, ?, ?)`, "hdsky", "HDSky", 1)

	mustExecCrossSeedTest(t, store.DB, `INSERT INTO seed_parameters (
		hash, torrent_id, site_name, nickname, title, subtitle, type, medium, video_codec,
		audio_codec, resolution, team, source, tags, title_components,
		screenshot_review_status, is_reviewed, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"source-hash", "1001", "hdsky", "HDSky", "Movie", "", "movie", "bluray", "h264",
		"dts", "1080p", "", "", "[]", "[]", "none", 1, "2026-04-14 10:00:00", "2026-04-14 10:00:00")

	mustExecCrossSeedTest(t, store.DB, `INSERT INTO torrents (hash, name, size, save_path, downloader_id, state, last_seen, sites, is_hidden)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		"source-hash", "Movie", 100, "/downloads", "qb-1", "做种中", "2026-04-14 10:00:00", "HDSky")
	mustExecCrossSeedTest(t, store.DB, `INSERT INTO torrents (hash, name, size, save_path, downloader_id, state, last_seen, sites, is_hidden)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		"target-hash", "Movie", 100, "/downloads", "qb-1", "未做种", "2026-04-14 10:00:00", "M-Team")

	result, err := service.QueryData(CrossSeedQueryParams{
		Page:               1,
		PageSize:           20,
		ExcludeTargetSites: "mteam",
	})
	if err != nil {
		t.Fatalf("QueryData returned error: %v", err)
	}

	total, ok := result["total"].(int)
	if !ok {
		t.Fatalf("expected int total, got %#v", result["total"])
	}
	if total != 0 {
		t.Fatalf("expected total 0 when target alias already exists, got %d", total)
	}
}

func insertCrossSeedReviewRow(t *testing.T, db *gorm.DB, torrentID string, title string, team string, tags string, titleComponents string, isReviewed int) {
	t.Helper()
	hash := "hash-" + torrentID
	mustExecCrossSeedTest(t, db, `INSERT INTO seed_parameters (
		hash, torrent_id, site_name, nickname, title, subtitle, type, medium, video_codec,
		audio_codec, resolution, team, source, tags, title_components,
		screenshot_review_status, is_reviewed, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		hash, torrentID, "hdsky", "HDSky", title, "", "category.movie", "medium.bluray", "video.h264",
		"audio.dts", "resolution.r1080p", team, "", tags, titleComponents, "none", isReviewed, "2026-04-14 10:00:00", "2026-04-14 10:00:00")
	mustExecCrossSeedTest(t, db, `INSERT INTO torrents (hash, name, size, save_path, downloader_id, state, last_seen, sites, is_hidden)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		hash, title, 100, "/downloads", "qb-1", "做种中", "2026-04-14 10:00:00", "HDSky")
}

func TestQueryDataReviewStatusUsesPublishableClassification(t *testing.T) {
	store := newCrossSeedTestStore(t)
	repo := repository.NewCrossSeedRepository(store)
	service := NewCrossSeedService(repo)

	insertCrossSeedReviewRow(t, store.DB, "1001", "Valid", "team.other", `[]`, `[]`, 1)
	insertCrossSeedReviewRow(t, store.DB, "1002", "BadTeam", "badteam", `[]`, `[]`, 1)
	insertCrossSeedReviewRow(t, store.DB, "1003", "Restricted", "team.other", `["禁转"]`, `[]`, 1)
	insertCrossSeedReviewRow(t, store.DB, "1004", "Unrecognized", "team.other", `[]`, `[{"key":"无法识别","value":"bad"}]`, 1)

	reviewed, err := service.QueryData(CrossSeedQueryParams{Page: 1, PageSize: 20, ReviewStatus: "reviewed"})
	if err != nil {
		t.Fatalf("QueryData reviewed returned error: %v", err)
	}
	if reviewed["total"].(int) != 1 {
		t.Fatalf("expected reviewed total 1, got %#v", reviewed["total"])
	}
	data := reviewed["data"].([]map[string]any)
	if len(data) != 1 || data[0]["torrent_id"] != "1001" {
		t.Fatalf("expected only publishable row in reviewed data, got %#v", data)
	}

	unreviewed, err := service.QueryData(CrossSeedQueryParams{Page: 1, PageSize: 20, ReviewStatus: "unreviewed"})
	if err != nil {
		t.Fatalf("QueryData unreviewed returned error: %v", err)
	}
	// 新语义：待检查只保留"无数据问题但不可发布"的行——Restricted（禁转属业务约束）1 条；
	// BadTeam（字段值非法）与 Unrecognized（含无法识别字段）归入"错误"。
	if unreviewed["total"].(int) != 1 {
		t.Fatalf("expected unreviewed total 1, got %#v", unreviewed["total"])
	}
	unreviewedData := unreviewed["data"].([]map[string]any)
	if len(unreviewedData) != 1 || unreviewedData[0]["torrent_id"] != "1003" {
		t.Fatalf("expected only business-restricted row in unreviewed data, got %#v", unreviewedData)
	}

	// 错误 = 未删除但存在数据问题（字段值未映射 / 含无法识别字段）。
	errorsResult, err := service.QueryData(CrossSeedQueryParams{Page: 1, PageSize: 20, ReviewStatus: "error"})
	if err != nil {
		t.Fatalf("QueryData error returned error: %v", err)
	}
	if errorsResult["total"].(int) != 2 {
		t.Fatalf("expected error total 2, got %#v", errorsResult["total"])
	}
	errorIDs := map[string]struct{}{}
	for _, row := range errorsResult["data"].([]map[string]any) {
		if id, ok := row["torrent_id"].(string); ok {
			errorIDs[id] = struct{}{}
		}
	}
	for _, want := range []string{"1002", "1004"} {
		if _, ok := errorIDs[want]; !ok {
			t.Fatalf("expected %s in error classification, got %#v", want, errorIDs)
		}
	}
}

// TestQueryDataReviewStatusDeletedAndClear 验证"已删除"分类与一键清除：
// 种子在下载器中已不存在时归入 deleted；一键清除后该批数据被物理删除。
func TestQueryDataReviewStatusDeletedAndClear(t *testing.T) {
	store := newCrossSeedTestStore(t)
	repo := repository.NewCrossSeedRepository(store)
	service := NewCrossSeedService(repo)

	// 仍在做种的行（不存在于 deleted）。
	insertCrossSeedReviewRow(t, store.DB, "3001", "Alive", "team.other", `[]`, `[]`, 1)
	// 已删除的行：seed_parameters 有记录，但 torrents 中无对应未隐藏种子。
	mustExecCrossSeedTest(t, store.DB, `INSERT INTO seed_parameters (
		hash, torrent_id, site_name, nickname, title, subtitle, type, medium, video_codec,
		audio_codec, resolution, team, source, tags, title_components,
		screenshot_review_status, is_reviewed, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"hash-3002", "3002", "hdsky", "HDSky", "Gone", "", "category.movie", "medium.bluray", "video.h264",
		"audio.dts", "resolution.r1080p", "team.other", "", "[]", "[]", "none", 1, "2026-04-14 10:00:00", "2026-04-14 10:00:00")

	deletedResult, err := service.QueryData(CrossSeedQueryParams{Page: 1, PageSize: 20, ReviewStatus: "deleted"})
	if err != nil {
		t.Fatalf("QueryData deleted returned error: %v", err)
	}
	if deletedResult["total"].(int) != 1 {
		t.Fatalf("expected deleted total 1, got %#v", deletedResult["total"])
	}

	cleared, status := service.ClearDeletedData()
	if status != 200 || cleared["success"] != true {
		t.Fatalf("ClearDeletedData failed: status=%d result=%#v", status, cleared)
	}
	if count, _ := cleared["deleted_count"].(int64); count != 1 {
		t.Fatalf("expected deleted_count 1, got %#v", cleared["deleted_count"])
	}

	afterClear, err := service.QueryData(CrossSeedQueryParams{Page: 1, PageSize: 20, ReviewStatus: "deleted"})
	if err != nil {
		t.Fatalf("QueryData deleted after clear returned error: %v", err)
	}
	if afterClear["total"].(int) != 0 {
		t.Fatalf("expected deleted total 0 after clear, got %#v", afterClear["total"])
	}
}

// TestQueryDataReviewFastPathExcludeTargetByHash 验证 fast-path（reviewed + exclude_target_sites）
// 下 target_hashes 改写的相关 NOT EXISTS 语义：种子在当前站存在、目标站已有同一 hash 时，
// 该行必须被排除（等价于原全量物化派生表的 target_hashes.hash IS NULL 判定）。
func TestQueryDataReviewFastPathExcludeTargetByHash(t *testing.T) {
	store := newCrossSeedTestStore(t)
	repo := repository.NewCrossSeedRepository(store)
	service := NewCrossSeedService(repo)

	mustExecCrossSeedTest(t, store.DB, `INSERT INTO sites (site, nickname, migration) VALUES (?, ?, ?)`, "mteam", "M-Team", 2)
	mustExecCrossSeedTest(t, store.DB, `INSERT INTO sites (site, nickname, migration) VALUES (?, ?, ?)`, "hdsky", "HDSky", 1)

	// 可发布的 reviewed 候选（当前站 HDSky 做种）。
	insertCrossSeedReviewRow(t, store.DB, "2001", "Candidate", "team.other", `[]`, `[]`, 1)
	// 同一 hash 在目标站 M-Team 已有种子：应被 target_hashes 排除。
	mustExecCrossSeedTest(t, store.DB, `INSERT INTO torrents (hash, name, size, save_path, downloader_id, state, last_seen, sites, is_hidden)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		"hash-2001", "Candidate", 100, "/downloads/target", "qb-2", "做种中", "2026-04-14 10:00:00", "M-Team")

	params := CrossSeedQueryParams{Page: 1, PageSize: 20, ReviewStatus: "reviewed", ExcludeTargetSites: "mteam"}
	result, err := service.QueryData(params)
	if err != nil {
		t.Fatalf("QueryData returned error: %v", err)
	}
	if result["total"].(int) != 0 {
		t.Fatalf("expected total 0 when target hash already exists, got %#v", result["total"])
	}

	// 新增一个未在目标站的 reviewed 候选，清缓存后应保留（防误杀）。
	insertCrossSeedReviewRow(t, store.DB, "2002", "OtherCandidate", "team.other", `[]`, `[]`, 1)
	service.invalidateCache()
	result, err = service.QueryData(params)
	if err != nil {
		t.Fatalf("QueryData returned error: %v", err)
	}
	if result["total"].(int) != 1 {
		t.Fatalf("expected total 1 after adding non-target candidate, got %#v", result["total"])
	}
}

// TestQueryDataCacheHitAndDeleteInvalidation 验证 30s 进程内缓存：
// 相同参数第二次查询命中缓存；删除后缓存立即失效，再次查询反映最新数据。
func TestQueryDataCacheHitAndDeleteInvalidation(t *testing.T) {
	store := newCrossSeedTestStore(t)
	repo := repository.NewCrossSeedRepository(store)
	service := NewCrossSeedService(repo)

	insertCrossSeedReviewRow(t, store.DB, "3001", "Valid", "team.other", `[]`, `[]`, 1)

	params := CrossSeedQueryParams{Page: 1, PageSize: 20, ReviewStatus: "reviewed"}
	first, err := service.QueryData(params)
	if err != nil {
		t.Fatalf("QueryData returned error: %v", err)
	}
	if first["total"].(int) != 1 {
		t.Fatalf("expected initial total 1, got %#v", first["total"])
	}
	key := queryCacheKey(params)
	if service.getCachedQuery(key) == nil {
		t.Fatalf("expected cached entry after first query")
	}

	// 直接从 DB 删除（不走 service 的失效路径），缓存仍命中旧结果，验证缓存确实生效。
	mustExecCrossSeedTest(t, store.DB, `DELETE FROM seed_parameters WHERE torrent_id = ?`, "3001")
	stale, err := service.QueryData(params)
	if err != nil {
		t.Fatalf("QueryData returned error: %v", err)
	}
	if stale["total"].(int) != 1 {
		t.Fatalf("expected cached total 1 (stale) while cache alive, got %#v", stale["total"])
	}

	// 通过 service 删除应使缓存失效：先恢复数据再走 DeleteCrossSeedData。
	mustExecCrossSeedTest(t, store.DB, `INSERT INTO seed_parameters (
		hash, torrent_id, site_name, nickname, title, subtitle, type, medium, video_codec,
		audio_codec, resolution, team, source, tags, title_components,
		screenshot_review_status, is_reviewed, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"hash-3001", "3001", "hdsky", "HDSky", "Valid", "", "category.movie", "medium.bluray", "video.h264",
		"audio.dts", "resolution.r1080p", "team.other", "", "[]", "[]", "none", 1, "2026-04-14 10:00:00", "2026-04-14 10:00:00")
	mustExecCrossSeedTest(t, store.DB, `INSERT INTO torrents (hash, name, size, save_path, downloader_id, state, last_seen, sites, is_hidden)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		"hash-3001", "Valid", 100, "/downloads", "qb-1", "做种中", "2026-04-14 10:00:00", "HDSky")

	if _, status := service.DeleteCrossSeedData(map[string]any{"torrent_id": "3001", "site_name": "hdsky"}); status != 200 {
		t.Fatalf("expected delete status 200, got %d", status)
	}
	if service.getCachedQuery(key) != nil {
		t.Fatalf("expected cache invalidated after delete")
	}
	afterDelete, err := service.QueryData(params)
	if err != nil {
		t.Fatalf("QueryData returned error: %v", err)
	}
	if afterDelete["total"].(int) != 0 {
		t.Fatalf("expected total 0 after delete, got %#v", afterDelete["total"])
	}
}

// TestLoadCrossSeedPageDetailsFiltersByIdentity 验证详情查询按 (hash, torrent_id, site_name)
// 精确取当前页行：同 hash 一站多种（多个站点/多个 torrent_id）时不得把兄弟行带进结果。
// 详情 SQL 必须保持 sp.hash IN 单一谓词——此前 20 组 OR 拼接会让优化器改走
// created_at 索引做全表扫描，详情查询退化到 2~11 秒。
func TestLoadCrossSeedPageDetailsFiltersByIdentity(t *testing.T) {
	store := newCrossSeedTestStore(t)
	repo := repository.NewCrossSeedRepository(store)
	service := NewCrossSeedService(repo)

	// 同一个 hash 在两个站点各有一条 seed_parameters（一站多种场景）。
	for _, item := range []struct {
		torrentID string
		site      string
	}{{"4001", "hdsky"}, {"4002", "mteam"}} {
		mustExecCrossSeedTest(t, store.DB, `INSERT INTO seed_parameters (
			hash, torrent_id, site_name, nickname, title, subtitle, type, medium, video_codec,
			audio_codec, resolution, team, source, tags, title_components,
			screenshot_review_status, is_reviewed, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"hash-shared", item.torrentID, item.site, item.site, "Movie", "", "category.movie", "medium.bluray", "video.h264",
			"audio.dts", "resolution.r1080p", "team.other", "", "[]", "[]", "none", 1, "2026-04-14 10:00:00", "2026-04-14 10:00:00")
	}

	// 同一 hash 两条做种记录（不同 last_seen），ct 子查询应取最新一条。
	mustExecCrossSeedTest(t, store.DB, `INSERT INTO torrents (hash, name, size, save_path, downloader_id, state, last_seen, sites, is_hidden)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		"hash-shared", "Movie", 100, "/downloads-old", "qb-1", "做种中", "2026-04-14 11:00:00", "HDSky")
	mustExecCrossSeedTest(t, store.DB, `INSERT INTO torrents (hash, name, size, save_path, downloader_id, state, last_seen, sites, is_hidden)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		"hash-shared", "Movie", 100, "/downloads-new", "qb-2", "做种中", "2026-04-14 12:00:00", "M-Team")

	reverseMappings := reversemapping.Build(nil)

	pageRows := []map[string]any{
		{"hash": "hash-shared", "torrent_id": "4001", "site_name": "hdsky"},
	}
	pageData, err := service.loadCrossSeedPageDetails(pageRows, reverseMappings)
	if err != nil {
		t.Fatalf("loadCrossSeedPageDetails returned error: %v", err)
	}
	if len(pageData) != 1 {
		t.Fatalf("expected exactly 1 detail row for single identity, got %d: %#v", len(pageData), pageData)
	}
	if pageData[0]["torrent_id"] != "4001" || pageData[0]["site_name"] != "hdsky" {
		t.Fatalf("expected identity (4001, hdsky), got %#v", pageData[0])
	}
	if pageData[0]["save_path"] != "/downloads-new" {
		t.Fatalf("expected ct join to pick newest torrent save_path, got %#v", pageData[0]["save_path"])
	}

	// 两个身份同时取详情：应各自返回一行，不得互相串行。
	pageRows = append(pageRows, map[string]any{"hash": "hash-shared", "torrent_id": "4002", "site_name": "mteam"})
	pageData, err = service.loadCrossSeedPageDetails(pageRows, reverseMappings)
	if err != nil {
		t.Fatalf("loadCrossSeedPageDetails returned error: %v", err)
	}
	if len(pageData) != 2 {
		t.Fatalf("expected 2 detail rows for two identities, got %d: %#v", len(pageData), pageData)
	}
	seen := map[string]bool{}
	for _, item := range pageData {
		seen[toString(item["torrent_id"], "")+"@"+toString(item["site_name"], "")] = true
	}
	if !seen["4001@hdsky"] || !seen["4002@mteam"] {
		t.Fatalf("expected both identities present, got %#v", seen)
	}
}
