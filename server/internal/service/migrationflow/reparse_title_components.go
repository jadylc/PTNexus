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
// 参数/返回：payload 可含 site_name / torrent_id 限定范围（均可选，缺省为全量）；
// 返回 success/message/updated/skipped/failed/total。
// 失败场景：数据库读取或写入失败时返回对应错误码。
// 副作用：批量更新 seed_parameters.title_components。
// 说明：这是给存量数据用的"重算口子"——标题解析规则升级（如 HDR10+/PMTP/HDTVRip
// 识别修复）后，已有数据不会自动刷新，需要显式重算。
// 输入取 title（缺省回退 name），媒体文本取 mediainfo 列；解析与入库流程一致。
func (s *MigrateService) ReparseTitleComponents(payload map[string]any) (map[string]any, int) {
	siteName := strings.TrimSpace(processingshared.ToString(payload["site_name"], ""))
	torrentID := strings.TrimSpace(processingshared.ToString(payload["torrent_id"], ""))

	selectSQL := "SELECT hash, torrent_id, site_name, title, subtitle, name, mediainfo FROM seed_parameters"
	where := make([]string, 0)
	args := make([]any, 0)
	if siteName != "" {
		where = append(where, "site_name = ?")
		args = append(args, siteName)
	}
	if torrentID != "" {
		where = append(where, "torrent_id = ?")
		args = append(args, torrentID)
	}
	if len(where) > 0 {
		selectSQL += " WHERE " + strings.Join(where, " AND ")
	}

	rows := []map[string]any{}
	if err := s.repo.DB().Raw(selectSQL, args...).Scan(&rows).Error; err != nil {
		logx.Errorf(reparseTitleComponentsLogModule, "读取种子数据失败 site=%s torrent_id=%s err=%v", siteName, torrentID, err)
		return map[string]any{"success": false, "message": "读取种子数据失败: " + err.Error()}, 500
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	updated, skipped, failed := 0, 0, 0
	for _, row := range rows {
		rowHash := strings.TrimSpace(processingshared.ToString(row["hash"], ""))
		rowTorrentID := strings.TrimSpace(processingshared.ToString(row["torrent_id"], ""))
		rowSite := strings.TrimSpace(processingshared.ToString(row["site_name"], ""))
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

	logx.Infof(reparseTitleComponentsLogModule, "标题重算完成 site=%s torrent_id=%s total=%d updated=%d skipped=%d failed=%d", siteName, torrentID, len(rows), updated, skipped, failed)
	return map[string]any{
		"success": true,
		"message": "重算完成",
		"total":   len(rows),
		"updated": updated,
		"skipped": skipped,
		"failed":  failed,
	}, 200
}
