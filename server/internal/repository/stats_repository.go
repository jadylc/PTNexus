package repository

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

type TrafficDeltaRow struct {
	TimeGroup    string  `gorm:"column:time_group"`
	DownloaderID string  `gorm:"column:downloader_id"`
	TotalUL      float64 `gorm:"column:total_ul"`
	TotalDL      float64 `gorm:"column:total_dl"`
}

type SpeedRow struct {
	TimeGroup    string  `gorm:"column:time_group"`
	DownloaderID string  `gorm:"column:downloader_id"`
	ULSpeed      float64 `gorm:"column:ul_speed"`
	DLSpeed      float64 `gorm:"column:dl_speed"`
}

type LatestSpeedRow struct {
	DownloaderID  string  `gorm:"column:downloader_id"`
	UploadSpeed   float64 `gorm:"column:upload_speed"`
	DownloadSpeed float64 `gorm:"column:download_speed"`
}

// TrafficStatRecord 表示一条待写入 traffic_stats 的原始采样记录。
type TrafficStatRecord struct {
	StatDatetime         time.Time
	DownloaderID         string
	Uploaded             int64
	Downloaded           int64
	UploadSpeed          int64
	DownloadSpeed        int64
	CumulativeUploaded   int64
	CumulativeDownloaded int64
}

// CumulativeSnapshot 表示某下载器最近一次累计流量快照。
type CumulativeSnapshot struct {
	CumulativeUploaded   int64
	CumulativeDownloaded int64
}

type SiteStatRow struct {
	SiteName     string `gorm:"column:site_name"`
	TotalSize    int64  `gorm:"column:total_size"`
	TorrentCount int64  `gorm:"column:torrent_count"`
}

// SiteUploadStatRow 表示按站点聚合的累计上传统计行。
type SiteUploadStatRow struct {
	SiteName      string `gorm:"column:site_name"`
	TotalUploaded int64  `gorm:"column:total_uploaded"`
	TorrentCount  int64  `gorm:"column:torrent_count"`
	TotalSize     int64  `gorm:"column:total_size"`
}

// SiteDailyStatRow 表示站点每日快照行（存各站点当日累计上传，用于计算每日增量）。
type SiteDailyStatRow struct {
	StatDate      string `gorm:"column:stat_date"`
	SiteName      string `gorm:"column:site_name"`
	TotalUploaded int64  `gorm:"column:total_uploaded"`
	TorrentCount  int64  `gorm:"column:torrent_count"`
}

type GroupStatRow struct {
	SiteName     string `gorm:"column:site_name"`
	GroupSuffix  string `gorm:"column:group_suffix"`
	TorrentCount int64  `gorm:"column:torrent_count"`
	TotalSize    int64  `gorm:"column:total_size"`
}

type StatsRepository struct {
	store *Store
}

func NewStatsRepository(store *Store) *StatsRepository {
	return &StatsRepository{store: store}
}

// EnsureTrafficStatsTables 确保流量原始表与小时聚合表存在。
// 参数/返回：无参数，成功返回 nil。
// 失败场景：数据库不可写或 SQL 执行失败时返回错误。
// 副作用：会在数据库中创建表结构（若不存在）。
func (r *StatsRepository) EnsureTrafficStatsTables() error {
	var trafficStatsSQL string
	var trafficStatsHourlySQL string

	switch r.store.DBType {
	case "mysql":
		trafficStatsSQL = `
			CREATE TABLE IF NOT EXISTS traffic_stats (
				stat_datetime DATETIME NOT NULL,
				downloader_id VARCHAR(64) NOT NULL,
				uploaded BIGINT DEFAULT 0,
				downloaded BIGINT DEFAULT 0,
				upload_speed BIGINT DEFAULT 0,
				download_speed BIGINT DEFAULT 0,
				cumulative_uploaded BIGINT NOT NULL DEFAULT 0,
				cumulative_downloaded BIGINT NOT NULL DEFAULT 0,
				PRIMARY KEY (stat_datetime, downloader_id)
			) ENGINE=InnoDB ROW_FORMAT=Dynamic
		`
		trafficStatsHourlySQL = `
			CREATE TABLE IF NOT EXISTS traffic_stats_hourly (
				stat_datetime DATETIME NOT NULL,
				downloader_id VARCHAR(64) NOT NULL,
				uploaded BIGINT DEFAULT 0,
				downloaded BIGINT DEFAULT 0,
				avg_upload_speed BIGINT DEFAULT 0,
				avg_download_speed BIGINT DEFAULT 0,
				samples INTEGER DEFAULT 0,
				cumulative_uploaded BIGINT NOT NULL DEFAULT 0,
				cumulative_downloaded BIGINT NOT NULL DEFAULT 0,
				PRIMARY KEY (stat_datetime, downloader_id)
			) ENGINE=InnoDB ROW_FORMAT=Dynamic
		`
	case "postgresql":
		trafficStatsSQL = `
			CREATE TABLE IF NOT EXISTS traffic_stats (
				stat_datetime TIMESTAMP NOT NULL,
				downloader_id VARCHAR(64) NOT NULL,
				uploaded BIGINT DEFAULT 0,
				downloaded BIGINT DEFAULT 0,
				upload_speed BIGINT DEFAULT 0,
				download_speed BIGINT DEFAULT 0,
				cumulative_uploaded BIGINT NOT NULL DEFAULT 0,
				cumulative_downloaded BIGINT NOT NULL DEFAULT 0,
				PRIMARY KEY (stat_datetime, downloader_id)
			)
		`
		trafficStatsHourlySQL = `
			CREATE TABLE IF NOT EXISTS traffic_stats_hourly (
				stat_datetime TIMESTAMP NOT NULL,
				downloader_id VARCHAR(64) NOT NULL,
				uploaded BIGINT DEFAULT 0,
				downloaded BIGINT DEFAULT 0,
				avg_upload_speed BIGINT DEFAULT 0,
				avg_download_speed BIGINT DEFAULT 0,
				samples INTEGER DEFAULT 0,
				cumulative_uploaded BIGINT NOT NULL DEFAULT 0,
				cumulative_downloaded BIGINT NOT NULL DEFAULT 0,
				PRIMARY KEY (stat_datetime, downloader_id)
			)
		`
	default:
		trafficStatsSQL = `
			CREATE TABLE IF NOT EXISTS traffic_stats (
				stat_datetime TEXT NOT NULL,
				downloader_id TEXT NOT NULL,
				uploaded INTEGER DEFAULT 0,
				downloaded INTEGER DEFAULT 0,
				upload_speed INTEGER DEFAULT 0,
				download_speed INTEGER DEFAULT 0,
				cumulative_uploaded INTEGER NOT NULL DEFAULT 0,
				cumulative_downloaded INTEGER NOT NULL DEFAULT 0,
				PRIMARY KEY (stat_datetime, downloader_id)
			)
		`
		trafficStatsHourlySQL = `
			CREATE TABLE IF NOT EXISTS traffic_stats_hourly (
				stat_datetime TEXT NOT NULL,
				downloader_id TEXT NOT NULL,
				uploaded INTEGER DEFAULT 0,
				downloaded INTEGER DEFAULT 0,
				avg_upload_speed INTEGER DEFAULT 0,
				avg_download_speed INTEGER DEFAULT 0,
				samples INTEGER DEFAULT 0,
				cumulative_uploaded INTEGER NOT NULL DEFAULT 0,
				cumulative_downloaded INTEGER NOT NULL DEFAULT 0,
				PRIMARY KEY (stat_datetime, downloader_id)
			)
		`
	}

	if err := r.store.DB.Exec(trafficStatsSQL).Error; err != nil {
		return err
	}
	if err := r.store.DB.Exec(trafficStatsHourlySQL).Error; err != nil {
		return err
	}
	return nil
}

// QueryLatestCumulativeByDownloader 查询每个下载器最近一条累计流量记录。
// 参数/返回：返回 downloader_id -> 累计值 的映射。
// 失败场景：查询失败时返回错误；表不存在时返回空映射且不报错。
// 副作用：无副作用，仅读取数据库。
// 性能说明：子查询 GROUP BY downloader_id 依赖 idx_traffic_stats_downloader_datetime
// (downloader_id, stat_datetime) 走 loose index scan，成本与采样行数无关。
func (r *StatsRepository) QueryLatestCumulativeByDownloader() (map[string]CumulativeSnapshot, error) {
	rows := make([]struct {
		DownloaderID         string `gorm:"column:downloader_id"`
		CumulativeUploaded   int64  `gorm:"column:cumulative_uploaded"`
		CumulativeDownloaded int64  `gorm:"column:cumulative_downloaded"`
	}, 0)

	query := `
		SELECT t.downloader_id, t.cumulative_uploaded, t.cumulative_downloaded
		FROM traffic_stats t
		JOIN (
			SELECT downloader_id, MAX(stat_datetime) AS max_dt
			FROM traffic_stats
			GROUP BY downloader_id
		) last ON t.downloader_id = last.downloader_id AND t.stat_datetime = last.max_dt
	`
	if err := r.store.DB.Raw(query).Scan(&rows).Error; err != nil {
		if isMissingTableError(err) {
			return map[string]CumulativeSnapshot{}, nil
		}
		return nil, err
	}

	result := map[string]CumulativeSnapshot{}
	for _, row := range rows {
		if strings.TrimSpace(row.DownloaderID) == "" {
			continue
		}
		result[row.DownloaderID] = CumulativeSnapshot{
			CumulativeUploaded:   row.CumulativeUploaded,
			CumulativeDownloaded: row.CumulativeDownloaded,
		}
	}
	return result, nil
}

// UpsertTrafficStats 批量写入原始流量采样数据。
// 参数/返回：records 为采样批次，返回成功写入条数。
// 失败场景：事务失败或 SQL 执行异常时回滚并返回错误。
// 副作用：写入/更新 traffic_stats 表数据。
func (r *StatsRepository) UpsertTrafficStats(records []TrafficStatRecord) (int, error) {
	if len(records) == 0 {
		return 0, nil
	}

	insertSQL := ""
	switch r.store.DBType {
	case "mysql":
		insertSQL = `
			INSERT INTO traffic_stats (
				stat_datetime, downloader_id, uploaded, downloaded,
				upload_speed, download_speed, cumulative_uploaded, cumulative_downloaded
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				uploaded = VALUES(uploaded),
				downloaded = VALUES(downloaded),
				upload_speed = VALUES(upload_speed),
				download_speed = VALUES(download_speed),
				cumulative_uploaded = VALUES(cumulative_uploaded),
				cumulative_downloaded = VALUES(cumulative_downloaded)
		`
	case "postgresql":
		insertSQL = `
			INSERT INTO traffic_stats (
				stat_datetime, downloader_id, uploaded, downloaded,
				upload_speed, download_speed, cumulative_uploaded, cumulative_downloaded
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(stat_datetime, downloader_id) DO UPDATE SET
				uploaded = EXCLUDED.uploaded,
				downloaded = EXCLUDED.downloaded,
				upload_speed = EXCLUDED.upload_speed,
				download_speed = EXCLUDED.download_speed,
				cumulative_uploaded = EXCLUDED.cumulative_uploaded,
				cumulative_downloaded = EXCLUDED.cumulative_downloaded
		`
	default:
		insertSQL = `
			INSERT INTO traffic_stats (
				stat_datetime, downloader_id, uploaded, downloaded,
				upload_speed, download_speed, cumulative_uploaded, cumulative_downloaded
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(stat_datetime, downloader_id) DO UPDATE SET
				uploaded = excluded.uploaded,
				downloaded = excluded.downloaded,
				upload_speed = excluded.upload_speed,
				download_speed = excluded.download_speed,
				cumulative_uploaded = excluded.cumulative_uploaded,
				cumulative_downloaded = excluded.cumulative_downloaded
		`
	}

	tx := r.store.DB.Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}

	for _, record := range records {
		timestamp := record.StatDatetime.Format("2006-01-02 15:04:05")
		if err := tx.Exec(
			insertSQL,
			timestamp,
			record.DownloaderID,
			record.Uploaded,
			record.Downloaded,
			record.UploadSpeed,
			record.DownloadSpeed,
			record.CumulativeUploaded,
			record.CumulativeDownloaded,
		).Error; err != nil {
			tx.Rollback()
			return 0, err
		}
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return len(records), nil
}

// AggregateHourlyTraffic 将历史原始流量按小时聚合到 traffic_stats_hourly，并清理旧原始记录。
// 参数/返回：retentionHours 为原始数据保留时长，返回聚合条数与清理条数。
// 失败场景：聚合查询、upsert 或删除失败时回滚并返回错误。
// 副作用：写入 traffic_stats_hourly 并删除 traffic_stats 中的旧数据。
func (r *StatsRepository) AggregateHourlyTraffic(retentionHours int) (int, int64, error) {
	if retentionHours <= 0 {
		retentionHours = 48
	}

	now := time.Now()
	cutoff := now.Add(-time.Duration(retentionHours) * time.Hour)
	safeCutoff := now.AddDate(0, 0, -3)
	safeCutoff = time.Date(safeCutoff.Year(), safeCutoff.Month(), safeCutoff.Day(), 0, 0, 0, 0, safeCutoff.Location())
	if cutoff.After(safeCutoff) {
		cutoff = safeCutoff
	}
	cutoffText := cutoff.Format("2006-01-02 15:04:05")
	groupExpr := r.hourlyGroupExpr()

	aggregatedRows := make([]struct {
		HourGroup        string  `gorm:"column:hour_group"`
		DownloaderID     string  `gorm:"column:downloader_id"`
		TotalUploaded    float64 `gorm:"column:total_uploaded"`
		TotalDownloaded  float64 `gorm:"column:total_downloaded"`
		AvgUploadSpeed   float64 `gorm:"column:avg_upload_speed"`
		AvgDownloadSpeed float64 `gorm:"column:avg_download_speed"`
		Samples          int64   `gorm:"column:samples"`
	}, 0)

	aggregateSQL := ""
	switch r.store.DBType {
	case "postgresql":
		aggregateSQL = fmt.Sprintf(`
			SELECT
				%s AS hour_group,
				downloader_id,
				GREATEST(0, (MAX(cumulative_uploaded) - MIN(cumulative_uploaded))::bigint) AS total_uploaded,
				GREATEST(0, (MAX(cumulative_downloaded) - MIN(cumulative_downloaded))::bigint) AS total_downloaded,
				AVG(upload_speed) AS avg_upload_speed,
				AVG(download_speed) AS avg_download_speed,
				COUNT(*) AS samples
			FROM traffic_stats
			WHERE stat_datetime < ?
			GROUP BY hour_group, downloader_id
		`, groupExpr)
	case "mysql":
		aggregateSQL = fmt.Sprintf(`
			SELECT
				%s AS hour_group,
				downloader_id,
				GREATEST(0, MAX(cumulative_uploaded) - MIN(cumulative_uploaded)) AS total_uploaded,
				GREATEST(0, MAX(cumulative_downloaded) - MIN(cumulative_downloaded)) AS total_downloaded,
				AVG(upload_speed) AS avg_upload_speed,
				AVG(download_speed) AS avg_download_speed,
				COUNT(*) AS samples
			FROM traffic_stats
			WHERE stat_datetime < ?
			GROUP BY hour_group, downloader_id
		`, groupExpr)
	default:
		aggregateSQL = fmt.Sprintf(`
			SELECT
				%s AS hour_group,
				downloader_id,
				CASE WHEN MAX(cumulative_uploaded) - MIN(cumulative_uploaded) > 0
					THEN MAX(cumulative_uploaded) - MIN(cumulative_uploaded)
					ELSE 0 END AS total_uploaded,
				CASE WHEN MAX(cumulative_downloaded) - MIN(cumulative_downloaded) > 0
					THEN MAX(cumulative_downloaded) - MIN(cumulative_downloaded)
					ELSE 0 END AS total_downloaded,
				AVG(upload_speed) AS avg_upload_speed,
				AVG(download_speed) AS avg_download_speed,
				COUNT(*) AS samples
			FROM traffic_stats
			WHERE stat_datetime < ?
			GROUP BY hour_group, downloader_id
		`, groupExpr)
	}
	if err := r.store.DB.Raw(aggregateSQL, cutoffText).Scan(&aggregatedRows).Error; err != nil {
		if isMissingTableError(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	if len(aggregatedRows) == 0 {
		return 0, 0, nil
	}

	cumulativeRows := make([]struct {
		HourGroup                 string `gorm:"column:hour_group"`
		DownloaderID              string `gorm:"column:downloader_id"`
		FinalCumulativeUploaded   int64  `gorm:"column:final_cumulative_uploaded"`
		FinalCumulativeDownloaded int64  `gorm:"column:final_cumulative_downloaded"`
	}, 0)
	cumulativeSQL := fmt.Sprintf(`
		SELECT
			%s AS hour_group,
			downloader_id,
			MAX(cumulative_uploaded) AS final_cumulative_uploaded,
			MAX(cumulative_downloaded) AS final_cumulative_downloaded
		FROM traffic_stats
		WHERE stat_datetime < ?
		GROUP BY hour_group, downloader_id
	`, groupExpr)
	if err := r.store.DB.Raw(cumulativeSQL, cutoffText).Scan(&cumulativeRows).Error; err != nil {
		if isMissingTableError(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}

	type cumulativeKey struct {
		HourGroup    string
		DownloaderID string
	}
	cumulativeMap := map[cumulativeKey]CumulativeSnapshot{}
	for _, row := range cumulativeRows {
		key := cumulativeKey{HourGroup: row.HourGroup, DownloaderID: row.DownloaderID}
		cumulativeMap[key] = CumulativeSnapshot{
			CumulativeUploaded:   row.FinalCumulativeUploaded,
			CumulativeDownloaded: row.FinalCumulativeDownloaded,
		}
	}

	upsertSQL := ""
	switch r.store.DBType {
	case "mysql":
		upsertSQL = `
			INSERT INTO traffic_stats_hourly (
				stat_datetime, downloader_id, uploaded, downloaded,
				avg_upload_speed, avg_download_speed, samples,
				cumulative_uploaded, cumulative_downloaded
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				uploaded = uploaded + VALUES(uploaded),
				downloaded = downloaded + VALUES(downloaded),
				avg_upload_speed = ((avg_upload_speed * samples) + (VALUES(avg_upload_speed) * VALUES(samples))) / (samples + VALUES(samples)),
				avg_download_speed = ((avg_download_speed * samples) + (VALUES(avg_download_speed) * VALUES(samples))) / (samples + VALUES(samples)),
				samples = samples + VALUES(samples),
				cumulative_uploaded = GREATEST(cumulative_uploaded, VALUES(cumulative_uploaded)),
				cumulative_downloaded = GREATEST(cumulative_downloaded, VALUES(cumulative_downloaded))
		`
	case "postgresql":
		upsertSQL = `
			INSERT INTO traffic_stats_hourly (
				stat_datetime, downloader_id, uploaded, downloaded,
				avg_upload_speed, avg_download_speed, samples,
				cumulative_uploaded, cumulative_downloaded
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(stat_datetime, downloader_id) DO UPDATE SET
				uploaded = traffic_stats_hourly.uploaded + EXCLUDED.uploaded,
				downloaded = traffic_stats_hourly.downloaded + EXCLUDED.downloaded,
				avg_upload_speed = ((traffic_stats_hourly.avg_upload_speed * traffic_stats_hourly.samples) + (EXCLUDED.avg_upload_speed * EXCLUDED.samples)) / (traffic_stats_hourly.samples + EXCLUDED.samples),
				avg_download_speed = ((traffic_stats_hourly.avg_download_speed * traffic_stats_hourly.samples) + (EXCLUDED.avg_download_speed * EXCLUDED.samples)) / (traffic_stats_hourly.samples + EXCLUDED.samples),
				samples = traffic_stats_hourly.samples + EXCLUDED.samples,
				cumulative_uploaded = GREATEST(traffic_stats_hourly.cumulative_uploaded, EXCLUDED.cumulative_uploaded),
				cumulative_downloaded = GREATEST(traffic_stats_hourly.cumulative_downloaded, EXCLUDED.cumulative_downloaded)
		`
	default:
		upsertSQL = `
			INSERT INTO traffic_stats_hourly (
				stat_datetime, downloader_id, uploaded, downloaded,
				avg_upload_speed, avg_download_speed, samples,
				cumulative_uploaded, cumulative_downloaded
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(stat_datetime, downloader_id) DO UPDATE SET
				uploaded = traffic_stats_hourly.uploaded + excluded.uploaded,
				downloaded = traffic_stats_hourly.downloaded + excluded.downloaded,
				avg_upload_speed = ((traffic_stats_hourly.avg_upload_speed * traffic_stats_hourly.samples) + (excluded.avg_upload_speed * excluded.samples)) / (traffic_stats_hourly.samples + excluded.samples),
				avg_download_speed = ((traffic_stats_hourly.avg_download_speed * traffic_stats_hourly.samples) + (excluded.avg_download_speed * excluded.samples)) / (traffic_stats_hourly.samples + excluded.samples),
				samples = traffic_stats_hourly.samples + excluded.samples,
				cumulative_uploaded = MAX(traffic_stats_hourly.cumulative_uploaded, excluded.cumulative_uploaded),
				cumulative_downloaded = MAX(traffic_stats_hourly.cumulative_downloaded, excluded.cumulative_downloaded)
		`
	}

	tx := r.store.DB.Begin()
	if tx.Error != nil {
		return 0, 0, tx.Error
	}

	for _, row := range aggregatedRows {
		key := cumulativeKey{HourGroup: row.HourGroup, DownloaderID: row.DownloaderID}
		cumulative := cumulativeMap[key]
		if err := tx.Exec(
			upsertSQL,
			row.HourGroup,
			row.DownloaderID,
			int64(row.TotalUploaded),
			int64(row.TotalDownloaded),
			int64(row.AvgUploadSpeed),
			int64(row.AvgDownloadSpeed),
			row.Samples,
			cumulative.CumulativeUploaded,
			cumulative.CumulativeDownloaded,
		).Error; err != nil {
			tx.Rollback()
			return 0, 0, err
		}
	}

	deleteResult := tx.Exec("DELETE FROM traffic_stats WHERE stat_datetime < ?", cutoffText)
	if deleteResult.Error != nil {
		tx.Rollback()
		return 0, 0, deleteResult.Error
	}

	if err := tx.Commit().Error; err != nil {
		return 0, 0, err
	}
	return len(aggregatedRows), deleteResult.RowsAffected, nil
}

func (r *StatsRepository) QueryTrafficDeltas(start, end time.Time, format string) ([]TrafficDeltaRow, error) {
	return r.queryTrafficDeltasTable("traffic_stats", start, end, format)
}

func (r *StatsRepository) QueryTrafficDeltasHourly(start, end time.Time, format string) ([]TrafficDeltaRow, error) {
	return r.queryTrafficDeltasTable("traffic_stats_hourly", start, end, format)
}

func (r *StatsRepository) queryTrafficDeltasTable(table string, start, end time.Time, format string) ([]TrafficDeltaRow, error) {
	groupExpr := r.timeGroupExpr(format)
	var query string
	switch r.store.DBType {
	case "postgresql":
		query = fmt.Sprintf(`
			SELECT %s AS time_group,
			       downloader_id,
			       GREATEST(0, (MAX(cumulative_uploaded) - MIN(cumulative_uploaded))::bigint) AS total_ul,
			       GREATEST(0, (MAX(cumulative_downloaded) - MIN(cumulative_downloaded))::bigint) AS total_dl
			FROM %s
			WHERE stat_datetime >= ? AND stat_datetime < ?
			GROUP BY time_group, downloader_id
			ORDER BY time_group
		`, groupExpr, table)
	case "mysql":
		query = fmt.Sprintf(`
			SELECT %s AS time_group,
			       downloader_id,
			       GREATEST(0, MAX(cumulative_uploaded) - MIN(cumulative_uploaded)) AS total_ul,
			       GREATEST(0, MAX(cumulative_downloaded) - MIN(cumulative_downloaded)) AS total_dl
			FROM %s
			WHERE stat_datetime >= ? AND stat_datetime < ?
			GROUP BY time_group, downloader_id
			ORDER BY time_group
		`, groupExpr, table)
	default:
		query = fmt.Sprintf(`
			SELECT %s AS time_group,
			       downloader_id,
			       CASE WHEN MAX(cumulative_uploaded) - MIN(cumulative_uploaded) > 0
			            THEN MAX(cumulative_uploaded) - MIN(cumulative_uploaded)
			            ELSE 0 END AS total_ul,
			       CASE WHEN MAX(cumulative_downloaded) - MIN(cumulative_downloaded) > 0
			            THEN MAX(cumulative_downloaded) - MIN(cumulative_downloaded)
			            ELSE 0 END AS total_dl
			FROM %s
			WHERE stat_datetime >= ? AND stat_datetime < ?
			GROUP BY time_group, downloader_id
			ORDER BY time_group
		`, groupExpr, table)
	}

	rows := make([]TrafficDeltaRow, 0)
	if err := r.store.DB.Raw(query, start, end).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *StatsRepository) QuerySpeedAverages(start, end time.Time, format string) ([]SpeedRow, error) {
	return r.querySpeedAveragesTable("traffic_stats", "upload_speed", "download_speed", start, end, format)
}

func (r *StatsRepository) QuerySpeedAveragesHourly(start, end time.Time, format string) ([]SpeedRow, error) {
	return r.querySpeedAveragesTable("traffic_stats_hourly", "avg_upload_speed", "avg_download_speed", start, end, format)
}

func (r *StatsRepository) querySpeedAveragesTable(table, ulCol, dlCol string, start, end time.Time, format string) ([]SpeedRow, error) {
	groupExpr := r.timeGroupExpr(format)
	query := fmt.Sprintf(`
		SELECT %s AS time_group,
		       downloader_id,
		       AVG(%s) AS ul_speed,
		       AVG(%s) AS dl_speed
		FROM %s
		WHERE stat_datetime >= ? AND stat_datetime < ?
		GROUP BY time_group, downloader_id
		ORDER BY time_group
	`, groupExpr, ulCol, dlCol, table)

	rows := make([]SpeedRow, 0)
	if err := r.store.DB.Raw(query, start, end).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *StatsRepository) QueryLatestSpeeds() ([]LatestSpeedRow, error) {
	query := `
		SELECT t1.downloader_id,
		       t1.upload_speed,
		       t1.download_speed
		FROM traffic_stats t1
		JOIN (
			SELECT downloader_id, MAX(stat_datetime) AS max_dt
			FROM traffic_stats
			GROUP BY downloader_id
		) t2 ON t1.downloader_id = t2.downloader_id AND t1.stat_datetime = t2.max_dt
	`
	rows := make([]LatestSpeedRow, 0)
	if err := r.store.DB.Raw(query).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *StatsRepository) QuerySiteStats() ([]SiteStatRow, error) {
	query := `
		SELECT sites AS site_name,
		       SUM(size) AS total_size,
		       COUNT(name) AS torrent_count
		FROM (
			SELECT DISTINCT name, size, sites
			FROM torrents
			WHERE sites IS NOT NULL AND sites != '' AND (is_hidden = 0 OR is_hidden IS NULL)
		) unique_torrents
		GROUP BY sites
		ORDER BY sites
	`
	rows := make([]SiteStatRow, 0)
	if err := r.store.DB.Raw(query).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// QuerySiteUploadStats 按站点聚合种库累计上传：上传量来自 torrent_upload_stats（按 hash 汇总），
// 站点归属来自 torrents.sites；返回按累计上传降序的列表。
// 参数/返回：无参数；返回站点名、累计上传、做种数量与总体积。
// 失败场景：数据库查询失败时返回错误。
// 副作用：仅读取，不写入。
func (r *StatsRepository) QuerySiteUploadStats() ([]SiteUploadStatRow, error) {
	query := `
		SELECT t.sites AS site_name,
		       COALESCE(SUM(u.total_uploaded), 0) AS total_uploaded,
		       COUNT(DISTINCT u.hash) AS torrent_count,
		       COALESCE(SUM(sizes.size), 0) AS total_size
		FROM (
			SELECT us.hash AS hash, SUM(us.uploaded) AS total_uploaded
			FROM torrent_upload_stats us
			WHERE (us.is_hidden = 0 OR us.is_hidden IS NULL)
			GROUP BY us.hash
		) u
		JOIN torrents t
		  ON t.hash = u.hash
		 AND (t.is_hidden = 0 OR t.is_hidden IS NULL)
		LEFT JOIN (
			SELECT hash, MAX(size) AS size
			FROM torrents
			GROUP BY hash
		) sizes ON sizes.hash = u.hash
		WHERE t.sites IS NOT NULL AND TRIM(t.sites) != ''
		GROUP BY t.sites
		ORDER BY total_uploaded DESC
	`
	rows := make([]SiteUploadStatRow, 0)
	if err := r.store.DB.Raw(query).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// EnsureSiteDailyStatsTable 确保站点每日快照表存在。
// 参数/返回：无参数，成功返回 nil。
// 失败场景：数据库不可写或 SQL 执行失败时返回错误。
// 副作用：会在数据库中创建表结构（若不存在）。
func (r *StatsRepository) EnsureSiteDailyStatsTable() error {
	var createSQL string
	switch r.store.DBType {
	case "mysql":
		createSQL = `
			CREATE TABLE IF NOT EXISTS site_daily_stats (
				stat_date DATE NOT NULL,
				site_name VARCHAR(128) NOT NULL,
				total_uploaded BIGINT NOT NULL DEFAULT 0,
				torrent_count INTEGER NOT NULL DEFAULT 0,
				PRIMARY KEY (stat_date, site_name)
			) ENGINE=InnoDB ROW_FORMAT=Dynamic
		`
	case "postgresql":
		createSQL = `
			CREATE TABLE IF NOT EXISTS site_daily_stats (
				stat_date DATE NOT NULL,
				site_name VARCHAR(128) NOT NULL,
				total_uploaded BIGINT NOT NULL DEFAULT 0,
				torrent_count INTEGER NOT NULL DEFAULT 0,
				PRIMARY KEY (stat_date, site_name)
			)
		`
	default:
		createSQL = `
			CREATE TABLE IF NOT EXISTS site_daily_stats (
				stat_date TEXT NOT NULL,
				site_name TEXT NOT NULL,
				total_uploaded INTEGER NOT NULL DEFAULT 0,
				torrent_count INTEGER NOT NULL DEFAULT 0,
				PRIMARY KEY (stat_date, site_name)
			)
		`
	}
	return r.store.DB.Exec(createSQL).Error
}

// UpsertSiteDailySnapshot 把当前各站点累计上传写入指定日期的快照（幂等：先删后插）。
// 参数/返回：statDate 为快照日期（YYYY-MM-DD）；成功返回 nil。
// 失败场景：聚合查询或写库失败时返回错误。
// 副作用：覆盖写 site_daily_stats 中该日期的行。
func (r *StatsRepository) UpsertSiteDailySnapshot(statDate string) error {
	rows, err := r.QuerySiteUploadStats()
	if err != nil {
		return err
	}
	return r.store.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("site_daily_stats").Where("stat_date = ?", statDate).Delete(nil).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		records := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			records = append(records, map[string]any{
				"stat_date":      statDate,
				"site_name":      row.SiteName,
				"total_uploaded": row.TotalUploaded,
				"torrent_count":  row.TorrentCount,
			})
		}
		return tx.Table("site_daily_stats").Create(records).Error
	})
}

// QuerySiteDailyStats 读取站点每日快照（可选起始日期，含当天）。
// 参数/返回：startDate 为空时读取全部；返回按日期升序的快照行。
// 失败场景：表不存在时返回空切片；查询失败返回错误。
// 副作用：仅读取。
func (r *StatsRepository) QuerySiteDailyStats(startDate string) ([]SiteDailyStatRow, error) {
	rows := make([]SiteDailyStatRow, 0)
	query := r.store.DB.Table("site_daily_stats").
		Select("stat_date, site_name, total_uploaded, torrent_count")
	if trimmed := strings.TrimSpace(startDate); trimmed != "" {
		query = query.Where("stat_date >= ?", trimmed)
	}
	if err := query.Order("stat_date ASC, site_name ASC").Scan(&rows).Error; err != nil {
		if isMissingTableError(err) {
			return []SiteDailyStatRow{}, nil
		}
		return nil, err
	}
	return rows, nil
}

func (r *StatsRepository) QueryGroupStats(siteName string) ([]GroupStatRow, error) {
	torrentGroupColumn := r.groupColumnRef("t")
	siteGroupColumn := r.groupColumnRef("s")
	normalizedTorrentGroup := r.normalizedGroupExpr(torrentGroupColumn)
	siteGroupMatchExpr := r.siteGroupMatchExpr("ut.group_suffix", siteGroupColumn)
	query := fmt.Sprintf(`
		SELECT s.nickname AS site_name,
		       ut.group_suffix AS group_suffix,
		       COUNT(*) AS torrent_count,
		       SUM(ut.size) AS total_size
		FROM (
			SELECT DISTINCT
			       t.name AS torrent_name,
			       t.size AS size,
			       %s AS group_suffix
			FROM torrents t
			WHERE %s IS NOT NULL
			  AND TRIM(%s) != ''
			  AND TRIM(%s) != ''
			  AND (t.is_hidden = 0 OR t.is_hidden IS NULL)
		) AS ut
		JOIN sites AS s ON %s
	`, normalizedTorrentGroup, torrentGroupColumn, torrentGroupColumn, normalizedTorrentGroup, siteGroupMatchExpr)

	args := make([]any, 0)
	trimmedSiteName := strings.TrimSpace(siteName)
	if trimmedSiteName != "" {
		query += `
		WHERE LOWER(COALESCE(s.nickname, '')) = LOWER(?) OR LOWER(COALESCE(s.site, '')) = LOWER(?)
		`
		args = append(args, trimmedSiteName, trimmedSiteName)
	}

	query += `
		GROUP BY s.nickname, ut.group_suffix
		ORDER BY s.nickname ASC, torrent_count DESC, ut.group_suffix ASC
	`

	rows := make([]GroupStatRow, 0)
	if err := r.store.DB.Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// QueryUngroupedStats 统计官组统计未覆盖的部分（口径：做种种子，与官组统计严格互补）。
//
// 判定流程（每个可见的 DISTINCT name+size 种子组）：
//  1. torrents.group 命中任一站点官组清单 → 已被 QueryGroupStats 统计，跳过；
//  2. 未命中，且 seed_parameters.title_components 的「制作组」组件有值（外部组名）
//     → 按该组名逐组统计（可从组名反推站点并补进官组清单）；
//  3. 未命中且没有制作组值 → 合并为一行「（无官组）」。
//
// 参数/返回：无参数；返回与 QueryGroupStats 同构的行，site_name 恒为 "未知站点"。
// 失败场景：读取 torrents/seed_parameters/sites 失败时返回错误（无表时降级为空）。
// 副作用：仅读取。
// 说明：title_components 是 JSON 数组 [{"key":"制作组","value":"BMDru"}]，各 DB 方言
// JSON 函数差异大，统一在 Go 侧解析；官组清单匹配也用 Go 侧 FIND_IN_SET 等价逻辑，
// 与 QueryGroupStats 的 SQL 口径一致，保证合并后恰好覆盖全部可见种子组、不重不漏。
func (r *StatsRepository) QueryUngroupedStats() ([]GroupStatRow, error) {
	type visibleRow struct {
		TorrentName string `gorm:"column:torrent_name"`
		Size        int64  `gorm:"column:size"`
		GroupSuffix string `gorm:"column:group_suffix"`
		Hash        string `gorm:"column:hash"`
	}
	type seedComponentRow struct {
		Hash            string `gorm:"column:hash"`
		Name            string `gorm:"column:name"`
		TitleComponents string `gorm:"column:title_components"`
	}
	type siteGroupRow struct {
		Nickname  string `gorm:"column:nickname"`
		SiteGroup string `gorm:"column:site_group"`
	}

	torrentGroupColumn := r.groupColumnRef("t")
	normalizedTorrentGroup := r.normalizedGroupExpr(torrentGroupColumn)

	// 可见种子逐行读取（不做 GROUP_CONCAT：MySQL 默认 group_concat_max_len=1024 会
	// 静默截断多 hash 组，导致关联 seed_parameters 漏掉）。Go 侧按 name+size 去重并
	// 收集该组全部 hash，与 QueryGroupStats 的 DISTINCT name+size 口径一致。
	visibleQuery := fmt.Sprintf(`
		SELECT t.name AS torrent_name,
		       t.size AS size,
		       %s AS group_suffix,
		       t.hash AS hash
		FROM torrents t
		WHERE t.name IS NOT NULL
		  AND TRIM(t.name) != ''
		  AND t.size IS NOT NULL
		  AND (t.is_hidden = 0 OR t.is_hidden IS NULL)
	`, normalizedTorrentGroup)
	visibleRows := make([]visibleRow, 0)
	if err := r.store.DB.Raw(visibleQuery).Scan(&visibleRows).Error; err != nil {
		if isMissingTableError(err) {
			return []GroupStatRow{}, nil
		}
		return nil, err
	}
	type visibleGroup struct {
		TorrentName string
		Size        int64
		GroupSuffix string
		Hashes      []string
	}
	visible := make([]visibleGroup, 0)
	visibleIndex := map[string]int{}
	for _, row := range visibleRows {
		key := groupKey(row.TorrentName, row.Size)
		idx, exists := visibleIndex[key]
		if !exists {
			idx = len(visible)
			visibleIndex[key] = idx
			visible = append(visible, visibleGroup{
				TorrentName: row.TorrentName,
				Size:        row.Size,
				GroupSuffix: row.GroupSuffix,
			})
		}
		if h := strings.TrimSpace(row.Hash); h != "" {
			visible[idx].Hashes = append(visible[idx].Hashes, h)
		}
	}

	// 制作组来源：seed_parameters.title_components（同一 hash 多站点记录时取任意非空值即可）。
	componentRows := make([]seedComponentRow, 0)
	componentQuery := `SELECT hash, name, title_components FROM seed_parameters WHERE title_components IS NOT NULL AND TRIM(title_components) != ''`
	if err := r.store.DB.Raw(componentQuery).Scan(&componentRows).Error; err != nil {
		if !isMissingTableError(err) {
			return nil, err
		}
	}
	teamByHash := map[string]string{}
	teamByName := map[string]string{}
	for _, row := range componentRows {
		team := extractTeamFromTitleComponents(row.TitleComponents)
		if team == "" {
			continue
		}
		if h := strings.TrimSpace(row.Hash); h != "" {
			if _, exists := teamByHash[h]; !exists {
				teamByHash[h] = team
			}
		}
		if n := strings.TrimSpace(row.Name); n != "" {
			if _, exists := teamByName[n]; !exists {
				teamByName[n] = team
			}
		}
	}

	// 站点官组清单：knownGroups 用于复刻 QueryGroupStats 的 FIND_IN_SET 匹配（判断哪些
	// 做种组已被官组统计覆盖）；groupToSite 用于把"制作组值本身是已知官组"的种子
	// 归回对应站点（避免 FRDS/MNHD-FRDS 这类明显官组被列进未知站点）。
	groupColumn := r.store.GroupColumn()
	siteRows := make([]siteGroupRow, 0)
	siteQuery := fmt.Sprintf(`SELECT nickname, %s AS site_group FROM sites`, groupColumn)
	if err := r.store.DB.Raw(siteQuery).Scan(&siteRows).Error; err != nil {
		if !isMissingTableError(err) {
			return nil, err
		}
	}
	knownGroups := map[string]struct{}{}
	groupToSite := map[string]string{}
	for _, site := range siteRows {
		for _, part := range strings.Split(site.SiteGroup, ",") {
			clean := strings.ToLower(strings.TrimLeft(strings.TrimSpace(part), "-"))
			clean = strings.TrimSpace(clean)
			if clean == "" {
				continue
			}
			knownGroups[clean] = struct{}{}
			if _, exists := groupToSite[clean]; !exists && strings.TrimSpace(site.Nickname) != "" {
				groupToSite[clean] = strings.TrimSpace(site.Nickname)
			}
		}
	}

	// matchKnownGroupSite 判断制作组值是否命中站点官组清单，返回归属站点：
	// 先精确匹配；再做长度≥3 的部分匹配（MNHD-FRDS / mUHD-FRDS 含 frds 应归月月），
	// 与 refreshGroupMatcher 的 Contains 部分匹配口径一致。
	matchKnownGroupSite := func(team string) string {
		norm := strings.ToLower(strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(team), "-")))
		if norm == "" {
			return ""
		}
		if site, ok := groupToSite[norm]; ok {
			return site
		}
		// 短 token（如 hd / 3d）不许参与部分匹配，避免误归站点
		if len(norm) < 3 {
			return ""
		}
		for group, site := range groupToSite {
			if len(group) < 3 {
				continue
			}
			if strings.Contains(norm, group) || strings.Contains(group, norm) {
				return site
			}
		}
		return ""
	}

	// 分桶：已归属站点（补进官组统计漏掉的行）/ 未知站点外部组 / 无官组。
	external := map[string]*GroupStatRow{}
	siteGroups := map[string]*GroupStatRow{}
	noGroup := &GroupStatRow{SiteName: "未知站点", GroupSuffix: "（无官组）"}
	for _, g := range visible {
		norm := strings.ToLower(strings.TrimSpace(g.GroupSuffix))
		if _, matched := knownGroups[norm]; matched {
			continue // 已被官组统计覆盖（严格同口径，避免重复计数）
		}
		team := ""
		for _, h := range g.Hashes {
			if v, ok := teamByHash[h]; ok {
				team = v
				break
			}
		}
		if team == "" {
			team = teamByName[strings.TrimSpace(g.TorrentName)]
		}
		if team == "" || strings.EqualFold(team, "nogroup") {
			// 未拉取种子：没有 seed_parameters.title_components，现成链路无从判定组名。
			// 用独立的名字兜底方法从 torrents.name 解析（不影响其他逻辑）：
			// 命中官组清单 → 归对应站点；清单外 token → 未知站点外部组；
			// 都扒不出 → 才计入（无官组）。
			site, parsedGroup, known := resolveGroupNameFromTorrentName(g.TorrentName, matchKnownGroupSite)
			if known {
				row, ok := siteGroups[site+"\x00"+parsedGroup]
				if !ok {
					row = &GroupStatRow{SiteName: site, GroupSuffix: parsedGroup}
					siteGroups[site+"\x00"+parsedGroup] = row
				}
				row.TorrentCount++
				row.TotalSize += g.Size
				continue
			}
			if parsedGroup != "" {
				row, ok := external[parsedGroup]
				if !ok {
					row = &GroupStatRow{SiteName: "未知站点", GroupSuffix: parsedGroup}
					external[parsedGroup] = row
				}
				row.TorrentCount++
				row.TotalSize += g.Size
				continue
			}
			noGroup.TorrentCount++
			noGroup.TotalSize += g.Size
			continue
		}
		// 制作组值本身是已知官组（含变形）→ 归回对应站点，不进未知站点。
		if site := matchKnownGroupSite(team); site != "" {
			row, ok := siteGroups[site+"\x00"+team]
			if !ok {
				row = &GroupStatRow{SiteName: site, GroupSuffix: team}
				siteGroups[site+"\x00"+team] = row
			}
			row.TorrentCount++
			row.TotalSize += g.Size
			continue
		}
		row, ok := external[team]
		if !ok {
			row = &GroupStatRow{SiteName: "未知站点", GroupSuffix: team}
			external[team] = row
		}
		row.TorrentCount++
		row.TotalSize += g.Size
	}

	rows := make([]GroupStatRow, 0, len(external)+len(siteGroups)+1)
	for _, row := range siteGroups {
		rows = append(rows, *row)
	}
	for _, row := range external {
		rows = append(rows, *row)
	}
	if noGroup.TorrentCount > 0 {
		rows = append(rows, *noGroup)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].TotalSize != rows[j].TotalSize {
			return rows[i].TotalSize > rows[j].TotalSize
		}
		return rows[i].GroupSuffix < rows[j].GroupSuffix
	})
	return rows, nil
}

// hashGroupConcatExpr 已移除：GROUP_CONCAT 在多 hash 分组下会被 MySQL 默认 1024 字节
// 上限静默截断，改为 Go 侧按 name+size 收集 hash，避免关联 seed_parameters 漏数据。

func groupKey(name string, size int64) string {
	return strings.TrimSpace(name) + "\x00" + strconv.FormatInt(size, 10)
}

// extractTeamFromTitleComponents 从标题组件 JSON 数组中提取「制作组」的 value。
// 参数/返回：raw 为 JSON 文本；找不到或解析失败返回空串。
func extractTeamFromTitleComponents(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var items []struct {
		Key   string `json:"key"`
		Value any    `json:"value"`
	}
	if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
		return ""
	}
	for _, item := range items {
		if strings.TrimSpace(item.Key) != "制作组" {
			continue
		}
		value := strings.TrimSpace(fmt.Sprint(item.Value))
		if value == "" || value == "<nil>" || strings.EqualFold(value, "nogroup") {
			return ""
		}
		return value
	}
	return ""
}

func (r *StatsRepository) groupColumnRef(alias string) string {
	switch r.store.DBType {
	case "postgresql":
		if strings.TrimSpace(alias) == "" {
			return `"group"`
		}
		return alias + `."group"`
	default:
		if strings.TrimSpace(alias) == "" {
			return "`group`"
		}
		return alias + ".`group`"
	}
}

func (r *StatsRepository) normalizedGroupExpr(column string) string {
	switch r.store.DBType {
	case "mysql":
		return fmt.Sprintf("TRIM(LEADING '-' FROM TRIM(%s))", column)
	case "postgresql":
		return fmt.Sprintf("LTRIM(BTRIM(%s), '-')", column)
	default:
		return fmt.Sprintf("LTRIM(TRIM(%s), '-')", column)
	}
}

func (r *StatsRepository) siteGroupMatchExpr(groupValueExpr string, siteGroupColumn string) string {
	normalizedSiteGroups := r.normalizedSiteGroupListExpr(siteGroupColumn)
	switch r.store.DBType {
	case "mysql":
		return fmt.Sprintf("FIND_IN_SET(LOWER(%s), %s) > 0", groupValueExpr, normalizedSiteGroups)
	default:
		return fmt.Sprintf("(',' || %s || ',') LIKE '%%,' || LOWER(%s) || ',%%'", normalizedSiteGroups, groupValueExpr)
	}
}

func (r *StatsRepository) normalizedSiteGroupListExpr(column string) string {
	switch r.store.DBType {
	case "mysql":
		return fmt.Sprintf(
			"REPLACE(TRIM(LEADING '-' FROM REPLACE(LOWER(COALESCE(%s, '')), ' ', '')), ',-', ',')",
			column,
		)
	case "postgresql":
		return fmt.Sprintf(
			"REPLACE(LTRIM(REPLACE(LOWER(COALESCE(%s, '')), ' ', ''), '-'), ',-', ',')",
			column,
		)
	default:
		return fmt.Sprintf(
			"REPLACE(LTRIM(REPLACE(LOWER(COALESCE(%s, '')), ' ', ''), '-'), ',-', ',')",
			column,
		)
	}
}

func (r *StatsRepository) timeGroupExpr(format string) string {
	switch r.store.DBType {
	case "mysql":
		mysqlFormat := strings.ReplaceAll(format, "%M", "%i")
		return "DATE_FORMAT(stat_datetime, '" + mysqlFormat + "')"
	case "postgresql":
		pgFormat := format
		pgFormat = strings.ReplaceAll(pgFormat, "%Y", "YYYY")
		pgFormat = strings.ReplaceAll(pgFormat, "%m", "MM")
		pgFormat = strings.ReplaceAll(pgFormat, "%d", "DD")
		pgFormat = strings.ReplaceAll(pgFormat, "%H", "HH24")
		pgFormat = strings.ReplaceAll(pgFormat, "%M", "MI")
		pgFormat = strings.ReplaceAll(pgFormat, "%S", "SS")
		return "TO_CHAR(stat_datetime, '" + pgFormat + "')"
	default:
		return "STRFTIME('" + format + "', stat_datetime)"
	}
}

func (r *StatsRepository) hourlyGroupExpr() string {
	switch r.store.DBType {
	case "mysql":
		return "DATE_FORMAT(stat_datetime, '%Y-%m-%d %H:00:00')"
	case "postgresql":
		return "TO_CHAR(stat_datetime, 'YYYY-MM-DD HH24:00:00')"
	default:
		return "STRFTIME('%Y-%m-%d %H:00:00', stat_datetime)"
	}
}
