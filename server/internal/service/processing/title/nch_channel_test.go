package title

import "testing"

// TestNCHChannelCleanup 回归：NCH 形式声道（如 6CH）不再残留在无法识别，
// 且能被音频编码值记录（AAC 6CH）。
func TestNCHChannelCleanup(t *testing.T) {
	cases := []struct {
		title        string
		audio        string
		unrecognized string
	}{
		{"The Terminal List 2026 S02 1080p 10bit WEBRip 6CH x265 HEVC-PSA", "", ""},
		{"Movie 2020 1080p WEB-DL AAC 6CH x264-GRP", "AAC 6CH", ""},
	}
	for _, tc := range cases {
		components := BuildSimpleTitleComponents(tc.title, "")
		if got := componentValue(components, "音频编码"); got != tc.audio {
			t.Fatalf("title %q: expected 音频编码=%q, got %q", tc.title, tc.audio, got)
		}
		if got := componentValue(components, "无法识别"); got != tc.unrecognized {
			t.Fatalf("title %q: expected 无法识别=%q, got %q", tc.title, tc.unrecognized, got)
		}
	}
}
