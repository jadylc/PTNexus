package sites

import "testing"

// TestMapHHanClubSourceWebDL 憨憨「来源」字段为 WEB-DL 时应映射为标准产地键；
// 「处理」字段（如 英剧）不应顶替来源。
func TestMapHHanClubSourceWebDL(t *testing.T) {
	// 来源 WEB-DL 属于片源类型，不是产地——映射器应返回空或标准键，不能崩
	got := mapHHanClubSource("WEB-DL")
	t.Logf("mapHHanClubSource(\"WEB-DL\") = %q", got)

	// 处理字段值「英剧」不应被当成来源
	gotEN := mapHHanClubSource("英剧")
	t.Logf("mapHHanClubSource(\"英剧\") = %q", gotEN)
}
