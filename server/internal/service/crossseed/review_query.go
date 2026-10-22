package crossseed

import (
	"fmt"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/service/reversemapping"
)

func canUseReviewQueryFastPath(params CrossSeedQueryParams, reviewStatus string) bool {
	return reviewStatus != "" &&
		len(params.PathFilters) == 0 &&
		strings.TrimSpace(params.IsDeleted) == ""
}

// queryReviewFilteredData 先读取发布状态判定所需的轻量字段，分页后再补当前页展示字段。
// 这样 reviewed/unreviewed 首屏不再为每一行构造完整的当前下载器种子视图。
func (s *CrossSeedService) queryReviewFilteredData(params CrossSeedQueryParams, reviewStatus string) (map[string]any, error) {
	start := time.Now()
	dbType := s.repo.DBType()
	fromClause := "FROM seed_parameters sp"
	fromArgs := make([]any, 0)
	whereConditions := []string{
		"sp.type IN ('category.movie', 'category.tv_series', 'category.animation', 'category.documentaries', 'category.tv_shows')",
	}
	args := make([]any, 0)
	if search := strings.TrimSpace(params.Search); search != "" {
		if strings.EqualFold(dbType, "postgresql") {
			whereConditions = append(whereConditions, "(sp.title ILIKE ? OR sp.torrent_id ILIKE ? OR sp.subtitle ILIKE ?)")
		} else {
			whereConditions = append(whereConditions, "(sp.title LIKE ? OR sp.torrent_id LIKE ? OR sp.subtitle LIKE ?)")
		}
		args = append(args, "%"+search+"%", "%"+search+"%", "%"+search+"%")
	}

	isDeletedExpr := reviewIsDeletedExpr(dbType, "sp")
	if excludedTarget := strings.TrimSpace(params.ExcludeTargetSites); excludedTarget != "" {
		currentTorrentsSubquery := buildCurrentTorrentsSubquery(dbType)
		fromClause += fmt.Sprintf(" LEFT JOIN (%s) ct ON sp.hash = ct.hash", currentTorrentsSubquery)
		hasPublishQueueTable := s.repo.DB() != nil && s.repo.DB().Migrator().HasTable("publish_queue_tasks")
		hasPublishLogTable := s.repo.DB() != nil && s.repo.DB().Migrator().HasTable("publish_logs")
		excludeJoins, excludeJoinArgs, excludeCondition, excludeArgs, err := s.buildExcludeTargetCondition(dbType, excludedTarget, hasPublishQueueTable, hasPublishLogTable)
		if err != nil {
			return nil, err
		}
		fromClause += excludeJoins
		fromArgs = append(fromArgs, excludeJoinArgs...)
		whereConditions = append(whereConditions, excludeCondition)
		args = append(args, excludeArgs...)
		isDeletedExpr = "CASE WHEN ct.hash IS NULL THEN 1 ELSE 0 END AS is_deleted"
		if strings.EqualFold(dbType, "postgresql") {
			isDeletedExpr = "CASE WHEN ct.hash IS NULL THEN true ELSE false END AS is_deleted"
		}
	}

	// 将 reviewed/unreviewed/error 的判定必要条件下推到 SQL，缩小全量扫描范围：
	// - reviewed = is_publishable => 必需 is_reviewed=1 且未删除（存在非隐藏 torrent）
	// - unreviewed = 未删除 且 不可发布 => 必需未删除
	// - error = 已删除 => 必需不存在非隐藏 torrent
	// 这些条件只是"必要条件"，最终仍由 Go 侧 matchesReviewStatus 完成精确判定，语义不变。
	if strings.EqualFold(reviewStatus, "reviewed") {
		whereConditions = append(whereConditions, "sp.is_reviewed = 1")
		whereConditions = append(whereConditions, reviewExistsActiveTorrentCondition(dbType, "sp"))
	} else if strings.EqualFold(reviewStatus, "unreviewed") {
		whereConditions = append(whereConditions, reviewExistsActiveTorrentCondition(dbType, "sp"))
	} else if strings.EqualFold(reviewStatus, "error") {
		whereConditions = append(whereConditions, reviewMissingActiveTorrentCondition(dbType, "sp"))
	}

	classificationQuery := fmt.Sprintf(`
		SELECT sp.hash, sp.torrent_id, sp.site_name,
		       sp.type, sp.medium, sp.video_codec, sp.audio_codec, sp.resolution,
		       sp.team, sp.source, sp.tags, sp.title_components, sp.is_reviewed,
		       %s
		%s
		WHERE %s
		ORDER BY sp.created_at DESC
	`, isDeletedExpr, fromClause, strings.Join(whereConditions, " AND "))
	classificationArgs := make([]any, 0, len(fromArgs)+len(args))
	classificationArgs = append(classificationArgs, fromArgs...)
	classificationArgs = append(classificationArgs, args...)
	classificationRows, err := s.repo.RawMaps(classificationQuery, classificationArgs...)
	if err != nil {
		return nil, err
	}
	classElapsed := time.Since(start)

	reverseMappings := reversemapping.Build(nil)
	filteredRows := make([]map[string]any, 0, len(classificationRows))
	for _, item := range classificationRows {
		normalizeQueryRow(item, reverseMappings)
		if matchesReviewStatus(item, reviewStatus) {
			filteredRows = append(filteredRows, item)
		}
	}
	filterElapsed := time.Since(start)

	total := len(filteredRows)
	offset := (params.Page - 1) * params.PageSize
	pageRows := paginateMaps(filteredRows, offset, params.PageSize)
	pageData := make([]map[string]any, 0)
	if len(pageRows) > 0 {
		pageData, err = s.loadCrossSeedPageDetails(pageRows, reverseMappings)
		if err != nil {
			return nil, err
		}
	}
	detailElapsed := time.Since(start)

	finalResult, buildErr := s.buildCrossSeedQueryResult(params, pageData, total, reverseMappings)
	if buildErr != nil {
		return nil, buildErr
	}
	logx.Infof("跨种查询", "review 查询完成 分类=%s 过滤=%s 详情=%s 总计=%s 分类行=%d total=%d rows=%d",
		classElapsed, filterElapsed, detailElapsed, time.Since(start), len(classificationRows), total, len(pageData))
	return finalResult, nil
}

func reviewIsDeletedExpr(dbType string, seedAlias string) string {
	expression := fmt.Sprintf(`CASE WHEN EXISTS (
		SELECT 1 FROM torrents current_torrent
		WHERE current_torrent.hash = %s.hash
		  AND (current_torrent.is_hidden = 0 OR current_torrent.is_hidden IS NULL)
	) THEN 0 ELSE 1 END AS is_deleted`, seedAlias)
	if strings.EqualFold(dbType, "postgresql") {
		expression = fmt.Sprintf(`CASE WHEN EXISTS (
			SELECT 1 FROM torrents current_torrent
			WHERE current_torrent.hash = %s.hash
			  AND (current_torrent.is_hidden = 0 OR current_torrent.is_hidden IS NULL)
		) THEN false ELSE true END AS is_deleted`, seedAlias)
	}
	return expression
}

// reviewExistsActiveTorrentCondition 返回"存在未隐藏同名种子"的 SQL 条件，
// 用于把 reviewed/unreviewed 的"未删除"必要条件下推，缩小全量扫描范围。
func reviewExistsActiveTorrentCondition(dbType string, seedAlias string) string {
	return fmt.Sprintf(`EXISTS (
		SELECT 1 FROM torrents current_torrent
		WHERE current_torrent.hash = %s.hash
		  AND (current_torrent.is_hidden = 0 OR current_torrent.is_hidden IS NULL)
	)`, seedAlias)
}

// reviewMissingActiveTorrentCondition 返回"不存在未隐藏同名种子"的 SQL 条件，
// 用于把 error（已删除）的必要条件下推。
func reviewMissingActiveTorrentCondition(dbType string, seedAlias string) string {
	return fmt.Sprintf(`NOT EXISTS (
		SELECT 1 FROM torrents current_torrent
		WHERE current_torrent.hash = %s.hash
		  AND (current_torrent.is_hidden = 0 OR current_torrent.is_hidden IS NULL)
	)`, seedAlias)
}

func (s *CrossSeedService) loadCrossSeedPageDetails(pageRows []map[string]any, reverseMappings map[string]any) ([]map[string]any, error) {
	hashes := make([]string, 0, len(pageRows))
	hashSet := map[string]struct{}{}
	// 详情查询用 sp.hash IN 一次性取回当前页涉及的 seed_parameters 行，
	// 再在 Go 侧按 (hash, torrent_id, site_name) 精确过滤。
	// 之前这里拼 20 组 "(hash=? AND torrent_id=? AND site_name=?) OR ..."：
	// 优化器为了省掉 ORDER BY created_at 的排序会改走 created_at 索引做全表扫描，
	// 每行评估 20 组 OR，详情查询因此退化到 2~11 秒（SLOW SQL >= 1s，rows 仅 20）。
	identitySet := make(map[string]struct{}, len(pageRows))
	for _, item := range pageRows {
		hash := strings.TrimSpace(toString(item["hash"], ""))
		torrentID := strings.TrimSpace(toString(item["torrent_id"], ""))
		siteName := strings.TrimSpace(toString(item["site_name"], ""))
		if hash == "" || torrentID == "" || siteName == "" {
			continue
		}
		if _, exists := hashSet[hash]; !exists {
			hashSet[hash] = struct{}{}
			hashes = append(hashes, hash)
		}
		identitySet[hash+"\x00"+torrentID+"\x00"+siteName] = struct{}{}
	}
	if len(hashes) == 0 {
		return []map[string]any{}, nil
	}

	currentTorrentsSubquery, currentArgs := buildCurrentTorrentsSubqueryForHashes(s.repo.DBType(), hashes)
	isDeletedExpr := "CASE WHEN ct.hash IS NULL THEN 1 ELSE 0 END AS is_deleted"
	if strings.EqualFold(s.repo.DBType(), "postgresql") {
		isDeletedExpr = "CASE WHEN ct.hash IS NULL THEN true ELSE false END AS is_deleted"
	}
	placeholders := make([]string, len(hashes))
	for index := range placeholders {
		placeholders[index] = "?"
	}
	detailQuery := fmt.Sprintf(`
		SELECT sp.hash, sp.torrent_id, sp.site_name, sp.nickname,
		       COALESCE(ct.save_path, '') AS save_path,
		       ct.downloader_id AS downloader_id,
		       sp.title, sp.subtitle, sp.type, sp.medium, sp.video_codec,
		       sp.audio_codec, sp.resolution, sp.team, sp.source, sp.tags,
		       sp.title_components, sp.screenshot_review_status,
		       %s,
		       sp.is_reviewed, sp.updated_at
		FROM seed_parameters sp
		LEFT JOIN (%s) ct ON sp.hash = ct.hash
		WHERE sp.hash IN (%s)
		ORDER BY sp.created_at DESC
	`, isDeletedExpr, currentTorrentsSubquery, strings.Join(placeholders, ", "))
	detailArgs := make([]any, 0, len(currentArgs)+len(hashes))
	detailArgs = append(detailArgs, currentArgs...)
	for _, hash := range hashes {
		detailArgs = append(detailArgs, hash)
	}
	detailRows, err := s.repo.RawMaps(detailQuery, detailArgs...)
	if err != nil {
		return nil, err
	}
	pageData := make([]map[string]any, 0, len(pageRows))
	for _, item := range detailRows {
		identity := strings.TrimSpace(toString(item["hash"], "")) + "\x00" +
			strings.TrimSpace(toString(item["torrent_id"], "")) + "\x00" +
			strings.TrimSpace(toString(item["site_name"], ""))
		if _, ok := identitySet[identity]; !ok {
			continue
		}
		normalizeQueryRow(item, reverseMappings)
		pageData = append(pageData, item)
	}
	return pageData, nil
}

func buildCurrentTorrentsSubqueryForHashes(dbType string, hashes []string) (string, []any) {
	placeholders := make([]string, len(hashes))
	for index := range placeholders {
		placeholders[index] = "?"
	}
	hashFilter := strings.Join(placeholders, ", ")
	if strings.EqualFold(dbType, "postgresql") {
		return fmt.Sprintf(`
			SELECT DISTINCT ON (t.hash) t.hash, t.name, t.size, t.save_path, t.downloader_id, t.state, t.last_seen
			FROM torrents t
			WHERE (t.is_hidden = 0 OR t.is_hidden IS NULL)
			  AND t.hash IN (%s)
			ORDER BY t.hash, %s, t.last_seen DESC
		`, hashFilter, stateRankExpr("t")), stringsToAny(hashes)
	}

	args := stringsToAny(hashes)
	args = append(args, stringsToAny(hashes)...)
	return fmt.Sprintf(`
		SELECT t.hash, t.name, t.size, t.save_path, t.downloader_id, t.state, t.last_seen
		FROM torrents t
		JOIN (
			SELECT hash,
			       MIN(%s) AS state_rank,
			       MAX(last_seen) AS max_last_seen
			FROM torrents t2
			WHERE (t2.is_hidden = 0 OR t2.is_hidden IS NULL)
			  AND t2.hash IN (%s)
			GROUP BY hash
		) ranked ON t.hash = ranked.hash
		WHERE %s = ranked.state_rank
		  AND t.last_seen = ranked.max_last_seen
		  AND (t.is_hidden = 0 OR t.is_hidden IS NULL)
		  AND t.hash IN (%s)
	`, stateRankExpr("t2"), hashFilter, stateRankExpr("t"), hashFilter), args
}

func stringsToAny(values []string) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func (s *CrossSeedService) buildCrossSeedQueryResult(params CrossSeedQueryParams, pageData []map[string]any, total int, reverseMappings map[string]any) (map[string]any, error) {
	targetSites, err := s.repo.RawStrings("SELECT nickname FROM sites WHERE migration IN (2, 3) ORDER BY nickname", "nickname")
	if err != nil {
		targetSites = []string{}
	}
	result := map[string]any{
		"success":          true,
		"data":             pageData,
		"count":            len(pageData),
		"total":            total,
		"page":             params.Page,
		"page_size":        params.PageSize,
		"reverse_mappings": reverseMappings,
		"target_sites":     targetSites,
	}
	if params.IncludeUniquePaths {
		uniquePaths, uniquePathErr := s.UniquePaths()
		if uniquePathErr == nil {
			if values, ok := uniquePaths["unique_paths"]; ok {
				result["unique_paths"] = values
			}
		}
	}
	return result, nil
}
