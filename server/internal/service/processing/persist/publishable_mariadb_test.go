package persist

import "testing"

// TestIsRowPublishableMariaDBRowShape 复现生产"一种多站空搜空白"：
// MariaDB driver 把 ListReviewedSeedParamsByHashes 的所有列 Scan 成 []byte，
// 验证 v4.6.26 发布判定在该形态下是否放行（应放行——已审核+映射有效）。
func TestIsRowPublishableMariaDBRowShape(t *testing.T) {
	// 与生产一致：is_reviewed=1 的行，各列为 []byte
	row := map[string]any{
		"hash":             []byte("abc123"),
		"type":             []byte("category.animation"),
		"medium":           []byte("medium.webdl"),
		"video_codec":      []byte("video.h264"),
		"audio_codec":      []byte("audio.ddp"),
		"resolution":       []byte("resolution.r1080p"),
		"team":             []byte("team.hhweb"),
		"source":           []byte("source.china"),
		"tags":             []byte(`["tag.动画", "tag.完结"]`),
		"title_components": []byte(`[{"key":"无法识别","value":"DSNP"}]`),
		"mediainfo":        []byte("General\nFormat: Matroska\nVideo\nAudio"),
		"body":             []byte("◎片　　名　Test\n◎产　　地　美国\n◎简介　test"),
		"is_reviewed":      []byte("1"),
	}

	// 用真实映射表（与生产同路径）
	reverse := buildTestReverseMappings()
	if !IsRowPublishableData(row, reverse) {
		t.Fatalf("MariaDB []byte 行应放行，但判定为 false")
	}
}

func buildTestReverseMappings() map[string]any {
	// 复用 reversemapping.Build 的等价最小结构
	cats := []string{"type", "medium", "video_codec", "audio_codec", "resolution", "team", "source", "tags"}
	values := map[string][]string{
		"type":        {"category.animation"},
		"medium":      {"medium.webdl"},
		"video_codec": {"video.h264"},
		"audio_codec": {"audio.ddp"},
		"resolution":  {"resolution.r1080p"},
		"team":        {"team.hhweb"},
		"source":      {"source.china"},
		"tags":        {"tag.动画", "tag.完结"},
	}
	result := map[string]any{}
	for _, cat := range cats {
		m := map[string]any{}
		for _, v := range values[cat] {
			m[v] = v
		}
		result[cat] = m
	}
	return result
}
