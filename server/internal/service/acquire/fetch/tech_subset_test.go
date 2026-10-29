package fetch

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestResolveSearchTechSubsetDegradation 技术词子集降级：不匹配的词在词集中间
// （中文词），尾部缩减只能产生前缀子集、永远删不掉它；降级为技术词子集后命中。
func TestResolveSearchTechSubsetDegradation(t *testing.T) {
	wantedInfo := bencodeInfo("Gattaca")
	wantedHash := infoHashOf(wantedInfo)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			search := r.URL.Query().Get("search")
			// 模拟 NexusPHP AND 语义：搜索词的每个词都要出现在标题中
			title := "Gattaca 1997 BluRay 1080p x264 AC3 2Audios-CMCT"
			allPresent := true
			for _, term := range strings.Fields(search) {
				if !strings.Contains(title, term) {
					allPresent = false
					break
				}
			}
			if !allPresent {
				fmt.Fprint(w, `<html><body><table class="torrents" id="torrenttable"></table></body></html>`)
				return
			}
			fmt.Fprint(w, `<html><body><table class="torrents" id="torrenttable"><tr><td>
				<a title="Gattaca 1997 BluRay 1080p x264 AC3 2Audios-CMCT" href="details.php?id=888888&amp;hit=1">Gattaca 1997 BluRay 1080p x264 AC3 2Audios-CMCT</a>
			</td></tr></table></body></html>`)
		case "/download.php":
			w.Header().Set("Content-Type", "application/x-bittorrent")
			_, _ = w.Write(wantedInfo)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// 中文词在词集中间，尾部缩减 [千钧一发 1977 1080p 国英双语 CMCT] 全部失败
	got, err := resolveTorrentIDBySearch(server.URL, "uid=1", "pk", "千钧一发 1997 1080p 国英双语 CMCT", wantedHash)
	if err != nil {
		t.Fatalf("expected tech-subset degradation to resolve, got error: %v", err)
	}
	if got != "888888" {
		t.Fatalf("expected id 888888, got %q", got)
	}
}

// TestContainsCJKText CJK 检测：技术 token 不含 CJK，中文词含 CJK。
func TestContainsCJKText(t *testing.T) {
	cases := map[string]bool{
		"1997":       false,
		"1080p":      false,
		"x265":       false,
		"CMCT":       false,
		"HDR10+":     false,
		"AE":         false,
		"千钧一发":       true,
		"国英双语":       true,
		"中英字幕":       true,
		"海外电影":       true,
		"english":    false,
		"西语":         true,
		"一二三四五六七八九十": true,
	}
	for s, want := range cases {
		if got := containsCJKText(s); got != want {
			t.Fatalf("containsCJKText(%q): expected %v, got %v", s, want, got)
		}
	}
}
