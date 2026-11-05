package stats

import "strings"

func (s *Service) GetSiteStats() ([]map[string]any, error) {
	rows, err := s.repo.QuerySiteStats()
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		result = append(result, map[string]any{
			"site_name":     row.SiteName,
			"total_size":    row.TotalSize,
			"torrent_count": row.TorrentCount,
		})
	}
	return result, nil
}

func (s *Service) GetGroupStats(siteName string) ([]map[string]any, error) {
	rows, err := s.repo.QueryGroupStats(siteName)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(rows)+16)
	for _, row := range rows {
		result = append(result, map[string]any{
			"site_name":     row.SiteName,
			"group_suffix":  normalizeGroupDisplay(row.GroupSuffix),
			"torrent_count": row.TorrentCount,
			"total_size":    row.TotalSize,
		})
	}

	// 未统计部分：未归属官组的组（逐组）与无官组（合计），统一挂在"未知站点"下，
	// 前端按站点聚合后自然成为一行"未知站点"，展开可见明细。
	// 站点筛选时不并入（用户明确在看某个站点的官组）。
	if strings.TrimSpace(siteName) == "" {
		ungrouped, ungroupedErr := s.repo.QueryUngroupedStats()
		if ungroupedErr == nil {
			for _, row := range ungrouped {
				display := normalizeGroupDisplay(row.GroupSuffix)
				if display == "" {
					continue
				}
				// 官组统计与未归属统计可能产生"同站点 + 同组名"的两行
				// （如月月/FRDS：一列来自 torrents.group，一列来自 title_components
				//  制作组补归）。合并计数，避免前端展开出现重复明细行。
				merged := false
				for _, existing := range result {
					if existing["site_name"] == row.SiteName && existing["group_suffix"] == display {
						existing["torrent_count"] = toInt64(existing["torrent_count"]) + row.TorrentCount
						existing["total_size"] = toInt64(existing["total_size"]) + row.TotalSize
						merged = true
						break
					}
				}
				if !merged {
					result = append(result, map[string]any{
						"site_name":     row.SiteName,
						"group_suffix":  display,
						"torrent_count": row.TorrentCount,
						"total_size":    row.TotalSize,
					})
				}
			}
		}
	}
	return result, nil
}

func toInt64(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}

func normalizeGroupDisplay(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	parts := strings.Split(trimmed, ",")
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(part), "-"))
		if item == "" {
			continue
		}
		normalized = append(normalized, item)
	}
	return strings.Join(normalized, ", ")
}
