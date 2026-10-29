package extract

import "testing"

// TestNormalizeSiteDualTitleYuanyueInfoBar 月月（keepfrds）实际数据：
// 主标题位是中文信息条（用 / 与全角括号分隔，无竖线），副标题位是英文发布名。
// 判定必须以"副标题像发布名"为准，不能被"信息条含竖线"卡住。
func TestNormalizeSiteDualTitleYuanyueInfoBar(t *testing.T) {
	cases := []struct {
		main     string
		sub      string
		wantMain string
	}{
		{
			main:     "【世界的主人/세계의 주인/若问世界谁无伤(港) /世界的珠仁】10bit HEVC版本 韩语 简繁韩字幕",
			sub:      "Segyeui Ju-in AKA The World of Love 2025 BluRay 1080p x265 10bit DDP5.1 MNHD-FRDS",
			wantMain: "Segyeui Ju-in AKA The World of Love 2025 BluRay 1080p x265 10bit DDP5.1 MNHD-FRDS",
		},
		{
			main:     "【终极名单 / 终结名单】第2季 中英字幕 10bit HEVC 版本",
			sub:      "The Terminal List 2026 S02 1080p 10bit WEBRip 6CH x265 HEVC-PSA",
			wantMain: "The Terminal List 2026 S02 1080p 10bit WEBRip 6CH x265 HEVC-PSA",
		},
		{
			main:     "【终极名单 / 终结名单】第1季 中英字幕 10bit HEVC 版本",
			sub:      "The Terminal List 2022 S01 1080p 10bit WEBRip 6CH x265 HEVC-PSA",
			wantMain: "The Terminal List 2022 S01 1080p 10bit WEBRip 6CH x265 HEVC-PSA",
		},
	}
	for _, tc := range cases {
		gotMain, gotSub := NormalizeSiteDualTitle(tc.main, tc.sub)
		if gotMain != tc.wantMain {
			t.Fatalf("main=%q: expected title %q, got %q", tc.main, tc.wantMain, gotMain)
		}
		if gotSub != tc.main {
			t.Fatalf("main=%q: expected subtitle to become the info bar, got %q", tc.main, gotSub)
		}
	}
}

// TestNormalizeSiteDualTitleStandardLayout 标准结构（西文主标题+中文副标题）不交换。
func TestNormalizeSiteDualTitleStandardLayout(t *testing.T) {
	main := "The Terminal List 2026 S02 1080p 10bit WEBRip 6CH x265 HEVC-PSA"
	sub := "【终极名单 / 终结名单】第2季 中英字幕 10bit HEVC 版本"
	gotMain, gotSub := NormalizeSiteDualTitle(main, sub)
	if gotMain != main || gotSub != sub {
		t.Fatalf("expected standard layout unchanged, got %q / %q", gotMain, gotSub)
	}
}

// TestNormalizeSiteDualTitleChineseSubtitleNoTech 中文副标题不含技术标记时不交换
// （避免误伤"西文主标题 + 中文简介副标题"的普通站点）。
func TestNormalizeSiteDualTitleChineseSubtitleNoTech(t *testing.T) {
	main := "中文电影名"
	sub := "另一段中文说明"
	gotMain, gotSub := NormalizeSiteDualTitle(main, sub)
	if gotMain != main || gotSub != sub {
		t.Fatalf("expected unchanged for CJK subtitle, got %q / %q", gotMain, gotSub)
	}
}
