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
// reSearchDetailLink 匹配种子列表行中的详情页链接：捕获完整属性串、数字 id 与链接文本。
// 说明：① href 前缀收紧为（可选路径/站点前缀+斜杠）——否则 userdetails.php 等链接
// 会因包含 "details.php?id=" 子串被误抓为候选（OurBits 实测复现）；
// ② NexusPHP 列表页的完整种子名常放在 <a title="..."> 属性里，可见文本可能被截断
// 或因搜索关键词高亮混入内部标签，因此需要同时提取属性串中的 title 与内部文本。
var reSearchDetailLink = regexp.MustCompile(`(?is)<a([^>]*href=["'](?:[^"']*/)?details\.php\?id=(\d+)[^"']*["'][^>]*)>(.*?)</a>`)

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
// 说明：IYUU 对部分站点（如彩虹岛/CHDBits、我堡/OurBits、家园/HDHome）返回的
// torrent_id 是 infohash，拼出的 details.php?id=<hash> 并非有效详情页。
// 定位策略分三级：
//  1. 精确匹配：候选的 title 属性或链接文本与种子名一致；
//  2. 唯一候选：搜索只返回一条时直接采用；
//  3. infohash 验证：逐个下载候选种子文件并计算 infohash，与期望值一致者即为目标。
//
// 搜索词降级策略（从强到弱）：
//  1. 整串搜索（原逻辑）：标题=种子名的站点直接命中，无回归风险。
//  2. 拆词多词 AND：种子名是"中文名.年份.分辨率.语言.字幕￡组名"的点号串，
//     站点标题是"英文名 年份 媒介 编码-组名"的空格串（中文名在副标题），
//     整串必然落空。拆成空格分隔多词后每个词在标题/副标题都能命中
//     （NexusPHP 搜索覆盖副标题，与用户手动搜"千钧一发 CMCT"行为一致）。
//  3. 最次——只用名字搜 / 名字+发布组：拆词仍无结果时退化为
//     仅搜资源名主词（如"千钧一发"）或名字+发布组（如"千钧一发 CMCT呆呆"）。
//     候选可能多，由 infohash 验证兜底。
func resolveTorrentIDBySearch(baseURL, cookie, passkey, torrentName, expectedHash string) (string, error) {
	normalizedBase := normalizeSiteBaseURL(baseURL)
	if strings.TrimSpace(normalizedBase) == "" {
		return "", fmt.Errorf("缺少 base_url")
	}
	name := strings.TrimSpace(torrentName)
	if name == "" {
		return "", fmt.Errorf("缺少用于搜索的种子名")
	}

	// 按给定词集合执行一次搜索并解析候选。
	searchByTerms := func(wordList []string) ([]searchCandidate, error) {
		if len(wordList) == 0 {
			return nil, nil
		}
		query := neturl.Values{}
		query.Set("search", strings.Join(wordList, " "))
		query.Set("search_area", "0")
		query.Set("search_mode", "0")
		query.Set("incldead", "1")
		searchURL := strings.TrimRight(normalizedBase, "/") + "/torrents.php?" + query.Encode()
		// fetchPageWithCookie 内部走 fetchBinarySmart：命中 CF 挑战会经 FlareSolverr 求解，
		// 求解得到的 Cookie/UA 在本轮搜索内已由重试逻辑使用。
		pageHTML, err := fetchPageWithCookie(searchURL, cookie, 45*time.Second)
		if err != nil {
			return nil, fmt.Errorf("搜索请求失败: %w", err)
		}
		candidates := collectSearchCandidates(pageHTML)
		logx.Infof(sourceDownloadLogModule, "按名称搜索定位站点种子ID 词数=%d 词集=%s 候选=%d", len(wordList), truncateForLog(strings.Join(wordList, " "), 80), len(candidates))
		if len(candidates) == 0 {
			// 候选为空时必须知道拿到的是什么页面：登录页（cookie 失效）/ CF 挑战页 /
			// 搜索词确实无结果三种情况的处理完全不同，此处打印页面特征。
			logx.Warnf(sourceDownloadLogModule, "搜索候选为空，页面特征: %s", describeSearchPage(pageHTML))
		}
		return candidates, nil
	}

	// 第1级：整串搜索（原逻辑）——标题=种子名的站点直接命中
	candidates, err := searchByTerms([]string{name})
	if err != nil {
		return "", err
	}

	// 第2级：拆词多词 AND——标题被改的站点（如家园"千钧一发"→"Gattaca"）
	if len(candidates) == 0 {
		terms := searchTerms(name)
		if len(terms) > 1 {
			logx.Warnf(sourceDownloadLogModule, "整串搜索无结果，降级为拆词多词AND搜索")
			candidates, err = searchByTerms(terms)
			if err != nil {
				return "", err
			}
		}
	}

	// 第3级：最次——只用名字搜 / 名字+发布组
	if len(candidates) == 0 {
		terms := searchTerms(name)
		if len(terms) > 0 {
			namePart := terms[0]
			logx.Warnf(sourceDownloadLogModule, "拆词搜索无结果，降级为只用名字搜: %s", truncateForLog(namePart, 80))
			candidates, err = searchByTerms([]string{namePart})
			if err != nil {
				return "", err
			}
			if len(candidates) == 0 && len(terms) > 1 {
				// 名字+发布组：首词+末词（发布组/组名）
				group := terms[len(terms)-1]
				if group != namePart {
					logx.Warnf(sourceDownloadLogModule, "只用名字无结果，降级为名字+发布组: %s", truncateForLog(namePart+" "+group, 80))
					candidates, err = searchByTerms([]string{namePart, group})
					if err != nil {
						return "", err
					}
				}
			}
		}
	}

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

	return "", fmt.Errorf("搜索命中 %d 个候选且无法唯一定位（精确名称匹配与 infohash 验证均未命中；候选预览: %s）", len(candidates), candidatePreview(candidates))
}

// reTermSep 种子名拆词分隔符：点号/连字符/下划线/空白/全角货币符与常见标点。

// reSearchEchoInput 匹配搜索结果页回显的搜索关键词输入框（NexusPHP 标准结构）。
var reSearchEchoInput = regexp.MustCompile(`(?is)<input[^>]*name=["']search["'][^>]*value=["']([^"']*)["']`)

// extractSearchEcho 提取页面回显的搜索关键词。
// 参数/返回：html 为页面内容；返回回显的搜索词（无输入框或为空时返回空串）。
// 失败场景：不适用。
// 副作用：无。
func extractSearchEcho(html string) string {
	if match := reSearchEchoInput.FindStringSubmatch(html); len(match) >= 2 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

// truncateForLog 截断文本用于日志输出。
// 参数/返回：text 为原文；limit 为最大长度；返回截断后的文本。
// 失败场景：不适用。
// 副作用：无。
func truncateForLog(text string, limit int) string {
	trimmed := strings.TrimSpace(text)
	if limit <= 0 || len([]rune(trimmed)) <= limit {
		return trimmed
	}
	runes := []rune(trimmed)
	return string(runes[:limit]) + "…"
}

// rePageTitleSnippet 匹配页面 <title>（诊断用）。
var rePageTitleSnippet = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// describeSearchPage 生成搜索结果页的诊断特征描述（用于候选为空时的日志判别）。
// 参数/返回：html 为搜索页内容；返回形如 "CF挑战页(title=...)" 的特征文本。
// 失败场景：不适用。
// 副作用：无。
// 说明：候选为空时最需要区分——拿到登录页（cookie 失效）、CF 挑战页（防护拦截）
// 还是搜索真的无结果（种子名与站点发布名差异）。三类的处理完全不同。
func describeSearchPage(html string) string {
	lower := strings.ToLower(html)
	title := ""
	if match := rePageTitleSnippet.FindStringSubmatch(html); len(match) >= 2 {
		title = strings.TrimSpace(match[1])
		if len([]rune(title)) > 60 {
			title = string([]rune(title)[:60]) + "…"
		}
	}

	switch {
	case strings.Contains(lower, "just a moment") || strings.Contains(lower, "cf-chl") || strings.Contains(lower, "challenge_platform"):
		return "疑似Cloudflare挑战页"
	case strings.Contains(lower, "challenge_external_return") || strings.Contains(lower, "_cf_chl"):
		return "疑似Cloudflare挑战页(external)"
	case strings.Contains(lower, "logout.php") && strings.Contains(lower, "userdetails.php") && !strings.Contains(lower, "torrents.php\""):
		return "疑似登录页"
	case strings.Contains(lower, "details.php"):
		return "含详情页链接但未匹配到候选模式"
	case strings.Contains(lower, "torrenttable") || strings.Contains(lower, "<table"):
		return "含结果表格标记但无详情链接"
	case title != "":
		return "无详情链接, title=" + title
	default:
		return "页面内容为空或极短"
	}
}

// reTermSep 种子名拆词分隔符：点号/连字符/下划线/空白/全角货币符与常见标点。
// 说明：下载器种子名常是"中文名.年份.分辨率.语言.字幕￡组名"的点号分隔串，
// 而 NexusPHP 搜索按空格分词做 AND —— 点号串必须拆开才能命中站点标题/副标题。
// 注意不拆 "+"：HDR10+/DoVi+ 这类技术标记保留完整 token 更准确。
var reTermSep = regexp.MustCompile(`[.\-_\s￡$¥€￥·、，,]+`)

// searchTerms 把种子名拆成多词（NexusPHP 空格 = AND 分词）。
// 参数/返回：name 为完整种子名；返回词列表（空词剔除）。
// 失败场景：不适用。
// 副作用：无。
func searchTerms(name string) []string {
	parts := reTermSep.Split(strings.TrimSpace(name), -1)
	terms := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		terms = append(terms, trimmed)
	}
	return terms
}

// candidatePreview 生成候选列表的简短预览（最多前 3 个，用于错误诊断）。
// 参数/返回：candidates 为候选列表；返回形如 "#593793(名称…)" 的预览文本。
// 失败场景：不适用。
// 副作用：无。
func candidatePreview(candidates []searchCandidate) string {
	preview := make([]string, 0, 3)
	for i, candidate := range candidates {
		if i >= 3 {
			break
		}
		label := candidate.title
		if label == "" {
			label = candidate.text
		}
		if len(label) > 24 {
			label = label[:24] + "…"
		}
		preview = append(preview, fmt.Sprintf("#%s(%s)", candidate.id, label))
	}
	return strings.Join(preview, ", ")
}

// reTorrentsTableStart 匹配 NexusPHP 搜索结果表格的起始标记（<table class="torrents">）。
var reTorrentsTableStart = regexp.MustCompile(`(?is)<table[^>]*(?:class=["'][^"']*torrents[^"']*["']|id=["']torrenttable["'])[^>]*>`)

// collectSearchCandidates 从搜索结果页解析候选行（按 id 去重，保留首个出现的 title/文本）。
// 参数/返回：html 为 torrents.php 页面内容；返回候选列表。
// 失败场景：不适用。
// 副作用：无。
// 说明：候选解析从 <table class="torrents"> 结果表格开始截取（OurBits 实测页面：
// 表格前的"热门海报"区块带 10 个全站固定 details 链接、用户信息区带 userdetails
// 链接，全页扫描会把它们误计入候选，表现为"每次搜索都多出同一批候选"且
// infohash 验证名额被固定链接占据导致真实目标漏验）。搜索结果表格内部存在
// 嵌套表格，因此只设起点不设终点（表格后的分页/页脚不含详情链接）；
// 找不到表格标记时回退全页解析（兼容非标准结构）。
func collectSearchCandidates(html string) []searchCandidate {
	scoped := html
	if loc := reTorrentsTableStart.FindStringIndex(html); loc != nil {
		scoped = html[loc[0]:]
	}
	matches := reSearchDetailLink.FindAllStringSubmatch(scoped, -1)
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
// 副作用：对每个候选发起一次种子文件下载请求（上限 24 个，避免无界）。
// 说明：候选种子文件很小，逐个验证是准确定位的最可靠手段——上限过低会造成
// 命中种子排在后面时被漏判（曾出现 13 个候选只验证前 8 个的假阴性）。
func verifyCandidatesByInfohash(baseURL, cookie, passkey string, ids []string, expectedHash string) (string, error) {
	expected := strings.ToLower(strings.TrimSpace(expectedHash))
	if expected == "" {
		return "", fmt.Errorf("缺少期望的 infohash")
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("没有可验证的候选")
	}

	const maxVerify = 24
	verified := ids
	if len(verified) > maxVerify {
		verified = verified[:maxVerify]
	}

	trimmedPasskey := strings.TrimSpace(passkey)
	for _, id := range verified {
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
