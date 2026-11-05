package repository

import (
	"testing"
)

// TestParseGroupNameTokensFromTorrentName 新方法只按分隔符切 token，不依赖标题解析组件。
func TestParseGroupNameTokensFromTorrentName(t *testing.T) {
	cases := []struct {
		name string
		want []string
	}{
		{"DouBan_IMDB.TOP250.Movies.Mixed.Collection.20240501.FRDS",
			[]string{"DouBan", "IMDB", "TOP250", "Movies", "Mixed", "Collection", "20240501", "FRDS"}},
		{"Pornographic Movies.Mixed.Collection.20240501.FRDS",
			[]string{"Pornographic", "Movies", "Mixed", "Collection", "20240501", "FRDS"}},
		{"Some.Movie.2024.1080p.WEB-DL-FRDS",
			[]string{"Some", "Movie", "2024", "1080p", "WEB", "DL", "FRDS"}},
		{"Sleepwalkers.2021.BluRay.1080p.DD5.1.x264-BMDru",
			[]string{"Sleepwalkers", "2021", "BluRay", "1080p", "DD5", "1", "x264", "BMDru"}},
		{"", nil},
	}
	for _, tc := range cases {
		got := parseGroupNameTokensFromTorrentName(tc.name)
		if len(got) != len(tc.want) {
			t.Errorf("%q => %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%q token[%d] = %q, want %q", tc.name, i, got[i], tc.want[i])
			}
		}
	}
}

// TestResolveGroupNameFromTorrentName 未拉取种子的名字兜底判定：
// 点号 .FRDS 命中官组归月月；清单外 -BMDru 作为外部组；纯技术串不算组名。
func TestResolveGroupNameFromTorrentName(t *testing.T) {
	matchSite := func(team string) string {
		switch team {
		case "FRDS", "MNHD-FRDS", "mUHD-FRDS":
			return "月月"
		case "MTeam":
			return "馒头"
		}
		return ""
	}
	cases := []struct {
		name      string
		wantSite  string
		wantGroup string
		wantKnown bool
	}{
		{"DouBan_IMDB.TOP250.Movies.Mixed.Collection.20240501.FRDS", "月月", "FRDS", true},
		{"Pornographic Movies.Mixed.Collection.20240501.FRDS", "月月", "FRDS", true},
		{"Sleepwalkers.2021.BluRay.1080p.DD5.1.x264-BMDru", "", "BMDru", false},
		{"Movie.2020.1080p.WEB-DL.x265.HDR.DDP5.1", "", "", false},
	}
	for _, tc := range cases {
		site, group, known := resolveGroupNameFromTorrentName(tc.name, matchSite)
		if site != tc.wantSite || group != tc.wantGroup || known != tc.wantKnown {
			t.Errorf("%q => (%q,%q,%v), want (%q,%q,%v)",
				tc.name, site, group, known, tc.wantSite, tc.wantGroup, tc.wantKnown)
		}
	}
}

// TestIsLikelyGroupNameToken 技术串不得被当成外部组名。
func TestIsLikelyGroupNameToken(t *testing.T) {
	for _, token := range []string{"1080p", "x265", "DDP", "2024", "HDR10", "10bit", "WEB", "AAC", "1"} {
		if isLikelyGroupNameToken(token) {
			t.Errorf("%q should not be a group name token", token)
		}
	}
	for _, token := range []string{"BMDru", "FLTBK", "WiKi", "FRDS"} {
		if !isLikelyGroupNameToken(token) {
			t.Errorf("%q should be a group name token", token)
		}
	}
}
