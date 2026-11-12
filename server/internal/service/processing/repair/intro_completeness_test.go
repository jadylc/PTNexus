package repair

import "testing"

// TestCheckIntroBodyCompletenessRealSamples 用生产库真实样本回归：
// 611 行 reviewed 全灭的根因定位（Go 迁移后全角空格/BBCode 兼容问题）。
func TestCheckIntroBodyCompletenessRealSamples(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "标准全角空格排版",
			body: "◎译　　名　成年人/Snowflakes\n◎片　　名　Adults\n◎年　　代　2025\n◎产　　地　美国\n◎类　　别　喜剧\n◎语　　言　英语\n◎简介　...",
			want: true,
		},
		{
			name: "BBCode 包裹的行",
			body: "[b][color=darkred]◎译　　名[/color][/b]　大侦探阿雷亚\n[b][color=darkred]◎片　　名[/color][/b]　Agent\n[b][color=darkred]◎产　　地[/color][/b]　印度\n◎简　　介　...",
			want: true,
		},
		{
			name: "半角空格排版",
			body: "◎片 名: Adults\n◎产 地: 美国\n◎简 介: test",
			want: true,
		},
		{
			name: "英文字段",
			body: "Title: Adults\nCountry: USA\nPlot: test",
			want: true,
		},
		{
			name: "真正缺字段",
			body: "只 Some text without any field markers",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckIntroBodyCompleteness(tc.body).IsComplete
			if got != tc.want {
				t.Errorf("body=%q IsComplete=%v, want %v", tc.body[:min(50, len(tc.body))], got, tc.want)
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
