package persist

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	processingmedia "github.com/pt-nexus/server/internal/service/processing/media"
	processingrepair "github.com/pt-nexus/server/internal/service/processing/repair"
	processingshared "github.com/pt-nexus/server/internal/service/processing/shared"
)

// 维护通过的统一校验口径：批量检测（migrationflow.BatchAutoCheck）与
// 维护面板"修改完成"（persist.ApplyManualUpdateFromPayload）共用同一函数，
// 两个入口对同一行数据给出一致结论。

// flexibleMaintainabilityValueRegex 与维护面板前端 invalidStandardParams 一致：
// 标准键格式形如 xxx.yyy。
var flexibleMaintainabilityValueRegex = compileMaintainabilityRegex()

func compileMaintainabilityRegex() *regexp.Regexp {
	return regexp.MustCompile(`^[\p{L}\p{N}_-]+\.[\p{L}\p{N}_+-]+$`)
}

// CheckSeedMaintainability 校验一行规范化种子数据是否达到"维护通过"标准。
// 参数/返回：normalized 为 QueryAndNormalizeSeed 或 NormalizeSeedRow 的行数据；
// 返回问题列表（空 = 通过，允许置 is_reviewed=true）。
// 失败场景：不适用。
// 副作用：无。
// 说明：校验项与面板"下一步"按钮禁用口径对齐——无法识别/海报/截图/正文完整性/
// 标准参数/制作组/MediaInfo/格式。
func CheckSeedMaintainability(normalized map[string]any) []string {
	problems := make([]string, 0)

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
		case []byte:
			_ = json.Unmarshal(typed, &components)
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
		problems = append(problems, "存在无法识别的标题内容: "+unrecognized)
	}

	// 2. 海报/截图/正文非空（对齐前端口径：poster/screenshots/body 三项，statement 不检查）
	poster := strings.TrimSpace(processingshared.ToString(normalized["poster"], ""))
	screenshots := strings.TrimSpace(processingshared.ToString(normalized["screenshots"], ""))
	body := strings.TrimSpace(processingshared.ToString(normalized["body"], ""))
	if poster == "" {
		problems = append(problems, "海报为空")
	}
	if screenshots == "" {
		problems = append(problems, "截图为空")
	}
	if body == "" {
		problems = append(problems, "正文为空")
	}

	// 3. 正文完整性（片名/产地/简介）；附正文长度与开头预览，便于诊断口径差异
	if body != "" {
		if completeness := processingrepair.CheckIntroBodyCompleteness(body); !completeness.IsComplete {
			preview := strings.Join(strings.Fields(body), " ")
			runes := []rune(preview)
			if len(runes) > 60 {
				preview = string(runes[:60]) + "…"
			}
			problems = append(problems,
				fmt.Sprintf("简介缺少关键字段: %s（后端读到的正文 %d 字，开头: %s）",
					strings.Join(completeness.MissingFields, "、"), len([]rune(body)), preview))
		}
	} else {
		problems = append(problems, "正文为空（后端未读到 body）")
	}

	// 4. 标准参数（tags 是数组单独提取）
	stdParams := map[string]string{}
	var tags []string
	if rawStd := normalized["standardized_params"]; rawStd != nil {
		parsed := map[string]any{}
		switch typed := rawStd.(type) {
		case map[string]any:
			parsed = typed
		case string:
			_ = json.Unmarshal([]byte(typed), &parsed)
		case []byte:
			_ = json.Unmarshal(typed, &parsed)
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
			problems = append(problems, fmt.Sprintf("标准参数 %s 为空", key))
		}
	}

	// 5. 制作组非空且非 NOGROUP
	if team == "" {
		problems = append(problems, "无制作组")
	} else if strings.EqualFold(team, "NOGROUP") {
		problems = append(problems, "制作组为 NOGROUP")
	}

	// 6. mediainfo 有效
	mediainfo := strings.TrimSpace(processingshared.ToString(normalized["mediainfo"], ""))
	if mediainfo == "" {
		problems = append(problems, "MediaInfo 为空")
	} else if isMediainfo, isBDInfo, reason := processingmedia.ValidateMediaInfoFormat(mediainfo); !isMediainfo && !isBDInfo {
		problems = append(problems, "MediaInfo 格式无效: "+reason)
	}

	// 7. 标准参数与 tags 格式合法（非空才验格式）
	for _, key := range []string{"type", "medium", "video_codec", "audio_codec", "resolution", "team", "source"} {
		value := stdParams[key]
		if value != "" && !flexibleMaintainabilityValueRegex.MatchString(value) {
			problems = append(problems, fmt.Sprintf("标准参数 %s 格式无效: %s", key, value))
		}
	}
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		if trimmed != "" && !flexibleMaintainabilityValueRegex.MatchString(trimmed) {
			problems = append(problems, fmt.Sprintf("标签格式无效: %s", trimmed))
		}
	}

	return problems
}
