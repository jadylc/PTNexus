package sites

import (
	"errors"
	stdhtml "html"
	"regexp"
	"strings"
)

// Depth Studio（dstudio.me）是 NexusPHP 的 ModernLight 主题改版：
// 详情页与下载链接仍是标准 details.php / download.php，fetch 层可完全复用；
// 但页面结构与传统 NexusPHP 不同，公共提取器会认错标题、类型与简介。
var (
	// reDStudioTitle 详情页 Hero 区标题（ModernLight：<h1 class="details-title">）。
	reDStudioTitle = regexp.MustCompile(`(?is)<h1[^>]*class=["'][^"']*\bdetails-title\b[^"']*["'][^>]*>(.*?)</h1>`)
	// reDStudioSubtitle 详情页副标题（<p class="details-subtitle">）。
	reDStudioSubtitle = regexp.MustCompile(`(?is)<p[^>]*class=["'][^"']*\bdetails-subtitle\b[^"']*["'][^>]*>(.*?)</p>`)
	// reDStudioBasicInfoCell 定位「基本信息」行：<td>基本信息</td><td>内容</td>。
	reDStudioBasicInfoCell = regexp.MustCompile(`(?is)<td[^>]*>\s*基本信息\s*</td>\s*<td[^>]*>(.*?)</td>`)
	// reDStudioBasicKV 解析基本信息内的内联键值：<b>标签:</b>值。
	reDStudioBasicKV = regexp.MustCompile(`(?is)<b>\s*([^<:：]{1,12})\s*[:：]\s*</b>([^<]*)`)
	// reDStudioTag 详情页标签：<span class="torrent-content-tag">值</span>。
	reDStudioTag = regexp.MustCompile(`(?is)<span[^>]*class=["'][^"']*\btorrent-content-tag\b[^"']*["'][^>]*>(.*?)</span>`)
	// reDStudioDesc 简介容器（#kdescr）。
	reDStudioDesc = regexp.MustCompile(`(?is)<div[^>]*id=["']kdescr["'][^>]*>(.*?)</div>`)
	// reDStudioFieldset 简介里的站点免责声明块（不属于影片简介正文）。
	reDStudioFieldset = regexp.MustCompile(`(?is)<fieldset.*?</fieldset>`)
	// reDStudioBBCodeImg 简介 BBCode 中的图片（海报/截图已单独提取）。
	reDStudioBBCodeImg = regexp.MustCompile(`(?is)\[img\].*?\[/img\]`)
	// reDStudioSiteLogo 站点 logo 图片（不应算作影片截图）。
	reDStudioSiteLogo = regexp.MustCompile(`(?i)dstudiov4\.png`)
)

// ExtractDStudio 提取 Depth Studio（NexusPHP ModernLight 主题）详情页参数。
// 参数/返回：输入详情页上下文与运行期依赖，返回统一 SeedData。
// 失败场景：公共提取器缺失。
// 副作用：无。
func ExtractDStudio(input Input, runtime Runtime) (SeedData, error) {
	if runtime.ExtractWithPublic == nil {
		return SeedData{}, errors.New("缺少公共提取器依赖")
	}
	data, err := runtime.ExtractWithPublic(input)
	page := strings.TrimSpace(input.PageHTML)

	// 标题：公共提取器会落到 <title>（"Depth Studio :: 种子详情 "…" - Powered by NexusPHP"），
	// 这里改取 Hero 区的 details-title。
	if title := cleanDStudioText(findDStudioTitle(page)); title != "" {
		data.Title = title
	}
	// 副标题：details-subtitle 比公共提取器的结果更贴近原站展示。
	if subtitle := cleanDStudioText(findDStudioFirst(page, reDStudioSubtitle)); subtitle != "" {
		data.Subtitle = subtitle
	}

	// 基本信息（类型/媒介/编码/分辨率/制作组等）：公共提取器对 inline `<b>标签:</b>值`
	// 已有解析，但类型映射与制作组标准化会落到默认值，这里按站内取值显式覆盖。
	if info := parseDStudioBasicInfo(page); len(info) > 0 {
		if mapped := mapDStudioType(info["类型"]); mapped != "" {
			data.Type = mapped
		}
		if mapped := mapDStudioMedium(info["媒介"]); mapped != "" {
			data.Medium = mapped
		}
		if mapped := mapDStudioVideoCodec(info["视频编码"]); mapped != "" {
			data.VideoCodec = mapped
		}
		if mapped := mapDStudioAudioCodec(info["音频编码"]); mapped != "" {
			data.AudioCodec = mapped
		}
		if mapped := mapDStudioResolution(info["分辨率"]); mapped != "" {
			data.Resolution = mapped
		}
		if rawTeam := strings.TrimSpace(info["制作组"]); rawTeam != "" && runtime.NormalizeTeamKey != nil {
			data.Team = runtime.NormalizeTeamKey(rawTeam)
		}
	}

	// 标签：站内标签为 .torrent-content-tag（公共提取器已覆盖大部分，
	// 此处仅在公共结果为空时兜底）。
	if len(data.Tags) == 0 {
		data.Tags = extractDStudioTags(page)
	}

	// 简介正文：公共提取器在无 [quote] 结构时只保留外链，这里按 #kdescr 重建。
	if body := buildDStudioBody(page, runtime); body != "" {
		data.Intro.Body = body
	}

	// 截图：剔除站点 logo（configs 的 unwanted_image_urls 也会按 URL 过滤）。
	data.Intro.Screenshots = removeDStudioSiteLogo(data.Intro.Screenshots)

	if runtime.BuildSourceParams != nil {
		data.SourceParams = runtime.BuildSourceParams(data)
	}
	return data.Normalize(input.FallbackTitle), err
}

func findDStudioTitle(page string) string {
	return findDStudioFirst(page, reDStudioTitle)
}

func findDStudioFirst(page string, pattern *regexp.Regexp) string {
	if strings.TrimSpace(page) == "" || pattern == nil {
		return ""
	}
	match := pattern.FindStringSubmatch(page)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

// cleanDStudioText 去掉 HTML 标签、反转义实体并压缩空白。
func cleanDStudioText(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	text := regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(raw, " ")
	text = stdhtml.UnescapeString(text)
	text = strings.ReplaceAll(text, "\u00a0", " ")
	text = strings.ReplaceAll(text, "\u3000", " ")
	return strings.Join(strings.Fields(text), " ")
}

// parseDStudioBasicInfo 解析「基本信息」行里的内联键值对。
// 参数/返回：page 为详情页 HTML；返回 标签→值（如 类型→纪录片-Documentaries）。
// 失败场景：不适用。
// 副作用：无。
func parseDStudioBasicInfo(page string) map[string]string {
	info := map[string]string{}
	match := reDStudioBasicInfoCell.FindStringSubmatch(page)
	if len(match) < 2 {
		return info
	}
	cell := match[1]
	pairs := reDStudioBasicKV.FindAllStringSubmatch(cell, -1)
	for _, pair := range pairs {
		if len(pair) < 3 {
			continue
		}
		label := cleanDStudioText(pair[1])
		value := cleanDStudioText(pair[2])
		if label == "" || value == "" {
			continue
		}
		if _, exists := info[label]; !exists {
			info[label] = value
		}
	}
	return info
}

// extractDStudioTags 提取 .torrent-content-tag 标签（去重、保持顺序）。
func extractDStudioTags(page string) []string {
	matches := reDStudioTag.FindAllStringSubmatch(page, -1)
	tags := make([]string, 0, len(matches))
	seen := map[string]struct{}{}
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		tag := cleanDStudioText(match[1])
		if tag == "" {
			continue
		}
		lower := strings.ToLower(tag)
		if _, exists := seen[lower]; exists {
			continue
		}
		seen[lower] = struct{}{}
		tags = append(tags, tag)
	}
	return tags
}

// buildDStudioBody 用 #kdescr 重建简介正文：转 BBCode、去掉免责声明与图片。
func buildDStudioBody(page string, runtime Runtime) string {
	match := reDStudioDesc.FindStringSubmatch(page)
	if len(match) < 2 {
		return ""
	}
	fragment := reDStudioFieldset.ReplaceAllString(match[1], "")
	body := ""
	if runtime.HTMLToBBCode != nil {
		body = runtime.HTMLToBBCode(fragment)
	}
	if strings.TrimSpace(body) == "" {
		body = cleanDStudioText(fragment)
	}
	body = reDStudioBBCodeImg.ReplaceAllString(body, "")
	body = strings.ReplaceAll(body, "\u00a0", " ")
	for strings.Contains(body, "\n\n\n") {
		body = strings.ReplaceAll(body, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(body)
}

// removeDStudioSiteLogo 从截图中剔除站点 logo。
func removeDStudioSiteLogo(screens string) string {
	if strings.TrimSpace(screens) == "" || !reDStudioSiteLogo.MatchString(screens) {
		return screens
	}
	lines := strings.Split(screens, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if reDStudioSiteLogo.MatchString(line) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// mapDStudioType 把站内类型文案映射为标准化 category.* 键。
func mapDStudioType(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	switch {
	case strings.Contains(value, "纪录片"), strings.Contains(lower, "documentaries"):
		return "category.documentaries"
	case strings.Contains(value, "电视剧"), strings.Contains(lower, "tv series"):
		return "category.tv_series"
	case strings.Contains(value, "综艺"), strings.Contains(lower, "tv shows"):
		return "category.tv_shows"
	case strings.Contains(value, "动漫"), strings.Contains(value, "动画"), strings.Contains(lower, "animes"):
		return "category.animation"
	case strings.Contains(value, "演唱会"), strings.Contains(lower, "music videos"):
		return "category.mv"
	case strings.Contains(value, "体育"), strings.Contains(lower, "sports"):
		return "category.sports"
	case strings.Contains(value, "音乐"), strings.Contains(lower, "music"):
		return "category.music"
	case strings.Contains(value, "其他"), strings.Contains(lower, "other"):
		return "category.other"
	case strings.Contains(value, "电影"), strings.Contains(lower, "movies"):
		return "category.movie"
	}
	return ""
}

// mapDStudioMedium 把站内媒介文案映射为标准化 medium.* 键。
func mapDStudioMedium(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case value == "", value == "-":
		return ""
	case strings.Contains(value, "web-dl"), strings.Contains(value, "webdl"):
		return "medium.webdl"
	case strings.Contains(value, "webrip"), strings.Contains(value, "web-rip"):
		return "medium.webrip"
	case strings.Contains(value, "remux"):
		return "medium.remux"
	case strings.Contains(value, "encode"), strings.Contains(value, "blu-ray"), strings.Contains(value, "bluray"), strings.Contains(value, "bd"):
		return "medium.bluray"
	case strings.Contains(value, "hdtv"):
		return "medium.hdtv"
	case strings.Contains(value, "dvd"):
		return "medium.dvdr"
	case strings.Contains(value, "flac"):
		return "medium.flac"
	case strings.Contains(value, "wav"):
		return "medium.wav"
	}
	return ""
}

// mapDStudioVideoCodec 把站内视频编码文案映射为标准化 video.* 键。
func mapDStudioVideoCodec(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case value == "", value == "-":
		return ""
	case strings.Contains(value, "h.265"), strings.Contains(value, "hevc"), strings.Contains(value, "x265"):
		return "video.h265"
	case strings.Contains(value, "h.264"), strings.Contains(value, "avc"), strings.Contains(value, "x264"):
		return "video.h264"
	case strings.Contains(value, "av1"):
		return "video.av1"
	case strings.Contains(value, "vp9"):
		return "video.vp9"
	}
	return ""
}

// mapDStudioAudioCodec 把站内音频编码文案映射为标准化 audio.* 键。
func mapDStudioAudioCodec(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case value == "", value == "-":
		return ""
	case strings.Contains(value, "ddp"), strings.Contains(value, "e-ac3"), strings.Contains(value, "eac3"):
		return "audio.ddp"
	case strings.Contains(value, "truehd"):
		return "audio.truehd"
	case strings.Contains(value, "dts-hd"), strings.Contains(value, "dtshd"):
		return "audio.dtshd"
	case strings.Contains(value, "dts"):
		return "audio.dts"
	case strings.Contains(value, "ac3"), strings.Contains(value, "ac-3"):
		return "audio.ac3"
	case strings.Contains(value, "aac"):
		return "audio.aac"
	case strings.Contains(value, "flac"):
		return "audio.flac"
	}
	return ""
}

// mapDStudioResolution 把站内分辨率文案映射为标准化 resolution.* 键。
func mapDStudioResolution(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case value == "", value == "-":
		return ""
	case strings.Contains(value, "2160"), strings.Contains(value, "4k"), strings.Contains(value, "8k"):
		return "resolution.r2160p"
	case strings.Contains(value, "1080"):
		return "resolution.r1080p"
	case strings.Contains(value, "720"):
		return "resolution.r720p"
	case strings.Contains(value, "480"):
		return "resolution.r480p"
	}
	return ""
}
