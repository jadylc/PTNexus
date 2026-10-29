package fetch

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// bencodeInfo 构造最小 info dict（name 字段），其 sha1 即为 infohash。
func bencodeInfo(name string) []byte {
	return []byte("d4:infod4:name" + bencodeInt(len([]rune(name))) + ":" + name + "ee")
}

func bencodeInt(n int) string {
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

func infoHashOf(info []byte) string {
	sum := sha1.Sum(info)
	return hex.EncodeToString(sum[:])
}

// TestResolveSearchHdHomeShrinkTerms 家园（HDHome）实测案例：
// 种子名"千钧一发.1997.1080p.国英双语.中英字幕￡CMCT呆呆"，站点标题
// "Gattaca 1997 BluRay 1080p x264 AC3 2Audios-CMCT"（无发布者词"呆呆"，中文名在副标题）。
// 拆词多词 AND 搜索应命中，逐词缩减（去掉"呆呆"）后唯一候选锁定。
func TestResolveSearchHdHomeShrinkTerms(t *testing.T) {
	wantedInfo := bencodeInfo("Gattaca")
	wantedHash := infoHashOf(wantedInfo)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			search := r.URL.Query().Get("search")
			if strings.Contains(search, "呆呆") {
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

	got, err := resolveTorrentIDBySearch(server.URL, "uid=1", "pk", "千钧一发.1997.1080p.国英双语.中英字幕￡CMCT呆呆", wantedHash)
	if err != nil {
		t.Fatalf("expected search to resolve id, got error: %v", err)
	}
	if got != "888888" {
		t.Fatalf("expected id 888888, got %q", got)
	}
}

// TestResolveSearchMultiCandidatesByInfohash 多候选（拆词后搜出多条）时，
// infohash 验证必须锁定正确目标——用户担心的"搜出其他的"由这一级兜底。
func TestResolveSearchMultiCandidatesByInfohash(t *testing.T) {
	wantedInfo := bencodeInfo("GG")
	wantedHash := infoHashOf(wantedInfo)
	otherInfo := bencodeInfo("OTHER")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			fmt.Fprint(w, `<html><body><table class="torrents" id="torrenttable"><tr><td>
				<a title="Gattaca 1997 BluRay 1080p x264 AC3 2Audios-CMCT" href="details.php?id=888888&amp;hit=1">Gattaca</a>
			</td></tr><tr><td>
				<a title="Gattaca 1997 BluRay 1080p x264 AC3 2Audios-OTHER" href="details.php?id=999999&amp;hit=1">Gattaca</a>
			</td></tr></table></body></html>`)
		case "/download.php":
			w.Header().Set("Content-Type", "application/x-bittorrent")
			if r.URL.Query().Get("id") == "999999" {
				_, _ = w.Write(otherInfo)
			} else {
				_, _ = w.Write(wantedInfo)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	got, err := resolveTorrentIDBySearch(server.URL, "uid=1", "pk", "Gattaca 1997 BluRay 1080p x264 AC3 2Audios-CMCT", wantedHash)
	if err != nil {
		t.Fatalf("expected infohash verification to succeed, got error: %v", err)
	}
	if got != "888888" {
		t.Fatalf("expected infohash verification to pick 888888, got %q", got)
	}
}

// TestSearchTermsSplit 拆词函数：点号/全角￡等分隔，空词剔除。
// 注意 CMCT呆呆 无分隔符，整体是一个词——站点标题不含发布者词时由尾部缩减剔除。
func TestSearchTermsSplit(t *testing.T) {
	got := searchTerms("千钧一发.1997.1080p.国英双语.中英字幕￡CMCT呆呆")
	want := []string{"千钧一发", "1997", "1080p", "国英双语", "中英字幕", "CMCT呆呆"}
	if len(got) != len(want) {
		t.Fatalf("expected %d terms, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("term %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}
