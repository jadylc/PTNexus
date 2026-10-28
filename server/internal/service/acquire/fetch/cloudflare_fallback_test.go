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

// TestHTTPStatusErrorCompactMessage 验证 HTTP 错误文本不再携带整页 HTML：
// CHD 404 场景（GBK 乱码 HTML）此前把整页内容写进日志/前端/MySQL（Error 1366）。
func TestHTTPStatusErrorCompactMessage(t *testing.T) {
	raw := `<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd">
<html xmlns="http://www.w3.org/1999/xhtml"><head><meta http-equiv="Content-Type" content="text/html; charset=utf-8" />
<title>404页面</title></head><body><div class="error-page"><h3><strong>404</strong>您访问的页面不存在</h3></div></body></html>`
	err := &httpStatusError{status: 404, body: []byte(raw)}
	message := err.Error()

	if !strings.Contains(message, "HTTP 404") {
		t.Fatalf("expected status prefix, got %q", message)
	}
	if len(message) > 200 {
		t.Fatalf("expected compact message, got %d chars", len(message))
	}
	if strings.Contains(message, "<!DOCTYPE") || strings.Contains(message, "<html") {
		t.Fatalf("expected no raw HTML, got %q", message)
	}
	if !strings.Contains(message, "404页面") {
		t.Fatalf("expected readable title in message, got %q", message)
	}
}

func TestCompactHTTPErrorBodyNonHTML(t *testing.T) {
	if got := compactHTTPErrorBody([]byte("plain text error")); got != "plain text error" {
		t.Fatalf("expected passthrough for non-HTML, got %q", got)
	}
	if got := compactHTTPErrorBody(nil); got != "空响应" {
		t.Fatalf("expected 空响应, got %q", got)
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
