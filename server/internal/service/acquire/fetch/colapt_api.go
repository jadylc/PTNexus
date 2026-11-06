package fetch

import (
	"encoding/json"
	"fmt"
	stdhtml "html"
	"io"
	"net/http"
	neturl "net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// colapt（可乐，www.colapt.com）是自研 SPA 架构：详情页为前端应用外壳，
// 数据全部走 JSON API；认证使用 x-api-key（站点 Key 管理页签「API Key」，
// 勾选「兼容面（MoviePilot）」创建）。本文件把 colapt 的 M-Team 兼容 API
// 桥接成"伪详情页 HTML"，使现有公共提取器无需感知 API 站点。
//
// 已实测可用的端点（2026-11-06，x-api-key 认证）：
//   POST {api域}/api/torrent/search   关键词搜索（需 catalog.read）
//   POST {api域}/api/torrent/genDlToken?id={tid}   换下载票（需 torrent.download）
//   GET  {主域}/downloads/{infohash}/{tid}?exp=&sig=   取 .torrent（凭票，无需再带 Key）
// 搜索响应字段（实测）：id/name/smallDescr/size/category/labelsNew/imdb/createdDate/status。

var (
	reColaptTorrentIDInPath = regexp.MustCompile(`torrents/(\d+)`)
	// 下载路径为 /downloads/{infohash}/{tid}?exp=&sig=；infohash 实测为 64 位
	// （BT v2 SHA-256），兼容 40 位 v1 写法。infohash 可能以数字开头，
	// 必须先消费掉整个 infohash 段再取 tid。
	reColaptDownloadIDInPath = regexp.MustCompile(`downloads/[0-9a-fA-F]{40,64}/(\d+)`)
	reColaptNameTailGroup    = regexp.MustCompile(`[.\-_]([A-Za-z][A-Za-z0-9]{2,})$`)
)

// colaptAPIBase 把站点主域换算成 API 主机：www.colapt.com → api.colapt.com。
// 参数/返回：baseURL 为站点主域；返回 API 基地址（含协议、去尾斜杠）。
// 失败场景：不适用。
// 副作用：无。
func colaptAPIBase(baseURL string) string {
	parsed, err := neturl.Parse(normalizeSiteBaseURL(baseURL))
	if err != nil || parsed.Host == "" {
		return strings.TrimRight(normalizeSiteBaseURL(baseURL), "/")
	}
	host := parsed.Hostname()
	if strings.HasPrefix(host, "www.") {
		host = "api." + strings.TrimPrefix(host, "www.")
	} else if !strings.HasPrefix(host, "api.") {
		host = "api." + host
	}
	result := parsed.Scheme + "://" + host
	if parsed.Port() != "" {
		result += ":" + parsed.Port()
	}
	return result
}

// buildColaptDetailHTML 用 colapt 搜索 API 定位种子并合成标准结构的伪详情页，
// 供公共提取器按既有 NexusPHP 口径解析（标题/副标题/基本信息/IMDb）。
// 参数/返回：baseURL 站点主域；apiKey 为 x-api-key 凭据；torrentName 为下载器种子名。
// 返回合成 HTML 与错误；未定位到匹配条目时返回错误。
// 失败场景：网络失败、HTTP 非 2xx、业务码非 0、无匹配条目。
// 副作用：发起网络请求。
func buildColaptDetailHTML(baseURL, apiKey, torrentName string) (string, error) {
	keyword := strings.TrimSpace(torrentName)
	if keyword == "" {
		return "", fmt.Errorf("colapt 搜索关键词为空")
	}
	if strings.TrimSpace(apiKey) == "" {
		return "", fmt.Errorf("colapt 缺少 API Key（请填入站点 Cookie 框）")
	}

	searchURL := colaptAPIBase(baseURL) + "/api/torrent/search"
	payload := fmt.Sprintf(`{"keyword":%q,"categories":[],"pageNumber":1,"pageSize":20}`, keyword)
	body, err := colaptPostJSON(searchURL, apiKey, payload)
	if err != nil {
		return "", err
	}

	entry, err := matchColaptSearchEntry(body, keyword)
	if err != nil {
		return "", err
	}
	return colaptEntryToDetailHTML(entry), nil
}

// colaptPostJSON 发起 colapt API 的 POST 请求并返回响应原文。
// 失败场景：网络错误、非 2xx 响应。
// 副作用：发起网络请求。
func colaptPostJSON(targetURL, apiKey, payload string) (string, error) {
	respBody, err := colaptHTTPRequest("POST", targetURL, apiKey, "application/json", payload)
	if err != nil {
		return "", err
	}
	return respBody, nil
}

// colaptHTTPRequest 执行带 x-api-key 的 HTTP 请求。
// 失败场景：网络错误、HTTP 非 2xx。
// 副作用：发起网络请求。
func colaptHTTPRequest(method, targetURL, apiKey, contentType, payload string) (string, error) {
	client := &http.Client{Timeout: 45 * time.Second}
	request, err := http.NewRequest(method, targetURL, strings.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("colapt 请求构造失败: %w", err)
	}
	request.Header.Set("x-api-key", strings.TrimSpace(apiKey))
	request.Header.Set("Accept", "application/json")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("colapt 请求失败: %w", err)
	}
	defer func() {
		_ = response.Body.Close()
	}()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return "", fmt.Errorf("colapt 响应读取失败: %w", err)
	}
	text := string(raw)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		trimmed := strings.TrimSpace(text)
		if len(trimmed) > 200 {
			trimmed = trimmed[:200]
		}
		return "", fmt.Errorf("colapt 接口返回 HTTP %d: %s", response.StatusCode, trimmed)
	}
	return text, nil
}

// colaptGenDlToken 调 genDlToken 换取带签名的短时下载 URL（需 torrent.download 权限）。
// 实测（2026-11-06）：POST {api域}/api/torrent/genDlToken?id={tid} →
// {"code":0,"data":"https://www.colapt.com/downloads/{infohash}/{tid}?exp=&sig="}。
// 参数/返回：baseURL 站点主域；apiKey 为 x-api-key 凭据；torrentID 为数字种子 id。
// 返回下载 URL 与错误。
// 失败场景：网络失败、业务码非 0、data 非字符串。
// 副作用：发起网络请求。
func colaptGenDlToken(baseURL, apiKey, torrentID string) (string, error) {
	if strings.TrimSpace(apiKey) == "" {
		return "", fmt.Errorf("colapt 缺少 API Key，无法换下载票")
	}
	id := strings.TrimSpace(torrentID)
	if id == "" {
		return "", fmt.Errorf("colapt 换票缺少种子 id")
	}
	tokenURL := colaptAPIBase(baseURL) + "/api/torrent/genDlToken?id=" + neturl.QueryEscape(id)
	body, err := colaptHTTPRequest("POST", tokenURL, apiKey, "", "")
	if err != nil {
		return "", err
	}
	var parsed struct {
		Code any    `json:"code"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return "", fmt.Errorf("colapt 换票响应解析失败: %w", err)
	}
	if codeText := fmt.Sprint(parsed.Code); codeText != "0" {
		return "", fmt.Errorf("colapt 换票业务码异常: %v", parsed.Code)
	}
	downloadURL := strings.TrimSpace(parsed.Data)
	if downloadURL == "" {
		return "", fmt.Errorf("colapt 换票未返回下载地址")
	}
	return downloadURL, nil
}

// colaptDownloadCandidates 生成 colapt 的下载候选（按优先级）：
//  1. API Key 换票（genDlToken，推荐——站点自动化的标准方式）；
//  2. Pass Key 直连 IYUU 兼容下载端点（备用，需用户另行填写 Pass Key）。
func colaptDownloadCandidates(baseURL, apiKey, passkey, detailURL, torrentID string) []string {
	id := extractColaptTorrentID(detailURL, torrentID)
	if id == "" {
		return nil
	}
	candidates := make([]string, 0, 2)
	if downloadURL, err := colaptGenDlToken(baseURL, apiKey, id); err == nil && strings.TrimSpace(downloadURL) != "" {
		candidates = append(candidates, downloadURL)
	} else if err != nil {
		_ = err // 换票失败时回落 Pass Key 路径，错误由下载环节统一暴露
	}
	if trimmedPasskey := strings.TrimSpace(passkey); trimmedPasskey != "" {
		candidates = append(candidates, fmt.Sprintf("%s/api/v1/iyuu/download?id=%s&passkey=%s",
			strings.TrimRight(baseURL, "/"), neturl.QueryEscape(id), neturl.QueryEscape(trimmedPasskey)))
	}
	return candidates
}

// matchColaptSearchEntry 在搜索响应里定位与种子名匹配的条目。
// 参数/返回：body 为搜索响应 JSON；torrentName 为下载器种子名。
// 返回条目的 name/smallDescr/size/category/imdb 与 team 尾段。
// 失败场景：响应结构异常、无匹配条目。
// 副作用：无。
func matchColaptSearchEntry(body, torrentName string) (map[string]string, error) {
	var parsed struct {
		Code any `json:"code"`
		Data struct {
			Data []struct {
				ID         any    `json:"id"`
				Name       string `json:"name"`
				SmallDescr string `json:"smallDescr"`
				Size       any    `json:"size"`
				Category   string `json:"category"`
				IMDb       string `json:"imdb"`
			} `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return nil, fmt.Errorf("colapt 搜索响应解析失败: %w", err)
	}
	if codeText := fmt.Sprint(parsed.Code); codeText != "0" {
		return nil, fmt.Errorf("colapt 搜索业务码异常: %v", parsed.Code)
	}

	want := normalizeColaptName(torrentName)
	if want == "" {
		return nil, fmt.Errorf("colapt 种子名为空，无法匹配")
	}
	for _, item := range parsed.Data.Data {
		if normalizeColaptName(item.Name) == want {
			entry := map[string]string{
				"name":       item.Name,
				"smallDescr": item.SmallDescr,
				"category":   item.Category,
				"imdb":       item.IMDb,
			}
			if size := fmt.Sprint(item.Size); size != "" && size != "<nil>" {
				entry["size"] = size
			}
			return entry, nil
		}
	}
	return nil, fmt.Errorf("colapt 搜索无同名结果 keyword=%q（候选 %d 条）", torrentName, len(parsed.Data.Data))
}

// normalizeColaptName 归一化种子名用于比对：小写、统一空白、忽略点/下划线与横杠差异
// （下载器名与站点名在分隔符上可能有细微差别）。
func normalizeColaptName(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	lower = strings.ReplaceAll(lower, "\u00a0", " ")
	fields := strings.Fields(lower)
	joined := strings.Join(fields, " ")
	joined = strings.ReplaceAll(joined, ".", " ")
	joined = strings.ReplaceAll(joined, "_", " ")
	return strings.Join(strings.Fields(joined), " ")
}

// colaptEntryToDetailHTML 把搜索条目合成为标准 NexusPHP 详情页结构的伪 HTML。
// 公共提取器对 h1#top / 副标题行 / 基本信息内联键值 / IMDb 行均有成熟解析。
// 参数/返回：entry 为匹配条目；返回合成 HTML。
// 失败场景：不适用。
// 副作用：无。
func colaptEntryToDetailHTML(entry map[string]string) string {
	name := stdhtml.UnescapeString(strings.TrimSpace(entry["name"]))
	subtitle := stdhtml.UnescapeString(strings.TrimSpace(entry["smallDescr"]))
	category := mapColaptCategory(entry["category"])
	team := colaptTeamFromName(entry["name"])
	imdb := strings.TrimSpace(entry["imdb"])

	var builder strings.Builder
	builder.WriteString("<html><head><title>" + stdhtml.EscapeString(name) + "</title></head><body>")
	builder.WriteString("<h1 id=\"top\">" + stdhtml.EscapeString(name) + "</h1>")
	builder.WriteString("<table>")
	if subtitle != "" {
		builder.WriteString("<tr><td class=\"rowhead\">副标题</td><td class=\"rowfollow\">" + stdhtml.EscapeString(subtitle) + "</td></tr>")
	}
	if category != "" || team != "" {
		builder.WriteString("<tr><td class=\"rowhead\">基本信息</td><td class=\"rowfollow\">")
		if category != "" {
			builder.WriteString("<b>类型:</b> " + stdhtml.EscapeString(category) + " &nbsp;")
		}
		if team != "" {
			builder.WriteString("<b>制作组: </b>" + stdhtml.EscapeString(team))
		}
		builder.WriteString("</td></tr>")
	}
	if imdb != "" {
		imdbURL := imdb
		if !strings.HasPrefix(strings.ToLower(imdbURL), "http") {
			imdbURL = "https://www.imdb.com/title/" + imdb + "/"
		}
		builder.WriteString("<tr><td class=\"rowhead\">IMDb信息</td><td class=\"rowfollow\"><a href=\"" + stdhtml.EscapeString(imdbURL) + "\">" + stdhtml.EscapeString(imdbURL) + "</a></td></tr>")
	}
	builder.WriteString("</table></body></html>")
	return builder.String()
}

// mapColaptCategory 把 colapt 分类 ID 映射为站内类型文案（M-Team 兼容分类，
// 与 NexusPHP 传统分类一致；实测 401=电影、402=电视剧）。
func mapColaptCategory(category string) string {
	switch strings.TrimSpace(category) {
	case "401":
		return "电影-Movies"
	case "402":
		return "电视剧-TV Series"
	case "403":
		return "综艺-TV Shows"
	case "404":
		return "纪录片-Documentaries"
	case "405":
		return "动漫-Animes"
	case "406":
		return "演唱会-Music Videos"
	case "407":
		return "体育节目-Sports"
	case "408":
		return "音乐-Music"
	case "409":
		return "其他-Other"
	}
	return ""
}

// colaptTeamFromName 从种子名尾段提取疑似制作组（.FRDS / -MNHD-FRDS 均可）。
func colaptTeamFromName(name string) string {
	trimmed := strings.TrimSpace(name)
	trimmed = strings.TrimSuffix(strings.TrimSuffix(trimmed, ".mkv"), ".torrent")
	match := reColaptNameTailGroup.FindStringSubmatch(trimmed)
	if len(match) >= 2 {
		return match[1]
	}
	return ""
}

// extractColaptTorrentID 从 colapt 的详情链接或显式 torrentID 中提取数字种子 id。
// 支持 https://www.colapt.com/torrents/14522、/downloads/{infohash}/14522、纯数字。
func extractColaptTorrentID(detailURL, torrentID string) string {
	for _, candidate := range []string{detailURL, torrentID} {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "" {
			continue
		}
		if match := reColaptDownloadIDInPath.FindStringSubmatch(trimmed); len(match) >= 2 {
			return match[1]
		}
		if match := reColaptTorrentIDInPath.FindStringSubmatch(trimmed); len(match) >= 2 {
			return match[1]
		}
		if _, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return trimmed
		}
	}
	return ""
}
