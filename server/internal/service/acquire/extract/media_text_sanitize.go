package extract

import (
	"regexp"
	"strings"
)

var (
	reMediaTextDescriptionField = regexp.MustCompile(`(?i)^\s*Description\s*:`)
	// reMediaTextEncoderSettingsField 匹配 x264/x265 的编码器参数行。
	// 该行是编码参数（cabac/ref/bluray_compat/bitrate…），不含媒介信息，
	// 但 bluray_compat=0 里的 "bluray" 字样会被媒介推断当成蓝光标记——
	// 导致所有 x264/x265 源种被判成 medium.bluray，再被
	// NormalizeMediumByMediaType 按「bluray + 有 MediaInfo = 压片」改成
	// medium.encode（2026.11 实测：WEB-DL 种子被识别成 Encode）。
	reMediaTextEncoderSettingsField = regexp.MustCompile(`(?i)^\s*(Encoding settings|编码设置)\s*:`)
	reMediaTextSectionHeader        = regexp.MustCompile(`(?i)^(General|Video|Audio|Text|Menu|Chapters)(\s*#\d+)?$|^(DISC INFO|PLAYLIST REPORT|QUICK SUMMARY|VIDEO:|AUDIO:|SUBTITLES:|FILES:|CHAPTERS:|DISC SIZE)$`)
)

// SanitizeMediaTextForAnalysis 生成供校验/推断使用的媒体文本视图：
// 清空 Description 值，并整体剔除 Encoding settings（编码器参数）行。
// 参数/返回：text 为原始 MediaInfo/BDInfo 文本；返回保留结构但已净化关键字段的副本。
// 失败场景：输入为空时直接返回空字符串。
// 副作用：无；不会修改原始文本或调用方持有的数据。
func SanitizeMediaTextForAnalysis(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}

	normalized := strings.ReplaceAll(trimmed, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")
	result := make([]string, 0, len(lines))
	skipContinuation := false

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		if skipContinuation {
			switch {
			case trimmedLine == "":
				continue
			case reMediaTextSectionHeader.MatchString(trimmedLine):
				skipContinuation = false
			case strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t"):
				continue
			default:
				skipContinuation = false
			}
		}

		if reMediaTextDescriptionField.MatchString(line) {
			if idx := strings.Index(line, ":"); idx >= 0 {
				result = append(result, line[:idx+1])
			} else {
				result = append(result, strings.TrimRight(line, " \t"))
			}
			skipContinuation = true
			continue
		}

		// 编码器参数行整体剔除：不含媒介信息，且 bluray_compat 会污染媒介推断。
		if reMediaTextEncoderSettingsField.MatchString(line) {
			skipContinuation = true
			continue
		}

		result = append(result, line)
	}

	return strings.TrimSpace(strings.Join(result, "\n"))
}
