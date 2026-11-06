package fetch

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMatchColaptSearchEntry 搜索响应定位：同名匹配优先，规范化忽略点/下划线差异。
func TestMatchColaptSearchEntry(t *testing.T) {
	body := `{"code":0,"data":{"data":[
		{"id":15321,"name":"我要你的性.I.Want.Your.Sex.2026.UHD.BluRay.2160p.x265.mUHD-FRDS","smallDescr":"副标题A","size":"17172493101","category":"401","imdb":"tt32332915"},
		{"id":15150,"name":"Memoirs of a Geisha 2005 UHD BluRay 2160p x265 mUHD-FRDS","smallDescr":"副标题B","size":"25014793080","category":"401","imdb":"tt0397535"}
	],"pageNumber":1,"totalPages":2}}`

	entry, err := matchColaptSearchEntry(body, "Memoirs of a Geisha 2005 UHD BluRay 2160p x265 mUHD-FRDS")
	if err != nil {
		t.Fatalf("match failed: %v", err)
	}
	if entry["name"] != "Memoirs of a Geisha 2005 UHD BluRay 2160p x265 mUHD-FRDS" {
		t.Errorf("wrong entry: %#v", entry)
	}
	if entry["imdb"] != "tt0397535" || entry["category"] != "401" {
		t.Errorf("unexpected fields: %#v", entry)
	}

	if _, err := matchColaptSearchEntry(body, "Totally.Missing.Name"); err == nil {
		t.Fatalf("expected no-match error")
	}
}

// TestMatchColaptSearchEntryParsesLabels labelsNew 必须进入 entry（此前漏取导致
// 提取无标签、触发不必要的修复流程）。
func TestMatchColaptSearchEntryParsesLabels(t *testing.T) {
	body := `{"code":0,"data":{"data":[
		{"id":1,"name":"Some.Seed.2024.1080p-WEB","smallDescr":"","size":"100","category":"401","labelsNew":["中字","国配","HDR10+"]}
	]}}`
	entry, err := matchColaptSearchEntry(body, "Some.Seed.2024.1080p-WEB")
	if err != nil {
		t.Fatalf("match failed: %v", err)
	}
	if entry["labels"] != "中字,国配,HDR10+" {
		t.Errorf("labels = %q, want 中字,国配,HDR10+", entry["labels"])
	}

	html := colaptEntryToDetailHTML(entry)
	if !strings.Contains(html, "<td class=\"rowhead\">标签</td>") || !strings.Contains(html, "<span>中字</span>") {
		t.Errorf("synthesized html missing tags row: %s", html)
	}
}

// TestColaptEntryToDetailHTML 合成伪详情页应包含公共提取器需要的全部锚点。
func TestColaptEntryToDetailHTML(t *testing.T) {
	html := colaptEntryToDetailHTML(map[string]string{
		"name":       "Some.Movie.2024.2160p.WEB-DL-GROUP",
		"smallDescr": "示例副标题",
		"category":   "401",
		"imdb":       "tt123",
	})
	for _, anchor := range []string{
		`<h1 id="top">Some.Movie.2024.2160p.WEB-DL-GROUP</h1>`,
		"副标题", "示例副标题",
		"基本信息", "类型:", "电影-Movies", "制作组:", "GROUP",
		"IMDb信息", "https://www.imdb.com/title/tt123/",
	} {
		if !strings.Contains(html, anchor) {
			t.Errorf("html missing %q: %s", anchor, html)
		}
	}
}

// TestMapColaptCategory 分类映射覆盖 401-409 与未知值。
func TestMapColaptCategory(t *testing.T) {
	want := map[string]string{
		"401": "电影-Movies", "402": "电视剧-TV Series", "403": "综艺-TV Shows",
		"404": "纪录片-Documentaries", "405": "动漫-Animes", "406": "演唱会-Music Videos",
		"407": "体育节目-Sports", "408": "音乐-Music", "409": "其他-Other",
	}
	for code, expect := range want {
		if got := mapColaptCategory(code); got != expect {
			t.Errorf("mapColaptCategory(%q) = %q, want %q", code, got, expect)
		}
	}
	if mapColaptCategory("999") != "" {
		t.Errorf("unknown category should map to empty")
	}
}

// TestColaptAPIBase 主域到 API 域的换算。
func TestColaptAPIBase(t *testing.T) {
	cases := map[string]string{
		"https://www.colapt.com":  "https://api.colapt.com",
		"https://www.colapt.com/": "https://api.colapt.com",
		"www.colapt.com":          "https://api.colapt.com",
		"colapt.com":              "https://api.colapt.com",
		"https://api.colapt.com":  "https://api.colapt.com",
	}
	for in, expect := range cases {
		if got := colaptAPIBase(in); got != expect {
			t.Errorf("colaptAPIBase(%q) = %q, want %q", in, got, expect)
		}
	}
}

// TestExtractColaptTorrentID 数字种子 id 提取（详情链接 / 显式 id）。
func TestExtractColaptTorrentID(t *testing.T) {
	cases := []struct {
		detailURL string
		torrentID string
		want      string
	}{
		{"https://www.colapt.com/torrents/14522", "", "14522"},
		{"https://www.colapt.com/downloads/224d00b61ca5e8a49441bedc1d9ffa8ac95bd334321e4178ab11a3aba213320f/14522?exp=1&sig=x", "", "14522"},
		{"https://www.colapt.com/media/rc983", "14522", "14522"},
		{"https://www.colapt.com/media/rc983", "media/rc983", ""},
		{"", "14522", "14522"},
	}
	for _, tc := range cases {
		if got := extractColaptTorrentID(tc.detailURL, tc.torrentID); got != tc.want {
			t.Errorf("extractColaptTorrentID(%q,%q) = %q, want %q", tc.detailURL, tc.torrentID, got, tc.want)
		}
	}
}

// TestResolveColaptTorrentIDExactNameMatch 搜索命中同名条目时直接返回其 id。
func TestResolveColaptTorrentIDExactNameMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/torrent/search" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"data": []map[string]any{
					{"id": 11972, "name": "Renegade Immortal S01E165 2023 2160p TX WEB-DL H.265 AAC2.0-COLAWEB"},
					{"id": 11971, "name": "Other.Seed.2023.1080p-WEB"},
				},
			},
		})
	}))
	defer server.Close()

	got, err := resolveColaptTorrentID(server.URL, "test-key", "", "Renegade Immortal S01E165 2023 2160p TX WEB-DL H.265 AAC2.0-COLAWEB", "deadbeef")
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if got != "11972" {
		t.Fatalf("expected 11972, got %q", got)
	}
}

// TestResolveColaptTorrentIDSingleCandidate 唯一候选直接采用（名称不完全一致场景）。
func TestResolveColaptTorrentIDSingleCandidate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"data": []map[string]any{
					{"id": 5370, "name": "Awkwafina The Unlikely Cook 2026 S01E02 2160p ATVP WEB-DL DDP5.1 Atmos HDR10P H.265-DepWeb"},
				},
			},
		})
	}))
	defer server.Close()

	got, err := resolveColaptTorrentID(server.URL, "test-key", "", "Awkwafina The Unlikely Cook 2026 S01E02 2160p ATVP WEB-DL DDP5.1 Atmos HDR10P H.265-DepWeb", "cafebabe")
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if got != "5370" {
		t.Fatalf("expected 5370, got %q", got)
	}
}

// TestResolveColaptTorrentIDEmptyResult 搜索无结果时报错清晰。
func TestResolveColaptTorrentIDEmptyResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"data": []any{}}})
	}))
	defer server.Close()

	_, err := resolveColaptTorrentID(server.URL, "test-key", "", "Missing.Seed.2026", "aa")
	if err == nil || !strings.Contains(err.Error(), "搜索结果为空") {
		t.Fatalf("expected empty result error, got %v", err)
	}
}

// TestColaptSearchEntriesStringCode 业务码为字符串"0"也能解析，且空 id 条目被跳过。
func TestColaptSearchEntriesStringCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"0","data":{"data":[{"id":123,"name":"A.B-C"},{"id":null,"name":"skip"}]}}`))
	}))
	defer server.Close()

	entries, err := colaptSearchEntries(server.URL, "k", "A.B")
	if err != nil {
		t.Fatalf("entries failed: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "123" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
}
