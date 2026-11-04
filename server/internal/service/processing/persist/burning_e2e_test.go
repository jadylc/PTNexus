package persist

import (
	"fmt"
	"strings"
	"testing"

	processingtitle "github.com/pt-nexus/server/internal/service/processing/title"
)

// TestBurningEndToEndBodyToComponents 模拟方案二全链路：
// 正文里内嵌 MediaInfo（列原本为空）→ 提取 → 组件构建应带音频编码。
func TestBurningEndToEndBodyToComponents(t *testing.T) {
	body := "◎译　　名 燃烧 / Burning\n◎年　　代 2018\n◎简　　介\n\nGeneral\nComplete name                            : Burning.2018.1080p.BluRay.1080p.x265.10bit.MNHD-FRDS.mkv\nFormat                                   : Matroska\nFile size                                : 7.75 GiB\nDuration                                 : 2 h 28 min\nOverall bit rate                         : 7 485 kb/s\n\nVideo\nID                                       : 1\nFormat                                   : HEVC\nWidth                                    : 1 920 pixels\nHeight                                   : 804 pixels\nBit depth                                : 10 bits\n\nAudio\nID                                       : 2\nFormat                                   : AC-3\nCommercial name                          : Dolby Digital\nCodec ID                                 : A_AC3\nBit rate                                 : 640 kb/s\nChannel(s)                               : 6 channels\nLanguage                                 : Korean\n"
	media, _ := ExtractMediainfoFromBody(body)
	if media == "" {
		t.Fatal("未提取到媒体文本")
	}
	result := processingtitle.BuildTitleComponentsForStorage("Burning 2018 1080p BluRay 1080p x265 10bit MNHD-FRDS", media, processingtitle.BuildSimpleTitleComponentsWithMediaInfo)
	for _, c := range result.Components {
		if key := strings.TrimSpace(fmt.Sprint(c["key"])); key == "音频编码" {
			fmt.Printf("音频编码 = %q\n", c["value"])
			if strings.TrimSpace(fmt.Sprint(c["value"])) == "" {
				t.Fatal("音频编码组件为空")
			}
			return
		}
	}
	t.Fatal("组件中不存在音频编码项")
}
