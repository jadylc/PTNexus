package migrationflow

import (
	"strings"

	"github.com/pt-nexus/server/internal/platform/logx"
	processingpersist "github.com/pt-nexus/server/internal/service/processing/persist"
	processingshared "github.com/pt-nexus/server/internal/service/processing/shared"
)

const batchAutoCheckLogModule = "迁移-批量自动检测"

// batchCheckItemResult 单条种子的自动检测结果。
type batchCheckItemResult struct {
	TorrentID string   `json:"torrent_id"`
	SiteName  string   `json:"site_name"`
	Passed    bool     `json:"passed"`
	Problems  []string `json:"problems"`
}

// BatchAutoCheck 维护列表批量自动检测：对勾选行执行与"点开维护详情页、
// 修改完成按钮可点"一致的校验口径，全部通过时将该行 is_reviewed 置为 true。
// 参数/返回：payload.items 为 [{torrent_id, site_name}]；返回逐条结果与汇总。
// 失败场景：items 缺失或数据库读取失败返回错误。
// 副作用：全部通过的行为会写 seed_parameters.is_reviewed=true。
// 说明：数据来源与单条维护面板同源（QueryAndNormalizeSeed → NormalizeSeedRow →
// BuildStandardizedParams），保证口径一致；校验项对齐前端 isNextButtonDisabled。
func (s *MigrateService) BatchAutoCheck(payload map[string]any) (map[string]any, int) {
	rawItems, _ := payload["items"].([]any)
	if len(rawItems) == 0 {
		return map[string]any{"success": false, "message": "错误：items 不能为空"}, 400
	}

	results := make([]batchCheckItemResult, 0, len(rawItems))
	passed := 0
	failed := 0
	for _, raw := range rawItems {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		torrentID := strings.TrimSpace(processingshared.ToString(item["torrent_id"], ""))
		siteName := strings.TrimSpace(processingshared.ToString(item["site_name"], ""))
		if torrentID == "" || siteName == "" {
			continue
		}

		checkResult := s.autoCheckOneSeed(torrentID, siteName)
		if checkResult.Passed {
			passed++
		} else {
			failed++
		}
		results = append(results, checkResult)
	}

	logx.Infof(batchAutoCheckLogModule, "批量自动检测完成 total=%d passed=%d failed=%d", len(results), passed, failed)
	return map[string]any{
		"success": true,
		"total":   len(results),
		"passed":  passed,
		"failed":  failed,
		"results": results,
	}, 200
}

// autoCheckOneSeed 对单条种子执行维护通过校验并回写 is_reviewed。
// 参数/返回：torrentID/siteName 定位行；返回检测结果。
// 失败场景：数据库读取失败计入 Problems。
// 副作用：全部通过时写 seed_parameters.is_reviewed=true（pending 截图状态清为 confirmed）。
// 说明：校验口径与维护面板"修改完成"保存共用 persist.CheckSeedMaintainability，
// 两个入口对同一行给出一致结论。
func (s *MigrateService) autoCheckOneSeed(torrentID, siteName string) batchCheckItemResult {
	result := batchCheckItemResult{TorrentID: torrentID, SiteName: siteName, Passed: false, Problems: []string{}}

	normalized, _, err := processingpersist.QueryAndNormalizeSeed(s.repo, torrentID, siteName)
	if err != nil {
		result.Problems = append(result.Problems, "数据库中未找到种子信息")
		return result
	}

	result.Problems = append(result.Problems, processingpersist.CheckSeedMaintainability(normalized)...)

	reviewStatus := strings.TrimSpace(processingshared.ToString(normalized["screenshot_review_status"], ""))

	result.Passed = len(result.Problems) == 0

	if result.Passed {
		// pending 状态一并清为 confirmed：批量检测已替代人工逐条确认，
		// 否则发布预览仍会被 pending 拦截
		screenshotStatus := ""
		if reviewStatus == processingshared.ScreenshotReviewStatusPending {
			screenshotStatus = processingshared.ScreenshotReviewStatusConfirmed
		}
		if err := s.repo.UpdateSeedParameterReviewed(torrentID, siteName, screenshotStatus); err != nil {
			logx.Warnf(batchAutoCheckLogModule, "写入 is_reviewed 失败 torrent_id=%s site=%s err=%v", torrentID, siteName, err)
			result.Problems = append(result.Problems, "标记已检查失败: "+err.Error())
			result.Passed = false
		}
	}
	return result
}
