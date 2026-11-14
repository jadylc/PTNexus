package persist

import (
	"testing"
)

// TestManualSavePassesMaintainabilityCheck 端到端复现维护面板保存：
// BuildManualUpdatedSeedRecord → NormalizeSeedRow → 校验。
// 回归 2026.11「填好参数仍提示标准参数为空、无法保存」。
func TestManualSavePassesMaintainabilityCheck(t *testing.T) {
	existing := map[string]any{
		"hash":             "hash-a",
		"torrent_id":       "1001",
		"site_name":        "hhanclub",
		"nickname":         "憨憨",
		"title":            "Test.Movie.2025.1080p.WEB-DL",
		"type":             "category.movie",
		"medium":           "medium.webdl",
		"video_codec":      "video.h264",
		"audio_codec":      "audio.ddp",
		"resolution":       "resolution.r1080p",
		"team":             "team.hhweb",
		"source":           "source.western",
		"tags":             `["tag.中字"]`,
		"title_components": `[{"key":"制作组","value":"HHWEB"}]`,
		"poster":           "https://img.example/p.jpg",
		"screenshots":      "https://img.example/s1.jpg",
		"body":             "◎片　　名　Test\n◎产　　地　美国\n◎简介　test synopsis",
		"mediainfo":        "General\nFormat : Matroska\nFile size : 1 GiB\nDuration : 1h\nOverall bit rate : 1 Mb/s\nVideo\nFormat : HEVC\nWidth : 1 pixels\nHeight : 1 pixels\nAudio\nFormat : E-AC-3\nLanguage : en",
	}
	updated := map[string]any{
		"title":                    "Test.Movie.2025.1080p.WEB-DL",
		"subtitle":                 "",
		"statement":                "",
		"body":                     existing["body"],
		"mediainfo":                existing["mediainfo"],
		"poster":                   existing["poster"],
		"screenshots":              existing["screenshots"],
		"screenshot_review_status": "confirmed",
		// 前端面板实际会一并提交的字段（seedFlow.ts 的 saveSeedEdits）
		"title_components": []any{map[string]any{"key": "制作组", "value": "HHWEB"}},
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

	out := BuildManualUpdatedSeedRecord(BuildManualUpdateInput{
		GeneratedHash: "hash-a",
		TorrentID:     "1001",
		SiteName:      "hhanclub",
		TorrentName:   "Test.Movie.2025.1080p.WEB-DL",
		Existing:      existing,
		Updated:       updated,
	})

	normalized := NormalizeSeedRow(out.Record)
	normalized["standardized_params"] = out.Standardized

	problems := CheckSeedMaintainability(normalized)
	if len(problems) != 0 {
		t.Fatalf("面板保存应通过校验，实际问题: %#v", problems)
	}
}
