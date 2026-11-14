package torrentdata

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/pt-nexus/server/internal/repository"
)

// GetData 聚合维护种子列表数据并按查询条件分页返回。
// 参数/返回：ctx 为请求上下文，客户端断开后数据库查询可取消；params 为页面、筛选、排序和 metadata 控制参数；返回前端表格数据及可选筛选元数据。
// 失败场景：站点配置、种子列表或筛选元数据查询失败时返回错误。
// 副作用：仅读取数据库和运行配置，不写入数据。
func (s *TorrentDataService) GetData(ctx context.Context, params TorrentsDataParams) (map[string]any, error) {
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 50
	}
	if params.PageSize > 200 {
		params.PageSize = 200
	}

	if params.MetadataOnly {
		return s.getDataMetadataResponse(ctx, params)
	}

	siteConfigs, err := s.repo.ListSiteConfigs(ctx)
	if err != nil {
		return nil, fmt.Errorf("load site configs failed: %w", err)
	}
	siteConfigMap := map[string]repository.SiteConfig{}
	targetSites := map[string]struct{}{}
	for _, item := range siteConfigs {
		siteConfigMap[item.Nickname] = item
		if item.Migration != 2 && item.Migration != 3 {
			continue
		}
		// 可转种目标站要求凭据可用：Cookie 或 Pass Key 任一非空（与目标站
		// 下拉过滤同口径）——未配置凭据的站发布必失败，不计入可转种数量。
		if strings.TrimSpace(item.Cookie) == "" && strings.TrimSpace(item.Passkey) == "" {
			continue
		}
		targetSites[item.Nickname] = struct{}{}
	}

	allDiscoveredSites, err := s.repo.ListAllDiscoveredSites(ctx)
	if err != nil {
		return nil, fmt.Errorf("load discovered sites failed: %w", err)
	}

	listFilters := repository.TorrentListFilters{
		OnlyCompleted:     params.OnlyCompleted,
		NameSearch:        params.NameSearch,
		PathFilters:       params.PathFilters,
		StateFilters:      params.StateFilters,
		DownloaderFilters: params.DownloaderFilters,
		ExcludeExisting:   params.ExcludeExisting,
	}
	databasePaginated := canPrePaginateTorrentGroups(params) && !params.OnlyPublishable
	databasePaginatedTotal := 0
	if databasePaginated {
		groupKeys, groupErr := s.repo.ListTorrentGroupKeysWithFilters(ctx, listFilters)
		if groupErr != nil {
			return nil, fmt.Errorf("load torrent group keys failed: %w", groupErr)
		}
		sortTorrentGroupKeys(groupKeys, params.SortProp, params.SortOrder)
		databasePaginatedTotal = len(groupKeys)
		groupKeys = paginateTorrentGroupKeys(groupKeys, params.Page, params.PageSize)
		if len(groupKeys) == 0 {
			listFilters = repository.TorrentListFilters{Groups: []repository.TorrentNameSizeKey{}}
		} else {
			// 分组键已经完成筛选，只按当前页键读取明细，避免再次扫描完成状态和已维护分组。
			listFilters = repository.TorrentListFilters{Groups: groupKeys}
		}
	}

	torrentRows := make([]repository.TorrentRecord, 0)
	if !databasePaginated || len(listFilters.Groups) > 0 {
		torrentRows, err = s.repo.ListTorrentsWithFilters(ctx, listFilters)
	}
	if err != nil {
		return nil, fmt.Errorf("load torrents failed: %w", err)
	}
	uploadTotals, err := s.repo.UploadTotalsByHashes(ctx, torrentRecordHashes(torrentRows))
	if err != nil {
		uploadTotals = map[string]int64{}
	}

	// OnlyPublishable：只显示"维护通过"的种子。维护通过的权威标记就是
	// seed_parameters.is_reviewed=1（用户点修改完成/批量检测通过时写入，
	// 两处共用同一校验口径），据此建立 hash 集合，组内任一副本命中即显示。
	// 判定只做纯字符串集合查找，不做字段/映射/类型转换，避免 MariaDB driver
	// 行形态差异影响结果。
	reviewedHashes := map[string]struct{}{}
	if params.OnlyPublishable {
		reviewedHashes, err = s.repo.ListReviewedHashes(ctx)
		if err != nil {
			return nil, fmt.Errorf("load reviewed hashes failed: %w", err)
		}
	}

	aggregated := map[string]*torrentSummary{}
	for _, row := range torrentRows {
		key := fmt.Sprintf("%s\x00%d", row.Name, row.Size)
		item, exists := aggregated[key]
		if !exists {
			item = &torrentSummary{
				Name:          row.Name,
				SavePath:      row.SavePath,
				Size:          row.Size,
				Progress:      row.Progress,
				StateSet:      map[string]struct{}{},
				Sites:         map[string]*siteSummary{},
				TotalUploaded: 0,
				Seeders:       row.Seeders,
				DownloaderIDs: []string{},
			}
			aggregated[key] = item
		}
		if item.SavePath == "" && row.SavePath != "" {
			item.SavePath = row.SavePath
		}
		if row.Progress > item.Progress {
			item.Progress = row.Progress
		}
		if row.State != "" {
			item.StateSet[row.State] = struct{}{}
		}
		if row.Seeders > item.Seeders {
			item.Seeders = row.Seeders
		}
		if row.Downloader != "" && !containsString(item.DownloaderIDs, row.Downloader) {
			item.DownloaderIDs = append(item.DownloaderIDs, row.Downloader)
		}

		uploaded := uploadTotals[row.Hash]
		item.TotalUploaded += uploaded

		siteName := strings.TrimSpace(row.Sites)
		if siteName == "" {
			continue
		}
		siteItem, exists := item.Sites[siteName]
		if !exists {
			migration := 0
			if config, ok := siteConfigMap[siteName]; ok {
				migration = config.Migration
			}
			siteItem = &siteSummary{Migration: migration}
			item.Sites[siteName] = siteItem
		}
		if params.OnlyPublishable {
			if _, ok := reviewedHashes[row.Hash]; ok {
				siteItem.Reviewed = true
			}
		}
		siteItem.Uploaded += uploaded
		siteItem.Comment = row.Details
		siteItem.State = row.State
		if row.Seeders > siteItem.Seeders {
			siteItem.Seeders = row.Seeders
		}
	}

	// 按聚合键排序后遍历：Go map 遍历顺序随机，直接 range 会让同值排序结果不稳定
	// （按做种数/大小排序时，同值行的相对顺序每次请求都不同，翻页重复/漏项）。
	// 这里先按 name+size 排序聚合键，保证输出基线顺序确定，再由 sortData 二次排序。
	aggregatedKeys := make([]string, 0, len(aggregated))
	for key := range aggregated {
		aggregatedKeys = append(aggregatedKeys, key)
	}
	sort.Slice(aggregatedKeys, func(i, j int) bool {
		left := aggregated[aggregatedKeys[i]]
		right := aggregated[aggregatedKeys[j]]
		if left.Name != right.Name {
			return customNameLess(left.Name, right.Name)
		}
		return left.Size < right.Size
	})

	allItems := make([]map[string]any, 0, len(aggregated))
	for _, key := range aggregatedKeys {
		value := aggregated[key]
		states := make([]string, 0, len(value.StateSet))
		for state := range value.StateSet {
			states = append(states, state)
		}
		sort.Strings(states)

		// Python baseline keeps the first-seen downloader order; don't sort.
		downloaderIDs := append([]string{}, value.DownloaderIDs...)

		sites := map[string]any{}
		existingSites := map[string]struct{}{}
		for siteName, details := range value.Sites {
			existingSites[siteName] = struct{}{}
			siteData := map[string]any{
				"uploaded":  details.Uploaded,
				"comment":   details.Comment,
				"migration": details.Migration,
				"state":     details.State,
				"seeders":   details.Seeders,
			}
			// OnlyPublishable 查询时附带每个站点副本的"维护通过"标记，
			// 供前端源站选择限定候选。
			if params.OnlyPublishable {
				siteData["reviewed"] = details.Reviewed
			}
			sites[siteName] = siteData
		}
		targetSiteCount := 0
		for target := range targetSites {
			if _, exists := existingSites[target]; !exists {
				targetSiteCount++
			}
		}

		item := map[string]any{
			"name":                     value.Name,
			"save_path":                value.SavePath,
			"size":                     value.Size,
			"progress":                 value.Progress,
			"state":                    strings.Join(states, ", "),
			"sites":                    sites,
			"total_uploaded":           value.TotalUploaded,
			"size_formatted":           formatBytes(value.Size),
			"total_uploaded_formatted": formatBytes(value.TotalUploaded),
			"site_count":               len(value.Sites),
			"total_site_count":         len(allDiscoveredSites),
			"target_sites_count":       targetSiteCount,
			"seeders":                  value.Seeders,
			"downloaderIds":            downloaderIDs,
			"downloader_ids":           downloaderIDs,
			"downloaderId":             s.selectBestDownloader(downloaderIDs),
			"unique_id":                fmt.Sprintf("%s_%d", value.Name, value.Size),
		}
		allItems = append(allItems, item)
	}

	postFilterParams := params
	postFilterParams.ExcludeExisting = false
	filtered := s.applyFilters(allItems, postFilterParams, siteConfigMap)

	// OnlyPublishable 精确判定：组内必须存在"维护通过"的站点副本
	// （SQL 预筛只保证存在 is_reviewed=1 的副本，映射/简介/MediaInfo 在此把关）。
	if params.OnlyPublishable {
		publishable := make([]map[string]any, 0, len(filtered))
		for _, item := range filtered {
			sites, _ := item["sites"].(map[string]any)
			for _, site := range sites {
				siteMap, ok := site.(map[string]any)
				if !ok {
					continue
				}
				if reviewed, _ := siteMap["reviewed"].(bool); reviewed {
					publishable = append(publishable, item)
					break
				}
			}
		}
		filtered = publishable
	}

	s.sortData(filtered, params.SortProp, params.SortOrder)

	total := len(filtered)
	paginated := filtered
	if databasePaginated {
		total = databasePaginatedTotal
	} else {
		start := (params.Page - 1) * params.PageSize
		if start > total {
			start = total
		}
		end := start + params.PageSize
		if end > total {
			end = total
		}
		paginated = filtered[start:end]
	}

	result := map[string]any{
		"data":     paginated,
		"total":    total,
		"page":     params.Page,
		"pageSize": params.PageSize,
	}
	if !params.SkipMetadata {
		metadata, err := s.dataMetadata(ctx, params, allDiscoveredSites)
		if err != nil {
			return nil, err
		}
		for key, value := range metadata {
			result[key] = value
		}
	}
	return result, nil
}

// canPrePaginateTorrentGroups 判断当前筛选和排序能否在聚合明细前先分页。
// 参数/返回：params 为列表参数；返回 true 时只需读取当前页 name+size 分组的明细。
// 失败场景：无；依赖站点明细或聚合数值的筛选/排序会返回 false 并走兼容路径。
// 副作用：无。
// 说明：ExcludedSourceSites（优先级弹窗禁用站点）依赖行级站点明细判断
// "所有源站点都被禁用"，不能在数据库层预分页，否则禁用过滤失效。
func canPrePaginateTorrentGroups(params TorrentsDataParams) bool {
	if len(params.SourceAvailabilityFilters) > 0 || len(params.ExistSiteNames) > 0 || len(params.NotExistSiteNames) > 0 {
		return false
	}
	if len(params.ExcludedSourceSites) > 0 {
		return false
	}
	sortProp := strings.TrimSpace(params.SortProp)
	switch sortProp {
	case "", "name", "size", "size_formatted":
		return true
	default:
		return false
	}
}

func sortTorrentGroupKeys(groups []repository.TorrentNameSizeKey, sortProp string, sortOrder string) {
	descending := strings.EqualFold(strings.TrimSpace(sortOrder), "descending")
	sortProp = strings.TrimSpace(sortProp)
	if sortProp == "size_formatted" {
		sortProp = "size"
	}

	sort.SliceStable(groups, func(i, j int) bool {
		left := groups[i]
		right := groups[j]
		if sortProp == "size" {
			if left.Size != right.Size {
				if descending {
					return left.Size > right.Size
				}
				return left.Size < right.Size
			}
		} else if left.Name != right.Name {
			if descending {
				return customNameLess(right.Name, left.Name)
			}
			return customNameLess(left.Name, right.Name)
		}

		if left.Name != right.Name {
			if descending {
				return customNameLess(right.Name, left.Name)
			}
			return customNameLess(left.Name, right.Name)
		}
		if descending {
			return left.Size > right.Size
		}
		return left.Size < right.Size
	})
}

func paginateTorrentGroupKeys(groups []repository.TorrentNameSizeKey, page int, pageSize int) []repository.TorrentNameSizeKey {
	start := (page - 1) * pageSize
	if start >= len(groups) {
		return []repository.TorrentNameSizeKey{}
	}
	end := start + pageSize
	if end > len(groups) {
		end = len(groups)
	}
	return groups[start:end]
}

// getDataMetadataResponse 返回维护种子列表筛选器需要的轻量元数据。
// 参数/返回：params 只使用完成状态和当前路径筛选；返回空 data 与路径、状态、站点、链接规则等选项。
// 失败场景：站点或筛选选项查询失败时返回错误。
// 副作用：仅读取数据库，不写入数据。
func (s *TorrentDataService) getDataMetadataResponse(ctx context.Context, params TorrentsDataParams) (map[string]any, error) {
	allDiscoveredSites, err := s.repo.ListAllDiscoveredSites(ctx)
	if err != nil {
		return nil, fmt.Errorf("load discovered sites failed: %w", err)
	}
	metadata, err := s.dataMetadata(ctx, params, allDiscoveredSites)
	if err != nil {
		return nil, err
	}
	metadata["data"] = []map[string]any{}
	metadata["total"] = 0
	metadata["page"] = params.Page
	metadata["pageSize"] = params.PageSize
	return metadata, nil
}

// dataMetadata 读取列表筛选和链接展示所需的元数据。
// 参数/返回：ctx 为请求上下文；allDiscoveredSites 可复用主列表已查询的站点集合；返回 unique_paths、unique_states、all_discovered_sites 等字段。
// 失败场景：路径/状态候选查询失败时返回错误，站点链接规则失败时降级为空 map。
// 副作用：仅读取数据库，不写入数据。
func (s *TorrentDataService) dataMetadata(ctx context.Context, params TorrentsDataParams, allDiscoveredSites []string) (map[string]any, error) {
	uniquePaths, uniqueStates, err := s.repo.ListTorrentFilterOptions(params.OnlyCompleted)
	if err != nil {
		return nil, fmt.Errorf("load torrent filter options failed: %w", err)
	}
	siteLinkRules, err := s.repo.SiteLinkRules()
	if err != nil {
		siteLinkRules = map[string]any{}
	}
	return map[string]any{
		"unique_paths":         uniquePaths,
		"unique_states":        uniqueStates,
		"all_discovered_sites": allDiscoveredSites,
		"site_link_rules":      siteLinkRules,
		"active_path_filters":  params.PathFilters,
	}, nil
}

func torrentRecordHashes(rows []repository.TorrentRecord) []string {
	hashes := make([]string, 0, len(rows))
	seen := map[string]struct{}{}
	for _, row := range rows {
		hash := strings.TrimSpace(row.Hash)
		if hash == "" {
			continue
		}
		if _, exists := seen[hash]; exists {
			continue
		}
		seen[hash] = struct{}{}
		hashes = append(hashes, hash)
	}
	return hashes
}
