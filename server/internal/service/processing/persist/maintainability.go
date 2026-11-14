package persist

import (
	"strings"

	processingmedia "github.com/pt-nexus/server/internal/service/processing/media"
	processingrepair "github.com/pt-nexus/server/internal/service/processing/repair"
	processingshared "github.com/pt-nexus/server/internal/service/processing/shared"
)

// CheckSeedMaintainability 维护通过的校验口径：**只校验简介和媒体信息两个框**。
// 参数/返回：normalized 为 QueryAndNormalizeSeed 或 NormalizeSeedRow 的行数据；
// 返回问题列表（空 = 通过，允许置 is_reviewed=true）。
// 失败场景：不适用。
// 副作用：无。
// 说明：批量检测（维护列表勾选）与维护面板「修改完成」共用本函数，口径一致。
// 不校验标题组件/海报/截图/标准参数/制作组/产地等——那些由用户自行判断。
func CheckSeedMaintainability(normalized map[string]any) []string {
	problems := make([]string, 0)

	// 1. 简介（简介框）：非空，且包含关键字段（片名/产地/简介）
	body := strings.TrimSpace(processingshared.ToString(normalized["body"], ""))
	if body == "" {
		problems = append(problems, "简介为空")
	} else if completeness := processingrepair.CheckIntroBodyCompleteness(body); !completeness.IsComplete {
		problems = append(problems, "简介缺少关键字段："+strings.Join(completeness.MissingFields, "、"))
	}

	// 2. 媒体信息（媒体信息框）：非空，且格式为有效 MediaInfo/BDInfo
	mediainfo := strings.TrimSpace(processingshared.ToString(normalized["mediainfo"], ""))
	if mediainfo == "" {
		problems = append(problems, "媒体信息为空")
	} else if isMediainfo, isBDInfo, reason := processingmedia.ValidateMediaInfoFormat(mediainfo); !isMediainfo && !isBDInfo {
		problems = append(problems, "媒体信息格式无效："+reason)
	}

	return problems
}
