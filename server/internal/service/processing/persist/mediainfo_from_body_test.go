package persist

import (
	"strings"
	"testing"
)

const markerBody = `◎片　　名 Burning
◎简　　介
剧情简介……

[url=javascript:void(0)]mediainfo - 点击展开[/url]
General
Complete name                            : Burning.mkv
Overall bit rate                         : 7 485 kb/s

Video
Format                                   : HEVC
Width                                    : 1 920 pixels

Audio
Format                                   : AC-3
Channel(s)                               : 6 channels`

const generalBody = `◎片　　名 Burning

General
Complete name                            : Burning.mkv
Format                                   : Matroska
Overall bit rate                         : 7 485 kb/s

Video
Format                                   : HEVC

Audio
Format                                   : AC-3
Channel(s)                               : 6 channels`

func TestExtractMediainfoFromBodyMarker(t *testing.T) {
	media, stripped := ExtractMediainfoFromBody(markerBody)
	if media == "" || !strings.Contains(media, "Complete name") || !strings.Contains(media, "AC-3") {
		t.Fatalf("标记形式提取失败: %q", media)
	}
	if !strings.Contains(stripped, "片　　名") || strings.Contains(stripped, "Complete name") {
		t.Fatalf("标记形式正文剥离异常: %q", stripped)
	}
}

func TestExtractMediainfoFromBodyGeneral(t *testing.T) {
	media, stripped := ExtractMediainfoFromBody(generalBody)
	if media == "" || !strings.Contains(media, "Overall bit rate") {
		t.Fatalf("General 形式提取失败: %q", media)
	}
	if !strings.HasPrefix(stripped, "◎片") || strings.Contains(stripped, "General") {
		t.Fatalf("General 形式正文剥离异常: %q", stripped)
	}
}

func TestExtractMediainfoFromBodyNoFalsePositive(t *testing.T) {
	plain := "◎片　　名 Some Movie\n◎简　　介\n普通简介，没有任何媒体信息表格。"
	media, _ := ExtractMediainfoFromBody(plain)
	if media != "" {
		t.Fatalf("普通正文不应误提取: %q", media)
	}
}

func TestExtractMediainfoFromBodyNBSP(t *testing.T) {
	body := "简介\n\nGeneral\nComplete name\u00a0: x.mkv\nOverall\u00a0bit\u00a0rate: 1 kb/s\n"
	media, _ := ExtractMediainfoFromBody(body)
	if media == "" {
		t.Fatalf("全角空格正文应能提取")
	}
	if strings.Contains(media, "\u00a0") {
		t.Fatalf("提取结果应替换全角空格: %q", media)
	}
}

func TestEnsureMediainfoColumn(t *testing.T) {
	// 列已有值：不动
	if got, changed := EnsureMediainfoColumn("General\nexisting", markerBody); changed || got != "General\nexisting" {
		t.Fatalf("列已有值不应变更")
	}
	// 列为空且正文可提取：补齐
	if got, changed := EnsureMediainfoColumn("", markerBody); !changed || !strings.Contains(got, "AC-3") {
		t.Fatalf("应从正文补齐 mediainfo 列: %q", got)
	}
	// 列为空且正文无媒体信息：保持空
	if _, changed := EnsureMediainfoColumn("", "普通简介"); changed {
		t.Fatalf("普通正文不应触发补列")
	}
}
