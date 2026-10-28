package fetch

import (
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// buildTestTorrent 构造一个可被 bdecode 解析的最小 torrent（announce + info.name）。
func buildTestTorrent(name string) []byte {
	const announce = "http://tracker.example/a"
	return []byte("d8:announce" + itoaBencode(len(announce)) + ":" + announce + "4:infod4:name" + itoaBencode(len(name)) + ":" + name + "ee")
}

func itoaBencode(n int) string {
	digits := ""
	if n == 0 {
		return "0"
	}
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func torrentInfohash(t *testing.T, body []byte) string {
	t.Helper()
	meta, err := ParseTorrentMeta(body)
	if err != nil {
		t.Fatalf("parse test torrent failed: %v", err)
	}
	return meta.InfoHash
}

// TestResolveTorrentIDBySearchMatchesTitleAttr 验证完整种子名在 title 属性中（可见文本被截断）时仍能精确定位。
func TestResolveTorrentIDBySearchMatchesTitleAttr(t *testing.T) {
	name := "Carrie.S01.2160p.AMZN.WEB-DL.DDP.5.1.Atmos.HDR10+.H.265-CHDWEB"
	html := `<table class="torrents">
		<tr><td><a href="details.php?id=100&amp;hit=1" title="Some.Other.Release-X">Some.Other…</a></td></tr>
		<tr><td><a href="details.php?id=593793&amp;hit=1" title="` + name + `">Carrie.S01.2160p…</a></td></tr>
		<tr><td><a href="details.php?id=200&amp;hit=1" title="Third.Release-Y">Third…</a></td></tr>
	</table>`

	candidates := collectSearchCandidates(html)
	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
	}
	// 精确 title 匹配应命中 593793（不依赖网络请求的部分在此覆盖）。
	hit := ""
	for _, candidate := range candidates {
		if candidate.title == name || candidate.text == name {
			hit = candidate.id
			break
		}
	}
	if hit != "593793" {
		t.Fatalf("expected exact title match 593793, got %q", hit)
	}
}

// TestCollectSearchCandidatesDedupesAndNormalizes 验证候选去重与文本归一化（内部高亮标签）。
func TestCollectSearchCandidatesDedupesAndNormalizes(t *testing.T) {
	html := `<tr>
		<td><a href="details.php?id=593793" title="Full.Name.Here">Name.With<b>Highlight</b> Text</a></td>
		<td><a href="details.php?id=593793">duplicate row link</a></td>
	</tr>`
	candidates := collectSearchCandidates(html)
	if len(candidates) != 1 {
		t.Fatalf("expected deduplicated 1 candidate, got %d: %#v", len(candidates), candidates)
	}
	if candidates[0].id != "593793" {
		t.Fatalf("expected id 593793, got %q", candidates[0].id)
	}
	if candidates[0].text != "Name.With Highlight Text" {
		t.Fatalf("expected normalized text, got %q", candidates[0].text)
	}
	if candidates[0].title != "Full.Name.Here" {
		t.Fatalf("expected title extracted, got %q", candidates[0].title)
	}
}

// TestResolveTorrentIDBySearchVerifyByInfohash 验证名称精确匹配失败时按 infohash 逐候选下载验证。
func TestResolveTorrentIDBySearchVerifyByInfohash(t *testing.T) {
	t.Setenv("PTNEXUS_DATA_DIR", t.TempDir())

	name := "Carrie.S01.2160p.AMZN.WEB-DL.DDP.5.1.Atmos.HDR10+.H.265-CHDWEB"
	// 两个候选：文本均与种子名不完全一致（模拟截断/变形），必须靠 infohash 验证区分。
	wantedTorrent := buildTestTorrent(name)
	wantedHash := torrentInfohash(t, wantedTorrent)
	otherTorrent := buildTestTorrent("Some.Other.Release")

	var wantedDownloaded, otherDownloaded int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			_, _ = w.Write([]byte(`<table class="torrents">
				<tr><td><a href="details.php?id=111&amp;hit=1" title="Carrie.S01 truncated A">Carrie.S01…</a></td></tr>
				<tr><td><a href="details.php?id=593793&amp;hit=1" title="Carrie.S01 truncated B">Carrie.S01…</a></td></tr>
			</table>`))
		case "/download.php":
			switch r.URL.Query().Get("id") {
			case "593793":
				wantedDownloaded++
				w.Header().Set("Content-Type", "application/x-bittorrent")
				_, _ = w.Write(wantedTorrent)
			case "111":
				otherDownloaded++
				w.Header().Set("Content-Type", "application/x-bittorrent")
				_, _ = w.Write(otherTorrent)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	got, err := resolveTorrentIDBySearch(server.URL, "uid=1", "pk", name, wantedHash)
	if err != nil {
		t.Fatalf("resolveTorrentIDBySearch failed: %v", err)
	}
	if got != "593793" {
		t.Fatalf("expected infohash-verified id 593793, got %q", got)
	}
	if wantedDownloaded == 0 {
		t.Fatalf("expected wanted candidate downloaded for verification")
	}
	_ = otherDownloaded
}

// TestResolveTorrentIDBySearchAmbiguousFailsCleanly 验证验证兜底也未命中时返回可读错误。
func TestResolveTorrentIDBySearchAmbiguousFailsCleanly(t *testing.T) {
	t.Setenv("PTNEXUS_DATA_DIR", t.TempDir())

	name := "Not.To.Be.Found-GROUP"
	otherTorrent := buildTestTorrent("Some.Other.Release")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			_, _ = w.Write([]byte(`<table class="torrents">
				<tr><td><a href="details.php?id=111" title="A">A</a></td></tr>
				<tr><td><a href="details.php?id=222" title="B">B</a></td></tr>
			</table>`))
		case "/download.php":
			w.Header().Set("Content-Type", "application/x-bittorrent")
			_, _ = w.Write(otherTorrent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	_, err := resolveTorrentIDBySearch(server.URL, "uid=1", "", name, strings.Repeat("ab", 20))
	if err == nil {
		t.Fatalf("expected error for unresolvable candidates")
	}
	if !strings.Contains(err.Error(), "无法唯一定位") {
		t.Fatalf("expected readable ambiguity error, got %v", err)
	}
}

// TestTorrentInfohashConsistency 校验测试辅助：同一字节构造的 infohash 稳定。
func TestTorrentInfohashConsistency(t *testing.T) {
	body := buildTestTorrent("X")
	first := torrentInfohash(t, body)
	second := torrentInfohash(t, body)
	if first != second {
		t.Fatalf("expected stable infohash")
	}
	sum := sha1.Sum(body)
	_ = hex.EncodeToString(sum[:])
	_ = time.Second
}
