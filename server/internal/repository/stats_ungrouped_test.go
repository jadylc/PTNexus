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

// TestQueryUngroupedStatsKnownTeamNotInUnknownSite 制作组值本身是已知官组（含
// MNHD-FRDS 这类发布子组变体）时，不能列进未知站点，应归回对应站点行。
// 对应真实 case：sp.team=team.frds、title_components.制作组=FRDS/MNHD-FRDS/mUHD-FRDS。
func TestQueryUngroupedStatsKnownTeamNotInUnknownSite(t *testing.T) {
	store := newStatsRepositoryTestStore(t)
	repo := NewStatsRepository(store)

	mustExecStatsTest(t, store.DB, `INSERT INTO sites (site, nickname, "group") VALUES (?, ?, ?)`, "keepfrds", "月月", "-FRDS")
	mustExecStatsTest(t, store.DB, `INSERT INTO sites (site, nickname, "group") VALUES (?, ?, ?)`, "m-team", "馒头", "-MTeam,-MWeb")

	cases := []struct {
		hash string
		name string
		team string
	}{
		{"h-frds", "Release.FRDS", "FRDS"},
		{"h-mnhd", "Release.MNHD-FRDS", "MNHD-FRDS"},
		{"h-muhd", "Release.mUHD-FRDS", "mUHD-FRDS"},
		{"h-mteam", "Release.MTeam", "MTeam"},
		{"h-ext", "Release.BMDru", "BMDru"}, // 真正的外部组
	}
	for _, c := range cases {
		mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
			c.hash, c.name, 100, "", "", "qb-1")
		mustExecStatsTest(t, store.DB, `INSERT INTO seed_parameters (hash, name, torrent_id, site_name, title_components) VALUES (?, ?, ?, ?, ?)`,
			c.hash, c.name, c.hash, "", `[{"key":"制作组","value":"`+c.team+`"}]`)
	}

	rows, err := repo.QueryUngroupedStats()
	if err != nil {
		t.Fatalf("QueryUngroupedStats returned error: %v", err)
	}

	// 未知站点里只应有 BMDru（外加无官组若存在），不应出现 FRDS/MNHD-FRDS/MTeam
	for _, row := range rows {
		if row.SiteName == "未知站点" && row.GroupSuffix != "BMDru" && row.GroupSuffix != "（无官组）" {
			t.Errorf("known group leaked into 未知站点: site=%q group=%q", row.SiteName, row.GroupSuffix)
		}
	}

	// 已知官组应归回对应站点
	bySiteGroup := map[string]int64{}
	for _, row := range rows {
		if row.SiteName != "未知站点" {
			bySiteGroup[row.SiteName+"\x00"+row.GroupSuffix] = row.TorrentCount
		}
	}
	if bySiteGroup["月月\x00FRDS"] != 1 {
		t.Errorf("expected 月月/FRDS count=1, got %#v", rows)
	}
	if bySiteGroup["月月\x00MNHD-FRDS"] != 1 || bySiteGroup["月月\x00mUHD-FRDS"] != 1 {
		t.Errorf("expected FRDS variants grouped under 月月, got %#v", rows)
	}
	if bySiteGroup["馒头\x00MTeam"] != 1 {
		t.Errorf("expected 馒头/MTeam count=1, got %#v", rows)
	}
	// 真正的外部组仍留在未知站点
	bmdruFound := false
	for _, row := range rows {
		if row.SiteName == "未知站点" && row.GroupSuffix == "BMDru" && row.TorrentCount == 1 {
			bmdruFound = true
		}
	}
	if !bmdruFound {
		t.Errorf("BMDru should stay in 未知站点 as external group, got %#v", rows)
	}
}

// TestQueryUngroupedStatsUnfetchedNameFallback 未拉取种子（无 seed_parameters）时，
// 用独立的名字兜底方法从 torrents.name 解析组名：点号 .FRDS 归月月、
// 清单外 -BMDru 进未知站点外部组、纯技术串才留（无官组）。
func TestQueryUngroupedStatsUnfetchedNameFallback(t *testing.T) {
	store := newStatsRepositoryTestStore(t)
	repo := NewStatsRepository(store)

	mustExecStatsTest(t, store.DB, `INSERT INTO sites (site, nickname, "group") VALUES (?, ?, ?)`, "keepfrds", "月月", "-FRDS")

	// 未拉取 + 点号 .FRDS 合集名 → 归月月（不再落无官组）
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"h-dot", "DouBan_IMDB.TOP250.Movies.Mixed.Collection.20240501.FRDS", 3659292361595, "", "", "qb-1")
	// 未拉取 + 清单外 -BMDru → 未知站点外部组
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"h-ext", "Sleepwalkers.2021.BluRay.1080p.DD5.1.x264-BMDru", 100, "", "", "qb-2")
	// 未拉取 + 纯技术串 → （无官组）
	mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		"h-tech", "Movie.2020.1080p.WEB-DL.x265.HDR.DDP5.1", 200, "", "", "qb-3")

	rows, err := repo.QueryUngroupedStats()
	if err != nil {
		t.Fatalf("QueryUngroupedStats returned error: %v", err)
	}
	byKey := map[string]*GroupStatRow{}
	for i := range rows {
		byKey[rows[i].SiteName+"\x00"+rows[i].GroupSuffix] = &rows[i]
	}
	if row, ok := byKey["月月\x00FRDS"]; !ok || row.TorrentCount != 1 || row.TotalSize != 3659292361595 {
		t.Fatalf("expected 月月/FRDS count=1, got %#v", rows)
	}
	if row, ok := byKey["未知站点\x00BMDru"]; !ok || row.TorrentCount != 1 {
		t.Fatalf("expected 未知站点/BMDru count=1, got %#v", rows)
	}
	if row, ok := byKey["未知站点\x00（无官组）"]; !ok || row.TorrentCount != 1 {
		t.Fatalf("expected （无官组） count=1 (tech-only), got %#v", rows)
	}
}

// TestQueryUngroupedStatsNoSubstringMismatch 子串误归回归（2026.11 用户实测）：
// Madeline 含 ADE、Book 含 ADEbook（人人）、Ream 含 Dream（杜比），
// 段级精确匹配下都应留在未知站点外部组；而 MNHD-FRDS 切段后命中 FRDS 仍归月月。
// 另：劳改所官组含 DYZ-WEB，单独的 WEB/Web 不是独立官组条目，不得误归劳改所；
// 但完整 token DYZ-Movie 应精确命中劳改所。
func TestQueryUngroupedStatsNoSubstringMismatch(t *testing.T) {
	store := newStatsRepositoryTestStore(t)
	repo := NewStatsRepository(store)

	mustExecStatsTest(t, store.DB, `INSERT INTO sites (site, nickname, "group") VALUES (?, ?, ?)`, "audiences", "人人", "-ADE,-Audies,-ADWeb,-ADEbook,-ADAudio")
	mustExecStatsTest(t, store.DB, `INSERT INTO sites (site, nickname, "group") VALUES (?, ?, ?)`, "hddolby", "杜比", "-QHstudIo,-Dream,-DBTV,-CornerMV")
	mustExecStatsTest(t, store.DB, `INSERT INTO sites (site, nickname, "group") VALUES (?, ?, ?)`, "keepfrds", "月月", "-FRDS")
	mustExecStatsTest(t, store.DB, `INSERT INTO sites (site, nickname, "group") VALUES (?, ?, ?)`, "laogaosuo", "劳改所", "-DYZ-Movie,-DYZ-WEB,-DYZ-TV,-DJWEB")

	cases := []struct {
		hash string
		name string
		team string
	}{
		{"h-madeline", "Some.Show.S01.Madeline", "Madeline"},
		{"h-book", "Some.Book.Collection", "Book"},
		{"h-ream", "Some.Movie.2024.Ream", "Ream"},
		{"h-web-upper", "Some.Movie.2024.WEB", "WEB"},
		{"h-web-mixed", "Some.Movie.2024.Web", "Web"},
		{"h-dyz-movie", "Some.Movie.2024.DYZ-Movie", "DYZ-Movie"},
		{"h-frds-variant", "Some.Movie.2024.MNHD-FRDS", "MNHD-FRDS"},
	}
	for _, c := range cases {
		mustExecStatsTest(t, store.DB, `INSERT INTO torrents (hash, name, size, sites, "group", downloader_id, is_hidden) VALUES (?, ?, ?, ?, ?, ?, 0)`,
			c.hash, c.name, 100, "", "", "qb-1")
		mustExecStatsTest(t, store.DB, `INSERT INTO seed_parameters (hash, name, torrent_id, site_name, title_components) VALUES (?, ?, ?, ?, ?)`,
			c.hash, c.name, c.hash, "", `[{"key":"制作组","value":"`+c.team+`"}]`)
	}

	rows, err := repo.QueryUngroupedStats()
	if err != nil {
		t.Fatalf("QueryUngroupedStats returned error: %v", err)
	}

	for _, row := range rows {
		if row.SiteName == "人人" || row.SiteName == "杜比" {
			t.Errorf("substring mismatch leaked into known site: site=%q group=%q", row.SiteName, row.GroupSuffix)
		}
		// 劳改所只允许精确命中的完整官组 token（DYZ-Movie）；
		// WEB/Web 不是独立官组条目，不得通过 DYZ-WEB 的子段误归进来。
		if row.SiteName == "劳改所" && row.GroupSuffix != "DYZ-Movie" {
			t.Errorf("劳改所 only DYZ-Movie expected, got group=%q", row.GroupSuffix)
		}
	}
	bySiteGroup := map[string]int64{}
	for _, row := range rows {
		bySiteGroup[row.SiteName+"\x00"+row.GroupSuffix] = row.TorrentCount
	}
	if bySiteGroup["月月\x00MNHD-FRDS"] != 1 {
		t.Errorf("expected MNHD-FRDS under 月月, got %#v", rows)
	}
	if bySiteGroup["劳改所\x00DYZ-Movie"] != 1 {
		t.Errorf("expected DYZ-Movie under 劳改所, got %#v", rows)
	}
	for _, external := range []string{"Madeline", "Book", "Ream", "WEB", "Web"} {
		if bySiteGroup["未知站点\x00"+external] != 1 {
			t.Errorf("expected %q in 未知站点, got %#v", external, rows)
		}
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
