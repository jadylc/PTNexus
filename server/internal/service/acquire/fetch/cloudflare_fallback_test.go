package fetch

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pt-nexus/server/internal/config"
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

	_, cookie, _, err := fetchBinarySmart(server.URL, "", "uid=1", "", 5*time.Second)
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

	got, cookie, _, err := fetchBinarySmart(server.URL, "", "uid=1", "", 5*time.Second)
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

// TestHTTPStatusErrorCarriesFlareSolverrFailure 验证 CF 挑战 + FlareSolverr 求解失败时，
// 错误信息透出 FlareSolverr 的失败原因（连接被拒/地址错误），而不是只剩原始 403。
// 这是用户实际踩到的坑：启用了 FlareSolverr 仍失败，但错误只显示 HTTP 403，
// 无从判断是服务没起还是地址填错。
func TestHTTPStatusErrorCarriesFlareSolverrFailure(t *testing.T) {
	// 启用一个指向不可达端口的 FlareSolverr 配置，让 Solve 必然失败。
	if err := flaresolverr.Install(config.FlareSolverrConfig{
		Enabled:        true,
		APIURL:         "http://127.0.0.1:1", // 保留端口 1，必然连接失败
		TimeoutSeconds: 2,
	}); err != nil {
		t.Fatalf("install flaresolverr config failed: %v", err)
	}
	t.Cleanup(func() {
		// 恢复为禁用状态，避免影响其他测试。
		_ = flaresolverr.Install(config.DefaultFlareSolverrConfig())
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<html><title>Just a moment...</title></html>"))
	}))
	defer server.Close()

	if _, _, _, err := fetchBinarySmart(server.URL, "", "uid=1", "", 3*time.Second); err == nil {
		t.Fatalf("expected error for challenge page with failing FlareSolverr")
	} else {
		message := err.Error()
		if !strings.Contains(message, "FlareSolverr") {
			t.Fatalf("expected FlareSolverr failure detail in error, got %q", message)
		}
		if !strings.Contains(message, "127.0.0.1:1") {
			t.Fatalf("expected unreachable endpoint echoed in error, got %q", message)
		}
	}
}

// TestFetchBinarySmartSolveTimeoutWithCookieRetriesWithSolverUA 完整复现我堡（OurBits）场景：
// FlareSolverr 求解超时返回空 body 但带 cf_clearance 与 userAgent——
// 必须用求解返回的 UA 直连重试并成功，而不是报"未返回页面内容"失败。
func TestFetchBinarySmartSolveTimeoutWithCookieRetriesWithSolverUA(t *testing.T) {
	t.Setenv("PTNEXUS_DATA_DIR", t.TempDir())

	solverUA := "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0"
	pageBody := []byte("d8:announce41:http://tracker.example/announcee")

	// 模拟 FlareSolverr：首次调用（求解）返回空 body + cookie + userAgent；
	// 二次调用（用求解 UA 的直连重试）返回真实页面。
	solveCalled := false
	var solver *httptest.Server
	solver = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !solveCalled {
			solveCalled = true
			_, _ = w.Write([]byte(`{"status":"ok","message":"","solution":{"url":"http://target.example/x","status":200,"body":"","userAgent":"` + solverUA + `","cookies":[{"name":"cf_clearance","value":"tok","domain":"target.example","path":"/"}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok","solution":{"url":"http://target.example/x","status":200,"body":"","userAgent":"` + solverUA + `","cookies":[]}}`))
	}))
	defer solver.Close()
	defer func() { solver = nil }()

	// 目标站点：校验 UA 与 Cookie；带求解 UA + cf_clearance 才返回真实内容，否则 CF 挑战页。
	var seenUA, seenCookie string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenUA = r.Header.Get("User-Agent")
		seenCookie = r.Header.Get("Cookie")
		if strings.Contains(seenCookie, "cf_clearance=tok") && seenUA == solverUA {
			_, _ = w.Write(pageBody)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<html><title>Just a moment...</title></html>"))
	}))
	defer target.Close()

	_ = solver
	if err := flaresolverr.Install(config.FlareSolverrConfig{
		Enabled:        true,
		APIURL:         solver.URL,
		TimeoutSeconds: 5,
	}); err != nil {
		t.Fatalf("install flaresolverr config failed: %v", err)
	}
	t.Cleanup(func() {
		_ = flaresolverr.Install(config.DefaultFlareSolverrConfig())
	})

	got, mergedCookie, usedUA, err := fetchBinarySmart(target.URL+"/page", "", "uid=1", "", 5*time.Second)
	if err != nil {
		t.Fatalf("expected solve-timeout cookie retry to succeed, got error: %v", err)
	}
	if string(got) != string(pageBody) {
		t.Fatalf("expected real page body after retry, got %q", string(got))
	}
	if !strings.Contains(mergedCookie, "cf_clearance=tok") {
		t.Fatalf("expected merged cookie to contain cf_clearance, got %q", mergedCookie)
	}
	if usedUA != solverUA {
		t.Fatalf("expected solver UA reused for subsequent requests, got %q", usedUA)
	}
	if seenUA != solverUA {
		t.Fatalf("expected retry request to carry solver UA, got %q", seenUA)
	}
	if !strings.Contains(seenCookie, "cf_clearance=tok") {
		t.Fatalf("expected retry request to carry merged cookie, got %q", seenCookie)
	}
}
