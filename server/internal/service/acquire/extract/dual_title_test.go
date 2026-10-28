package extract

import "testing"

// TestNormalizeMultilineTitle 用户案例：详情页顶部标题混入两行（中文信息条+英文发布名），
// 需拆分为英文主标题 + 中文副标题，而不是把英文发布名判进无法识别。
func TestNormalizeMultilineTitle(t *testing.T) {
	main := "行骗天下JP | 全10集 | 1080p | 类型: 剧情/喜剧 | 导演: 田中亮/金井纮 | 主演: 长泽雅美/东出昌大/小日向文世/江口洋介/吉濑美智子 | A\nThe Confidence Man JP S01 2018 1080p NF WEB-DL AAC2.0 H264-HHWEB"

	gotTitle, gotSubtitle := NormalizeMultilineTitle(main, "")
	if gotTitle != "The Confidence Man JP S01 2018 1080p NF WEB-DL AAC2.0 H264-HHWEB" {
		t.Fatalf("expected English release line as title, got %q", gotTitle)
	}
	if gotSubtitle != "行骗天下JP | 全10集 | 1080p | 类型: 剧情/喜剧 | 导演: 田中亮/金井纮 | 主演: 长泽雅美/东出昌大/小日向文世/江口洋介/吉濑美智子 | A" {
		t.Fatalf("expected Chinese info line as subtitle, got %q", gotSubtitle)
	}
}

// TestNormalizeMultilineTitleKeepsExistingSubtitle 已有副标题内容与拆分行一致时不重复拼接。
func TestNormalizeMultilineTitleKeepsExistingSubtitle(t *testing.T) {
	blob := "行骗天下JP | 全10集 | 1080p"
	main := blob + "\nThe Confidence Man JP S01 2018 1080p NF WEB-DL AAC2.0 H264-HHWEB"

	gotTitle, gotSubtitle := NormalizeMultilineTitle(main, blob)
	if gotTitle != "The Confidence Man JP S01 2018 1080p NF WEB-DL AAC2.0 H264-HHWEB" {
		t.Fatalf("unexpected title %q", gotTitle)
	}
	if gotSubtitle != blob {
		t.Fatalf("expected existing subtitle kept, got %q", gotSubtitle)
	}
}

// TestNormalizeMultilineTitleSingleLine 单行标题不受影响。
func TestNormalizeMultilineTitleSingleLine(t *testing.T) {
	main := "The Confidence Man JP S01 2018 1080p NF WEB-DL AAC2.0 H264-HHWEB"
	gotTitle, gotSubtitle := NormalizeMultilineTitle(main, "副标题")
	if gotTitle != main || gotSubtitle != "副标题" {
		t.Fatalf("unexpected result %q / %q", gotTitle, gotSubtitle)
	}
}

// TestNormalizeMultilineTitleSpaceJoined 存量数据场景：两行被空格折叠连接成单行，
// 副标题片段仍可顺序定位出切割点，剩余的英文发布名为主标题。
func TestNormalizeMultilineTitleSpaceJoined(t *testing.T) {
	blob := "行骗天下JP | 全10集 | 1080p | 类型: 剧情/喜剧 | 导演: 田中亮/金井纮 | 主演: 长泽雅美/东出昌大/小日向文世/江口洋介/吉濑美智子 | A"
	title := blob + " The Confidence Man JP S01 2018 1080p NF WEB-DL AAC2.0 H264-HHWEB"

	gotTitle, gotSubtitle := NormalizeMultilineTitle(title, blob)
	if gotTitle != "The Confidence Man JP S01 2018 1080p NF WEB-DL AAC2.0 H264-HHWEB" {
		t.Fatalf("expected English release line as title, got %q", gotTitle)
	}
	if gotSubtitle != blob {
		t.Fatalf("expected blob kept as subtitle, got %q", gotSubtitle)
	}
}

// TestNormalizeSiteDualTitle 互倒场景：主标题为中文信息条且副标题为英文发布名时交换。
func TestNormalizeSiteDualTitle(t *testing.T) {
	blob := "行骗天下JP | 全10集 | 1080p | 类型: 剧情"
	english := "The Confidence Man JP S01 2018 1080p NF WEB-DL AAC2.0 H264-HHWEB"

	gotTitle, gotSubtitle := NormalizeSiteDualTitle(blob, english)
	if gotTitle != english || gotSubtitle != blob {
		t.Fatalf("expected swap, got %q / %q", gotTitle, gotSubtitle)
	}

	// 标准结构（英文主标题+中文副标题）不交换。
	gotTitle, gotSubtitle = NormalizeSiteDualTitle(english, blob)
	if gotTitle != english || gotSubtitle != blob {
		t.Fatalf("expected no swap for standard layout, got %q / %q", gotTitle, gotSubtitle)
	}
}
