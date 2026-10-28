package extract

import "testing"

// TestExtractKeepfrdsTitles 验证月月主副标题语义：
// 种子 name（英文主标题）填在「副标题」（small_descr）行，页面顶部 h1 是中文显示名。
// 主标题应取「副标题」行（英文），顶部 h1（中文）作为副标题。
func TestExtractKeepfrdsTitles(t *testing.T) {
	pageHTML := `<html><head><title>Full.Circle.2023.S01.2160p.IAMAFISH.WEB-DL.H.265-HHWEB - 月月PT</title></head>
	<body>
	<h1><a href="details.php?id=1">【月月PT】副标题行填写完整原盘命名 Full.Circle.2023.S01.2160p.IAMAFISH.WEB-DL.H.265-HHWEB</a></h1>
	<table>
		<tr><td>副标题</td><td>Full.Circle.2023.S01.2160p.IAMAFISH.WEB-DL.H.265-HHWEB</td></tr>
		<tr><td>IMDb</td><td>https://www.imdb.com/title/tt1234567/</td></tr>
	</table>
	</body></html>`

	mainTitle, subtitle := extractKeepfrdsTitles(pageHTML, "fallback")
	if mainTitle != "Full.Circle.2023.S01.2160p.IAMAFISH.WEB-DL.H.265-HHWEB" {
		t.Fatalf("expected English main title from 副标题 row, got %q", mainTitle)
	}
	if subtitle == "" {
		t.Fatalf("expected top title (Chinese display name) as subtitle")
	}
}

// TestExtractKeepfrdsTitlesWithoutSubtitleRow 无「副标题」行时回退到顶部标题。
func TestExtractKeepfrdsTitlesWithoutSubtitleRow(t *testing.T) {
	pageHTML := `<html><head><title>Full.Circle.2023.S01.2160p.IAMAFISH.WEB-DL.H.265-HHWEB</title></head>
	<body><h1>Full.Circle.2023.S01.2160p.IAMAFISH.WEB-DL.H.265-HHWEB</h1></body></html>`

	mainTitle, subtitle := extractKeepfrdsTitles(pageHTML, "fallback")
	if mainTitle == "" {
		t.Fatalf("expected fallback to top title")
	}
	if subtitle != "" {
		t.Fatalf("expected empty subtitle without 副标题 row, got %q", subtitle)
	}
}

// TestExtractKeepfrdsTitlesSameTitle 顶部标题与副标题行相同时，副标题应为空（避免重复）。
func TestExtractKeepfrdsTitlesSameTitle(t *testing.T) {
	same := "Same.Title.2024-GROUP"
	pageHTML := `<html><head><title>` + same + `</title></head>
	<body><h1>` + same + `</h1>
	<table><tr><td>副标题</td><td>` + same + `</td></tr></table></body></html>`

	mainTitle, subtitle := extractKeepfrdsTitles(pageHTML, "fallback")
	if mainTitle != same {
		t.Fatalf("expected main title %q, got %q", same, mainTitle)
	}
	if subtitle != "" {
		t.Fatalf("expected empty subtitle when identical, got %q", subtitle)
	}
}
