package persist

import (
	"strings"
	"testing"

	parser "github.com/pt-nexus/server/internal/service/acquire/extract"
)

// TestReparseEmptyAudioColumnFillScenario 验证重算列修复依赖的推断本身：
// 列值为空串时，从 标题+mediainfo 推断出的标准键应能用于补列。
// （hdhome/49650 场景：mediainfo 列 7764 字节含 AC-3，audio_codec=""）
func TestReparseEmptyAudioColumnFillScenario(t *testing.T) {
	bodyMedia := "General\nComplete name : Burning.2018.1080p.BluRay.1080p.x265.10bit.MNHD-FRDS.mkv\nFormat : Matroska\nOverall bit rate : 7 485 kb/s\n\nVideo\nFormat : HEVC\nWidth : 1 920 pixels\nHeight : 804 pixels\n\nAudio\nID : 2\nFormat : AC-3\nFormat/Info : Audio Coding 3\nCommercial name : Dolby Digital\nCodec ID : A_AC3\nBit rate : 640 kb/s\nChannel(s) : 6 channels\nLanguage : Korean\n"
	inferred := parser.InferStandardizedValues("Burning 2018 1080p BluRay 1080p x265 10bit MNHD-FRDS", bodyMedia, "")
	got := strings.TrimSpace(inferred["audio_codec"])
	if got == "" {
		t.Fatalf("mediainfo 含 AC-3 时应推断出 audio_codec，实际为空")
	}
	if !strings.Contains(got, "ac3") {
		t.Fatalf("期望推断为 ac3 族, got %q", got)
	}
}
