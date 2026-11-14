package persist

import (
	"testing"
)

// TestCheckSeedMaintainabilityWithStandardizedParams 回归 2026.11 误报：
// 面板保存路径曾把"标准化参数未并入校验行"当成"标准参数全空"，导致
// 填了参数也保存不了。这里验证 standardized_params 齐全时校验通过。
func TestCheckSeedMaintainabilityWithStandardizedParams(t *testing.T) {
	row := map[string]any{
		"title_components": `[{"key":"制作组","value":"HHWEB"}]`,
		"poster":           "https://img.example/poster.jpg",
		"screenshots":      "https://img.example/s1.jpg",
		"body":             "◎片　　名　Test\n◎产　　地　美国\n◎简介　test synopsis",
		"mediainfo":        "General\nFormat : Matroska\nFile size : 1 GiB\nDuration : 1h\nOverall bit rate : 1 Mb/s\nVideo\nFormat : HEVC\nWidth : 1 pixels\nHeight : 1 pixels\nAudio\nFormat : E-AC-3\nLanguage : en",
		"standardized_params": map[string]any{
			"type":        "category.movie",
			"medium":      "medium.webdl",
			"video_codec": "video.h264",
			"audio_codec": "audio.ddp",
			"resolution":  "resolution.r1080p",
			"team":        "team.hhweb",
			"source":      "source.western",
			"tags":        []any{"tag.中字"},
		},
	}

	problems := CheckSeedMaintainability(row)
	if len(problems) != 0 {
		t.Fatalf("标准化参数齐全时不应报错，got %#v", problems)
	}
}

// TestCheckSeedMaintainabilityMissingStdParams 标准化参数缺失时报中文提示。
func TestCheckSeedMaintainabilityMissingStdParams(t *testing.T) {
	row := map[string]any{
		"poster":      "https://img.example/poster.jpg",
		"screenshots": "https://img.example/s1.jpg",
		"body":        "◎片　　名　Test\n◎产　　地　美国\n◎简介　test synopsis",
		"mediainfo":   "General\nFormat : Matroska\nFile size : 1 GiB\nDuration : 1h\nOverall bit rate : 1 Mb/s\nVideo\nFormat : HEVC\nWidth : 1 pixels\nHeight : 1 pixels\nAudio\nFormat : E-AC-3\nLanguage : en",
		"standardized_params": map[string]any{
			"type": "category.movie",
		},
	}

	problems := CheckSeedMaintainability(row)
	if len(problems) == 0 {
		t.Fatalf("缺失参数时应报错")
	}
	joined := ""
	for _, p := range problems {
		joined += p
	}
	for _, want := range []string{"媒介未填写", "视频编码未填写", "音频编码未填写", "分辨率未填写"} {
		if !containsSub(joined, want) {
			t.Fatalf("期望包含 %q，实际 %#v", want, problems)
		}
	}
}

func containsSub(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
