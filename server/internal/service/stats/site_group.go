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

// GetSiteUploadStats 返回按站点聚合的累计上传统计（流量统计页"站点数据统计"数据源）。
// 参数/返回：无参数；返回站点名、累计上传、做种数量与总体积，按累计上传降序。
// 失败场景：数据库查询失败时返回错误。
// 副作用：仅读取。
func (s *Service) GetSiteUploadStats() ([]map[string]any, error) {
	rows, err := s.repo.QuerySiteUploadStats()
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		result = append(result, map[string]any{
			"site_name":      row.SiteName,
			"total_uploaded": row.TotalUploaded,
			"torrent_count":  row.TorrentCount,
			"total_size":     row.TotalSize,
		})
	}
	return result, nil
}

func (s *Service) GetGroupStats(siteName string) ([]map[string]any, error) {
	rows, err := s.repo.QueryGroupStats(siteName)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		result = append(result, map[string]any{
			"site_name":     row.SiteName,
			"group_suffix":  normalizeGroupDisplay(row.GroupSuffix),
			"torrent_count": row.TorrentCount,
			"total_size":    row.TotalSize,
		})
	}
	return result, nil
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
