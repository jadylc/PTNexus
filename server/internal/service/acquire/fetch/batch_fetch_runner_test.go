package fetch

import (
	"strings"
	"testing"
)

func TestSplitSiteList(t *testing.T) {
	got := splitSiteList(" ssd, 彩虹岛 , hdsky ")
	want := []string{"ssd", "彩虹岛", "hdsky"}
	if len(got) != len(want) {
		t.Fatalf("expected %#v, got %#v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %#v, got %#v", want, got)
		}
	}
	if len(splitSiteList("")) != 0 {
		t.Fatalf("expected empty list for empty input")
	}
}

func TestResolveSiteTorrentIDPrefersAuthoritative(t *testing.T) {
	siteIDs := map[string]string{"彩虹岛": "593793"}
	got := resolveSiteTorrentID(siteIDs, "彩虹岛", "")
	if got != "593793" {
		t.Fatalf("expected 593793, got %s", got)
	}
}

func TestResolveSiteTorrentIDFallsBackToDetails(t *testing.T) {
	siteIDs := map[string]string{"ssd": "123"}
	got := resolveSiteTorrentID(siteIDs, "彩虹岛", "https://ptchdbits.co/details.php?id=593793&hit=1")
	if got != "593793" {
		t.Fatalf("expected 593793 from details, got %s", got)
	}
}

func TestResolveSiteTorrentIDNoInfohashFallback(t *testing.T) {
	// 站点 ID 缺失且详情页无有效 ID 时，不能回退到 infohash。
	got := resolveSiteTorrentID(map[string]string{}, "彩虹岛", "")
	if got != "" {
		t.Fatalf("expected empty id (no infohash fallback), got %s", got)
	}
	infohash := "42293da64df095a71380aa0ab3a8a77d8b22ca08"
	got = resolveSiteTorrentID(map[string]string{}, "彩虹岛", infohash)
	if got != "" {
		t.Fatalf("expected empty id for infohash-only details, got %s", got)
	}
}

func TestResolveSiteTorrentIDRejectsInfohashFromAuthoritative(t *testing.T) {
	infohash := "42293da64df095a71380aa0ab3a8a77d8b22ca08"
	// seed_parameters 里若存了 infohash（历史脏数据），应拒绝并回退到 details。
	got := resolveSiteTorrentID(
		map[string]string{"彩虹岛": infohash},
		"彩虹岛",
		"https://ptchdbits.co/details.php?id=593793&hit=1",
	)
	if got != "593793" {
		t.Fatalf("expected 593793 from details after rejecting infohash, got %q", got)
	}
}

func TestResolveSiteTorrentIDRejectsInfohashDetails(t *testing.T) {
	infohash := "42293da64df095a71380aa0ab3a8a77d8b22ca08"
	got := resolveSiteTorrentID(map[string]string{}, "彩虹岛", "https://ptchdbits.co/"+infohash)
	if got != "" {
		t.Fatalf("expected empty for infohash-shaped details, got %q", got)
	}
}

// TestRunBatchFetchUsesRealSiteID 复现故障日志：details 为空、hash 为 infohash，
// 验证批量抓取最终把站点真实 ID（593793）作为 searchTerm 传给抓取流程。
func TestRunBatchFetchUsesRealSiteID(t *testing.T) {
	infohash := "42293da64df095a71380aa0ab3a8a77d8b22ca08"
	rows := []map[string]any{
		{
			"name":      "Carrie.S01.2160p.AMZN.WEB-DL.DDP.5.1.Atmos.HDR10+.H.265-CHDWEB",
			"sites":     "彩虹岛",
			"hash":      infohash,
			"details":   "",
			"save_path": "/downloads/qb",
			"site_torrent_ids": map[string]string{
				"彩虹岛": "593793",
			},
		},
	}

	captured := map[string]any{}
	RunBatchFetch(
		[]string{"Carrie.S01.2160p.AMZN.WEB-DL.DDP.5.1.Atmos.HDR10+.H.265-CHDWEB"},
		[]string{"彩虹岛"},
		rows,
		BatchFetchRunnerDeps{
			FetchAndStore: func(payload map[string]any) (map[string]any, int) {
				captured = payload
				return map[string]any{"success": true}, 200
			},
			OnResult: func(bool, map[string]any) {},
		},
	)

	searchTerm := strings.TrimSpace(toStringAny(captured["searchTerm"], ""))
	if searchTerm != "593793" {
		t.Fatalf("expected searchTerm=593793, got %q", searchTerm)
	}
	detailURL := buildDetailURL("https://ptchdbits.co", "chdbits", searchTerm)
	if detailURL != "https://ptchdbits.co/details.php?id=593793" {
		t.Fatalf("expected details.php?id=593793, got %s", detailURL)
	}
}

func TestSelectSourceByPriorityUsesSiteIDs(t *testing.T) {
	entries := []map[string]any{
		{
			"sites":   "彩虹岛",
			"hash":    "42293da64df095a71380aa0ab3a8a77d8b22ca08",
			"details": "",
			"site_torrent_ids": map[string]string{
				"彩虹岛": "593793",
			},
		},
	}
	site, torrentID, matched := selectSourceByPriority(entries, []string{"彩虹岛"})
	if !matched {
		t.Fatalf("expected match")
	}
	if site != "彩虹岛" || torrentID != "593793" {
		t.Fatalf("expected 彩虹岛/593793, got %s/%s", site, torrentID)
	}
}

func TestSelectSourceByPrioritySkipsUnknownIDs(t *testing.T) {
	entries := []map[string]any{
		{
			"sites":   "彩虹岛",
			"hash":    "42293da64df095a71380aa0ab3a8a77d8b22ca08",
			"details": "",
		},
	}
	if _, _, matched := selectSourceByPriority(entries, []string{"彩虹岛"}); matched {
		t.Fatalf("expected no match when no authoritative id is available")
	}
}

func TestSelectSourceByPriorityHandlesMultiSiteRow(t *testing.T) {
	entries := []map[string]any{
		{
			"sites":   "ssd,彩虹岛",
			"hash":    "abc",
			"details": "",
			"site_torrent_ids": map[string]string{
				"ssd": "100",
				"彩虹岛": "593793",
			},
		},
	}
	site, torrentID, matched := selectSourceByPriority(entries, []string{"彩虹岛"})
	if !matched {
		t.Fatalf("expected match for 彩虹岛 in multi-site row")
	}
	if site != "彩虹岛" || torrentID != "593793" {
		t.Fatalf("expected 彩虹岛/593793, got %s/%s", site, torrentID)
	}
}

func TestSiteTorrentIDsHandlesAnyMap(t *testing.T) {
	row := map[string]any{
		"site_torrent_ids": map[string]any{
			"彩虹岛": "593793",
		},
	}
	got := siteTorrentIDs(row)
	if got["彩虹岛"] != "593793" {
		t.Fatalf("expected 593793, got %#v", got)
	}
	if len(siteTorrentIDs(map[string]any{})) != 0 {
		t.Fatalf("expected empty map for missing key")
	}
}

func TestExtractTorrentIDFromDetailsVariants(t *testing.T) {
	cases := []struct {
		name    string
		details string
		want    string
	}{
		{"数字 id", "https://x.co/details.php?id=593793&hit=1", "593793"},
		{"torrent_id 参数", "https://x.co/torrent_id=777", "777"},
		{"空", "", ""},
		{"infohash 不应被当作 ID", "https://x.co/42293da64df095a71380aa0ab3a8a77d8b22ca08", ""},
		{"uuid", "https://rousi.pro/torrent/3f2a1b4c-5d6e-7f80-9a1b-2c3d4e5f6a7b", "3f2a1b4c-5d6e-7f80-9a1b-2c3d4e5f6a7b"},
		{"字母数字 id", "https://x.co/details.php?id=abc123", "abc123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractTorrentIDFromDetails(tc.details)
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestExtractTorrentIDFromDetailsNoInfohashFallback(t *testing.T) {
	infohash := "42293da64df095a71380aa0ab3a8a77d8b22ca08"
	if got := extractTorrentIDFromDetails(infohash); got != "" {
		t.Fatalf("expected empty for bare infohash, got %q", got)
	}
}
