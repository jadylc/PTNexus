package fetch

import (
	"regexp"
	"sort"
	"strings"

	"gorm.io/gorm"
)

// AggregatedQueryInput 定义聚合种子查询输入参数。
type AggregatedQueryInput struct {
	Page              int
	PageSize          int
	NameSearch        string
	PathFilters       []string
	StateFilters      []string
	DownloaderFilters []string
}

// AggregatedQueryResult 定义聚合种子查询结果。
type AggregatedQueryResult struct {
	Items      []map[string]any
	Total      int
	Page       int
	PageSize   int
	TotalPages int
}

// QueryAggregatedTorrents 读取 torrents + seed_parameters 并按 name 聚合返回分页结果。
// 参数/返回：db 为 GORM 连接；input 为筛选与分页条件；返回聚合列表与分页信息。
// 失败场景：数据库查询异常时返回 error；若业务表不存在则返回空结果不报错。
// 副作用：仅执行数据库只读查询。
func QueryAggregatedTorrents(db *gorm.DB, input AggregatedQueryInput) (AggregatedQueryResult, error) {
	result := AggregatedQueryResult{
		Page:     input.Page,
		PageSize: input.PageSize,
		Items:    []map[string]any{},
	}
	if result.Page <= 0 {
		result.Page = 1
	}
	if result.PageSize <= 0 {
		result.PageSize = 50
	}
	if result.PageSize > 500 {
		result.PageSize = 500
	}
	nameSearch := strings.ToLower(strings.TrimSpace(input.NameSearch))

	rows := make([]map[string]any, 0)
	query := db.Table("torrents").
		Select("hash, name, save_path, size, progress, state, sites, details, downloader_id, last_seen").
		Where("state != ? AND (is_hidden = 0 OR is_hidden IS NULL)", "不存在")
	if nameSearch != "" {
		query = query.Where("LOWER(name) LIKE ?", "%"+nameSearch+"%")
	}
	if len(input.PathFilters) > 0 {
		query = query.Where("save_path IN ?", input.PathFilters)
	}
	if len(input.StateFilters) > 0 {
		query = query.Where("state IN ?", input.StateFilters)
	}
	if len(input.DownloaderFilters) > 0 {
		query = query.Where("downloader_id IN ?", input.DownloaderFilters)
	}
	if err := query.Order("name ASC").Scan(&rows).Error; err != nil {
		if isMissingTableError(err) {
			return result, nil
		}
		return result, err
	}

	existingNamesRaw := make([]string, 0)
	_ = db.Table("seed_parameters").
		Distinct("name").
		Where("name IS NOT NULL AND name != ''").
		Pluck("name", &existingNamesRaw).Error
	existingNames := map[string]struct{}{}
	for _, name := range existingNamesRaw {
		trimmed := strings.TrimSpace(name)
		if trimmed != "" {
			existingNames[trimmed] = struct{}{}
		}
	}

	aggMap := map[string]map[string]any{}
	for _, row := range rows {
		name := strings.TrimSpace(toStringAny(row["name"], ""))
		if name == "" {
			continue
		}
		agg, exists := aggMap[name]
		if !exists {
			agg = map[string]any{
				"name":           name,
				"save_path":      toStringAny(row["save_path"], ""),
				"size":           int64(toFloatAny(row["size"])),
				"progress":       toFloatAny(row["progress"]),
				"states":         []string{},
				"sites":          map[string]any{},
				"downloader_ids": []string{},
			}
			aggMap[name] = agg
		}

		state := strings.TrimSpace(toStringAny(row["state"], ""))
		if state != "" {
			agg["states"] = appendUniqueString(agg["states"].([]string), state)
		}
		downloaderID := strings.TrimSpace(toStringAny(row["downloader_id"], ""))
		if downloaderID != "" {
			agg["downloader_ids"] = appendUniqueString(agg["downloader_ids"].([]string), downloaderID)
		}
		siteName := strings.TrimSpace(toStringAny(row["sites"], ""))
		if siteName != "" {
			sitesMap := agg["sites"].(map[string]any)
			sitesMap[siteName] = map[string]any{
				"torrentId": extractTorrentIDFromDetails(toStringAny(row["details"], "")),
				"comment":   toStringAny(row["details"], ""),
			}
		}

		if current := toFloatAny(row["progress"]); current > toFloatAny(agg["progress"]) {
			agg["progress"] = current
		}
	}

	aggList := make([]map[string]any, 0, len(aggMap))
	for _, item := range aggMap {
		name := toStringAny(item["name"], "")
		_, exists := existingNames[name]
		item["is_cached"] = exists
		item["site_count"] = len(item["sites"].(map[string]any))
		item["state"] = strings.Join(item["states"].([]string), ",")
		aggList = append(aggList, item)
	}

	sort.SliceStable(aggList, func(i, j int) bool {
		return strings.ToLower(toStringAny(aggList[i]["name"], "")) < strings.ToLower(toStringAny(aggList[j]["name"], ""))
	})

	total := len(aggList)
	offset := (result.Page - 1) * result.PageSize
	if offset > total {
		offset = total
	}
	end := offset + result.PageSize
	if end > total {
		end = total
	}
	result.Items = aggList[offset:end]
	result.Total = total
	if result.PageSize > 0 {
		result.TotalPages = (total + result.PageSize - 1) / result.PageSize
	}
	return result, nil
}

func isMissingTableError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(text, "no such table") ||
		strings.Contains(text, "does not exist") ||
		strings.Contains(text, "undefined table")
}

func appendUniqueString(items []string, value string) []string {
	for _, item := range items {
		if strings.EqualFold(item, value) {
			return items
		}
	}
	return append(items, value)
}

// extractTorrentIDFromDetails 从详情页链接中提取站点内部 torrent_id。
// 参数/返回：details 为 torrents.details 列内容；返回站点内部 ID（数字、UUID 或字母数字），无有效 ID 时返回空串。
// 失败场景：不适用。
// 副作用：无。
func extractTorrentIDFromDetails(details string) string {
	trimmed := strings.TrimSpace(details)
	if trimmed == "" {
		return ""
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`id=(\d+)`),
		regexp.MustCompile(`torrent_id\s*[:=]\s*(\d+)`),
		regexp.MustCompile(`/details\.php\?id=(\d+)`),
		regexp.MustCompile(`id=([A-Za-z0-9_-]+)`),
	}
	for _, pattern := range patterns {
		if matches := pattern.FindStringSubmatch(trimmed); len(matches) > 1 {
			return matches[1]
		}
	}
	if matches := reUUIDInURL.FindStringSubmatch(trimmed); len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// reInfohash 匹配 40 位十六进制 infohash。
var reInfohash = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// isInfohash 判断给定值是否为 infohash（40 位十六进制）。
// 参数/返回：value 为待判断文本；返回是否命中 infohash 模式。
// 失败场景：不适用。
// 副作用：无。
func isInfohash(value string) bool {
	return reInfohash.MatchString(strings.TrimSpace(value))
}
