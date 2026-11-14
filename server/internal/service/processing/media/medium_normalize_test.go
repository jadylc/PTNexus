package media

import "testing"

// TestNormalizeMediumByMediaType 回归 2026.11：Web-DL 种子被改成 encode。
func TestNormalizeMediumByMediaType(t *testing.T) {
	cases := []struct {
		medium string
		isMI   bool
		isBD   bool
		want   string
		reason string
	}{
		{"medium.webdl", true, false, "medium.webdl", "Web-DL + MediaInfo 不应变 encode"},
		{"medium.webdl", true, true, "medium.webdl", "Web-DL + BDInfo 不应变"},
		{"medium.bluray", true, false, "medium.encode", "BluRay + MediaInfo → encode（压片）"},
		{"medium.uhd_bluray", true, false, "medium.encode", "UHD BluRay + MediaInfo → encode"},
		{"medium.remux", true, false, "medium.remux", "Remux 不参与"},
		{"medium.other", true, false, "medium.other", "other 保持"},
		{"medium.webdl", false, false, "medium.webdl", "无媒体信息保持原值"},
	}
	for _, c := range cases {
		if got := NormalizeMediumByMediaType(c.medium, c.isMI, c.isBD); got != c.want {
			t.Errorf("NormalizeMediumByMediaType(%q, mi=%v, bd=%v) = %q, want %q（%s）",
				c.medium, c.isMI, c.isBD, got, c.want, c.reason)
		}
	}
}
