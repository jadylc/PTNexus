package fetch

import (
	"fmt"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	processingshared "github.com/pt-nexus/server/internal/service/processing/shared"
)

// BatchFetchRunnerDeps 定义批量抓取执行器依赖。
type BatchFetchRunnerDeps struct {
	FetchAndStore func(payload map[string]any) (map[string]any, int)
	OnResult      func(success bool, result map[string]any)
	Sleep         func(duration time.Duration)
	// ShouldCancel 在每处理完一个种子后检查是否已请求取消；返回 true 时立即停止剩余条目。
	// 取消是协作式的：正在执行的当前这一项不会被打断（避免留下半写状态）。
	ShouldCancel func() bool
}

// RunBatchFetch 按种子名称列表执行批量抓取流程。
// 参数/返回：torrentNames 为待抓取名称，sourcePriority 为站点优先级，excludedSites 为屏蔽站点（不参与拉取），
// rows 为 torrents 查询结果，deps 为流程回调依赖。
// 失败场景：单项抓取失败会记录失败结果并继续下一项，不中断整体任务；deps.ShouldCancel 返回 true 时停止处理剩余种子。
// 副作用：会通过 FetchAndStore 触发抓取写库，并通过 OnResult 回调持续上报每项结果。
func RunBatchFetch(torrentNames []string, sourcePriority []string, excludedSites []string, rows []map[string]any, deps BatchFetchRunnerDeps) {
	byName := map[string][]map[string]any{}
	for _, row := range rows {
		name := strings.TrimSpace(toStringAny(row["name"], ""))
		if name == "" {
			continue
		}
		byName[name] = append(byName[name], row)
	}

	sleep := deps.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	emit := func(success bool, result map[string]any) {
		if deps.OnResult != nil {
			deps.OnResult(success, result)
		}
	}

	for _, name := range torrentNames {
		if deps.ShouldCancel != nil && deps.ShouldCancel() {
			return
		}
		result := map[string]any{"name": name}
		entries := byName[name]
		if len(entries) == 0 {
			result["status"] = "failed"
			result["reason"] = "未找到种子记录"
			emit(false, result)
			continue
		}

		// 按优先级顺序生成候选站点列表，逐个尝试抓取：
		// 高优站点失败（CF 挑战、网络错误、详情页 404 等）自动切换下一个站点，
		// 直到某个站点成功；全部失败才报错（附各站点失败原因）。
		candidates := selectSourceCandidatesByPriority(entries, sourcePriority, excludedSites)
		if len(candidates) == 0 {
			result["status"] = "failed"
			result["reason"] = "未找到可用源站点或详情ID"
			result["attempted_sites"] = len(entries)
			emit(false, result)
			continue
		}

		savePath := strings.TrimSpace(toStringAny(entries[0]["save_path"], ""))
		downloaderID := strings.TrimSpace(toStringAny(entries[0]["downloader_id"], ""))
		if deps.FetchAndStore == nil {
			result["status"] = "failed"
			result["reason"] = "内部错误：FetchAndStore 回调未配置"
			emit(false, result)
			continue
		}

		attemptFailures := make([]string, 0, len(candidates))
		var succeededSite, succeededID string
		var fetchResult map[string]any
		successStatus := 0
		for _, candidate := range candidates {
			if deps.ShouldCancel != nil && deps.ShouldCancel() {
				return
			}
			attemptResult, attemptStatus := deps.FetchAndStore(map[string]any{
				"sourceSite":           candidate.site,
				"searchTerm":           candidate.torrentID,
				"torrentName":          name,
				"savePath":             savePath,
				"downloaderId":         downloaderID,
				"screenshotReviewMode": processingshared.ScreenshotReviewModeBackground,
			})
			if attemptStatus == 200 && isSuccessResult(attemptResult) {
				succeededSite = candidate.site
				succeededID = candidate.torrentID
				fetchResult = attemptResult
				successStatus = attemptStatus
				break
			}
			reason := toStringAny(attemptResult["message"], "抓取失败")
			attemptFailures = append(attemptFailures, fmt.Sprintf("%s: %s", candidate.site, reason))
			logx.Warnf("迁移-种子下载", "源站点抓取失败，尝试下一个候选 source_site=%s torrent_name=%s err=%s", candidate.site, name, reason)
		}

		if len(attemptFailures) == len(candidates) {
			result["status"] = "failed"
			result["reason"] = "所有候选源站点均抓取失败: " + strings.Join(attemptFailures, "; ")
			result["attempted_sites"] = len(candidates)
			emit(false, result)
			continue
		}
		_ = successStatus

		screenshotReviewStatus := processingshared.NormalizeScreenshotReviewStatus(toStringAny(fetchResult["screenshot_review_status"], processingshared.ScreenshotReviewStatusNone))
		if processingshared.NeedsScreenshotManualReview(screenshotReviewStatus) {
			result["status"] = "pending_review"
			result["message"] = "抓取成功，截图待人工确认"
		} else {
			result["status"] = "success"
			result["message"] = "抓取成功"
		}
		result["source_site"] = succeededSite
		result["torrent_id"] = succeededID
		result["task_id"] = toStringAny(fetchResult["task_id"], "")
		result["screenshot_review_status"] = screenshotReviewStatus
		emit(true, result)
		sleep(50 * time.Millisecond)
	}
}

// sourceCandidate 表示一个可尝试的源站点候选（站点标识 + 站点内部 torrent_id）。
type sourceCandidate struct {
	site      string
	torrentID string
}

// selectSourceCandidatesByPriority 按优先级顺序生成候选源站点列表。
// 参数/返回：entries 为同名种子的 torrents 行；sourcePriority 为站点优先级；
// excludedSites 为屏蔽站点；返回按优先级排序的候选列表（无优先级匹配时按站点出现顺序兜底）。
// 失败场景：无可尝试站点时返回空列表。
// 副作用：无。
// 说明：调用方对候选逐个尝试抓取，一个成功即停——高优站点失败自动切换后续站点。
func selectSourceCandidatesByPriority(entries []map[string]any, sourcePriority []string, excludedSites []string) []sourceCandidate {
	sourceBySite := map[string]string{}
	order := make([]string, 0, 4)
	for _, row := range entries {
		sites := splitSiteList(toStringAny(row["sites"], ""))
		if len(sites) == 0 {
			continue
		}
		siteIDs := siteTorrentIDs(row)
		details := toStringAny(row["details"], "")
		rowHash := toStringAny(row["hash"], "")
		for _, site := range sites {
			// 屏蔽列表中的站点不参与拉取
			if isSiteExcluded(site, excludedSites) {
				continue
			}
			if _, exists := sourceBySite[site]; exists {
				continue
			}
			torrentID := resolveSiteTorrentID(siteIDs, site, details, rowHash)
			if torrentID == "" {
				continue
			}
			sourceBySite[site] = torrentID
			order = append(order, site)
		}
	}
	if len(sourceBySite) == 0 {
		return nil
	}

	candidates := make([]sourceCandidate, 0, len(sourceBySite))
	used := map[string]struct{}{}
	// 先按优先级顺序排列
	for _, preferred := range sourcePriority {
		for _, site := range order {
			if _, usedOK := used[site]; usedOK {
				continue
			}
			if strings.EqualFold(site, preferred) {
				candidates = append(candidates, sourceCandidate{site: site, torrentID: sourceBySite[site]})
				used[site] = struct{}{}
				break
			}
		}
	}
	// 兜底：优先级未覆盖的站点按出现顺序追加（仍可尝试）
	for _, site := range order {
		if _, usedOK := used[site]; !usedOK {
			candidates = append(candidates, sourceCandidate{site: site, torrentID: sourceBySite[site]})
			used[site] = struct{}{}
		}
	}
	return candidates
}

// isSiteExcluded 判断站点是否在屏蔽列表中（大小写不敏感）。
// 参数/返回：site 为站点标识；excludedSites 为屏蔽列表；返回是否被屏蔽。
// 失败场景：不适用。
// 副作用：无。
func isSiteExcluded(site string, excludedSites []string) bool {
	for _, excluded := range excludedSites {
		if strings.EqualFold(strings.TrimSpace(site), strings.TrimSpace(excluded)) {
			return true
		}
	}
	return false
}

// splitSiteList 将 torrents.sites 列（逗号分隔）拆分为站点标识列表。
// 参数/返回：raw 为 sites 列原始文本；返回去除空白后的站点标识列表。
// 失败场景：不适用。
// 副作用：无。
func splitSiteList(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		site := strings.TrimSpace(part)
		if site != "" {
			result = append(result, site)
		}
	}
	return result
}

// siteTorrentIDs 读取行上的 site_torrent_ids 映射（站点标识 -> 站点内部 torrent_id）。
// 参数/返回：row 为批量抓取行；返回站点标识到内部 ID 的映射，缺失或结构异常时返回空映射。
// 失败场景：不适用。
// 副作用：无。
func siteTorrentIDs(row map[string]any) map[string]string {
	if row == nil {
		return map[string]string{}
	}
	result := map[string]string{}
	raw, ok := row["site_torrent_ids"]
	if !ok {
		return result
	}
	switch typed := raw.(type) {
	case map[string]string:
		for site, id := range typed {
			if strings.TrimSpace(site) != "" && strings.TrimSpace(id) != "" {
				result[strings.TrimSpace(site)] = strings.TrimSpace(id)
			}
		}
	case map[string]any:
		for site, value := range typed {
			id := strings.TrimSpace(toStringAny(value, ""))
			if strings.TrimSpace(site) != "" && id != "" {
				result[strings.TrimSpace(site)] = id
			}
		}
	}
	return result
}

// resolveSiteTorrentID 解析指定站点的 torrent_id。
// 参数/返回：siteIDs 为 seed_parameters 权威站点 ID 映射，site 为站点标识，
// details 为详情页链接，rowHash 为该行 torrents.hash。
// 失败场景：不适用（无可用标识时返回空串）。
// 副作用：无。
// 说明：优先返回站点内部 ID（数字/UUID）；infohash 形式的候选一律不当作站点 ID。
// 但当没有任何站点 ID 时，返回 40 位 infohash 本身——下载层会按种子名在站点
// 搜索定位真实数字 ID（IYUU 对彩虹岛等站点的 torrent_id 就是 infohash）。
func resolveSiteTorrentID(siteIDs map[string]string, site, details, rowHash string) string {
	trimmedSite := strings.TrimSpace(site)
	if len(siteIDs) > 0 {
		if id := strings.TrimSpace(siteIDs[trimmedSite]); id != "" && !isInfohash(id) {
			return id
		}
		if id := strings.TrimSpace(siteIDs[strings.ToLower(trimmedSite)]); id != "" && !isInfohash(id) {
			return id
		}
	}
	if id := extractTorrentIDFromDetails(details); id != "" && !isInfohash(id) {
		return id
	}
	if isInfohash(rowHash) {
		return strings.TrimSpace(rowHash)
	}
	return ""
}

func isSuccessResult(result map[string]any) bool {
	if result == nil {
		return false
	}
	value, ok := result["success"]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		text := strings.ToLower(strings.TrimSpace(typed))
		return text == "true" || text == "1" || text == "yes" || text == "ok"
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
	case float32:
		return typed != 0
	case float64:
		return typed != 0
	default:
		return false
	}
}
