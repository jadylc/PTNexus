package crossseed

import (
	"fmt"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/repository"
	"github.com/pt-nexus/server/internal/service/reversemapping"
)

var inactiveTorrentStates = []string{"未做种", "已暂停", "已停止", "错误", "等待", "队列"}

func stateRankExpr(alias string) string {
	quoted := make([]string, 0, len(inactiveTorrentStates))
	for _, value := range inactiveTorrentStates {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		quoted = append(quoted, fmt.Sprintf("'%s'", strings.ReplaceAll(trimmed, "'", "''")))
	}
	statesSQL := strings.Join(quoted, ", ")
	return fmt.Sprintf("CASE WHEN %s.state NOT IN (%s) THEN 0 ELSE 1 END", alias, statesSQL)
}

func normalizeReviewStatus(reviewStatus string) string {
	normalized := strings.ToLower(strings.TrimSpace(reviewStatus))
	switch normalized {
	case "reviewed", "unreviewed", "error":
		return normalized
	default:
		return ""
	}
}

func buildReviewStatusCondition(dbType string, reviewStatus string) (string, []any) {
	return "", nil
}

func buildCurrentTorrentsSubquery(dbType string) string {
	// Match Python build_current_torrents_subquery().
	// 收敛：ct 只 JOIN seed_parameters 中存在的 hash，全 torrents 聚合按 hash 去重时
	// 可显著缩小排序/分组范围；不存在于 seed_parameters 的 torrents 对 JOIN 结果无贡献。
	if strings.EqualFold(dbType, "postgresql") {
		return fmt.Sprintf(`
			SELECT DISTINCT ON (t.hash) t.hash, t.name, t.size, t.save_path, t.downloader_id, t.state, t.last_seen
			FROM torrents t
			WHERE (t.is_hidden = 0 OR t.is_hidden IS NULL)
			  AND EXISTS (SELECT 1 FROM seed_parameters sp WHERE sp.hash = t.hash)
			ORDER BY t.hash, %s, t.last_seen DESC
		`, stateRankExpr("t"))
	}
	return fmt.Sprintf(`
		SELECT t.hash, t.name, t.size, t.save_path, t.downloader_id, t.state, t.last_seen
		FROM torrents t
		JOIN (
			SELECT hash,
			       MIN(%s) AS state_rank,
			       MAX(last_seen) AS max_last_seen
			FROM torrents t2
			WHERE (t2.is_hidden = 0 OR t2.is_hidden IS NULL)
			  AND EXISTS (SELECT 1 FROM seed_parameters sp WHERE sp.hash = t2.hash)
			GROUP BY hash
		) ranked ON t.hash = ranked.hash
		WHERE %s = ranked.state_rank
		  AND t.last_seen = ranked.max_last_seen
		  AND (t.is_hidden = 0 OR t.is_hidden IS NULL)
	`, stateRankExpr("t2"), stateRankExpr("t"))
}

func (s *CrossSeedService) UniquePaths() (map[string]any, error) {
	query := `
		SELECT DISTINCT t.save_path
		FROM torrents t
		WHERE (t.is_hidden = 0 OR t.is_hidden IS NULL)
		  AND t.save_path IS NOT NULL AND t.save_path != ''
		  AND EXISTS (
			SELECT 1
			FROM seed_parameters sp
			WHERE sp.hash = t.hash
			  AND sp.type IN ('category.movie', 'category.tv_series', 'category.animation', 'category.documentaries', 'category.tv_shows')
		  )
		ORDER BY t.save_path
	`
	paths, err := s.repo.RawStrings(query, "save_path")
	if err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "unique_paths": paths}, nil
}

func (s *CrossSeedService) QueryData(params CrossSeedQueryParams) (map[string]any, error) {
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 20
	}
	if params.PageSize > 100 {
		params.PageSize = 100
	}

	// 30s 进程内缓存：跨种页每次请求都会执行无 LIMIT 全量分类查询与
	// 全 torrents 聚合派生表，短时间重复进入时会显著拖慢页面。
	key := queryCacheKey(params)
	if cached := s.getCachedQuery(key); cached != nil {
		logx.Infof("跨种查询", "查询缓存命中 page=%d page_size=%d review_status=%s",
			params.Page, params.PageSize, params.ReviewStatus)
		return cached, nil
	}

	result, err := s.queryDataUncached(params)
	if err != nil {
		return nil, err
	}
	s.setCachedQuery(key, result)
	return result, nil
}

func (s *CrossSeedService) queryDataUncached(params CrossSeedQueryParams) (map[string]any, error) {
	start := time.Now()
	offset := (params.Page - 1) * params.PageSize
	dbType := s.repo.DBType()
	normalizedReviewStatus := normalizeReviewStatus(params.ReviewStatus)
	if canUseReviewQueryFastPath(params, normalizedReviewStatus) {
		return s.queryReviewFilteredData(params, normalizedReviewStatus)
	}
	currentTorrentsSubquery := buildCurrentTorrentsSubquery(dbType)
	hasPublishQueueTable := s.repo.DB() != nil && s.repo.DB().Migrator().HasTable("publish_queue_tasks")
	hasPublishLogTable := s.repo.DB() != nil && s.repo.DB().Migrator().HasTable("publish_logs")

	fromClause := fmt.Sprintf(`
		FROM seed_parameters sp
		LEFT JOIN (%s) ct ON sp.hash = ct.hash
	`, currentTorrentsSubquery)
	fromArgs := make([]any, 0)

	whereConditions := make([]string, 0)
	args := make([]any, 0)

	// 过滤仅保留视频类型，排除电子书、有声书、音乐、软件、游戏等非视频内容
	whereConditions = append(whereConditions, "sp.type IN ('category.movie', 'category.tv_series', 'category.animation', 'category.documentaries', 'category.tv_shows')")

	search := strings.TrimSpace(params.Search)
	if search != "" {
		if strings.EqualFold(dbType, "postgresql") {
			whereConditions = append(whereConditions, "(sp.title ILIKE ? OR sp.torrent_id ILIKE ? OR sp.subtitle ILIKE ?)")
		} else {
			whereConditions = append(whereConditions, "(sp.title LIKE ? OR sp.torrent_id LIKE ? OR sp.subtitle LIKE ?)")
		}
		args = append(args, "%"+search+"%", "%"+search+"%", "%"+search+"%")
	}

	if len(params.PathFilters) > 0 {
		placeholders := make([]string, 0, len(params.PathFilters))
		for _, value := range params.PathFilters {
			trimmed := strings.TrimSpace(value)
			if trimmed == "" {
				continue
			}
			placeholders = append(placeholders, "?")
			args = append(args, trimmed)
		}
		if len(placeholders) > 0 {
			whereConditions = append(whereConditions, "ct.save_path IN ("+strings.Join(placeholders, ", ")+")")
		}
	}

	switch strings.TrimSpace(params.IsDeleted) {
	case "1":
		whereConditions = append(whereConditions, "ct.hash IS NULL")
	case "0":
		whereConditions = append(whereConditions, "ct.hash IS NOT NULL")
	}

	if normalizedReviewStatus == "" {
		if reviewCondition, reviewArgs := buildReviewStatusCondition(dbType, params.ReviewStatus); reviewCondition != "" {
			whereConditions = append(whereConditions, reviewCondition)
			args = append(args, reviewArgs...)
		}
	}
	// 将 review 判定的必要条件下推到 SQL，避免普通路径在无 LIMIT 时全量扫描：
	// - reviewed = is_publishable => 必需 is_reviewed=1 且未删除（ct 存在）
	// - unreviewed = 未删除且不可发布 => 必需未删除
	// - error = 已删除 => 必需 ct 不存在
	// 这些只是必要条件，最终仍由 Go 侧 matchesReviewStatus 精确判定，语义不变。
	switch normalizedReviewStatus {
	case "reviewed":
		whereConditions = append(whereConditions, "sp.is_reviewed = 1", "ct.hash IS NOT NULL")
	case "unreviewed":
		whereConditions = append(whereConditions, "ct.hash IS NOT NULL")
	case "error":
		whereConditions = append(whereConditions, "ct.hash IS NULL")
	}

	if excludedTarget := strings.TrimSpace(params.ExcludeTargetSites); excludedTarget != "" {
		excludeJoins, excludeJoinArgs, excludeCondition, excludeArgs, err := s.buildExcludeTargetCondition(dbType, excludedTarget, hasPublishQueueTable, hasPublishLogTable)
		if err != nil {
			return nil, err
		}
		fromClause += excludeJoins
		fromArgs = append(fromArgs, excludeJoinArgs...)
		whereConditions = append(whereConditions, excludeCondition)
		args = append(args, excludeArgs...)
	}

	whereClause := ""
	if len(whereConditions) > 0 {
		whereClause = "WHERE " + strings.Join(whereConditions, " AND ")
	}
	queryArgs := make([]any, 0, len(fromArgs)+len(args))
	queryArgs = append(queryArgs, fromArgs...)
	queryArgs = append(queryArgs, args...)

	type countRow struct {
		Total int64 `gorm:"column:total"`
	}
	var count countRow
	countElapsed := time.Duration(0)
	// reviewed/unreviewed 需要按标准映射在 Go 中判定可发布性，最终总数会由过滤结果给出；
	// 此时提前执行同一套重型 JOIN 的 COUNT 没有意义，只会让列表首屏重复扫描数据库。
	if normalizedReviewStatus == "" {
		countStart := time.Now()
		countQuery := fmt.Sprintf("SELECT COUNT(*) AS total %s %s", fromClause, whereClause)
		if err := s.repo.DB().Raw(countQuery, queryArgs...).Scan(&count).Error; err != nil {
			return nil, err
		}
		countElapsed = time.Since(countStart)
	}

	isDeletedExpr := "CASE WHEN ct.hash IS NULL THEN 1 ELSE 0 END AS is_deleted"
	if strings.EqualFold(dbType, "postgresql") {
		isDeletedExpr = "CASE WHEN ct.hash IS NULL THEN true ELSE false END AS is_deleted"
	}

	dataQuery := fmt.Sprintf(`
			SELECT sp.hash, sp.torrent_id, sp.site_name, sp.nickname,
			       COALESCE(ct.save_path, '') AS save_path,
			       ct.downloader_id AS downloader_id,
			       sp.title, sp.subtitle, sp.type, sp.medium, sp.video_codec,
			       sp.audio_codec, sp.resolution, sp.team, sp.source, sp.tags,
			       sp.title_components, sp.screenshot_review_status,
			       %s,
			       sp.is_reviewed, sp.updated_at
			%s
			%s
			ORDER BY sp.created_at DESC
		`, isDeletedExpr, fromClause, whereClause)

	dataArgs := queryArgs
	if normalizedReviewStatus == "" {
		dataQuery += "\n\t\t\tLIMIT ? OFFSET ?"
		dataArgs = make([]any, 0, len(queryArgs)+2)
		dataArgs = append(dataArgs, queryArgs...)
		dataArgs = append(dataArgs, params.PageSize, offset)
	}

	dataStart := time.Now()
	pageData, err := s.repo.RawMaps(dataQuery, dataArgs...)
	if err != nil {
		return nil, err
	}
	dataElapsed := time.Since(dataStart)

	reverseMappings := reversemapping.Build(nil)
	for _, item := range pageData {
		normalizeQueryRow(item, reverseMappings)
	}

	if normalizedReviewStatus != "" {
		filteredData := make([]map[string]any, 0, len(pageData))
		for _, item := range pageData {
			if matchesReviewStatus(item, normalizedReviewStatus) {
				filteredData = append(filteredData, item)
			}
		}
		count.Total = int64(len(filteredData))
		pageData = paginateMaps(filteredData, offset, params.PageSize)
	}

	finalResult, buildErr := s.buildCrossSeedQueryResult(params, pageData, int(count.Total), reverseMappings)
	if buildErr != nil {
		return nil, buildErr
	}
	logx.Infof("跨种查询", "general 查询完成 count=%s data=%s 总计=%s total=%d rows=%d",
		countElapsed, dataElapsed, time.Since(start), int(count.Total), len(pageData))
	return finalResult, nil
}

func (s *CrossSeedService) DeleteCrossSeedData(payload map[string]any) (map[string]any, int) {
	if rawItems, ok := payload["items"]; ok {
		items, ok := rawItems.([]any)
		if !ok {
			return map[string]any{"success": false, "error": "items 必须是数组"}, 400
		}
		if len(items) == 0 {
			return map[string]any{"success": false, "error": "项目列表不能为空"}, 400
		}
		deletedCount := int64(0)
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			torrentID := toString(item["torrent_id"], "")
			siteName := toString(item["site_name"], "")
			if torrentID == "" || siteName == "" {
				continue
			}
			// Python baseline increments deleted_count per valid item, regardless of DB rows affected.
			_, _ = s.repo.Exec("DELETE FROM seed_parameters WHERE torrent_id = ? AND site_name = ?", torrentID, siteName)
			deletedCount++
		}
		s.invalidateCache()
		return map[string]any{"success": true, "message": fmt.Sprintf("成功删除 %d 条数据", deletedCount), "deleted_count": deletedCount}, 200
	}

	torrentID := toString(payload["torrent_id"], "")
	siteName := toString(payload["site_name"], "")
	if torrentID == "" || siteName == "" {
		return map[string]any{"success": false, "error": "缺少必需参数: 单个删除需要 torrent_id 和 site_name，批量删除需要 items 数组"}, 400
	}
	_, err := s.repo.Exec("DELETE FROM seed_parameters WHERE torrent_id = ? AND site_name = ?", torrentID, siteName)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}, 500
	}
	s.invalidateCache()
	return map[string]any{"success": true, "message": fmt.Sprintf("种子数据 %s from %s 已删除", torrentID, siteName)}, 200
}

func buildAliasMatchCondition(dbType string, field string, aliases []string) string {
	if len(aliases) == 0 {
		return "1 = 0"
	}

	placeholders := make([]string, 0, len(aliases))
	for range aliases {
		placeholders = append(placeholders, "?")
	}

	if strings.EqualFold(dbType, "mysql") {
		return fmt.Sprintf("%s IN (%s)", field, strings.Join(placeholders, ", "))
	}
	return fmt.Sprintf("LOWER(COALESCE(%s, '')) IN (%s)", field, strings.Join(placeholders, ", "))
}

func aliasMatchArgs(dbType string, aliases []string) []any {
	result := make([]any, 0, len(aliases))
	for _, alias := range aliases {
		value := strings.TrimSpace(alias)
		if strings.EqualFold(dbType, "mysql") {
			result = append(result, value)
			continue
		}
		result = append(result, strings.ToLower(value))
	}
	return result
}

func (s *CrossSeedService) buildExcludeTargetCondition(dbType string, excludedTarget string, hasPublishQueueTable bool, hasPublishLogTable bool) (string, []any, string, []any, error) {
	targetAliases, err := s.repo.ResolveSiteAliases(excludedTarget)
	if err != nil {
		return "", nil, "", nil, fmt.Errorf("resolve target site aliases failed: %w", err)
	}
	if len(targetAliases) == 0 {
		targetAliases = []string{excludedTarget}
	}

	spNicknameCondition := buildAliasMatchCondition(dbType, "sp.nickname", targetAliases)
	spSiteCondition := buildAliasMatchCondition(dbType, "sp.site_name", targetAliases)
	targetGroupSiteCondition := buildAliasMatchCondition(dbType, "target_group_source.sites", targetAliases)
	aliasArgs := aliasMatchArgs(dbType, targetAliases)

	// target_groups 依赖 ct 的 name/size（seed_parameters 无 size 列），无法脱离 ct，
	// 只能保留物化派生表；但这里改为只对目标站匹配条件做派生，不再把 target_hashes
	// 一并物化，减少每次请求全 torrents 上的聚合开销。
	joins := fmt.Sprintf(`
		LEFT JOIN (
			SELECT DISTINCT target_group_source.name, target_group_source.size
			FROM torrents target_group_source
			WHERE (target_group_source.is_hidden = 0 OR target_group_source.is_hidden IS NULL)
			  AND %s
		) target_groups ON target_groups.name = ct.name AND target_groups.size = ct.size
	`, targetGroupSiteCondition)
	joinArgs := make([]any, 0, len(aliasArgs))
	joinArgs = append(joinArgs, aliasArgs...)
	// NOT EXISTS(target_hash_source) 位于条件最前，其 sites 占位符参数必须先入 args。
	args := make([]any, 0, len(aliasArgs))
	args = append(args, aliasArgs...)
	conditionParts := []string{
		"ct.hash IS NOT NULL",
		// target_hashes 语义：排除"目标站已有同名种子（torrents 中该 hash 的
		// sites 命中目标站）"。原实现会物化全量 DISTINCT hash 派生表，这里改为
		// 对 ct.hash 的相关 NOT EXISTS，直接利用 torrents(hash) 索引逐行判断，
		// 不再每次请求都物化全表。语义等价：原内层 EXISTS(seed_parameters) 在
		// ct.hash 关联下恒成立（ct 本身仅包含存在于 seed_parameters 的 hash）。
		fmt.Sprintf(`NOT EXISTS (
			SELECT 1
			FROM torrents target_hash_source
			WHERE target_hash_source.hash = ct.hash
			  AND (target_hash_source.is_hidden = 0 OR target_hash_source.is_hidden IS NULL)
			  AND %s
		)`, buildAliasMatchCondition(dbType, "target_hash_source.sites", targetAliases)),
		"target_groups.name IS NULL",
	}

	if hasPublishQueueTable {
		queueSiteCondition := buildAliasMatchCondition(dbType, "pqt.target_site", targetAliases)
		conditionParts = append(conditionParts, fmt.Sprintf(`NOT EXISTS (
			SELECT 1
			FROM publish_queue_tasks pqt
			WHERE pqt.torrent_id = sp.torrent_id
			  AND %s
			  AND pqt.status IN (?, ?, ?)
		)`, queueSiteCondition))
		args = append(args, aliasArgs...)
		args = append(args, repository.PublishQueueStatusQueued, repository.PublishQueueStatusRunning, repository.PublishQueueStatusSuccess)
	}

	if hasPublishLogTable {
		logSiteCondition := buildAliasMatchCondition(dbType, "pl.target_site", targetAliases)
		conditionParts = append(conditionParts, fmt.Sprintf(`NOT EXISTS (
			SELECT 1
			FROM publish_logs pl
			WHERE pl.torrent_id = sp.torrent_id
			  AND %s
			  AND pl.status = ?
		)`, logSiteCondition))
		args = append(args, aliasArgs...)
		args = append(args, "success")
	}

	conditionParts = append(conditionParts, fmt.Sprintf("NOT (%s OR %s)", spNicknameCondition, spSiteCondition))
	args = append(args, aliasArgs...)
	args = append(args, aliasArgs...)
	return joins, joinArgs, strings.Join(conditionParts, " AND "), args, nil
}
