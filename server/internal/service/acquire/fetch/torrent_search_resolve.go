package fetch

import (
	"fmt"
	neturl "net/url"
	"regexp"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
)

// reSearchDetailLink 匹配种子列表行中的详情页链接：捕获完整属性串、数字 id 与链接文本。
// 说明：NexusPHP 列表页的完整种子名常放在 <a title="..."> 属性里，可见文本可能被截断
// 或因搜索关键词高亮混入内部标签，因此需要同时提取属性串中的 title 与内部文本。
var reSearchDetailLink = regexp.MustCompile(`(?is)<a([^>]*href=["'][^"']*details\.php\?id=(\d+)[^"']*["'][^>]*)>(.*?)</a>`)

// reTitleAttr 从属性串中提取 title="..." 的值。
var reTitleAttr = regexp.MustCompile(`(?is)title=["']([^"']*)["']`)

// searchCandidate 表示搜索结果中的一个候选行。
type searchCandidate struct {
	id    string
	text  string
	title string
}

// resolveTorrentIDBySearch 在站点种子列表按种子名搜索，定位站点内部数字 torrent_id。
// 参数/返回：baseURL 为站点根地址；cookie 为站点登录态；passkey 用于候选验证下载；
// torrentName 为完整种子名；expectedHash 为期望的 infohash（用于逐候选验证）；
// 返回数字 torrent_id。
// 失败场景：缺少 base_url/种子名、搜索请求失败（含 Cloudflare 挑战）、结果为空、
// 无法唯一匹配且逐候选验证未命中时返回 error。
// 副作用：发起一次 torrents.php 搜索请求；精确匹配失败且候选有限时逐个下载候选种子验证。
// 说明：IYUU 对部分站点（如彩虹岛/CHDBits、我堡/OurBits）返回的 torrent_id 是 infohash，
// 拼出的 details.php?id=<hash> 并非有效详情页。定位策略分三级：
//  1. 精确匹配：候选的 title 属性或链接文本与种子名一致；
//  2. 唯一候选：搜索只返回一条时直接采用；
//  3. infohash 验证：逐个下载候选种子文件并计算 infohash，与期望值一致者即为目标。
func resolveTorrentIDBySearch(baseURL, cookie, passkey, torrentName, expectedHash string) (string, error) {
	normalizedBase := normalizeSiteBaseURL(baseURL)
	if strings.TrimSpace(normalizedBase) == "" {
		return "", fmt.Errorf("缺少 base_url")
	}
	name := strings.TrimSpace(torrentName)
	if name == "" {
		return "", fmt.Errorf("缺少用于搜索的种子名")
	}

	query := neturl.Values{}
	query.Set("search", name)
	query.Set("search_area", "0")
	query.Set("search_mode", "0")
	query.Set("incldead", "1")
	searchURL := strings.TrimRight(normalizedBase, "/") + "/torrents.php?" + query.Encode()

	logx.Infof(sourceDownloadLogModule, "按名称搜索定位站点种子ID search_url=%s", redactURLQuery(searchURL))
	// fetchPageWithCookie 内部走 fetchBinarySmart：命中 CF 挑战会经 FlareSolverr 求解，
	// 求解得到的 Cookie/UA 在本轮搜索内已由重试逻辑使用。
	html, err := fetchPageWithCookie(searchURL, cookie, 45*time.Second)
	if err != nil {
		return "", fmt.Errorf("搜索请求失败: %w", err)
	}

	candidates := collectSearchCandidates(html)
	if len(candidates) == 0 {
		return "", fmt.Errorf("搜索结果为空")
	}

	// 第一级：精确匹配 title 属性或链接文本（归一化比较，容忍空白与内部标签差异）。
	for _, candidate := range candidates {
		if candidate.title == name || candidate.text == name {
			return candidate.id, nil
		}
	}

	// 第二级：唯一候选直接采用。
	if len(candidates) == 1 {
		return candidates[0].id, nil
	}

	// 第三级：按 infohash 逐候选验证（下载候选种子文件并比对 infohash）。
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.id)
	}
	if verifiedID, verifyErr := verifyCandidatesByInfohash(normalizedBase, cookie, passkey, ids, expectedHash); verifyErr == nil {
		logx.Infof(sourceDownloadLogModule, "按infohash验证命中站点种子ID torrent_id=%s", verifiedID)
		return verifiedID, nil
	} else {
		logx.Warnf(sourceDownloadLogModule, "按infohash验证候选未命中 err=%v", verifyErr)
	}

	return "", fmt.Errorf("搜索命中 %d 个候选且无法唯一定位（精确名称匹配与 infohash 验证均未命中）", len(candidates))
}

// collectSearchCandidates 从搜索结果页解析候选行（按 id 去重，保留首个出现的 title/文本）。
// 参数/返回：html 为 torrents.php 页面内容；返回候选列表。
// 失败场景：不适用。
// 副作用：无。
func collectSearchCandidates(html string) []searchCandidate {
	matches := reSearchDetailLink.FindAllStringSubmatch(html, -1)
	seen := map[string]struct{}{}
	candidates := make([]searchCandidate, 0, len(matches))
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		id := strings.TrimSpace(match[2])
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		title := ""
		if titleMatch := reTitleAttr.FindStringSubmatch(match[1]); len(titleMatch) >= 2 {
			title = normalizeSearchName(titleMatch[1])
		}
		candidates = append(candidates, searchCandidate{
			id:    id,
			text:  normalizeSearchName(match[3]),
			title: title,
		})
	}
	return candidates
}

// normalizeSearchName 归一化候选名称：去内部标签、解码常见实体、折叠空白。
// 参数/返回：raw 为原始文本；返回归一化后的名称。
// 失败场景：不适用。
// 副作用：无。
func normalizeSearchName(raw string) string {
	return strings.Join(strings.Fields(stripHTMLTags(raw)), " ")
}

// verifyCandidatesByInfohash 逐个下载候选种子并比对 infohash，命中即返回该候选 ID。
// 参数/返回：baseURL 为站点根地址；cookie 为登录态；passkey 为下载密钥；
// ids 为候选数字 ID 列表；expectedHash 为期望 infohash；返回命中的候选 ID。
// 失败场景：所有候选下载/解析失败或均不匹配时返回 error。
// 副作用：对每个候选发起一次种子文件下载请求（上限 8 个）。
func verifyCandidatesByInfohash(baseURL, cookie, passkey string, ids []string, expectedHash string) (string, error) {
	expected := strings.ToLower(strings.TrimSpace(expectedHash))
	if expected == "" {
		return "", fmt.Errorf("缺少期望的 infohash")
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("没有可验证的候选")
	}

	const maxVerify = 8
	if len(ids) > maxVerify {
		ids = ids[:maxVerify]
	}

	trimmedPasskey := strings.TrimSpace(passkey)
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		downloadURL := strings.TrimRight(baseURL, "/") + "/download.php?id=" + neturl.QueryEscape(id)
		if trimmedPasskey != "" {
			downloadURL += "&passkey=" + neturl.QueryEscape(trimmedPasskey)
		}
		body, err := fetchBinaryWithCookie(downloadURL, "", cookie, 60*time.Second)
		if err != nil {
			logx.Warnf(sourceDownloadLogModule, "候选种子下载失败 candidate=%s err=%v", downloadURL, err)
			continue
		}
		if !isLikelyTorrent(body) {
			continue
		}
		meta, metaErr := ParseTorrentMeta(body)
		if metaErr != nil {
			continue
		}
		if strings.EqualFold(meta.InfoHash, expected) {
			return id, nil
		}
	}
	return "", fmt.Errorf("候选均未命中期望 infohash")
}
