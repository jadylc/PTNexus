package repository

import (
	"errors"
	"fmt"
	"strings"
)

// TargetExistsQuery 定义“目标站已存在种子”本地检测输入。
// Hash 为内容指纹（最强）；Name+Size 为组指纹；TorrentID+SourceSite 为源站种子标识。
type TargetExistsQuery struct {
	Hash       string
	Name       string
	Size       int64 // -1 表示未知
	TorrentID  string
	SourceSite string
	TargetSite string
}

// ResolveSiteAliases 解析站点昵称/站点代码的全部别名（对外暴露，批量场景可预先解析一次复用）。
// 参数/返回：siteName 为站点昵称或站点代码；返回去重后的别名列表。
// 失败场景：sites 表不存在或查询失败时返回错误。
// 副作用：仅读取数据库。
func (r *MigrateRepository) ResolveSiteAliases(siteName string) ([]string, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil, errors.New("repo is nil")
	}
	return r.resolveSiteAliases(siteName)
}

// CheckTargetExists 在本地数据中判断目标站是否已存在该种子，返回命中来源与说明。
// 检测顺序：下载器快照（torrents.sites）→ 发布队列（publish_queue_tasks）→ 发种日志（publish_logs）。
// 返回：source 为 snapshot/queue/publish_log/空字符串；detail 为命中说明。
// 失败场景：仓库未初始化、目标站为空、SQL 错误时返回 error；对应表不存在时跳过该来源。
// 副作用：仅读取数据库。
func (r *MigrateRepository) CheckTargetExists(input TargetExistsQuery) (string, string, error) {
	targetSite := strings.TrimSpace(input.TargetSite)
	if targetSite == "" {
		return "", "", errors.New("target site is empty")
	}

	aliases, err := r.ResolveSiteAliases(targetSite)
	if err != nil {
		return "", "", err
	}
	return r.CheckTargetExistsWithAliases(input, aliases)
}

// CheckTargetExistsWithAliases 与 CheckTargetExists 等价，但站点别名由调用方预先解析（批量入队等循环场景避免每种子重复查询 sites 表）。
// aliases 为空时回退到目标站原始名称。
func (r *MigrateRepository) CheckTargetExistsWithAliases(input TargetExistsQuery, aliases []string) (string, string, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return "", "", errors.New("repo is nil")
	}

	targetSite := strings.TrimSpace(input.TargetSite)
	if targetSite == "" {
		return "", "", errors.New("target site is empty")
	}
	if len(aliases) == 0 {
		aliases = []string{targetSite}
	}

	// 1) 下载器快照：torrents 表已做种于目标站（hash 或 name+size 匹配）。
	source, detail, err := r.checkTargetExistsSnapshot(input, aliases)
	if err != nil {
		return "", "", err
	}
	if source != "" {
		return source, detail, nil
	}

	// 2) 发布队列：同源种子已有发往目标站的进行中或成功记录。
	if r.hasTableCached("publish_queue_tasks") {
		source, detail, err = r.checkTargetExistsQueue(input, aliases)
		if err != nil {
			return "", "", err
		}
		if source != "" {
			return source, detail, nil
		}
	}

	// 3) 发种日志：同源种子已有发往目标站的进行中或成功记录。
	if r.hasTableCached("publish_logs") {
		source, detail, err = r.checkTargetExistsPublishLog(input, aliases)
		if err != nil {
			return "", "", err
		}
		if source != "" {
			return source, detail, nil
		}
	}

	return "", "", nil
}

// resolveSiteAliases 解析站点昵称/站点代码的全部别名（与 CrossSeedRepository.ResolveSiteAliases 逻辑一致）。
func (r *MigrateRepository) resolveSiteAliases(siteName string) ([]string, error) {
	trimmed := strings.TrimSpace(siteName)
	if trimmed == "" {
		return []string{}, nil
	}

	type siteAliasRow struct {
		Site     string `gorm:"column:site"`
		Nickname string `gorm:"column:nickname"`
	}

	rows := make([]siteAliasRow, 0)
	query := `SELECT site, nickname FROM sites WHERE LOWER(nickname) = LOWER(?) OR LOWER(site) = LOWER(?)`
	if err := r.store.DB.Raw(query, trimmed, trimmed).Scan(&rows).Error; err != nil {
		return nil, err
	}

	aliases := make([]string, 0, len(rows)*2+1)
	seen := map[string]struct{}{}
	appendAlias := func(value string) {
		name := strings.TrimSpace(value)
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		aliases = append(aliases, name)
	}

	appendAlias(trimmed)
	for _, row := range rows {
		appendAlias(row.Nickname)
		appendAlias(row.Site)
	}

	return aliases, nil
}

// checkTargetExistsSnapshot 检测下载器快照中目标站做种情况。
// sites 字段支持单站点与逗号分隔多站点历史格式；别名命中采用精确匹配或逗号边界 LIKE 匹配。
func (r *MigrateRepository) checkTargetExistsSnapshot(input TargetExistsQuery, aliases []string) (string, string, error) {
	hash := strings.TrimSpace(input.Hash)
	name := strings.TrimSpace(input.Name)

	// 站点条件：精确相等，或以逗号为边界的子串（兼容 "siteA,siteB" 历史格式，容忍空格）。
	siteCondParts := make([]string, 0, len(aliases)*4)
	siteArgs := make([]any, 0, len(aliases)*4)
	for _, alias := range aliases {
		aliasLower := strings.ToLower(strings.TrimSpace(alias))
		if aliasLower == "" {
			continue
		}
		siteCondParts = append(siteCondParts,
			"LOWER(REPLACE(COALESCE(t.sites, ''), ' ', '')) = ?",
			"LOWER(REPLACE(COALESCE(t.sites, ''), ' ', '')) LIKE ?",
			"LOWER(REPLACE(COALESCE(t.sites, ''), ' ', '')) LIKE ?",
			"LOWER(REPLACE(COALESCE(t.sites, ''), ' ', '')) LIKE ?",
		)
		siteArgs = append(siteArgs, aliasLower, aliasLower+",%", "%,"+aliasLower, "%,"+aliasLower+",%")
	}
	if len(siteCondParts) == 0 {
		return "", "", nil
	}
	siteCondition := "(" + strings.Join(siteCondParts, " OR ") + ")"

	conds := make([]string, 0, 2)
	args := make([]any, 0, len(siteArgs)+4)
	args = append(args, siteArgs...)
	if hash != "" {
		conds = append(conds, "t.hash = ?")
		args = append(args, hash)
	}
	if name != "" {
		if input.Size > 0 {
			conds = append(conds, "LOWER(COALESCE(t.name, '')) = LOWER(?) AND t.size = ?")
			args = append(args, name, input.Size)
		} else {
			conds = append(conds, "LOWER(COALESCE(t.name, '')) = LOWER(?)")
			args = append(args, name)
		}
	}
	if len(conds) == 0 {
		return "", "", nil
	}

	row := struct {
		Name string `gorm:"column:name"`
	}{}
	query := fmt.Sprintf(`SELECT t.name
		FROM torrents t
		WHERE (t.is_hidden = 0 OR t.is_hidden IS NULL)
		  AND %s
		  AND (%s)
		LIMIT 1`, siteCondition, strings.Join(conds, " OR "))
	if err := r.store.DB.Raw(query, args...).Scan(&row).Error; err != nil {
		return "", "", err
	}

	if strings.TrimSpace(row.Name) == "" {
		return "", "", nil
	}
	detail := fmt.Sprintf("下载器快照显示该种子已在 %s 做种", strings.Join(aliases, "/"))
	return "snapshot", detail, nil
}

// checkTargetExistsQueue 检测发布队列中同源种子发往目标站的记录。
// 匹配优先级：torrent_id 精确匹配；缺失时按 title（种子名）精确匹配或 upload_data_json/context_json 中 hash 匹配。
func (r *MigrateRepository) checkTargetExistsQueue(input TargetExistsQuery, aliases []string) (string, string, error) {
	torrentID := strings.TrimSpace(input.TorrentID)
	name := strings.TrimSpace(input.Name)
	hash := strings.TrimSpace(input.Hash)

	fingerprintParts := make([]string, 0, 2)
	args := make([]any, 0, len(aliases)+8)
	if torrentID != "" {
		fingerprintParts = append(fingerprintParts, "pqt.torrent_id = ?")
		args = append(args, torrentID)
	}
	if name != "" {
		fingerprintParts = append(fingerprintParts, "LOWER(COALESCE(pqt.title, '')) = LOWER(?)")
		args = append(args, name)
	}
	if hash != "" {
		// 使用子串函数而非 LIKE：modernc/sqlite 的 LIKE 在模式含引号且不以 % 结尾时无法命中，
		// 且子串函数可保持 " 边界精确匹配（避免命中更长 hash 前缀）。
		hashFragment := `"hash":"` + strings.ToLower(hash) + `"`
		containsExpr := "instr(pqt.upload_data_json, ?) > 0"
		if r.store.DBType == "postgresql" {
			containsExpr = "strpos(pqt.upload_data_json, ?) > 0"
		}
		fingerprintParts = append(fingerprintParts, containsExpr, containsExpr)
		args = append(args, hashFragment, hashFragment)
	}
	if len(fingerprintParts) == 0 {
		return "", "", nil
	}

	for _, alias := range aliases {
		args = append(args, strings.ToLower(strings.TrimSpace(alias)))
	}
	aliasPlaceholders := strings.TrimSuffix(strings.Repeat("?, ", len(aliases)), ", ")

	conditionParts := []string{
		"(" + strings.Join(fingerprintParts, " OR ") + ")",
		fmt.Sprintf("LOWER(COALESCE(pqt.target_site, '')) IN (%s)", aliasPlaceholders),
	}
	if sourceSite := strings.TrimSpace(input.SourceSite); sourceSite != "" {
		conditionParts = append(conditionParts, "LOWER(COALESCE(pqt.source_site, '')) = ?")
		args = append(args, strings.ToLower(sourceSite))
	}
	conditionParts = append(conditionParts, "pqt.status IN (?, ?, ?)")
	args = append(args, PublishQueueStatusQueued, PublishQueueStatusRunning, PublishQueueStatusSuccess)

	row := struct {
		ID int64 `gorm:"column:id"`
	}{}
	query := fmt.Sprintf(`SELECT pqt.id
		FROM publish_queue_tasks pqt
		WHERE %s
		LIMIT 1`, strings.Join(conditionParts, " AND "))
	if err := r.store.DB.Raw(query, args...).Scan(&row).Error; err != nil {
		return "", "", err
	}

	if row.ID <= 0 {
		return "", "", nil
	}
	return "queue", fmt.Sprintf("发布队列中存在该种子发往 %s 的进行中/成功记录", strings.Join(aliases, "/")), nil
}

// checkTargetExistsPublishLog 检测发种日志中同源种子发往目标站的记录。
// 匹配优先级：torrent_id 精确匹配；缺失时按 title（种子名）精确匹配。
func (r *MigrateRepository) checkTargetExistsPublishLog(input TargetExistsQuery, aliases []string) (string, string, error) {
	torrentID := strings.TrimSpace(input.TorrentID)
	name := strings.TrimSpace(input.Name)

	fingerprintParts := make([]string, 0, 2)
	args := make([]any, 0, len(aliases)+8)
	if torrentID != "" {
		fingerprintParts = append(fingerprintParts, "pl.torrent_id = ?")
		args = append(args, torrentID)
	}
	if name != "" {
		fingerprintParts = append(fingerprintParts, "LOWER(COALESCE(pl.title, '')) = LOWER(?)")
		args = append(args, name)
	}
	if len(fingerprintParts) == 0 {
		return "", "", nil
	}

	for _, alias := range aliases {
		args = append(args, strings.ToLower(strings.TrimSpace(alias)))
	}
	aliasPlaceholders := strings.TrimSuffix(strings.Repeat("?, ", len(aliases)), ", ")

	conditionParts := []string{
		"(" + strings.Join(fingerprintParts, " OR ") + ")",
		fmt.Sprintf("LOWER(COALESCE(pl.target_site, '')) IN (%s)", aliasPlaceholders),
	}
	if sourceSite := strings.TrimSpace(input.SourceSite); sourceSite != "" {
		conditionParts = append(conditionParts, "LOWER(COALESCE(pl.source_site, '')) = ?")
		args = append(args, strings.ToLower(sourceSite))
	}
	conditionParts = append(conditionParts, "pl.status IN (?, ?, ?, ?, ?)")
	args = append(args,
		PublishQueueStatusQueued,
		PublishQueueStatusRunning,
		PublishQueueStatusSuccess,
		"exists",
		"edited",
	)

	row := struct {
		ID uint64 `gorm:"column:id"`
	}{}
	query := fmt.Sprintf(`SELECT pl.id
		FROM publish_logs pl
		WHERE %s
		LIMIT 1`, strings.Join(conditionParts, " AND "))
	if err := r.store.DB.Raw(query, args...).Scan(&row).Error; err != nil {
		return "", "", err
	}

	if row.ID <= 0 {
		return "", "", nil
	}
	return "publish_log", fmt.Sprintf("发种日志中存在该种子发往 %s 的进行中/成功记录", strings.Join(aliases, "/")), nil
}
