package persist

import (
	"regexp"
	"strings"
)

var (
	// reBodyMediainfoMarker 匹配源站正文里发布者插入的 "mediainfo - xxx" 超链接标记，
	// 标记之后到正文结尾即媒体信息文本（与前端 splitMediaInfoFromIntroBody 同规则）。
	reBodyMediainfoMarker = regexp.MustCompile(`(?i)\[url=javascript:void\(0\)\]\s*mediainfo\s*-\s*[\s\S]*?\[/url\]\s*`)
	// reBodyGeneralSection 匹配正文中的 "General" 独立段行。
	reBodyGeneralSection = regexp.MustCompile(`(?:^|\n)General\s*\n`)
	// reBodyOverallBitrate 与 reBodyCompleteName 联合判定尾段是否像 MediaInfo 文本。
	reBodyOverallBitrate = regexp.MustCompile(`(?i)Overall\s*bit\s*rate`)
	reBodyCompleteName   = regexp.MustCompile(`(?i)Complete\s*name`)
)

// ExtractMediainfoFromBody 从简介正文中提取内嵌的 MediaInfo 文本。
// 参数/返回：body 为正文原文；返回 (媒体文本, 剥离媒体文本后的正文)。
// 两者均可能为空串：未命中标记且不像 MediaInfo 时，media 为空、body 原样返回。
// 失败场景：不适用。
// 副作用：无。
// 说明：与前端 CrossSeedPanel.normalizeIntroBodyAndMediainfo 的拆分规则一致——
// ① 优先 "[url=javascript:void(0)]mediainfo - ...[/url]" 标记后的内容；
// ② 其次从 "General" 段起、且尾段同时含 Overall bit rate 与 Complete name 特征。
// 用于把"发布者把 MediaInfo 贴在正文里"的数据统一落进 mediainfo 列。
func ExtractMediainfoFromBody(body string) (string, string) {
	// \u00a0（不间断空格）归一为普通空格：前端 JS 的 \s 匹配 \u00a0 而 Go 不匹配，
	// 源站正文常用 \u00a0 做列对齐，必须先归一再判定。
	normalized := strings.ReplaceAll(strings.TrimSpace(body), "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\u00a0", " ")
	if normalized == "" {
		return "", body
	}

	if loc := reBodyMediainfoMarker.FindStringIndex(normalized); loc != nil {
		// 标记本身之前的正文保留，标记之后的内容即媒体文本
		media := normalized[loc[1]:]
		media = strings.TrimSpace(media)
		stripped := strings.TrimSpace(normalized[:loc[0]])
		if media == "" {
			return "", body
		}
		return media, stripped
	}

	if idx := reBodyGeneralSection.FindStringIndex(normalized); idx != nil {
		start := idx[0]
		// 从 "General" 行本身开始截取（匹配串可能含前导换行）
		generalAt := strings.Index(normalized[start:], "General")
		if generalAt < 0 {
			return "", body
		}
		tail := normalized[start+generalAt:]
		if reBodyOverallBitrate.MatchString(tail) && reBodyCompleteName.MatchString(tail) {
			media := strings.TrimSpace(tail)
			stripped := strings.TrimSpace(normalized[:start+generalAt])
			if media != "" {
				return media, stripped
			}
		}
	}

	return "", body
}

// EnsureMediainfoColumn 在 mediainfo 列为空时尝试从 body 提取补齐。
// 参数/返回：mediainfo/body 为当前列值；返回 (媒体文本, 是否命中)。
// 失败场景：不适用。
// 副作用：无。
func EnsureMediainfoColumn(mediainfo, body string) (string, bool) {
	if strings.TrimSpace(mediainfo) != "" {
		return mediainfo, false
	}
	media, _ := ExtractMediainfoFromBody(body)
	if strings.TrimSpace(media) == "" {
		return mediainfo, false
	}
	return media, true
}
