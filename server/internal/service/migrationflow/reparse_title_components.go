package migrationflow

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	extract "github.com/pt-nexus/server/internal/service/acquire/extract"
	"github.com/pt-nexus/server/internal/service/crossseed"
	processingshared "github.com/pt-nexus/server/internal/service/processing/shared"
	processingtagging "github.com/pt-nexus/server/internal/service/processing/tagging"
	processingtitle "github.com/pt-nexus/server/internal/service/processing/title"
	"github.com/pt-nexus/server/internal/service/reversemapping"
)

const reparseTitleComponentsLogModule = "迁移-标题重算"

// reparseStandardColumns 参与行级问题判定与列值修复的标准字段。
var reparseStandardColumns = []string{"type", "medium", "video_codec", "audio_codec", "resolution", "team", "source"}

// ReparseTitleComponents 按当前解析规则重建 seed_parameters.title_components 并写回。
// 参数/返回：payload 可含 site_name / torrent_id / items（勾选列表）限定范围；only_errors 控制范围——
// true（批量默认）仅处理"错误"分类的行（标题组件含「无法识别」或标准列值无效），
// false（维护详情页单条显式重算）处理指定行；返回 success/message/updated/skipped/failed/total。
// 失败场景：数据库读取或写入失败时返回对应错误码。
// 副作用：批量更新 seed_parameters 的 title_components 及被修复的列值。
// 说明：这是给存量数据用的"重算口子"——标题解析规则升级（如 HDR10+/PMTP/HDTVRip
// 识别修复）后，已有数据不会自动刷新，需要显式重算。
// 输入取 title（缺省回退 name），媒体文本取 mediainfo 列；解析与入库流程一致。
// 解析结果与库中内容一致时跳过写回，避免无谓地刷新 updated_at。
// 除标题组件外，还会把解析推断出的标准键修复到"当前列值无效"的列
// （如旧值 team.hdsky / 2Audio 这类映射不到的值），有效列值不动。
func (s *MigrateService) ReparseTitleComponents(payload map[string]any) (map[string]any, int) {
	siteName := strings.TrimSpace(processingshared.ToString(payload["site_name"], ""))
	torrentID := strings.TrimSpace(processingshared.ToString(payload["torrent_id"], ""))

	// 显式选中列表（列表页勾选后重算）：逐条定位，视为显式意图，不受 only_errors 限制。
	type reparseItem struct{ site, torrentID string }
	items := make([]reparseItem, 0)
	if rawItems, ok := payload["items"].([]any); ok {
		for _, raw := range rawItems {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			itemSite := strings.TrimSpace(processingshared.ToString(item["site_name"], ""))
			itemTorrentID := strings.TrimSpace(processingshared.ToString(item["torrent_id"], ""))
			if itemSite == "" || itemTorrentID == "" {
				continue
			}
			items = append(items, reparseItem{site: itemSite, torrentID: itemTorrentID})
		}
	}

	// 范围优先级：items（显式选中）> 单条（torrent_id）> 批量（默认仅错误行）。
	onlyErrors := true
	if len(items) > 0 {
		onlyErrors = false
	} else if torrentID != "" {
		if raw, ok := payload["only_errors"].(bool); ok {
			onlyErrors = raw
		} else {
			onlyErrors = false
		}
	} else if raw, ok := payload["only_errors"].(bool); ok {
		onlyErrors = raw
	}

	// 批量"仅错误行"改为行级判定（无法识别 OR 标准列值无效）：
	// 此前用 SQL LIKE 只筛组件含"无法识别"的行，会漏掉列值无效的行
	//（如 team=team.hdsky、audio_codec=2Audio），这些行重算后依然标红。
	selectSQL := "SELECT hash, torrent_id, site_name, title, subtitle, name, mediainfo, title_components, type, medium, video_codec, audio_codec, resolution, team, source, tags FROM seed_parameters"
	where := make([]string, 0)
	args := make([]any, 0)
	if len(items) > 0 {
		orConds := make([]string, 0, len(items))
		for _, item := range items {
			orConds = append(orConds, "(site_name = ? AND torrent_id = ?)")
			args = append(args, item.site, item.torrentID)
		}
		where = append(where, "("+strings.Join(orConds, " OR ")+")")
	}
	if siteName != "" {
		where = append(where, "site_name = ?")
		args = append(args, siteName)
	}
	if torrentID != "" {
		where = append(where, "torrent_id = ?")
		args = append(args, torrentID)
	}
	if onlyErrors {
		// 粗筛下推：组件含无法识别 或 列值缺标准键格式（含点）——最终行级判定在循环内做。
		where = append(where, "(title_components LIKE ? OR type NOT LIKE '%.%' OR medium NOT LIKE '%.%' OR video_codec NOT LIKE '%.%' OR audio_codec NOT LIKE '%.%' OR resolution NOT LIKE '%.%' OR team NOT LIKE '%.%' OR source NOT LIKE '%.%')")
		args = append(args, "%无法识别%")
	}
	if len(where) > 0 {
		selectSQL += " WHERE " + strings.Join(where, " AND ")
	}

	rows := []map[string]any{}
	if err := s.repo.DB().Raw(selectSQL, args...).Scan(&rows).Error; err != nil {
		logx.Errorf(reparseTitleComponentsLogModule, "读取种子数据失败 site=%s torrent_id=%s only_errors=%t err=%v", siteName, torrentID, onlyErrors, err)
		return map[string]any{"success": false, "message": "读取种子数据失败: " + err.Error()}, 500
	}

	reverseMappings := reversemapping.Build(nil)
	now := time.Now().Format("2006-01-02 15:04:05")
	updated, skipped, failed := 0, 0, 0
	for _, row := range rows {
		rowHash := strings.TrimSpace(processingshared.ToString(row["hash"], ""))
		rowTorrentID := strings.TrimSpace(processingshared.ToString(row["torrent_id"], ""))
		rowSite := strings.TrimSpace(processingshared.ToString(row["site_name"], ""))
		storedComponents := strings.TrimSpace(processingshared.ToString(row["title_components"], ""))
		title := strings.TrimSpace(processingshared.ToString(row["title"], ""))
		subtitle := strings.TrimSpace(processingshared.ToString(row["subtitle"], ""))
		if title == "" {
			title = strings.TrimSpace(processingshared.ToString(row["name"], ""))
		}
		if title == "" {
			skipped++
			continue
		}
		mediaInfoText := strings.TrimSpace(processingshared.ToString(row["mediainfo"], ""))

		// 批量"仅错误行"的精确行级判定：组件含无法识别，或任一标准列值无效。
		if onlyErrors && !reparseRowHasProblem(row, reverseMappings) {
			skipped++
			continue
		}

		// 双标题纠正：① 主标题为中文信息条且副标题为英文发布名时交换（互倒场景）；
		// ② 顶部标题混入多行（信息条+发布名）时按西文发布名行拆分。
		// 纠正需同步写回 title/subtitle，否则下次读取仍是混排状态。
		fixedTitle, fixedSubtitle := extract.NormalizeSiteDualTitle(title, subtitle)
		fixedTitle, fixedSubtitle = extract.NormalizeMultilineTitle(fixedTitle, fixedSubtitle)
		updates := map[string]any{}

		result := processingtitle.BuildTitleComponentsForStorage(fixedTitle, mediaInfoText, processingtitle.BuildSimpleTitleComponentsWithMediaInfo)
		if len(result.Components) == 0 {
			skipped++
			continue
		}
		encoded, err := json.Marshal(result.Components)
		if err != nil {
			logx.Warnf(reparseTitleComponentsLogModule, "序列化失败 seed=%s_%s_%s err=%v", rowHash, rowTorrentID, rowSite, err)
			failed++
			continue
		}

		// 无效列值修复：当前列值映射不到标准键时，用标题推断出的标准键替换
		//（如 team.hdsky → team.hds、2Audio → audio.other）；有效列值不动。
		inferred := extract.InferStandardizedValues(fixedTitle, mediaInfoText, "")
		for _, column := range reparseStandardColumns {
			current := strings.TrimSpace(processingshared.ToString(row[column], ""))
			if current == "" {
				continue
			}
			if !crossseed.IsInvalidStandardValue(column, current, reverseMappings) {
				continue
			}
			fixed := strings.TrimSpace(inferred[column])
			if fixed != "" && !crossseed.IsInvalidStandardValue(column, fixed, reverseMappings) {
				updates[column] = fixed
			}
		}

		// 无效标签修复：tags 列中映射不到标准键的条目（如 tag.Atmos——Atmos 是
		// 音频特性而非标签语义），能映射的替换、技术特性词移除，其余保留人工处理。
		if fixedTags, tagsChanged := repairRowTags(row, rowSite, reverseMappings); tagsChanged {
			encodedTags, err := json.Marshal(fixedTags)
			if err == nil {
				updates["tags"] = string(encodedTags)
			}
		}

		// 解析结果与库中一致且无列值修复：内容没有变化，跳过写回（保护 updated_at 与人工状态）。
		if strings.TrimSpace(string(encoded)) == storedComponents && fixedTitle == title && fixedSubtitle == subtitle && len(updates) == 0 {
			skipped++
			continue
		}
		updates["title_components"] = string(encoded)
		if fixedTitle != title {
			updates["title"] = fixedTitle
		}
		if fixedSubtitle != subtitle {
			updates["subtitle"] = fixedSubtitle
		}
		updates["updated_at"] = now
		if err := s.repo.UpdateSeedParameterByKey(rowHash, rowTorrentID, rowSite, updates); err != nil {
			logx.Warnf(reparseTitleComponentsLogModule, "写回失败 seed=%s_%s_%s err=%v", rowHash, rowTorrentID, rowSite, err)
			failed++
			continue
		}
		updated++
	}

	logx.Infof(reparseTitleComponentsLogModule, "标题重算完成 site=%s torrent_id=%s only_errors=%t total=%d updated=%d skipped=%d failed=%d", siteName, torrentID, onlyErrors, len(rows), updated, skipped, failed)
	if updated > 0 {
		// 通知跨种服务失效查询缓存，避免维护/跨种列表在 TTL 内展示旧数据。
		s.notifySeedParametersChanged()
	}
	return map[string]any{
		"success":     true,
		"message":     "重算完成",
		"total":       len(rows),
		"updated":     updated,
		"skipped":     skipped,
		"failed":      failed,
		"only_errors": onlyErrors,
	}, 200
}

// reparseRowHasProblem 判断行是否属于"错误"分类：组件含无法识别或标准列值无效。
// 参数/返回：row 为 seed_parameters 行；reverseMappings 为反向映射表；返回是否命中。
// 失败场景：不适用。
// 副作用：无。
// 说明：判定口径与维护列表"错误"页签一致（hasRowDataProblem）。
func reparseRowHasProblem(row map[string]any, reverseMappings map[string]any) bool {
	titleComponents := parseTitleComponentsJSON(processingshared.ToString(row["title_components"], ""))
	if strings.TrimSpace(crossseed.ExtractUnrecognizedValue(titleComponents)) != "" {
		return true
	}
	for _, column := range reparseStandardColumns {
		value := strings.TrimSpace(processingshared.ToString(row[column], ""))
		if value == "" {
			continue
		}
		if crossseed.IsInvalidStandardValue(column, value, reverseMappings) {
			return true
		}
	}
	// 无效标签（如 tag.Atmos）同样属于数据问题。
	for _, tag := range parseRowTags(row) {
		if crossseed.IsInvalidStandardValue("tags", tag, reverseMappings) {
			return true
		}
	}
	return false
}

// reTechTraitTagToken 匹配不属于标签语义的音频/视频/HDR 技术特性词。
// 说明：这类值（如 tag.Atmos）是历史数据把技术特性误存为标签，标准映射里
// 不存在对应键，应从 tags 中移除而非保留为无效标签。
var reTechTraitTagToken = regexp.MustCompile(`(?i)^(?:atmos|dolby|vision|dovi|dv|hdr10\+|hdr|hlg|sdr|truehd|ddp|dd|aac|dts|flac|lpcm|pcm|ac3|opus|avc|hevc|x264|x265|h\.?26[45]|2160p|1080p|720p|web-?dl|webrip|bluray|remux)$`)

// repairRowTags 修复行内无效标签：可映射的替换为标准键，技术特性词移除，其余保留。
// 参数/返回：row 为 seed_parameters 行；siteCode 为站点标识；reverseMappings 为反向映射表；
// 返回修复后的标签列表与是否发生变化。
// 失败场景：不适用（解析失败或全部有效时原样返回）。
// 副作用：无。
func repairRowTags(row map[string]any, siteCode string, reverseMappings map[string]any) ([]string, bool) {
	tags := parseRowTags(row)
	if len(tags) == 0 {
		return tags, false
	}
	changed := false
	out := make([]string, 0, len(tags))
	seen := map[string]struct{}{}
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		if trimmed == "" {
			changed = true
			continue
		}
		if _, exists := seen[trimmed]; exists {
			changed = true
			continue
		}
		seen[trimmed] = struct{}{}
		if !crossseed.IsInvalidStandardValue("tags", trimmed, reverseMappings) {
			out = append(out, trimmed)
			continue
		}
		// 尝试经站点/全局标签映射表转成标准键（去掉历史 tag. 前缀后按原始词映射）。
		raw := strings.TrimSpace(strings.TrimPrefix(trimmed, "tag."))
		if raw == "" {
			changed = true
			continue
		}
		mapped, _ := processingtagging.MapTagsToStandard([]string{raw}, siteCode)
		replaced := false
		for _, candidate := range mapped {
			if !crossseed.IsInvalidStandardValue("tags", candidate, reverseMappings) {
				if _, exists := seen[candidate]; exists {
					replaced = true
					break
				}
				seen[candidate] = struct{}{}
				out = append(out, candidate)
				replaced = true
				break
			}
		}
		if replaced {
			changed = true
			continue
		}
		// 映射不到且属于音频/视频/HDR 技术特性词：不是标签语义，移除。
		if reTechTraitTagToken.MatchString(raw) {
			changed = true
			continue
		}
		// 其余无效标签保留，由人工在维护页处理。
		out = append(out, trimmed)
	}
	if !changed {
		return tags, false
	}
	return out, true
}

// parseRowTags 解析行内 tags 列（JSON 数组文本）。
// 参数/返回：row 为 seed_parameters 行；返回标签列表（空/解析失败返回 nil）。
// 失败场景：不适用。
// 副作用：无。
func parseRowTags(row map[string]any) []string {
	raw := strings.TrimSpace(processingshared.ToString(row["tags"], ""))
	if raw == "" {
		return nil
	}
	var parsed []string
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil
	}
	out := make([]string, 0, len(parsed))
	for _, item := range parsed {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// parseTitleComponentsJSON 解析标题组件 JSON 为通用切片。
// 参数/返回：raw 为 JSON 文本；返回组件切片（解析失败返回 nil）。
// 失败场景：不适用。
// 副作用：无。
func parseTitleComponentsJSON(raw string) []any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var parsed []any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return nil
	}
	return parsed
}
