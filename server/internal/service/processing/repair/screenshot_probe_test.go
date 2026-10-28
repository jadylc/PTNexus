package repair

import "testing"

// TestIsAcceptableImageContentType 内容类型判定：image/* 与 octet-stream/空接受，html 判死。
func TestIsAcceptableImageContentType(t *testing.T) {
	accept := []string{"image/png", "image/jpeg", "image/webp", "", "application/octet-stream"}
	for _, ct := range accept {
		if !isAcceptableImageContentType(ct) {
			t.Fatalf("expected %q accepted", ct)
		}
	}
	reject := []string{"text/html", "text/plain", "application/json", "image"}
	for _, ct := range reject {
		if isAcceptableImageContentType(ct) {
			t.Fatalf("expected %q rejected", ct)
		}
	}
}

// TestImageProbeReferer 从图片直链推导图床根域 Referer。
func TestImageProbeReferer(t *testing.T) {
	cases := map[string]string{
		"https://img108.pixhost.to/images/12/34/abc.png": "https://img108.pixhost.to/",
		"http://i.example.com/a.jpg":                     "http://i.example.com/",
		"//cdn.example.com/b.png":                        "",
		"":                                               "",
	}
	for target, want := range cases {
		if got := imageProbeReferer(target); got != want {
			t.Fatalf("target %q: expected %q, got %q", target, want, got)
		}
	}
}
