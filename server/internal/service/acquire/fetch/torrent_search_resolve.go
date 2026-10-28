package fetch

import (
	"fmt"
	neturl "net/url"
	"regexp"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
)

// reSearchDetailLink 匹配种子列表行中的详情页链接：捕获数字 id 与链接文本（即种子名）。
var reSearchDetailLink = regexp.MustCompile(`(?is)<a[^>]*href=["'][^"']*details\.php\?id=(\d+)[^"']*["'][^>]*>(.*?)</a>`)

// resolveTorrentIDBySearch 在站点种子列表按种子名搜索，定位站点内部数字 torrent_id。
// 参数/返回：baseURL 为站点根地址；cookie 为站点登录态；torrentName 为完整种子名；返回数字 torrent_id。
// 失败场景：缺少 base_url/种子名、搜索请求失败、结果为空、无法唯一匹配时返回 error。
// 副作用：发起一次 torrents.php 搜索请求。
// 说明：IYUU 对部分站点（如彩虹岛/CHDBits）返回的 torrent_id 是 infohash，
// 拼出的 details.php?id=<hash> 并非有效详情页；按名称搜索 torrents.php 是把
// 这类站点补齐为可用源站的途径（与彩虹岛标签提取使用的搜索同源）。
func resolveTorrentIDBySearch(baseURL, cookie, torrentName string) (string, error) {
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
	html, err := fetchPageWithCookie(searchURL, cookie, 45*time.Second)
	if err != nil {
		return "", fmt.Errorf("搜索请求失败: %w", err)
	}

	matches := reSearchDetailLink.FindAllStringSubmatch(html, -1)
	if len(matches) == 0 {
		return "", fmt.Errorf("搜索结果为空")
	}

	// 优先精确匹配：链接文本（剥标签后）与种子名完全一致。
	seen := map[string]struct{}{}
	ids := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		id := strings.TrimSpace(match[1])
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)

		linkText := strings.TrimSpace(stripHTMLTags(match[2]))
		if linkText == name {
			return id, nil
		}
	}

	// 无精确匹配：完整种子名按 AND 分词搜索，结果唯一时直接采用。
	if len(ids) == 1 {
		return ids[0], nil
	}
	return "", fmt.Errorf("搜索命中 %d 个候选，无法唯一定位", len(ids))
}
