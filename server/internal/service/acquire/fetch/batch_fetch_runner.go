package fetch

import (
	"strings"
	"time"

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
// 参数/返回：torrentNames 为待抓取名称，sourcePriority 为站点优先级，rows 为 torrents 查询结果，deps 为流程回调依赖。
// 失败场景：单项抓取失败会记录失败结果并继续下一项，不中断整体任务；deps.ShouldCancel 返回 true 时停止处理剩余种子。
// 副作用：会通过 FetchAndStore 触发抓取写库，并通过 OnResult 回调持续上报每项结果。
func RunBatchFetch(torrentNames []string, sourcePriority []string, rows []map[string]any, deps BatchFetchRunnerDeps) {
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

		sourceSite, torrentID, matched := selectSourceByPriority(entries, sourcePriority)
		if !matched {
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
			result["source_site"] = sourceSite
			result["torrent_id"] = torrentID
			emit(false, result)
			continue
		}

		fetchResult, status := deps.FetchAndStore(map[string]any{
			"sourceSite":           sourceSite,
			"searchTerm":           torrentID,
			"torrentName":          name,
			"savePath":             savePath,
			"downloaderId":         downloaderID,
			"screenshotReviewMode": processingshared.ScreenshotReviewModeBackground,
		})
		if status != 200 || !isSuccessResult(fetchResult) {
			result["status"] = "failed"
			result["reason"] = toStringAny(fetchResult["message"], "抓取失败")
			result["source_site"] = sourceSite
			result["torrent_id"] = torrentID
			emit(false, result)
			continue
		}

		screenshotReviewStatus := processingshared.NormalizeScreenshotReviewStatus(toStringAny(fetchResult["screenshot_review_status"], processingshared.ScreenshotReviewStatusNone))
		if processingshared.NeedsScreenshotManualReview(screenshotReviewStatus) {
			result["status"] = "pending_review"
			result["message"] = "抓取成功，截图待人工确认"
		} else {
			result["status"] = "success"
			result["message"] = "抓取成功"
		}
		result["source_site"] = sourceSite
		result["torrent_id"] = torrentID
		result["task_id"] = toStringAny(fetchResult["task_id"], "")
		result["screenshot_review_status"] = screenshotReviewStatus
		emit(true, result)
		sleep(50 * time.Millisecond)
	}
}

func selectSourceByPriority(entries []map[string]any, sourcePriority []string) (string, string, bool) {
	sourceBySite := map[string]string{}
	for _, row := range entries {
		sites := splitSiteList(toStringAny(row["sites"], ""))
		if len(sites) == 0 {
			continue
		}
		siteIDs := siteTorrentIDs(row)
		details := toStringAny(row["details"], "")
		for _, site := range sites {
			torrentID := resolveSiteTorrentID(siteIDs, site, details)
			if torrentID == "" {
				continue
			}
			sourceBySite[site] = torrentID
		}
	}
	if len(sourceBySite) == 0 {
		return "", "", false
	}

	for _, preferred := range sourcePriority {
		for site, torrentID := range sourceBySite {
			if strings.EqualFold(site, preferred) {
				return site, torrentID, true
			}
		}
	}
	for site, torrentID := range sourceBySite {
		return site, torrentID, true
	}
	return "", "", false
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
// 参数/返回：siteIDs 为 seed_parameters 权威站点 ID 映射，site 为站点标识，details 为详情页链接。
// 失败场景：不适用。
// 副作用：无。
// 说明：infohash（40 位十六进制）不是站点内部 ID，一律拒绝，避免再次拼出无效详情页 URL。
func resolveSiteTorrentID(siteIDs map[string]string, site, details string) string {
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
