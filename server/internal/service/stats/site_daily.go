package stats

import (
	"sort"
	"time"
)

// GetSiteDailyUploadStats 返回按天统计的各站点上传增量（流量统计页"站点数据统计"数据源）。
// 参数/返回：days 为返回的天数窗口（默认 30，上限 365）；返回 dates（升序）与各站点的每日增量序列。
// 计算口径：相邻两次快照的累计上传差值；窗口首日以更早的历史快照为基线（无更早快照时为 0）。
// 说明：快照由后台采集线程每日写入（site_daily_stats），历史从部署后开始记录。
// 失败场景：数据库查询失败时返回错误。
// 副作用：仅读取 site_daily_stats 快照表。
func (s *Service) GetSiteDailyUploadStats(days int) (map[string]any, error) {
	if days <= 0 {
		days = 30
	}
	if days > 365 {
		days = 365
	}

	rows, err := s.repo.QuerySiteDailyStats("")
	if err != nil {
		return nil, err
	}

	// 全历史快照日期升序（服务停机导致的缺日不补齐，差值聚合仍正确）
	dates := make([]string, 0, 64)
	dateIndex := map[string]int{}
	for _, row := range rows {
		if _, exists := dateIndex[row.StatDate]; !exists {
			dateIndex[row.StatDate] = len(dates)
			dates = append(dates, row.StatDate)
		}
	}

	// 每站点各日期的累计值：仅填充真实存在的快照行，缺行在增量阶段沿用前值
	cumulative := map[string][]int64{}
	present := map[string][]bool{}
	torrentCount := map[string]int64{}
	for _, row := range rows {
		arr, exists := cumulative[row.SiteName]
		if !exists {
			arr = make([]int64, len(dates))
			cumulative[row.SiteName] = arr
			present[row.SiteName] = make([]bool, len(dates))
		}
		idx := dateIndex[row.StatDate]
		arr[idx] = row.TotalUploaded
		present[row.SiteName][idx] = true
		torrentCount[row.SiteName] = row.TorrentCount
	}

	// 每日增量：缺行沿用前值；快照存在但归零说明该站种子已清理，按 0 处理
	daily := map[string][]int64{}
	for site, arr := range cumulative {
		deltas := make([]int64, len(dates))
		prev := int64(0)
		for i := range dates {
			current := arr[i]
			if !present[site][i] {
				current = prev
			}
			delta := current - prev
			if delta < 0 {
				delta = 0
			}
			deltas[i] = delta
			prev = current
		}
		daily[site] = deltas
	}

	// 输出窗口：最后 days 个快照日期
	windowStart := 0
	if len(dates) > days {
		windowStart = len(dates) - days
	}
	windowDates := dates[windowStart:]

	type siteSeries struct {
		name  string
		daily []int64
		total int64
		count int64
	}
	series := make([]siteSeries, 0, len(daily))
	for site, deltas := range daily {
		window := deltas[windowStart:]
		total := int64(0)
		for _, value := range window {
			total += value
		}
		series = append(series, siteSeries{name: site, daily: window, total: total, count: torrentCount[site]})
	}
	sort.Slice(series, func(i, j int) bool { return series[i].total > series[j].total })

	result := make([]map[string]any, 0, len(series))
	for _, item := range series {
		result = append(result, map[string]any{
			"site_name":      item.name,
			"daily":          item.daily,
			"total_uploaded": item.total,
			"torrent_count":  item.count,
		})
	}

	return map[string]any{
		"dates":       windowDates,
		"sites":       result,
		"snapshot_at": time.Now().Format("2006-01-02 15:04:05"),
	}, nil
}
