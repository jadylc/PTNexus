package repository

import "testing"

// TestQueryUngroupedStatsUsesTitleComponentsTeam 口径 1：官组统计未覆盖的做种组，
// 用 seed_parameters.title_components 的「制作组」组件值作为外部组名逐组统计；
// 组件缺失/NOGROUP 的合并为「（无官组）」。
func TestQueryUngroupedStatsUsesTitleComponentsTeam(t *testing.T) {
	store := newStatsRepositoryTestStore(t)
	repo := NewStatsRepository(store)

	mustExecStatsTest(t, store.DB, `INSERT INTO sites (site, nickname, "group") VALUES (?, ?, ?)`, "mteam", "M-Team", "-MTeam")

	// 已归属：group 命中官组清单 → 不应出现在未知站点行
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-known", "Known.Release", 100, "M-Team", "-MTeam", "qb-1")
	mustExecStatsTest(t, store.DB, `INSERT INTO seed_parameters (hash, name, torrent_id, site_name, title_components) VALUES (?, ?, ?, ?, ?)`,
		"hash-known", "Known.Release", "1", "M-Team", `[{"key":"制作组","value":"MTeam"}]`)

	// 外部组：torrents.group 为空，但 title_components 有制作组 → 逐组统计
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-ext-1", "Stranger.Thing.1", 200, "M-Team", "", "qb-2")
	mustExecStatsTest(t, store.DB, `INSERT INTO seed_parameters (hash, name, torrent_id, site_name, title_components) VALUES (?, ?, ?, ?, ?)`,
		"hash-ext-1", "Stranger.Thing.1", "2", "M-Team", `[{"key":"主标题","value":"Stranger.Thing.1"},{"key":"制作组","value":"BMDru"}]`)
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-ext-2", "Stranger.Thing.2", 50, "M-Team", "", "tr-1")
	mustExecStatsTest(t, store.DB, `INSERT INTO seed_parameters (hash, name, torrent_id, site_name, title_components) VALUES (?, ?, ?, ?, ?)`,
		"hash-ext-2", "Stranger.Thing.2", "3", "M-Team", `[{"key":"制作组","value":"BMDru"}]`)

	// 另一个外部组
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-ext-3", "Other.Group.Seed", 80, "M-Team", "", "qb-3")
	mustExecStatsTest(t, store.DB, `INSERT INTO seed_parameters (hash, name, torrent_id, site_name, title_components) VALUES (?, ?, ?, ?, ?)`,
		"hash-ext-3", "Other.Group.Seed", "4", "M-Team", `[{"key":"制作组","value":"FLTBK"}]`)

	// 无官组：组件缺失 / NOGROUP → 合并统计
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-no-comp", "No.Component.Seed", 300, "M-Team", "", "qb-4")
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-nogroup", "NoGroup.Seed", 60, "M-Team", "", "qb-5")
	mustExecStatsTest(t, store.DB, `INSERT INTO seed_parameters (hash, name, torrent_id, site_name, title_components) VALUES (?, ?, ?, ?, ?)`,
		"hash-nogroup", "NoGroup.Seed", "6", "M-Team", `[{"key":"制作组","value":"NOGROUP"}]`)

	// 隐藏的不应统计
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 1)`,
		"hash-hidden", "Hidden.Seed", 9999, "M-Team", "", "qb-6")

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

	if len(byGroup) != 3 {
		t.Fatalf("expected 3 buckets (BMDru/FLTBK/无官组), got %d: %#v", len(byGroup), rows)
	}
	bmdru, ok := byGroup["BMDru"]
	if !ok || bmdru.TorrentCount != 2 || bmdru.TotalSize != 250 {
		t.Fatalf("expected BMDru count=2 size=250, got %#v", bmdru)
	}
	fltbk, ok := byGroup["FLTBK"]
	if !ok || fltbk.TorrentCount != 1 || fltbk.TotalSize != 80 {
		t.Fatalf("expected FLTBK count=1 size=80, got %#v", fltbk)
	}
	empty, ok := byGroup["（无官组）"]
	if !ok || empty.TorrentCount != 2 || empty.TotalSize != 360 {
		t.Fatalf("expected 无官组 count=2 size=360, got %#v", empty)
	}
}

// TestQueryUngroupedStatsMultipleHashesSameGroup 同名同 size 的多个 hash 分组时，
// 制作组关联不能因聚合截断而漏掉（旧 GROUP_CONCAT 在 MySQL 默认 1024 字节会截断）。
func TestQueryUngroupedStatsMultipleHashesSameGroup(t *testing.T) {
	store := newStatsRepositoryTestStore(t)
	repo := NewStatsRepository(store)

	mustExecStatsTest(t, store.DB, `INSERT INTO sites (site, nickname, "group") VALUES (?, ?, ?)`, "mteam", "M-Team", "-MTeam")
	// 三条同 name+size，制作组只挂在第三个 hash 的 seed_parameters 上
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-a", "Same.Group.Release", 500, "M-Team", "", "qb-1")
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-b", "Same.Group.Release", 500, "M-Team", "", "tr-1")
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-c", "Same.Group.Release", 500, "M-Team", "", "qb-2")
	mustExecStatsTest(t, store.DB, `INSERT INTO seed_parameters (hash, name, torrent_id, site_name, title_components) VALUES (?, ?, ?, ?, ?)`,
		"hash-c", "Same.Group.Release", "3", "M-Team", `[{"key":"制作组","value":"BMDru"}]`)

	rows, err := repo.QueryUngroupedStats()
	if err != nil {
		t.Fatalf("QueryUngroupedStats returned error: %v", err)
	}
	if len(rows) != 1 || rows[0].GroupSuffix != "BMDru" || rows[0].TorrentCount != 1 || rows[0].TotalSize != 500 {
		t.Fatalf("expected single BMDru row count=1 size=500, got %#v", rows)
	}
}

// TestQueryUngroupedStatsComplementsGroupStats 口径校验：官组统计（全站点）
// 与未知站点行的种子组数相加应等于全部可见种子组数，不重不漏。
func TestQueryUngroupedStatsComplementsGroupStats(t *testing.T) {
	store := newStatsRepositoryTestStore(t)
	repo := NewStatsRepository(store)

	mustExecStatsTest(t, store.DB, `INSERT INTO sites (site, nickname, "group") VALUES (?, ?, ?)`, "mteam", "M-Team", "-MTeam")
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-known", "Known.Release", 100, "M-Team", "-MTeam", "qb-1")
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-known-dupe", "Known.Release", 100, "M-Team", "-MTeam", "tr-1")
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"hash-ext", "Stranger.Thing", 200, "M-Team", "", "qb-2")
	mustExecStatsTest(t, store.DB, `INSERT INTO seed_parameters (hash, name, torrent_id, site_name, title_components) VALUES (?, ?, ?, ?, ?)`,
		"hash-ext", "Stranger.Thing", "2", "M-Team", `[{"key":"制作组","value":"BMDru"}]`)
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

	// 全部可见组（DISTINCT name+size）：Known.Release(100) + Stranger.Thing + No.Group.Seed = 3
	if matchedCount+ungroupedCount != 3 {
		t.Fatalf("expected matched+ungrouped=3, got matched=%d ungrouped=%d (%#v / %#v)", matchedCount, ungroupedCount, matched, ungrouped)
	}
}
