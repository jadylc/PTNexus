package extract

import (
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"
)

const chdbitsID = "593793"

func TestExtractTorrentIDFromRowNumeric(t *testing.T) {
	html := `<tr><td><a href="details.php?id=` + chdbitsID + `">Name</a></td></tr>`
	root, err := xhtml.Parse(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse html failed: %v", err)
	}
	if got := extractTorrentIDFromRow(root); got != chdbitsID {
		t.Fatalf("expected %s, got %s", chdbitsID, got)
	}
}

func TestFindTorrentRowHTMLFromListPageNumericID(t *testing.T) {
	html := `<html><body><table class="torrents"><tbody>
		<tr>
			<td><a href="details.php?id=` + chdbitsID + `">Name</a></td>
			<td><div class="tag">禁转</div></td>
		</tr>
	</tbody></table></body></html>`
	row, found := findTorrentRowHTMLFromListPage(html, chdbitsID)
	if !found {
		t.Fatalf("expected row found for numeric id")
	}
	if !strings.Contains(row, "禁转") {
		t.Fatalf("expected tags in row html, got %q", row)
	}
}

func TestExtractTagsFromListRowHTMLNumeric(t *testing.T) {
	row := `<tr><td><a href="details.php?id=` + chdbitsID + `">Name</a></td><td><div class="tag">禁转</div><div class="tag">中字</div></td></tr>`
	tags := extractTagsFromListRowHTML(row)
	if len(tags) != 2 {
		t.Fatalf("expected 2 tags, got %#v", tags)
	}
}
