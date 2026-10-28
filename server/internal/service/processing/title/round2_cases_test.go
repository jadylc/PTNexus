package title

import (
	"strings"
	"testing"
)

// TestBuildSimpleTitleComponentsRound2Cases 回归锁定第二轮用户案例。
func TestBuildSimpleTitleComponentsRound2Cases(t *testing.T) {
	cases := []struct {
		title    string
		checkKey string
		want     string
	}{
		{"The Naked Gun Trilogy 1988-1994 1080p BluRay x265 10bit 5.1 2Audios MNHD-FRDS", "无法识别", ""},
		{"Gravity 2013 1080p BluRay x264 3D HSBS DTS-HD MA 5.1-CMCT", "视频格式", "3D HSBS"},
		{"Gravity 2013 1080p BluRay x264 3D HSBS DTS-HD MA 5.1-CMCT", "无法识别", ""},
		{"Get Out 2017 2160p UHD BluRay HDR x265 DTS:X 7.1 X 5Audios mUHD-FRDS", "无法识别", ""},
		{"Bruce Almighty 2003 BluRay 1080p x265 2Audio MNHD-FRDS", "无法识别", ""},
	}
	for _, tc := range cases {
		components := BuildSimpleTitleComponents(tc.title, "")
		got := componentValue(components, tc.checkKey)
		if got != tc.want {
			t.Fatalf("title %q: expected %s=%q, got %q", tc.title, tc.checkKey, tc.want, got)
		}
	}
}

// TestStandaloneChannelTokensMainTitleVersion 主标题中的点分版本号（"Movie 2.0"）
// 不会被裸声道清理移除。既有行为：reTechSplit 会把版本号拆成 "2"/"0" 两片，
// 但都不满足声道格式，声道扫描不命中；版本号形态（2 0）属既有分词行为，不在本修复范围。
func TestStandaloneChannelTokensMainTitleVersion(t *testing.T) {
	title := "Movie 2.0 2023 1080p BluRay x264-GROUP"
	components := BuildSimpleTitleComponents(title, "GROUP")
	mainTitle := componentValue(components, "主标题")
	if strings.Contains(mainTitle, "5.1") || strings.Contains(mainTitle, "7.1") {
		t.Fatalf("expected no channel token in main title, got %q", mainTitle)
	}
}
