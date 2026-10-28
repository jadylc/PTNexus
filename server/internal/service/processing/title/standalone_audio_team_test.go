package title

import (
	"strings"
	"testing"
)

// TestBuildSimpleTitleComponentsStandalone2Audio 无编码关键字、仅 "2Audio" 标注双音轨：
// 音轨数标记不泄漏进无法识别（Bruce Almighty / The Mermaid 案例）。
func TestBuildSimpleTitleComponentsStandalone2Audio(t *testing.T) {
	for _, title := range []string{
		"Bruce Almighty 2003 BluRay 1080p x265 2Audio MNHD-FRDS",
		"The Mermaid 2016 BluRay 1080p x265 10bit 2Audio MNHD-FRDS",
	} {
		components := BuildSimpleTitleComponents(title, "")
		unrecognized := componentValue(components, "无法识别")
		if strings.Contains(strings.ToLower(unrecognized), "2audio") {
			t.Fatalf("title %q: expected 2Audio NOT in 无法识别, got %q", title, unrecognized)
		}
	}
}

// TestBuildSimpleTitleComponentsUploaderAtGroup "上传者@站点组" 制作组归一化：
// ping181@HDSky 取 @ 后段 HDSky，可映射到标准制作组键。
func TestBuildSimpleTitleComponentsUploaderAtGroup(t *testing.T) {
	title := "The Roundup No Way Out 2023 1080p WEB-DL H.264 AAC 2.0-ping181@HDSky"
	components := BuildSimpleTitleComponents(title, "")

	if got := componentValue(components, "制作组"); got != "HDSky" {
		t.Fatalf("expected 制作组=HDSky, got %q", got)
	}
	unrecognized := componentValue(components, "无法识别")
	if strings.Contains(strings.ToLower(unrecognized), "ping181") {
		t.Fatalf("expected uploader prefix NOT in 无法识别, got %q", unrecognized)
	}
}
