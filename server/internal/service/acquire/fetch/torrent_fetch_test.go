package fetch

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildDetailURLNumericID(t *testing.T) {
	got := buildDetailURL("https://ptchdbits.co", "chdbits", "593793")
	want := "https://ptchdbits.co/details.php?id=593793"
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestBuildDetailURLAlreadyDetailsURL(t *testing.T) {
	got := buildDetailURL("https://ptchdbits.co", "chdbits", "details.php?id=593793&hit=1")
	want := "https://ptchdbits.co/details.php?id=593793&hit=1"
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestBuildDetailURLRousiUUIDUnchanged(t *testing.T) {
	uuid := "3f2a1b4c-5d6e-7f80-9a1b-2c3d4e5f6a7b"
	got := buildDetailURL("https://rousi.pro", "rousi", uuid)
	want := "https://rousi.pro/torrent/" + uuid
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestExtractDownloadIDNumeric(t *testing.T) {
	detailURL := "https://ptchdbits.co/details.php?id=593793&hit=1"
	got := extractDownloadID("chdbits", detailURL, "593793")
	if got != "593793" {
		t.Fatalf("expected 593793, got %s", got)
	}
}

func TestExtractDownloadIDNumericURLOnly(t *testing.T) {
	detailURL := "https://ptchdbits.co/details.php?id=593793&hit=1"
	got := extractDownloadID("chdbits", detailURL, "")
	if got != "593793" {
		t.Fatalf("expected 593793, got %s", got)
	}
}

func TestBuildDirectDownloadURLNumericWithPasskey(t *testing.T) {
	detailURL := "https://ptchdbits.co/details.php?id=593793&hit=1"
	got := buildDirectDownloadURL("https://ptchdbits.co", "chdbits", detailURL, "593793", "pk")
	want := "https://ptchdbits.co/download.php?id=593793&passkey=pk"
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestExtractDownloadCandidatesFromDetailNumeric(t *testing.T) {
	html := `<html><body><a href="download.php?id=593793&amp;passkey=pk">Download</a></body></html>`
	got := extractDownloadCandidatesFromDetail(html, "https://ptchdbits.co", "chdbits", "https://ptchdbits.co/details.php?id=593793")
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d: %#v", len(got), got)
	}
	if !strings.Contains(got[0], "id=593793") {
		t.Fatalf("expected candidate to contain numeric id, got %s", got[0])
	}
}

func TestDownloadTorrentForSourceNumericID(t *testing.T) {
	t.Setenv("PTNEXUS_DATA_DIR", t.TempDir())

	torrentBody := []byte("d8:announce40:http://tracker.example/announcee")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/details.php":
			_, _ = w.Write([]byte(`<html><body><a href="download.php?id=593793&passkey=pk">Download</a></body></html>`))
		case "/download.php":
			w.Header().Set("Content-Type", "application/x-bittorrent")
			_, _ = w.Write(torrentBody)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	sourceInfo := map[string]any{
		"base_url": server.URL,
		"site":     "chdbits",
		"nickname": "彩虹岛",
		"cookie":   "uid=1",
		"passkey":  "pk",
	}

	path, detailURL, body, err := DownloadTorrentForSource(sourceInfo, "593793")
	if err != nil {
		t.Fatalf("DownloadTorrentForSource failed: %v", err)
	}

	wantDetail := server.URL + "/details.php?id=593793"
	if detailURL != wantDetail {
		t.Fatalf("expected detail url %s, got %s", wantDetail, detailURL)
	}
	if path == "" {
		t.Fatalf("expected torrent path")
	}
	if string(body) != string(torrentBody) {
		t.Fatalf("expected torrent body preserved, got %q", string(body))
	}
}
