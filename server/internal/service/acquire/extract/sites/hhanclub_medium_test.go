package sites

import "testing"

// TestMapHHanClubMediumWebDL 回归 2026.11：憨憨详情页「媒介」为 WEB-DL
// 时应映射为 medium.webdl，不能落到 medium.encode。
func TestMapHHanClubMediumWebDL(t *testing.T) {
	cases := map[string]string{
		"WEB-DL":         "medium.webdl",
		"WEB-DL (1080p)": "medium.webdl",
		"WEB DL":         "medium.webdl",
		"Web-DL":         "medium.webdl",
		"WEBDL":          "medium.webdl",
		"Encode":         "medium.encode",
		"Remux":          "medium.remux",
		"BluRay":         "medium.bluray",
		"WEBRip":         "medium.webrip",
	}
	for raw, want := range cases {
		if got := mapHHanClubMedium(raw); got != want {
			t.Errorf("mapHHanClubMedium(%q) = %q, want %q", raw, got, want)
		}
	}
}
