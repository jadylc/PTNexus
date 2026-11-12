package persist

import (
	"testing"

	"github.com/pt-nexus/server/internal/service/reversemapping"
)

// TestIsRowPublishableMariaDBRowShape 复现生产"一种多站空搜空白"：
// MariaDB driver 把 ListReviewedSeedParamsByHashes 的所有列 Scan 成 []byte，
// 用生产同款真实 global_mappings.yaml 验证 v4.6.27 发布判定是否放行。
func TestIsRowPublishableMariaDBRowShape(t *testing.T) {
	// 与生产 /k 那 57 行一致：is_reviewed=1 的行，各列为 []byte
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

	reverse := reversemapping.Build(nil)
	if !IsRowPublishableData(row, reverse) {
		t.Fatalf("MariaDB []byte 行 + 真实映射表应放行，但判定为 false")
	}
}
