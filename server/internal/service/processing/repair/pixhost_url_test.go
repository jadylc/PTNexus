package repair

import "testing"

// TestPixhostShowToDirectURLPreservesShard 保证图床轮换 CDN 分片域名（img1/img2/img3...）时
// 直链推导不会再写死成某个固定编号，这是截图显示为裂图的根因。
func TestPixhostShowToDirectURLPreservesShard(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "show 页沿用自身分片编号",
			input: "https://img3.pixhost.to/th/491/1510139525_s1_00h12m30s.jpg",
			want:  "https://img3.pixhost.to/images/491/1510139525_s1_00h12m30s.jpg",
		},
		{
			name:  "已经是直链时原样返回",
			input: "https://img7.pixhost.to/images/491/1510139525_s1_00h12m30s.jpg",
			want:  "https://img7.pixhost.to/images/491/1510139525_s1_00h12m30s.jpg",
		},
		{
			name:  "show 页去掉缩略图后缀",
			input: "https://pixhost.to/show/491/1510139525_s1_00h12m30s_abc.jpg",
			want:  "https://img2.pixhost.to/images/491/1510139525_s1_00h12m30s.jpg",
		},
		{
			name:  "无分片信息时回退历史兜底编号",
			input: "https://pixhost.to/show/491/1510139525_s1_00h12m30s.jpg",
			want:  "https://img2.pixhost.to/images/491/1510139525_s1_00h12m30s.jpg",
		},
		{name: "空输入", input: "   ", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PixhostShowToDirectURL(tc.input); got != tc.want {
				t.Fatalf("PixhostShowToDirectURL(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestNormalizePixhostDirectHostKeepsAnyShard 校验 img<N> 域名一律保留，不再被强行改写。
func TestNormalizePixhostDirectHostKeepsAnyShard(t *testing.T) {
	for _, shard := range []string{"img1", "img2", "img3", "img42"} {
		input := "https://" + shard + ".pixhost.to/images/491/1510139525_a.jpg"
		if got := NormalizePixhostDirectHost(input); got != input {
			t.Fatalf("NormalizePixhostDirectHost(%q) = %q, want %q", input, got, input)
		}
	}

	if got := NormalizePixhostDirectHost("https://pixhost.to/images/491/1510139525_a.jpg"); got != "https://img2.pixhost.to/images/491/1510139525_a.jpg" {
		t.Fatalf("缺少分片编号时应回退兜底域名, got %q", got)
	}
	if got := NormalizePixhostDirectHost("https://example.com/foo.png"); got != "" {
		t.Fatalf("非 pixhost URL 应返回空串, got %q", got)
	}
}

// TestExtractPixhostDirectURLFromResponse 校验真实直链优先取自上传响应，且与字段名解耦。
func TestExtractPixhostDirectURLFromResponse(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "data.image_url 形态",
			body: `{"status":"ok","data":{"show_url":"https://pixhost.to/show/491/1510139525_a.jpg","image_url":"https://img3.pixhost.to/images/491/1510139525_a.jpg"}}`,
			want: "https://img3.pixhost.to/images/491/1510139525_a.jpg",
		},
		{
			name: "顶层直链字段形态",
			body: `{"show_url":"https://pixhost.to/show/491/1510139525_a.jpg","direct_url":"https://img5.pixhost.to/images/491/1510139525_a.jpg"}`,
			want: "https://img5.pixhost.to/images/491/1510139525_a.jpg",
		},
		{
			name: "非 JSON 响应也能扫出直链",
			body: `<img id="image" src="https://img4.pixhost.to/images/491/1510139525_a.jpg">`,
			want: "https://img4.pixhost.to/images/491/1510139525_a.jpg",
		},
		{
			name: "响应里没有直链时返回空串",
			body: `{"show_url":"https://pixhost.to/show/491/1510139525_a.jpg"}`,
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractPixhostDirectURL([]byte(tc.body)); got != tc.want {
				t.Fatalf("ExtractPixhostDirectURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPixhostShardOf 校验分片编号提取与兜底。
func TestPixhostShardOf(t *testing.T) {
	if got := pixhostShardOf("https://img3.pixhost.to/th/491/a.jpg"); got != "img3" {
		t.Fatalf("pixhostShardOf = %q, want img3", got)
	}
	if got := pixhostShardOf("https://pixhost.to/show/491/a.jpg"); got != pixhostFallbackShard {
		t.Fatalf("pixhostShardOf = %q, want %s", got, pixhostFallbackShard)
	}
	if got := pixhostShardOf("://bad url"); got != pixhostFallbackShard {
		t.Fatalf("非法 URL 应回退兜底编号, got %q", got)
	}
}
