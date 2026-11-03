package migrationflow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/pt-nexus/server/internal/platform/logx"
	processingmedia "github.com/pt-nexus/server/internal/service/processing/media"
	processingpersist "github.com/pt-nexus/server/internal/service/processing/persist"
	processingrepair "github.com/pt-nexus/server/internal/service/processing/repair"
	processingshared "github.com/pt-nexus/server/internal/service/processing/shared"
)

const batchAutoCheckLogModule = "迁移-批量自动检测"

// flexibleStandardValueRegex 与前端 invalidStandardParams 的 flexibleRegex 一致：
// 标准键格式 形如 xxx.yyy（字母/数字/下划线/横杠 + 点 + 字母/数字/下划线/加号/横杠）。
var flexibleStandardValueRegex = regexp.MustCompile(`^[\p{L}\p{N}_-]+\.[\p{L}\p{N}_+-]+$`)

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

// autoCheckOneSeed 对单条种子执行"修改完成可点"口径校验，全部通过时置 is_reviewed=true。
// 参数/返回：torrentID/siteName 定位行；返回逐项结果。
// 失败场景：行不存在时返回未通过并标注原因。
// 副作用：通过时写 is_reviewed=true。
// 说明：数据走 QueryAndNormalizeSeed（与单条维护面板加载同源），其内部会
// NormalizeSeedRow + BuildStandardizedParams 重建标准化参数——与面板展示一致。
func (s *MigrateService) autoCheckOneSeed(torrentID, siteName string) batchCheckItemResult {
	result := batchCheckItemResult{TorrentID: torrentID, SiteName: siteName, Passed: false, Problems: []string{}}

	// 与单条维护面板同源：NormalizeSeedRow + BuildStandardizedParams
	normalized, _, err := processingpersist.QueryAndNormalizeSeed(s.repo, torrentID, siteName)
	if err != nil {
		result.Problems = append(result.Problems, "数据库中未找到种子信息")
		return result
	}

	// 解析 title_components，取"无法识别"与"制作组"
	type titleComponent struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	components := []titleComponent{}
	if rawComponents := normalized["title_components"]; rawComponents != nil {
		switch typed := rawComponents.(type) {
		case []any:
			for _, item := range typed {
				if componentMap, ok := item.(map[string]any); ok {
					components = append(components, titleComponent{
						Key:   strings.TrimSpace(processingshared.ToString(componentMap["key"], "")),
						Value: processingshared.ToString(componentMap["value"], ""),
					})
				}
			}
		case string:
			_ = json.Unmarshal([]byte(typed), &components)
		}
	}
	unrecognized := ""
	team := ""
	for _, component := range components {
		switch component.Key {
		case "无法识别":
			unrecognized = strings.TrimSpace(component.Value)
		case "制作组":
			team = strings.TrimSpace(component.Value)
		}
	}

	// 1. 无法识别
	if unrecognized != "" {
		result.Problems = append(result.Problems, "存在无法识别的标题内容: "+unrecognized)
	}

	// 2. 简介/海报/截图非空
	statement := strings.TrimSpace(processingshared.ToString(normalized["statement"], ""))
	poster := strings.TrimSpace(processingshared.ToString(normalized["poster"], ""))
	screenshots := strings.TrimSpace(processingshared.ToString(normalized["screenshots"], ""))
	body := strings.TrimSpace(processingshared.ToString(normalized["body"], ""))
	if statement == "" {
		result.Problems = append(result.Problems, "简介为空")
	}
	if poster == "" {
		result.Problems = append(result.Problems, "海报为空")
	}
	if screenshots == "" {
		result.Problems = append(result.Problems, "截图为空")
	}

	// 3. 简介正文完整性（片名/产地/简介）
	if body != "" {
		if completeness := processingrepair.CheckIntroBodyCompleteness(body); !completeness.IsComplete {
			result.Problems = append(result.Problems, "简介缺少关键字段: "+strings.Join(completeness.MissingFields, "、"))
		}
	}

	// 4. 标准参数（BuildStandardizedParams 重建，tags 是数组单独提取）
	stdParams := map[string]string{}
	var tags []string
	if rawStd := normalized["standardized_params"]; rawStd != nil {
		parsed := map[string]any{}
		switch typed := rawStd.(type) {
		case map[string]any:
			parsed = typed
		case string:
			_ = json.Unmarshal([]byte(typed), &parsed)
		}
		for key, value := range parsed {
			if key == "tags" {
				if tagArray, ok := value.([]any); ok {
					for _, tagItem := range tagArray {
						if tagText := strings.TrimSpace(processingshared.ToString(tagItem, "")); tagText != "" {
							tags = append(tags, tagText)
						}
					}
				}
				continue
			}
			stdParams[key] = strings.TrimSpace(processingshared.ToString(value, ""))
		}
	}
	// 制作组以 title_components 为准（面板口径），standardized_params.team 作兜底
	if team == "" {
		team = stdParams["team"]
	}
	for _, key := range []string{"type", "medium", "video_codec", "audio_codec", "resolution"} {
		if stdParams[key] == "" {
			result.Problems = append(result.Problems, fmt.Sprintf("标准参数 %s 为空", key))
		}
	}

	// 5. 制作组非空且非 NOGROUP
	if team == "" {
		result.Problems = append(result.Problems, "无制作组")
	} else if strings.EqualFold(team, "NOGROUP") {
		result.Problems = append(result.Problems, "制作组为 NOGROUP")
	}

	// 6. mediainfo 有效
	mediainfo := strings.TrimSpace(processingshared.ToString(normalized["mediainfo"], ""))
	if mediainfo == "" {
		result.Problems = append(result.Problems, "MediaInfo 为空")
	} else if isMediainfo, isBDInfo, reason := processingmedia.ValidateMediaInfoFormat(mediainfo); !isMediainfo && !isBDInfo {
		result.Problems = append(result.Problems, "MediaInfo 格式无效: "+reason)
	}

	// 7. 标准参数与 tags 格式合法（与前端 invalidStandardParams 一致：非空才验格式）
	for _, key := range []string{"type", "medium", "video_codec", "audio_codec", "resolution", "team", "source"} {
		value := stdParams[key]
		if value != "" && !flexibleStandardValueRegex.MatchString(value) {
			result.Problems = append(result.Problems, fmt.Sprintf("标准参数 %s 格式无效: %s", key, value))
		}
	}
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		if trimmed != "" && !flexibleStandardValueRegex.MatchString(trimmed) {
			result.Problems = append(result.Problems, fmt.Sprintf("标签格式无效: %s", trimmed))
		}
	}

	// 8. 截图待人工确认
	if reviewStatus := strings.TrimSpace(processingshared.ToString(normalized["screenshot_review_status"], "")); reviewStatus == processingshared.ScreenshotReviewStatusPending {
		result.Problems = append(result.Problems, "截图待人工确认")
	}

	result.Passed = len(result.Problems) == 0

	// 全部通过：等价于用户点开维护、按钮可点、点了修改完成 → is_reviewed=true
	if result.Passed {
		if err := s.repo.UpdateSeedParameterReviewed(torrentID, siteName); err != nil {
			logx.Warnf(batchAutoCheckLogModule, "写入 is_reviewed 失败 torrent_id=%s site=%s err=%v", torrentID, siteName, err)
			result.Problems = append(result.Problems, "标记已检查失败: "+err.Error())
			result.Passed = false
		}
	}
	return result
}
