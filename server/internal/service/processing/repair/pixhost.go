package repair

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
)

const maxPosterBytes = 25 * 1024 * 1024
const posterTransferLogModule = "迁移-海报转存"
const posterTransferDownloadRetry = 2

var (
	rePixhostDirect      = regexp.MustCompile(`(\d+)/([^/]+\.(?:jpg|jpeg|png|gif|webp))`)
	rePixhostOgImage     = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:image["'][^>]*content=["']([^"']+)["']`)
	rePixhostImageTag    = regexp.MustCompile(`(?is)<img[^>]+id=["']image["'][^>]*src=["']([^"']+)["']`)
	rePixhostThumbSuffix = regexp.MustCompile(`_[^.]{1,3}\.(jpg|jpeg|png|gif|webp)$`)
	// 图床 CDN 分片域名是 img<N>.pixhost.to，N 会随时间轮换（img1 -> img2 -> img3 ...）。
	// 这里只校验“任意编号 + /images/ 路径”的形态，绝不写死具体编号：写死后每次图床轮换
	// 都会让新上传的图片全部打不开（历史上已经因此从 img1 修到 img2 一次）。
	rePixhostDirectURL = regexp.MustCompile(`^https://img[0-9]+\.pixhost\.to/images/\d+/[^/]+\.(jpg|jpeg|png|gif|webp)$`)
	// rePixhostShardHost 用于从 img<N>.pixhost.to 里取回图床实际使用的分片编号。
	rePixhostShardHost = regexp.MustCompile(`^img([0-9]+)\.pixhost\.to$`)
	// rePixhostAnyDirect 在响应体/页面里扫描任意分片编号的直链。
	rePixhostAnyDirect = regexp.MustCompile(`https?://img[0-9]+\.pixhost\.to/images/\d+/[^"'\\<>\s]+\.(?:jpg|jpeg|png|gif|webp)`)

	posterTransferProxyPrefixes = []string{
		"http://pt-nexus-proxy.sqing33.dpdns.org/",
		"http://pt-nexus-proxy.1395251710.workers.dev/",
	}
)

// pixhostFallbackShard 是在完全拿不到真实分片编号时的历史兜底域名。
// 只作为最后手段使用；正确来源永远是上传响应或 show 页里的 img<N> 直链。
const pixhostFallbackShard = "img2"

// NormalizePosterBBCode 规范化海报字段，统一输出为单个 [img]...[/img]。
func NormalizePosterBBCode(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	urls := ExtractImageURLsFromText(trimmed)
	if len(urls) == 0 {
		return trimmed
	}
	primary := strings.TrimSpace(urls[0])
	if primary == "" {
		return trimmed
	}

	normalized := NormalizePosterURL(primary)
	if normalized == "" {
		normalized = primary
	}
	return "[img]" + normalized + "[/img]"
}

// NormalizePosterURL 对海报 URL 做直链修复，并尽量转存到 Pixhost。
func NormalizePosterURL(raw string) string {
	url := strings.TrimSpace(raw)
	if url == "" {
		return ""
	}

	lower := strings.ToLower(url)
	if strings.Contains(lower, "pixhost.to") {
		if resolved, err := ResolvePixhostImageURL(url); err == nil && strings.TrimSpace(resolved) != "" {
			logx.Infof(posterTransferLogModule, "海报URL已是Pixhost，直链解析成功 source=%s resolved=%s", CompactLogText(url, 160), CompactLogText(resolved, 160))
			return strings.TrimSpace(resolved)
		} else if err != nil {
			logx.Warnf(posterTransferLogModule, "海报URL已是Pixhost但直链解析失败 source=%s err=%v", CompactLogText(url, 160), err)
		}
		if direct := NormalizePixhostDirectHost(url); direct != "" {
			logx.Infof(posterTransferLogModule, "海报URL已是Pixhost，域名规范化完成 source=%s normalized=%s", CompactLogText(url, 160), CompactLogText(direct, 160))
			return direct
		}
		logx.Warnf(posterTransferLogModule, "海报URL已是Pixhost但无法规范化，保留原始URL source=%s", CompactLogText(url, 160))
		return url
	}

	transferred, err := TransferRemoteImageToPixhost(url)
	if err != nil || strings.TrimSpace(transferred) == "" {
		if err != nil {
			logx.Warnf(posterTransferLogModule, "海报转存Pixhost失败，回退原始URL source=%s err=%v", CompactLogText(url, 160), err)
		} else {
			logx.Warnf(posterTransferLogModule, "海报转存Pixhost失败，回退原始URL source=%s err=empty transfer result", CompactLogText(url, 160))
		}
		return url
	}
	logx.Infof(posterTransferLogModule, "海报转存Pixhost成功 source=%s target=%s", CompactLogText(url, 160), CompactLogText(transferred, 160))
	return strings.TrimSpace(transferred)
}

// TransferRemoteImageToPixhost 将远程图片下载后上传到 Pixhost，再返回可用直链。
func TransferRemoteImageToPixhost(imageURL string) (string, error) {
	trimmed := strings.TrimSpace(imageURL)
	if trimmed == "" {
		return "", fmt.Errorf("empty image url")
	}

	candidates := buildPosterDownloadCandidates(trimmed)
	logx.Infof(posterTransferLogModule, "开始海报转存 source=%s candidates=%d", CompactLogText(trimmed, 160), len(candidates))
	errMsgs := make([]string, 0, len(candidates)*posterTransferDownloadRetry)

	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		for attempt := 1; attempt <= posterTransferDownloadRetry; attempt++ {
			data, contentType, downloadErr := downloadPosterImage(candidate)
			if downloadErr != nil {
				errMsgs = append(errMsgs, fmt.Sprintf("下载失败 candidate=%s attempt=%d err=%v", CompactLogText(candidate, 120), attempt, downloadErr))
				logx.Warnf(posterTransferLogModule, "下载海报失败 candidate=%s attempt=%d/%d err=%v", CompactLogText(candidate, 160), attempt, posterTransferDownloadRetry, downloadErr)
				if attempt < posterTransferDownloadRetry {
					time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
				}
				continue
			}

			logx.Infof(
				posterTransferLogModule,
				"下载海报成功 candidate=%s attempt=%d bytes=%d content_type=%s",
				CompactLogText(candidate, 160),
				attempt,
				len(data),
				contentType,
			)

			tmpPath, tmpErr := writePosterTempFile(data, contentType)
			if tmpErr != nil {
				errMsgs = append(errMsgs, fmt.Sprintf("落盘失败 candidate=%s err=%v", CompactLogText(candidate, 120), tmpErr))
				logx.Warnf(posterTransferLogModule, "海报临时文件写入失败 candidate=%s err=%v", CompactLogText(candidate, 160), tmpErr)
				continue
			}

			showURL, uploadErr := UploadImageToPixhost(tmpPath)
			_ = os.Remove(tmpPath)
			if uploadErr != nil {
				errMsgs = append(errMsgs, fmt.Sprintf("上传失败 candidate=%s attempt=%d err=%v", CompactLogText(candidate, 120), attempt, uploadErr))
				logx.Warnf(posterTransferLogModule, "上传海报到Pixhost失败 candidate=%s attempt=%d/%d err=%v", CompactLogText(candidate, 160), attempt, posterTransferDownloadRetry, uploadErr)
				if attempt < posterTransferDownloadRetry {
					time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
				}
				continue
			}

			if resolved, resolveErr := ResolvePixhostImageURL(showURL); resolveErr == nil && strings.TrimSpace(resolved) != "" {
				logx.Infof(posterTransferLogModule, "Pixhost直链解析成功 show_url=%s resolved=%s", CompactLogText(showURL, 160), CompactLogText(resolved, 160))
				return strings.TrimSpace(resolved), nil
			} else if resolveErr != nil {
				logx.Warnf(posterTransferLogModule, "Pixhost直链解析失败 show_url=%s err=%v", CompactLogText(showURL, 160), resolveErr)
			}

			if direct := NormalizePixhostDirectHost(showURL); direct != "" {
				logx.Infof(posterTransferLogModule, "Pixhost直链解析回退成功 show_url=%s direct=%s", CompactLogText(showURL, 160), CompactLogText(direct, 160))
				return direct, nil
			}
			base := strings.TrimSpace(showURL)
			if base == "" {
				errMsgs = append(errMsgs, fmt.Sprintf("Pixhost返回空URL candidate=%s", CompactLogText(candidate, 120)))
				logx.Warnf(posterTransferLogModule, "Pixhost上传返回空URL candidate=%s", CompactLogText(candidate, 160))
				if attempt < posterTransferDownloadRetry {
					time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
				}
				continue
			}
			logx.Warnf(posterTransferLogModule, "Pixhost返回show_url但未解析为直链，回退show_url show_url=%s", CompactLogText(base, 160))
			return base, nil
		}
	}

	if len(errMsgs) == 0 {
		return "", fmt.Errorf("poster transfer failed without details")
	}
	return "", fmt.Errorf(strings.Join(errMsgs, " | "))
}

func buildPosterDownloadCandidates(imageURL string) []string {
	trimmed := strings.TrimSpace(imageURL)
	if trimmed == "" {
		return []string{}
	}

	candidates := make([]string, 0, 1+len(posterTransferProxyPrefixes))
	seen := map[string]struct{}{}
	appendCandidate := func(url string) {
		item := strings.TrimSpace(url)
		if item == "" {
			return
		}
		if _, exists := seen[item]; exists {
			return
		}
		seen[item] = struct{}{}
		candidates = append(candidates, item)
	}

	appendCandidate(trimmed)
	if isPosterURLProxyWrapped(trimmed) {
		return candidates
	}
	for _, prefix := range posterTransferProxyPrefixes {
		proxyURL := makeProxyWrappedPosterURL(prefix, trimmed)
		appendCandidate(proxyURL)
	}
	return candidates
}

func isPosterURLProxyWrapped(targetURL string) bool {
	trimmed := strings.TrimSpace(targetURL)
	if trimmed == "" {
		return false
	}
	for _, prefix := range posterTransferProxyPrefixes {
		p := strings.TrimSpace(prefix)
		if p == "" {
			continue
		}
		if !strings.HasSuffix(p, "/") {
			p += "/"
		}
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	return false
}

func makeProxyWrappedPosterURL(prefix, targetURL string) string {
	p := strings.TrimSpace(prefix)
	t := strings.TrimSpace(targetURL)
	if p == "" || t == "" {
		return ""
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	if strings.HasPrefix(t, p) {
		return t
	}
	return p + t
}

func downloadPosterImage(target string) ([]byte, string, error) {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return nil, "", fmt.Errorf("empty download url")
	}

	req, err := http.NewRequest(http.MethodGet, trimmed, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("download poster http %d", resp.StatusCode)
	}

	reader := io.LimitReader(resp.Body, maxPosterBytes+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("empty poster content")
	}
	if len(data) > maxPosterBytes {
		return nil, "", fmt.Errorf("poster too large")
	}

	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if semi := strings.Index(contentType, ";"); semi >= 0 {
		contentType = strings.TrimSpace(contentType[:semi])
	}
	if contentType == "" {
		contentType = strings.ToLower(strings.TrimSpace(http.DetectContentType(data)))
	}
	if !strings.HasPrefix(contentType, "image/") {
		return nil, "", fmt.Errorf("content-type not image: %s", contentType)
	}
	return data, contentType, nil
}

func writePosterTempFile(data []byte, contentType string) (string, error) {
	ext := ".jpg"
	if extensions, extErr := mime.ExtensionsByType(contentType); extErr == nil && len(extensions) > 0 {
		ext = extensions[0]
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}

	tmpFile, err := os.CreateTemp("", "ptnexus-poster-*"+ext)
	if err != nil {
		return "", err
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return tmpPath, nil
}

// pixhostUploadReply 是一次成功的 Pixhost 上传响应。
type pixhostUploadReply struct {
	ShowURL   string // 展示页地址，形如 https://pixhost.to/show/<目录号>/<文件名>
	DirectURL string // 图床响应带回的真实直链（含 img<N> 分片编号），可能为空
}

// UploadImageToPixhost 上传本地图片到 Pixhost，返回 show_url。
func UploadImageToPixhost(imagePath string) (string, error) {
	reply, _, err := uploadToPixhostDirectStream(imagePath, "https://api.pixhost.to/images", func(string, ...any) {})
	return reply.ShowURL, err
}

// UploadImageToPixhostNarrative 按 Python 版日志风格上传图片到 Pixhost，支持主备域名切换。
// 参数/返回：imagePath 为本地图片路径；成功返回 show_url；失败返回错误。
// 失败场景：文件不存在、网络错误、Pixhost 非 200、响应 JSON 不合法。
// 副作用：读取本地文件并发起 HTTP 请求；会输出叙事式纯文本日志。
func UploadImageToPixhostNarrative(imagePath string) (string, error) {
	return UploadImageToPixhostNarrativeWithLogger(imagePath, logx.PlainInfof)
}

// UploadImageToPixhostNarrativeWithLogger 按 Python 版日志风格上传图片到 Pixhost，支持主备域名切换。
// 参数/返回：logLine 用于输出单行日志（可用于并发场景下的日志缓冲）；成功返回 show_url。
func UploadImageToPixhostNarrativeWithLogger(imagePath string, logLine func(string, ...any)) (string, error) {
	reply, err := uploadImageToPixhostReply(imagePath, logLine)
	return reply.ShowURL, err
}

// UploadImageToPixhostDirectWithLogger 上传图片并直接返回可显示的 img<N> 直链。
// 截图链路专用：历史实现是把 show_url 用字符串替换拼成写死分片编号的直链，
// 图床把 CDN 分片从 img2 轮换到 img3 后，新截图链接全部指向错误域名而显示为裂图。
func UploadImageToPixhostDirectWithLogger(imagePath string, logLine func(string, ...any)) (string, error) {
	reply, err := uploadImageToPixhostReply(imagePath, logLine)
	if err != nil {
		return "", err
	}
	direct := strings.TrimSpace(ResolvePixhostDirectURLFromUpload(reply.DirectURL, reply.ShowURL))
	if direct == "" {
		return strings.TrimSpace(reply.ShowURL), nil
	}
	return direct, nil
}

// uploadImageToPixhostReply 带主备域名重试地把图片上传到 Pixhost，并返回解析后的响应。
// 参数/返回：成功返回 show_url 与图床给出的直链；全部域名失败返回最后一次错误。
func uploadImageToPixhostReply(imagePath string, logLine func(string, ...any)) (pixhostUploadReply, error) {
	apiURLs := []string{
		"https://api.pixhost.to/images",
		"http://pt-nexus-proxy.sqing33.dpdns.org/https://api.pixhost.to/images",
		"http://pt-nexus-proxy.1395251710.workers.dev/https://api.pixhost.to/images",
	}

	logLine("准备上传图片: %s", imagePath)
	if _, err := os.Stat(imagePath); err != nil {
		logLine("错误：文件不存在 %s", imagePath)
		return pixhostUploadReply{}, err
	}

	maxRetries := 3
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		for i, apiURL := range apiURLs {
			domainName := "主域名"
			if i != 0 {
				domainName = "备用域名"
			}
			logLine("尝试使用%s: %s", domainName, apiURL)
			reply, statusCode, err := uploadToPixhostDirectStream(imagePath, apiURL, logLine)
			if err == nil && strings.TrimSpace(reply.ShowURL) != "" {
				logLine("%s上传成功", domainName)
				return reply, nil
			}
			lastErr = err
			if statusCode > 0 {
				logLine("   ❌ 直接上传失败 (状态码: %d)", statusCode)
			} else if err != nil {
				logLine("   ❌ 直接上传失败: %s", classifyPixhostUploadError(err))
			} else {
				logLine("   ❌ 直接上传失败")
			}
			logLine("%s上传失败，尝试下一个", domainName)
		}
		if attempt < maxRetries {
			time.Sleep(2 * time.Second)
		}
	}

	logLine("所有API域名都上传失败")
	if lastErr != nil {
		return pixhostUploadReply{}, lastErr
	}
	return pixhostUploadReply{}, fmt.Errorf("Pixhost 上传失败")
}

func uploadToPixhostDirectStream(imagePath string, apiURL string, logLine func(string, ...any)) (pixhostUploadReply, int, error) {
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	var (
		writeErr error
		once     sync.Once
	)
	closeWithErr := func(err error) {
		once.Do(func() {
			writeErr = err
			_ = pw.CloseWithError(err)
		})
	}

	go func() {
		defer func() {
			if writeErr == nil {
				_ = pw.Close()
			}
		}()
		defer writer.Close()

		file, err := os.Open(imagePath)
		if err != nil {
			closeWithErr(err)
			return
		}
		defer file.Close()

		part, err := writer.CreateFormFile("img", filepath.Base(imagePath))
		if err != nil {
			closeWithErr(err)
			return
		}
		if _, err := io.Copy(part, file); err != nil {
			closeWithErr(err)
			return
		}
		if err := writer.WriteField("content_type", "0"); err != nil {
			closeWithErr(err)
			return
		}
	}()

	logLine("正在发送上传请求到 Pixhost...")
	req, err := http.NewRequest(http.MethodPost, apiURL, pr)
	if err != nil {
		_ = pr.Close()
		return pixhostUploadReply{}, 0, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36")

	client := &http.Client{Timeout: 180 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		_ = pr.Close()
		if writeErr != nil {
			return pixhostUploadReply{}, 0, writeErr
		}
		return pixhostUploadReply{}, 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return pixhostUploadReply{}, resp.StatusCode, err
	}
	if resp.StatusCode != http.StatusOK {
		return pixhostUploadReply{}, resp.StatusCode, fmt.Errorf("Pixhost HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	parsed := map[string]any{}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return pixhostUploadReply{}, resp.StatusCode, fmt.Errorf("Pixhost 响应解析失败: %w", err)
	}
	showURL := strings.TrimSpace(toStringAny(parsed["show_url"], ""))
	if showURL == "" {
		if dataMap, ok := parsed["data"].(map[string]any); ok {
			showURL = strings.TrimSpace(toStringAny(dataMap["show_url"], ""))
		}
	}
	if showURL == "" {
		return pixhostUploadReply{}, resp.StatusCode, fmt.Errorf("Pixhost 未返回 show_url")
	}

	// 优先采用图床响应里自带的直链：它带的是本次上传真正使用的 img<N> 分片域名，
	// 而 show_url 里没有分片信息，只能靠猜——猜错就是新图全部裂开。
	directURL := ExtractPixhostDirectURL(respBody)

	logLine("直接上传成功！图片链接: %s", showURL)
	return pixhostUploadReply{ShowURL: showURL, DirectURL: directURL}, resp.StatusCode, nil
}

func classifyPixhostUploadError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(text, "x509") || strings.Contains(text, "tls") || strings.Contains(text, "certificate"):
		return "SSL连接错误"
	case strings.Contains(text, "timeout") || strings.Contains(text, "i/o timeout") || strings.Contains(text, "context deadline"):
		return "请求超时"
	case strings.Contains(text, "connection reset") || strings.Contains(text, "broken pipe") || strings.Contains(text, "connection refused"):
		return "网络连接被重置"
	default:
		return "网络请求失败"
	}
}

// ResolvePixhostImageURL 将 show_url 解析为可访问的直链 URL。
func ResolvePixhostImageURL(showURL string) (string, error) {
	trimmed := strings.TrimSpace(showURL)
	if trimmed == "" {
		return "", fmt.Errorf("empty show_url")
	}

	direct := PixhostShowToDirectURL(trimmed)
	if direct != "" && IsImageURLReachable(direct) {
		return direct, nil
	}

	body, err := FetchPageWithTimeout(trimmed)
	if err == nil && strings.TrimSpace(body) != "" {
		for _, re := range []*regexp.Regexp{rePixhostOgImage, rePixhostImageTag} {
			match := re.FindStringSubmatch(body)
			if len(match) < 2 {
				continue
			}
			candidate := AbsolutizeURL(trimmed, strings.TrimSpace(match[1]))
			if candidate == "" {
				continue
			}
			if strings.Contains(candidate, "pixhost.to/show/") || strings.Contains(candidate, "pixhost.to/th/") {
				candidate = PixhostShowToDirectURL(candidate)
			}
			candidate = NormalizePixhostDirectHost(candidate)
			if candidate != "" && IsImageURLReachable(candidate) {
				return candidate, nil
			}
		}
	}

	if direct != "" {
		return direct, fmt.Errorf("直链可用性校验失败，返回推导直链")
	}
	return trimmed, fmt.Errorf("无法解析 pixhost 直链，返回 show_url")
}

// PixhostShowToDirectURL 尝试将 pixhost show/th 页面地址转换为图片直链。
// 输入里若已带 img<N> 分片编号则原样沿用；只有在完全没有线索时才退回历史兜底编号。
func PixhostShowToDirectURL(showURL string) string {
	trimmed := strings.TrimSpace(showURL)
	if trimmed == "" {
		return ""
	}

	// 已经是合法直链时直接返回，避免任何不必要的域名改写。
	if rePixhostDirectURL.MatchString(trimmed) {
		return trimmed
	}

	base := "https://" + pixhostShardOf(trimmed) + ".pixhost.to/images/"
	direct := strings.Replace(trimmed, "https://pixhost.to/show/", base, 1)
	direct = strings.Replace(direct, "https://pixhost.to/th/", base, 1)
	direct = strings.Replace(direct, "http://pixhost.to/show/", base, 1)
	direct = strings.Replace(direct, "http://pixhost.to/th/", base, 1)
	direct = rePixhostThumbSuffix.ReplaceAllString(direct, `.$1`)

	if rePixhostDirectURL.MatchString(direct) {
		return direct
	}
	if match := rePixhostDirect.FindStringSubmatch(direct); len(match) >= 3 {
		candidate := base + match[1] + "/" + match[2]
		if rePixhostDirectURL.MatchString(candidate) {
			return candidate
		}
	}
	if match := rePixhostDirect.FindStringSubmatch(trimmed); len(match) >= 3 {
		candidate := base + match[1] + "/" + match[2]
		if rePixhostDirectURL.MatchString(candidate) {
			return candidate
		}
	}
	return ""
}

// NormalizePixhostDirectHost 规范化 Pixhost 直链域名到 img<N>.pixhost.to。
// 关键约束：输入里已经带分片编号（img1/img2/img3...）时一律保留，不再强行改写成固定编号，
// 否则图床轮换分片后产出的直链会全部指向错误域名而显示为裂图。
func NormalizePixhostDirectHost(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	parsed, err := neturl.Parse(trimmed)
	if err != nil || parsed == nil {
		return ""
	}
	if parsed.Scheme == "" {
		parsed.Scheme = "https"
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Host))
	if rePixhostShardHost.MatchString(host) {
		parsed.Scheme = "https"
		return parsed.String()
	}
	if strings.Contains(host, "pixhost.to") && strings.Contains(parsed.Path, "/images/") {
		parsed.Host = pixhostShardOf(trimmed) + ".pixhost.to"
		parsed.Scheme = "https"
		return parsed.String()
	}
	if match := rePixhostDirect.FindStringSubmatch(trimmed); len(match) >= 3 {
		return fmt.Sprintf("https://%s.pixhost.to/images/%s/%s", pixhostShardOf(trimmed), match[1], match[2])
	}
	return ""
}

// pixhostShardOf 取出 URL 中的 img<N> 分片编号；取不到时返回历史兜底编号。
func pixhostShardOf(value string) string {
	parsed, err := neturl.Parse(strings.TrimSpace(value))
	if err != nil || parsed == nil {
		return pixhostFallbackShard
	}
	if match := rePixhostShardHost.FindStringSubmatch(strings.ToLower(strings.TrimSpace(parsed.Host))); len(match) == 2 {
		return "img" + match[1]
	}
	return pixhostFallbackShard
}

// ExtractPixhostDirectURL 从图床响应体（JSON 或 HTML）里扫出真实直链。
// 刻意不绑定 image_url / display_url 这类具体字段名：Pixhost 换字段或换分片域名时，
// 只读某一个字段的做法会静默退化成“猜域名”，这正是截图裂图的根因。
func ExtractPixhostDirectURL(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	decoded := any(nil)
	if err := json.Unmarshal(raw, &decoded); err == nil {
		if found := findPixhostDirectURLInValue(decoded); found != "" {
			return found
		}
	}
	match := rePixhostAnyDirect.FindString(string(raw))
	if match == "" {
		return ""
	}
	return NormalizePixhostDirectHost(match)
}

// findPixhostDirectURLInValue 递归遍历已解析的 JSON 值，按 key 字典序保证结果稳定。
func findPixhostDirectURLInValue(value any) string {
	switch typed := value.(type) {
	case string:
		match := rePixhostAnyDirect.FindString(typed)
		if match == "" {
			return ""
		}
		return NormalizePixhostDirectHost(match)
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if found := findPixhostDirectURLInValue(typed[key]); found != "" {
				return found
			}
		}
	case []any:
		for _, item := range typed {
			if found := findPixhostDirectURLInValue(item); found != "" {
				return found
			}
		}
	}
	return ""
}

// ResolvePixhostDirectURLFromUpload 按「上传响应直链 > show 页解析 > 字符串推导」的优先级
// 挑出真正可显示的 Pixhost 图片直链。
// 参数：responseDirectURL 为上传响应里带回的直链（可为空）；showURL 为上传响应里的展示页地址。
// 返回：可用的 img<N> 直链；全部失败时回退 showURL（调用方自行决定是否接受）。
func ResolvePixhostDirectURLFromUpload(responseDirectURL string, showURL string) string {
	showURL = strings.TrimSpace(showURL)
	normalizedResponse := NormalizePixhostDirectHost(responseDirectURL)

	if normalizedResponse != "" && IsImageURLReachable(normalizedResponse) {
		return normalizedResponse
	}

	if resolved, err := ResolvePixhostImageURL(showURL); err == nil && rePixhostDirectURL.MatchString(resolved) {
		return resolved
	}

	// 探测不通通常是我这台机器到图床的网络问题，而不是图床给错了链接。
	// 所以响应直链的优先级仍然要高于“靠历史编号猜出来”的推导结果，否则会把正确链接丢掉。
	if normalizedResponse != "" {
		return normalizedResponse
	}
	if candidate := PixhostShowToDirectURL(showURL); candidate != "" {
		return candidate
	}
	return showURL
}

// IsImageURLReachable 通过 HEAD/GET 探测 URL 是否可访问且内容类型为图片。
func IsImageURLReachable(target string) bool {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return false
	}
	client := &http.Client{Timeout: 12 * time.Second}

	headReq, err := http.NewRequest(http.MethodHead, trimmed, nil)
	if err == nil {
		headReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
		if resp, headErr := client.Do(headReq); headErr == nil {
			ct := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 400 && strings.HasPrefix(ct, "image/") {
				return true
			}
		}
	}

	getReq, err := http.NewRequest(http.MethodGet, trimmed, nil)
	if err != nil {
		return false
	}
	getReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	getReq.Header.Set("Range", "bytes=0-32")
	resp, err := client.Do(getReq)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	ct := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	return resp.StatusCode >= 200 && resp.StatusCode < 400 && strings.HasPrefix(ct, "image/")
}
