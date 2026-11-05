package repository

import "testing"

// TestQueryUngroupedStatsCoversUnmatchedAndEmptyGroups 未知站点行：
// 组名匹配不到任何站点官组清单的种子逐组统计；group 为空的种子合并为"（无官组）"。
func TestQueryUngroupedStatsCoversUnmatchedAndEmptyGroups(t *testing.T) {
	store := newStatsRepositoryTestStore(t)
	repo := NewStatsRepository(store)

	mustExecStatsTest(t, store.DB, `INSERT INTO sites (site, nickname, "group") VALUES (?, ?, ?)`, "mteam", "M-Team", "-MTeam")

	// 已归属：不应出现在未统计部分
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-known", "Known.Release", 100, "M-Team", "-MTeam", "qb-1")
	// 未归属组：组名不在任何站点官组清单
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-unknown-1", "Stranger.Thing.1", 200, "M-Team", "-WeirdGRP", "qb-2")
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-unknown-2", "Stranger.Thing.2", 50, "M-Team", "-WeirdGRP", "tr-1")
	// 无官组：group 为空 / 仅空白，合并统计
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-empty", "No.Group.Seed", 300, "M-Team", "", "qb-3")
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-null", "Null.Group.Seed", 50, "M-Team", "  ", "qb-4")
	// 隐藏的不应统计
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 1)`,
		"hash-hidden", "Hidden.Seed", 9999, "M-Team", "-WeirdGRP", "qb-5")

	rows, err := repo.QueryUngroupedStats()
	if err != nil {
		t.Fatalf("QueryUngroupedStats returned error: %v", err)
	}

	byGroup := map[string]GroupStatRow{}
	for _, row := range rows {
		if row.SiteName != "未知站点" {
			t.Fatalf("expected site_name 未知站点, got %q", row.SiteName)
		}
		byGroup[row.GroupSuffix] = row
	}

	if len(byGroup) != 2 {
		t.Fatalf("expected 2 group buckets, got %d: %#v", len(byGroup), rows)
	}

	weird, ok := byGroup["WeirdGRP"]
	if !ok {
		t.Fatalf("expected WeirdGRP row, got %#v", rows)
	}
	if weird.TorrentCount != 2 || weird.TotalSize != 250 {
		t.Fatalf("expected WeirdGRP count=2 size=250, got count=%d size=%d", weird.TorrentCount, weird.TotalSize)
	}

	empty, ok := byGroup["（无官组）"]
	if !ok {
		t.Fatalf("expected （无官组） row, got %#v", rows)
	}
	if empty.TorrentCount != 2 || empty.TotalSize != 350 {
		t.Fatalf("expected 无官组 count=2 size=350, got count=%d size=%d", empty.TorrentCount, empty.TotalSize)
	}
}

// TestQueryUngroupedStatsComplementsGroupStats 口径校验：QueryGroupStats（全站点）
// 与 QueryUngroupedStats 的种子组数相加应等于全部可见种子组数，两组不重不漏。
func TestQueryUngroupedStatsComplementsGroupStats(t *testing.T) {
	store := newStatsRepositoryTestStore(t)
	repo := NewStatsRepository(store)

	mustExecStatsTest(t, store.DB, `INSERT INTO sites (site, nickname, "group") VALUES (?, ?, ?)`, "mteam", "M-Team", "-MTeam")
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-known", "Known.Release", 100, "M-Team", "-MTeam", "qb-1")
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-known-dupe", "Known.Release", 100, "M-Team", "-MTeam", "tr-1")
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-unknown", "Stranger.Thing", 200, "M-Team", "-WeirdGRP", "qb-2")
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-no-group", "No.Group.Seed", 300, "M-Team", "", "qb-3")

	matched, err := repo.QueryGroupStats("")
	if err != nil {
		t.Fatalf("QueryGroupStats returned error: %v", err)
	}
	ungrouped, err := repo.QueryUngroupedStats()
	if err != nil {
		t.Fatalf("QueryUngroupedStats returned error: %v", err)
	}

	matchedCount := int64(0)
	for _, row := range matched {
		matchedCount += row.TorrentCount
	}
	ungroupedCount := int64(0)
	for _, row := range ungrouped {
		ungroupedCount += row.TorrentCount
	}

	// 全部可见组（DISTINCT name+size）：Known.Release(100) 计 1 + Stranger.Thing + No.Group.Seed = 3
	if matchedCount+ungroupedCount != 3 {
		t.Fatalf("expected matched+ungrouped=3, got matched=%d ungrouped=%d (%#v / %#v)", matchedCount, ungroupedCount, matched, ungrouped)
	}
}
