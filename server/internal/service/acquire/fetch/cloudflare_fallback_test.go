package fetch

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pt-nexus/server/internal/platform/flaresolverr"
)

func TestIsCloudflareChallengeBody(t *testing.T) {
	challenge := []byte(`<!DOCTYPE html><html><head><title>Just a moment...</title></head><body></body></html>`)
	if !isCloudflareChallengeBody(challenge) {
		t.Fatalf("expected challenge page detected")
	}

	normal := []byte(`<!DOCTYPE html><html><head><title>OurBits</title></head><body><a href="download.php">dl</a></body></html>`)
	if isCloudflareChallengeBody(normal) {
		t.Fatalf("expected normal page not detected as challenge")
	}

	if isCloudflareChallengeBody(nil) {
		t.Fatalf("expected empty body not detected")
	}
}

func TestMergeCookieStrings(t *testing.T) {
	merged := mergeCookieStrings("uid=42; passhash=xyz", []flaresolverr.Cookie{
		{Name: "cf_clearance", Value: "abc"},
		{Name: "uid", Value: "99"},
	})
	for _, want := range []string{"uid=99", "passhash=xyz", "cf_clearance=abc"} {
		if !strings.Contains(merged, want) {
			t.Fatalf("merged cookie %q missing %q", merged, want)
		}
	}
	if strings.Count(merged, "uid") != 1 {
		t.Fatalf("expected uid overwritten once, got %q", merged)
	}
}

func TestFetchBinarySmartChallengeWithoutSolver(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<html><title>Just a moment...</title></html>"))
	}))
	defer server.Close()

	_, cookie, err := fetchBinarySmart(server.URL, "", "uid=1", 5*time.Second)
	if err == nil {
		t.Fatalf("expected error for challenge page")
	}
	if cookie != "uid=1" {
		t.Fatalf("expected cookie preserved, got %q", cookie)
	}
	if !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("expected original HTTP 403 error, got %v", err)
	}
}

func TestFetchBinarySmartNormalPage(t *testing.T) {
	body := []byte("d8:announce41:http://tracker.example/announcee")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()

	got, cookie, err := fetchBinarySmart(server.URL, "", "uid=1", 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("expected body preserved")
	}
	if cookie != "uid=1" {
		t.Fatalf("expected cookie preserved, got %q", cookie)
	}
}
