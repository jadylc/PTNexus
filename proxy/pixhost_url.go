package main

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Pixhost 的图片直链域名是 img<N>.pixhost.to，其中 N 会由图床单方面轮换
// （历史上已经从 img1 换到 img2，又换到 img3）。
// 盒子端代理与 server 是两个独立 Go module，无法共享 repair 包的实现，
// 因此这里保留一份等价的最小实现：真实分片编号永远优先取自图床自己的响应或 show 页，
// 只有在两者都拿不到时才退回历史兜底编号。
const (
	pixhostImagesPath    = "/images/"
	pixhostDomainSuffix  = ".pixhost.to"
	pixhostFallbackShard = "img2"
)

var (
	// reProxyPixhostDirectURL 判定一个 URL 是否已经是合法直链（接受任意分片编号）。
	reProxyPixhostDirectURL = regexp.MustCompile(`^https://img[0-9]+\.pixhost\.to/images/\d+/[^/]+\.(jpg|jpeg|png|gif|webp)$`)
	// reProxyPixhostAnyDirect 用于在上传响应体里扫描任意分片编号的直链。
	reProxyPixhostAnyDirect = regexp.MustCompile(`https?://img[0-9]+\.pixhost\.to/images/\d+/[^"'\\<>\s]+\.(?:jpg|jpeg|png|gif|webp)`)
	// reProxyPixhostShard 从 URL 里取出 img<N> 分片编号。
	reProxyPixhostShard = regexp.MustCompile(`(?i)^https?://(img[0-9]+)\.pixhost\.to/`)
	// reProxyPixhostPageURL 匹配 show/th 展示页地址，含带分片域名与不带分片域名两种写法。
	reProxyPixhostPageURL = regexp.MustCompile(`(?i)^https?://(?:img[0-9]+\.)?pixhost\.to/(?:show|th)/`)
	// reProxyPixhostOgImage 与 reProxyPixhostImageTag 用于从 show 页解析真实直链。
	reProxyPixhostOgImage  = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:image["'][^>]*content=["']([^"']+)["']`)
	reProxyPixhostImageTag = regexp.MustCompile(`(?is)<img[^>]+id=["']image["'][^>]*src=["']([^"']+)["']`)
)

// pixhostShardOf 取出 URL 自带的分片域名（img<N>）；取不到时返回历史兜底编号。
func pixhostShardOf(rawURL string) string {
	if match := reProxyPixhostShard.FindStringSubmatch(strings.TrimSpace(rawURL)); len(match) == 2 {
		return strings.ToLower(match[1])
	}
	return pixhostFallbackShard
}

// pixhostDirectBaseOf 返回 URL 对应的直链域名根，例如 https://img3.pixhost.to/images/。
func pixhostDirectBaseOf(rawURL string) string {
	return "https://" + pixhostShardOf(rawURL) + pixhostDomainSuffix + pixhostImagesPath
}

// extractPixhostDirectURL 从图床响应体里扫出真实直链；找不到返回空串。
// 不绑定具体字段名（image_url / display_url 都出现过），避免图床改字段后再次退化成猜域名。
func extractPixhostDirectURL(raw []byte) string {
	match := reProxyPixhostAnyDirect.FindString(string(raw))
	if match == "" {
		return ""
	}
	normalized := normalizeProxyPixhostDirectURL(match)
	if normalized == "" {
		return ""
	}
	return normalized
}

// normalizeProxyPixhostDirectURL 把任意 img<N> 直链统一为 https 形态；非直链返回空串。
func normalizeProxyPixhostDirectURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "http://img") {
		trimmed = "https" + strings.TrimPrefix(trimmed, "http")
	}
	if !reProxyPixhostDirectURL.MatchString(trimmed) {
		return ""
	}
	return trimmed
}

// showPageToPixhostDirectURL 由 show/th 页面地址推导出直链（沿用输入自带的分片编号）。
// 注意 show/th 页地址有两种写法：https://pixhost.to/show/... 与带分片域名的
// https://img9.pixhost.to/th/...，必须都能处理，否则带分片域名的地址会直接推导失败。
func showPageToPixhostDirectURL(showURL string) string {
	trimmed := strings.TrimSpace(showURL)
	if trimmed == "" {
		return ""
	}
	if direct := normalizeProxyPixhostDirectURL(trimmed); direct != "" {
		return direct
	}
	if !reProxyPixhostPageURL.MatchString(trimmed) {
		return ""
	}
	base := pixhostDirectBaseOf(trimmed)
	direct := reProxyPixhostPageURL.ReplaceAllStringFunc(trimmed, func(string) string { return base })
	return normalizeProxyPixhostDirectURL(direct)
}

// resolvePixhostDirectURL 按「上传响应直链 > show 页解析 > 字符串推导」的优先级挑出可显示直链。
// 参数：responseDirectURL 为上传响应带回的直链（可为空）；showURL 为上传响应的展示页地址。
// 返回：可用直链；全部失败时回退 showURL。
func resolvePixhostDirectURL(responseDirectURL string, showURL string) string {
	showURL = strings.TrimSpace(showURL)

	if direct := normalizeProxyPixhostDirectURL(responseDirectURL); direct != "" {
		return direct
	}
	if pageDirect := fetchPixhostDirectFromShowPage(showURL); pageDirect != "" {
		return pageDirect
	}
	if derived := showPageToPixhostDirectURL(showURL); derived != "" {
		return derived
	}
	return showURL
}

// fetchPixhostDirectFromShowPage 抓取 show 页面，从 og:image / <img id="image"> 中解析真实直链。
// 网络失败或页面结构变化时返回空串，由调用方继续回退。
func fetchPixhostDirectFromShowPage(showURL string) string {
	trimmed := strings.TrimSpace(showURL)
	if trimmed == "" {
		return ""
	}
	req, err := http.NewRequest(http.MethodGet, trimmed, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return ""
	}
	page := string(body)
	for _, pattern := range []*regexp.Regexp{reProxyPixhostOgImage, reProxyPixhostImageTag} {
		match := pattern.FindStringSubmatch(page)
		if len(match) < 2 {
			continue
		}
		if direct := normalizeProxyPixhostDirectURL(match[1]); direct != "" {
			return direct
		}
		if direct := showPageToPixhostDirectURL(match[1]); direct != "" {
			return direct
		}
	}
	return ""
}
