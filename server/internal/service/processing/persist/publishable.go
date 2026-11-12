package persist

import (
	"encoding/json"
	"fmt"
	"strings"

	processingmedia "github.com/pt-nexus/server/internal/service/processing/media"
	processingrepair "github.com/pt-nexus/server/internal/service/processing/repair"
)

// 本文件提供"行是否可发布（维护通过）"的统一判定，供跨种列表（crossseed）
// 与一种多站列表（torrentdata）共用同一口径：
//   字段齐全且映射有效 + 无禁转标签 + 无无法识别标题 + MediaInfo 规范 + 简介规范。

// publishableFieldCategories 参与有效性校验的标准字段类别。
var publishableFieldCategories = []string{"type", "medium", "video_codec", "audio_codec", "resolution", "team", "source"}

// IsRowPublishableData 判断一行种子参数是否"维护通过"（可发布）。
// 参数/返回：item 为行数据 map（标准化字段、tags、title_components、mediainfo、body、
// is_reviewed、is_deleted）；reverseMappings 为反向映射表；返回是否可发布。
// 失败场景：不适用。
// 副作用：无。
func IsRowPublishableData(item map[string]any, reverseMappings map[string]any) bool {
	if boolFromAnyData(item["is_deleted"]) || !boolFromAnyData(item["is_reviewed"]) {
		return false
	}

	for _, category := range publishableFieldCategories {
		value := strings.TrimSpace(toStringData(item[category], ""))
		if value == "" {
			continue
		}
		if !isValidStandardValueData(value) || !isStandardValueMappedData(reverseMappings, category, value) {
			return false
		}
	}

	// MediaInfo 规范性：空或格式无效（既非 MediaInfo 也非 BDInfo）判不规范。
	mediainfo := strings.TrimSpace(toStringData(item["mediainfo"], ""))
	if mediainfo == "" {
		return false
	}
	if isMediainfo, isBDInfo, _ := processingmedia.ValidateMediaInfoFormat(mediainfo); !isMediainfo && !isBDInfo {
		return false
	}

	// 简介规范性：正文为空或缺关键字段（片名/产地/简介）判不规范。
	body := strings.TrimSpace(toStringData(item["body"], ""))
	if body == "" {
		return false
	}
	if completeness := processingrepair.CheckIntroBodyCompleteness(body); !completeness.IsComplete {
		return false
	}

	tags := parseStringArrayData(item["tags"])
	if hasRestrictedStandardTagData(tags) {
		return false
	}
	for _, tag := range tags {
		if !isValidStandardValueData(tag) || !isStandardValueMappedData(reverseMappings, "tags", tag) {
			return false
		}
	}

	return !hasUnrecognizedValueData(parseAnyArrayData(item["title_components"]))
}

// HasRowDataProblemData 判断行是否存在数据问题（错误分类口径，与可发布判定互补共用）。
// 参数/返回：同 IsRowDataPublishableData；返回是否存在问题。
// 失败场景：不适用。
// 副作用：无。
func HasRowDataProblemData(item map[string]any, reverseMappings map[string]any) bool {
	if hasUnrecognizedValueData(parseAnyArrayData(item["title_components"])) {
		return true
	}

	for _, category := range publishableFieldCategories {
		value := strings.TrimSpace(toStringData(item[category], ""))
		if value == "" {
			continue
		}
		if !isValidStandardValueData(value) || !isStandardValueMappedData(reverseMappings, category, value) {
			return true
		}
	}

	mediainfo := strings.TrimSpace(toStringData(item["mediainfo"], ""))
	if mediainfo == "" {
		return true
	}
	if isMediainfo, isBDInfo, _ := processingmedia.ValidateMediaInfoFormat(mediainfo); !isMediainfo && !isBDInfo {
		return true
	}
	body := strings.TrimSpace(toStringData(item["body"], ""))
	if body == "" {
		return true
	}
	if completeness := processingrepair.CheckIntroBodyCompleteness(body); !completeness.IsComplete {
		return true
	}
	return false
}

func isValidStandardValueData(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed == "" || (strings.Contains(trimmed, ".") && !strings.HasPrefix(trimmed, ".") && !strings.HasSuffix(trimmed, "."))
}

func isStandardValueMappedData(reverseMappings map[string]any, category string, standardValue string) bool {
	categoryMap := toStringMapAnyData(reverseMappings[category])
	if len(categoryMap) == 0 {
		return false
	}
	_, ok := categoryMap[standardValue]
	return ok
}

func toStringMapAnyData(value any) map[string]string {
	result := map[string]string{}
	switch typed := value.(type) {
	case map[string]string:
		return typed
	case map[string]any:
		for key, item := range typed {
			result[strings.TrimSpace(key)] = strings.TrimSpace(toStringData(item, ""))
		}
	case map[any]any:
		for key, item := range typed {
			result[strings.TrimSpace(toStringData(key, ""))] = strings.TrimSpace(toStringData(item, ""))
		}
	}
	return result
}

func hasRestrictedStandardTagData(tags []string) bool {
	for _, tag := range tags {
		switch strings.TrimSpace(tag) {
		case "禁转", "tag.禁转", "限转", "tag.限转", "分集", "tag.分集":
			return true
		}
	}
	return false
}

func hasUnrecognizedValueData(titleComponents []any) bool {
	return strings.TrimSpace(extractUnrecognizedData(titleComponents)) != ""
}

func extractUnrecognizedData(titleComponents []any) string {
	for _, raw := range titleComponents {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if toStringData(item["key"], "") == "无法识别" {
			return toStringData(item["value"], "")
		}
	}
	return ""
}

func parseStringArrayData(value any) []string {
	if value == nil {
		return []string{}
	}
	switch typed := value.(type) {
	case []string:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			trimmed := strings.TrimSpace(item)
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			trimmed := strings.TrimSpace(toStringData(item, ""))
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	case []byte:
		// MariaDB/MySQL driver 把 TEXT 列 Scan 成 []byte——按 JSON 解析。
		return parseStringArrayData(string(typed))
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return []string{}
		}
		parsed := []any{}
		if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
			return []string{}
		}
		return parseStringArrayData(parsed)
	default:
		return []string{}
	}
}

func parseAnyArrayData(value any) []any {
	if value == nil {
		return []any{}
	}
	if typed, ok := value.([]any); ok {
		return typed
	}
	if typed, ok := value.([]byte); ok {
		// MariaDB/MySQL driver 把 TEXT 列 Scan 成 []byte。
		return parseAnyArrayData(string(typed))
	}
	if typed, ok := value.(string); ok {
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return []any{}
		}
		parsed := []any{}
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
			return parsed
		}
	}
	return []any{}
}

func boolFromAnyData(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case []byte:
		trimmed := strings.TrimSpace(string(typed))
		return trimmed == "1" || strings.EqualFold(trimmed, "true")
	case string:
		trimmed := strings.TrimSpace(typed)
		return trimmed == "1" || strings.EqualFold(trimmed, "true")
	case int:
		return typed == 1
	case int64:
		return typed == 1
	case float64:
		return typed == 1
	default:
		return false
	}
}

func toStringData(value any, fallback string) string {
	switch typed := value.(type) {
	case nil:
		return fallback
	case []byte:
		// MariaDB/MySQL driver 把 TEXT 列 Scan 成 []byte——直接按字节转字符串，
		// 不能走 fmt.Sprintf（会输出 "[104 97 ...]" 数组字面量）。
		text := strings.TrimSpace(string(typed))
		if text == "" {
			return fallback
		}
		return text
	case string:
		if strings.TrimSpace(typed) == "" {
			return fallback
		}
		return typed
	default:
		text := strings.TrimSpace(fmt.Sprintf("%v", value))
		if text == "" {
			return fallback
		}
		return text
	}
}
