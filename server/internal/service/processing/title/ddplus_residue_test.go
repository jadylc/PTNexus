package title

import (
	"strings"
	"testing"
)

// TestDDPlusChannelNotResidue 标题写 "DD+5.1"（归一化为 DDP 5.1）时，
// 加号变体 token 必须完整清理，不允许泄漏进"无法识别"；
// 同时主标题里的 "5+" 不得被误伤。
func TestDDPlusChannelNotResidue(t *testing.T) {
	components := BuildSimpleTitleComponentsWithMediaInfo(
		"Survive Style 5+ 2004 1080p UHD BluRay DD+5.1 HDR10 x265-PTer", "", "")
	values := map[string]string{}
	for _, item := range components {
		key := strings.TrimSpace(toTestString(item["key"]))
		values[key] = strings.TrimSpace(toTestString(item["value"]))
	}
	if values["音频编码"] != "DDP 5.1" {
		t.Fatalf("expected 音频编码=DDP 5.1, got %q", values["音频编码"])
	}
	if values["无法识别"] != "" {
		t.Fatalf("expected 无法识别 empty, got %q", values["无法识别"])
	}
	if values["主标题"] != "Survive Style 5+" {
		t.Fatalf("expected 主标题=Survive Style 5+, got %q", values["主标题"])
	}
}

// TestDDPlusGluedChannelsVariants 全角加号与多级声道变体同样不得泄漏。
func TestDDPlusGluedChannelsVariants(t *testing.T) {
	cases := map[string]string{
		"Some.Movie.2020 2160p WEB-DL DD+7.1 Atmos x265-GRP":    "DDP 7.1 Atmos",
		"Other.Show.2021 1080p WEB-DL DDP 5.1 AAC 2.0 x264-GRP": "",
	}
	for title, wantAudio := range cases {
		components := BuildSimpleTitleComponentsWithMediaInfo(title, "", "")
		residue := ""
		audio := ""
		for _, item := range components {
			key := strings.TrimSpace(toTestString(item["key"]))
			value := strings.TrimSpace(toTestString(item["value"]))
			if key == "无法识别" {
				residue = value
			}
			if key == "音频编码" {
				audio = value
			}
		}
		if residue != "" {
			t.Fatalf("title %q leaked 无法识别=%q", title, residue)
		}
		if wantAudio != "" && audio != wantAudio {
			t.Fatalf("title %q expected audio %q, got %q", title, wantAudio, audio)
		}
	}
}

func toTestString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
