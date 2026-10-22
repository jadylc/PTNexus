package main

import "testing"

// TestResolvePixhostDirectURLPrefersResponse 校验盒子端截图直链优先采用图床响应里的
// img<N> 域名，而不是写死的分片编号——图床轮换分片时写死编号会导致新截图全部裂图。
func TestResolvePixhostDirectURLPrefersResponse(t *testing.T) {
	showURL := "https://pixhost.to/show/491/1510139525_s1_00h12m30s.jpg"
	responseDirect := "https://img3.pixhost.to/images/491/1510139525_s1_00h12m30s.jpg"

	if got := resolvePixhostDirectURL(responseDirect, showURL); got != responseDirect {
		t.Fatalf("应优先使用响应直链, got %q", got)
	}
}

// TestShowPageToPixhostDirectURL 校验字符串推导分支保留输入自带的分片编号。
func TestShowPageToPixhostDirectURL(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "th 页沿用 img9 分片",
			input: "https://img9.pixhost.to/th/491/1510139525_a.jpg",
			want:  "https://img9.pixhost.to/images/491/1510139525_a.jpg",
		},
		{
			name:  "show 页无分片信息时回退兜底",
			input: "https://pixhost.to/show/491/1510139525_a.jpg",
			want:  "https://img2.pixhost.to/images/491/1510139525_a.jpg",
		},
		{name: "非 pixhost 地址", input: "https://example.com/a.jpg", want: ""},
		{name: "空串", input: "  ", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := showPageToPixhostDirectURL(tc.input); got != tc.want {
				t.Fatalf("showPageToPixhostDirectURL(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestExtractPixhostDirectURLFromResponse 校验扫描逻辑与字段名解耦。
func TestExtractPixhostDirectURLFromResponse(t *testing.T) {
	body := []byte(`{"status":"ok","data":{"show_url":"https://pixhost.to/show/491/1510139525_a.jpg","image_url":"https://img6.pixhost.to/images/491/1510139525_a.jpg"}}`)
	if got := extractPixhostDirectURL(body); got != "https://img6.pixhost.to/images/491/1510139525_a.jpg" {
		t.Fatalf("extractPixhostDirectURL = %q", got)
	}
	if got := extractPixhostDirectURL([]byte(`{"show_url":"https://pixhost.to/show/491/a.jpg"}`)); got != "" {
		t.Fatalf("响应无直链时应返回空串, got %q", got)
	}
}
