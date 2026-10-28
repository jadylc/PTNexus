package title

import "testing"

// TestResolutionSizeForm 尺寸写法分辨率（1920x1080）识别为标准分辨率并从无法识别清理。
func TestResolutionSizeForm(t *testing.T) {
	cases := []struct {
		title      string
		mainTitle  string
		resolution string
	}{
		{"Galileo Season1 BDRIP 1920x1080 X264 FLAC-APPLE51320&KUNO-DIY", "Galileo Season1", "1080p"},
		{"Movie 2020 3840x2160 UHD BDRip x265-GROUP", "Movie", "2160p"},
	}
	for _, tc := range cases {
		components := BuildSimpleTitleComponents(tc.title, "")
		if got := componentValue(components, "主标题"); got != tc.mainTitle {
			t.Fatalf("title %q: expected main title %q, got %q", tc.title, tc.mainTitle, got)
		}
		if got := componentValue(components, "分辨率"); got != tc.resolution {
			t.Fatalf("title %q: expected 分辨率=%q, got %q", tc.title, tc.resolution, got)
		}
		if got := componentValue(components, "无法识别"); got != "" {
			t.Fatalf("title %q: expected empty 无法识别, got %q", tc.title, got)
		}
	}
}
