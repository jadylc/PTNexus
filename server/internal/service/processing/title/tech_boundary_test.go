package title

import "testing"

// TestTechStartWordBoundary 锁定 techStart 词边界修复：候选 token 出现在主标题词汇内部
// （Confidence 内含平台缩写 NF、House/Shou 内含布局缩写 HOU）时不得把技术区切进主标题。
func TestTechStartWordBoundary(t *testing.T) {
	cases := []struct {
		title     string
		mainTitle string
	}{
		{"The Confidence Man JP S01 2018 1080p NF WEB-DL AAC2.0 H264-HHWEB", "The Confidence Man JP"},
		{"The Last House 2026 2160p NF WEB-DL H.265 DDP5.1.Atmos-HHWEB", "The Last House"},
		{"Shou Hu Jie Fang Xi S01 2019 2160p 50fps WEB-DL HDR HEVC 10bit AAC 2.0-AGSVWEB", "Shou Hu Jie Fang Xi"},
		{"Mickey Mouse Clubhouse+ S01 2025 1080p DSNP WEB-DL H.264 DDP 5.1 3Audios-FROGWeb", "Mickey Mouse Clubhouse+"},
		{"The Housemaid 2025 iT WEBRip 1080p x265 10bit DDP5.1 Atmos DV HDR10plus MNHD-FRDS", "The Housemaid"},
		{"Tondemo Skill de Isekai Hourou Meshi S01 2023 1080p WEB-DL H.265 AAC 2.0-ZmWeb", "Tondemo Skill de Isekai Hourou Meshi"},
		{"Master of the House S01 2024 1080p NF WEB-DL H.264 DDP Atmos 5.1-HHWEB", "Master of the House"},
	}
	for _, tc := range cases {
		components := BuildSimpleTitleComponents(tc.title, "")
		if got := componentValue(components, "主标题"); got != tc.mainTitle {
			t.Fatalf("title %q: expected main title %q, got %q", tc.title, tc.mainTitle, got)
		}
		if got := componentValue(components, "无法识别"); got != "" {
			t.Fatalf("title %q: expected empty 无法识别, got %q", tc.title, got)
		}
	}
}

// TestVideoLayoutWordBoundary 3D 布局标记用词边界匹配，HOU 不误命中主标题词。
func TestVideoLayoutWordBoundary(t *testing.T) {
	// 真 HOU（独立 token）应被识别。
	components := BuildSimpleTitleComponents("Movie 2020 1080p HOU BluRay x264-GRP", "GRP")
	if got := componentValue(components, "视频格式"); got != "HOU" {
		t.Fatalf("expected HOU as video format, got %q", got)
	}
	// 主标题内嵌 HOU（House/Shou）不得被识别为布局。
	components = BuildSimpleTitleComponents("The Last House 2026 2160p NF WEB-DL H.265 DDP 5.1-HHWEB", "HHWEB")
	if got := componentValue(components, "视频格式"); got != "" {
		t.Fatalf("expected no video format for embedded HOU, got %q", got)
	}
}

// TestBrandTokenCleanup Dolby/Audio 品牌词从无法识别中清理。
func TestBrandTokenCleanup(t *testing.T) {
	title := "Exhuma 2024 HK Blu-ray 1080p AVC Dolby TrueHD Audio 5.1 - MTeam"
	components := BuildSimpleTitleComponents(title, "MTeam")
	if got := componentValue(components, "无法识别"); got != "" {
		t.Fatalf("expected empty 无法识别, got %q", got)
	}
	if got := componentValue(components, "音频编码"); got != "TrueHD 5.1" {
		t.Fatalf("expected TrueHD 5.1, got %q", got)
	}
}
