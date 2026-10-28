package migrationflow

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	extract "github.com/pt-nexus/server/internal/service/acquire/extract"
	processingshared "github.com/pt-nexus/server/internal/service/processing/shared"
	processingtitle "github.com/pt-nexus/server/internal/service/processing/title"
)

const reparseTitleComponentsLogModule = "迁移-标题重算"

// ReparseTitleComponents 按当前解析规则重建 seed_parameters.title_components 并写回。
// 参数/返回：payload 可含 site_name / torrent_id 限定范围；only_errors 控制范围——
// true（批量默认）仅处理标题组件含「无法识别」的行，正确数据与人工修改过的行一律不动；
// false（维护详情页单条显式重算）处理指定行；返回 success/message/updated/skipped/failed/total。
// 失败场景：数据库读取或写入失败时返回对应错误码。
// 副作用：批量更新 seed_parameters.title_components（仅范围内且解析结果有变化的行）。
// 说明：这是给存量数据用的"重算口子"——标题解析规则升级（如 HDR10+/PMTP/HDTVRip
// 识别修复）后，已有数据不会自动刷新，需要显式重算。
// 输入取 title（缺省回退 name），媒体文本取 mediainfo 列；解析与入库流程一致。
// 另外解析结果与库中内容一致时跳过写回，避免无谓地刷新 updated_at。
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

	selectSQL := "SELECT hash, torrent_id, site_name, title, subtitle, name, mediainfo, title_components FROM seed_parameters"
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
		where = append(where, "title_components LIKE ?")
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
		// 解析结果与库中一致：内容没有变化，跳过写回（保护 updated_at 与人工状态）。
		if strings.TrimSpace(string(encoded)) == storedComponents && fixedTitle == title && fixedSubtitle == subtitle {
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
