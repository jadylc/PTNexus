package crossseed

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func parseISOTime(raw string) (time.Time, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}
	return time.Parse("2006-01-02 15:04:05", value)
}

func parseStringArray(value any) []string {
	if value == nil {
		return []string{}
	}
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			entry := strings.TrimSpace(toString(item, ""))
			if entry != "" {
				result = append(result, entry)
			}
		}
		return result
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return []string{}
		}
		if strings.HasPrefix(trimmed, "[") {
			parsed := []string{}
			if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
				return parsed
			}
			parsedAny := []any{}
			if err := json.Unmarshal([]byte(trimmed), &parsedAny); err == nil {
				result := make([]string, 0, len(parsedAny))
				for _, item := range parsedAny {
					entry := strings.TrimSpace(toString(item, ""))
					if entry != "" {
						result = append(result, entry)
					}
				}
				return result
			}
		}
		parts := strings.Split(trimmed, ",")
		result := make([]string, 0, len(parts))
		for _, part := range parts {
			entry := strings.TrimSpace(part)
			if entry != "" {
				result = append(result, entry)
			}
		}
		return result
	default:
		return []string{}
	}
}

func parseAnyArray(value any) []any {
	if value == nil {
		return []any{}
	}
	if typed, ok := value.([]any); ok {
		return typed
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

func extractUnrecognized(titleComponents []any) string {
	for _, raw := range titleComponents {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if toString(item["key"], "") == "无法识别" {
			return toString(item["value"], "")
		}
	}
	return ""
}

func isValidStandardValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed == "" || (strings.Contains(trimmed, ".") && !strings.HasPrefix(trimmed, ".") && !strings.HasSuffix(trimmed, "."))
}

// IsInvalidStandardValue 判断标准列值是否无效（缺省格式或反查映射缺失）。
// 参数/返回：category 为列类别（type/medium/video_codec/audio_codec/resolution/team/source），
// value 为列值，reverseMappings 为反向映射表；返回是否无效（空值视为有效，不判错）。
// 失败场景：不适用。
// 副作用：无。
// 说明：供标题重算等跨服务场景复用，判定口径与列表"错误"分类一致。
func IsInvalidStandardValue(category string, value string, reverseMappings map[string]any) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	if !isValidStandardValue(trimmed) {
		return true
	}
	return !isStandardValueMapped(reverseMappings, category, trimmed)
}

// ExtractUnrecognizedValue 返回标题组件中的"无法识别"值（供跨服务复用）。
// 参数/返回：titleComponents 为标题组件切片；返回无法识别值（空串表示无）。
// 失败场景：不适用。
// 副作用：无。
func ExtractUnrecognizedValue(titleComponents []any) string {
	return extractUnrecognized(titleComponents)
}

func hasRestrictedStandardTag(tags []string) bool {
	for _, raw := range tags {
		switch strings.TrimSpace(raw) {
		case "禁转", "tag.禁转", "限转", "tag.限转", "分集", "tag.分集":
			return true
		}
	}
	return false
}

func hasUnrecognizedValue(titleComponents []any) bool {
	return strings.TrimSpace(extractUnrecognized(titleComponents)) != ""
}

func isRowPublishable(item map[string]any, reverseMappings map[string]any) bool {
	if boolFromAny(item["is_deleted"]) || !boolFromAny(item["is_reviewed"]) {
		return false
	}

	fieldCategories := []string{"type", "medium", "video_codec", "audio_codec", "resolution", "team", "source"}
	for _, category := range fieldCategories {
		value := strings.TrimSpace(toString(item[category], ""))
		if value == "" {
			continue
		}
		if !isValidStandardValue(value) || !isStandardValueMapped(reverseMappings, category, value) {
			return false
		}
	}

	tags := parseStringArray(item["tags"])
	if hasRestrictedStandardTag(tags) {
		return false
	}
	for _, tag := range tags {
		if !isValidStandardValue(tag) || !isStandardValueMapped(reverseMappings, "tags", tag) {
			return false
		}
	}

	return !hasUnrecognizedValue(parseAnyArray(item["title_components"]))
}

func isStandardValueMapped(reverseMappings map[string]any, category string, standardValue string) bool {
	categoryMap := toStringMapAny(reverseMappings[category])
	if len(categoryMap) == 0 {
		return false
	}
	_, ok := categoryMap[standardValue]
	return ok
}

func toStringMapAny(value any) map[string]string {
	result := map[string]string{}
	switch typed := value.(type) {
	case map[string]string:
		return typed
	case map[string]any:
		for key, item := range typed {
			result[strings.TrimSpace(key)] = strings.TrimSpace(toString(item, ""))
		}
	case map[any]any:
		for key, item := range typed {
			result[strings.TrimSpace(toString(key, ""))] = strings.TrimSpace(toString(item, ""))
		}
	}
	return result
}

func normalizeQueryRow(item map[string]any, reverseMappings map[string]any) {
	item["tags"] = parseStringArray(item["tags"])
	item["is_deleted"] = boolFromAny(item["is_deleted"])
	item["is_reviewed"] = boolFromAny(item["is_reviewed"])

	titleComponents := parseAnyArray(item["title_components"])
	item["unrecognized"] = extractUnrecognized(titleComponents)
	item["has_problem"] = hasRowDataProblem(titleComponents, item, reverseMappings)
	item["is_publishable"] = isRowPublishable(item, reverseMappings)
}

// hasRowDataProblem 判断该行是否存在需要人工修复的数据问题（"错误"类的判定依据）。
// 参数/返回：titleComponents 为标题组件；item 为当前行；reverseMappings 为反向映射表；
// 返回是否存在数据问题。
// 失败场景：不适用。
// 副作用：无。
// 说明：数据问题包含两类——标题组件含「无法识别」字段（标题解析失败），
// 或已填写的标准字段值非法/无法反向映射（映射缺失）。
// 禁转/限转/分集属业务约束而非数据错误，不计入问题；它们落回"待检查"由人工判断。
func hasRowDataProblem(titleComponents []any, item map[string]any, reverseMappings map[string]any) bool {
	if hasUnrecognizedValue(titleComponents) {
		return true
	}

	fieldCategories := []string{"type", "medium", "video_codec", "audio_codec", "resolution", "team", "source"}
	for _, category := range fieldCategories {
		value := strings.TrimSpace(toString(item[category], ""))
		if value == "" {
			continue
		}
		if !isValidStandardValue(value) || !isStandardValueMapped(reverseMappings, category, value) {
			return true
		}
	}
	return false
}

func paginateMaps(items []map[string]any, offset int, pageSize int) []map[string]any {
	if offset >= len(items) {
		return []map[string]any{}
	}
	end := offset + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// matchesReviewStatus 判断行是否命中指定检查状态。
// 参数/返回：item 为归一化后的行；reviewStatus 为 reviewed/unreviewed/error/deleted；
// 返回是否命中。
// 失败场景：不适用。
// 副作用：无。
// 说明：四类语义互斥且覆盖全部行——
//   - reviewed（已检查）：可发布（字段齐全、映射有效、无数据问题、无受限标签）；
//   - error（错误）：未删除但存在数据问题（无法识别字段 / 字段值未映射）；
//   - unreviewed（待检查）：未删除、无数据问题、但尚不可发布（未审核或业务受限）；
//   - deleted（已删除）：种子在下载器中已不存在。
func matchesReviewStatus(item map[string]any, reviewStatus string) bool {
	publishable, ok := item["is_publishable"].(bool)
	if !ok {
		publishable = false
	}
	hasProblem, ok := item["has_problem"].(bool)
	if !ok {
		hasProblem = false
	}
	deleted := boolFromAny(item["is_deleted"])

	switch reviewStatus {
	case "reviewed":
		return publishable
	case "unreviewed":
		return !deleted && !publishable && !hasProblem
	case "error":
		return !deleted && hasProblem
	case "deleted":
		return deleted
	default:
		return true
	}
}

func boolFromAny(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case *interface{}:
		if typed == nil {
			return false
		}
		return boolFromAny(*typed)
	case sql.NullBool:
		if !typed.Valid {
			return false
		}
		return typed.Bool
	case sql.NullInt64:
		if !typed.Valid {
			return false
		}
		return typed.Int64 != 0
	case []byte:
		// MySQL driver may return TINYINT(1)/BIT(1) as raw bytes like []byte{0x00} / []byte{0x01}
		// (not ASCII "0"/"1"), so handle that explicitly.
		if len(typed) == 1 {
			return typed[0] != 0
		}
		lower := strings.ToLower(strings.TrimSpace(string(typed)))
		if lower == "1" || lower == "true" || lower == "yes" {
			return true
		}
		if lower == "0" || lower == "false" || lower == "no" || lower == "" {
			return false
		}
		if parsed, err := strconv.ParseInt(lower, 10, 64); err == nil {
			return parsed != 0
		}
		return false
	case sql.RawBytes:
		raw := []byte(typed)
		if len(raw) == 1 {
			return raw[0] != 0
		}
		lower := strings.ToLower(strings.TrimSpace(string(raw)))
		if lower == "1" || lower == "true" || lower == "yes" {
			return true
		}
		if lower == "0" || lower == "false" || lower == "no" || lower == "" {
			return false
		}
		if parsed, err := strconv.ParseInt(lower, 10, 64); err == nil {
			return parsed != 0
		}
		return false
	case int:
		return typed != 0
	case int8:
		return typed != 0
	case int16:
		return typed != 0
	case int32:
		return typed != 0
	case int64:
		return typed != 0
	case uint:
		return typed != 0
	case uint8:
		return typed != 0
	case uint16:
		return typed != 0
	case uint32:
		return typed != 0
	case uint64:
		return typed != 0
	case float64:
		return typed != 0
	case string:
		lower := strings.ToLower(strings.TrimSpace(typed))
		if lower == "1" || lower == "true" || lower == "yes" {
			return true
		}
		if lower == "0" || lower == "false" || lower == "no" || lower == "" {
			return false
		}
		if parsed, err := strconv.ParseInt(lower, 10, 64); err == nil {
			return parsed != 0
		}
		return false
	default:
		return false
	}
}

func toSet(values []string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		result[trimmed] = struct{}{}
	}
	return result
}

func toString(value any, fallback string) string {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return fallback
		}
		return typed
	case nil:
		return fallback
	default:
		text := strings.TrimSpace(fmt.Sprintf("%v", value))
		if text == "" {
			return fallback
		}
		return text
	}
}
