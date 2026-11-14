package persist

import (
	"fmt"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	processingack "github.com/pt-nexus/server/internal/service/processing/acknowledgment"
)

// ManualUpdateRepo 定义手工参数回写所需仓储接口。
type ManualUpdateRepo interface {
	GetSeedParameter(torrentID, siteName string) (map[string]any, error)
	UpsertSeedParameter(record map[string]any) error
	ListSitesGroupAndDescription() ([]map[string]any, error)
}

// ApplyManualUpdateFromPayload 执行转种面板手工参数回写，并返回发布预览所需结构。
// 参数/返回：payload 为前端提交参数；newID 用于生成兜底 hash；reverseMappings 为前端映射配置。
// 失败场景：缺少 torrent_id/site_name 或写库失败时返回对应状态码。
// 副作用：覆盖写入 seed_parameters（Delete + Create）。
func ApplyManualUpdateFromPayload(
	repo ManualUpdateRepo,
	payload map[string]any,
	newID func(prefix string) string,
	reverseMappings map[string]any,
) (map[string]any, int) {
	torrentID := strings.TrimSpace(toStringAny(payload["torrent_id"], ""))
	siteName := strings.TrimSpace(toStringAny(payload["site_name"], ""))
	torrentName := strings.TrimSpace(toStringAny(payload["torrent_name"], ""))
	updated, _ := payload["updated_parameters"].(map[string]any)
	if updated == nil {
		updated = map[string]any{}
	}
	if torrentID == "" || siteName == "" {
		return map[string]any{"success": false, "message": "错误：torrent_id 和 site_name 不能为空"}, 400
	}

	existing, _ := repo.GetSeedParameter(torrentID, siteName)
	generatedHash := strings.TrimSpace(toStringAny(existing["hash"], ""))
	if generatedHash == "" {
		if newID != nil {
			generatedHash = strings.TrimSpace(newID("hash"))
		}
		if generatedHash == "" {
			generatedHash = fmt.Sprintf("hash-%d", time.Now().UnixNano())
		}
	}

	buildResult := BuildManualUpdatedSeedRecord(BuildManualUpdateInput{
		GeneratedHash: generatedHash,
		TorrentID:     torrentID,
		SiteName:      siteName,
		TorrentName:   torrentName,
		Existing:      existing,
		Updated:       updated,
	})
	standardized := buildResult.Standardized
	record := buildResult.Record

	// 维护通过的规范性把关（与批量检测同一口径）：面板保存的产物先过
	// NormalizeSeedRow（得到与批量检测同形态的行），再跑统一校验——
	// 不合规拒绝保存并提示具体问题，用户修复后再点完成。
	// 注意：必须把标准化参数并进校验行——批量检测的 QueryAndNormalizeSeed
	// 自带 standardized_params，面板保存产物需手动补上，否则校验会把已填写的
	// 标准参数一律判为空（2026.11 误报「标准参数 type 为空」的根因）。
	normalizedRecord := NormalizeSeedRow(record)
	normalizedRecord["standardized_params"] = standardized
	if problems := CheckSeedMaintainability(normalizedRecord); len(problems) > 0 {
		return map[string]any{
			"success": false,
			"message": "维护未通过，请补全以下内容后重试：" + strings.Join(problems, "；"),
		}, 422
	}

	applyManualUpdateTeamAcknowledgment(repo, record)
	if err := repo.UpsertSeedParameter(record); err != nil {
		return map[string]any{"success": false, "message": "更新失败: " + err.Error()}, 500
	}

	normalized := NormalizeSeedRow(record)
	normalized["standardized_params"] = standardized
	return map[string]any{
		"success":                  true,
		"standardized_params":      standardized,
		"final_publish_parameters": BuildFinalPublishParameters(normalized),
		"complete_publish_params":  BuildCompletePublishParams(normalized),
		"raw_params_for_preview":   BuildRawPreviewParams(normalized),
		"reverse_mappings":         reverseMappings,
		"message":                  "参数更新并标准化成功",
	}, 200
}

func applyManualUpdateTeamAcknowledgment(repo ManualUpdateRepo, record map[string]any) {
	if repo == nil || record == nil {
		return
	}
	rawSites, err := repo.ListSitesGroupAndDescription()
	if err != nil {
		logx.Warnf(teamAckLogModule, "官组致谢读取sites失败(手工回写) torrent_id=%s site=%s err=%v",
			strings.TrimSpace(toStringAny(record["torrent_id"], "")),
			strings.TrimSpace(toStringAny(record["site_name"], "")),
			err,
		)
		return
	}
	sites := make([]processingack.SiteRow, 0, len(rawSites))
	for _, row := range rawSites {
		sites = append(sites, processingack.SiteRow{
			Group:       strings.TrimSpace(toStringAny(row["group"], "")),
			Description: strings.TrimSpace(toStringAny(row["description"], "")),
		})
	}
	teamKey := strings.TrimSpace(toStringAny(record["team"], ""))
	statement := strings.TrimSpace(toStringAny(record["statement"], ""))
	logx.Infof(teamAckLogModule, "官组致谢检查(手工回写) torrent_id=%s site=%s team_key=%s statement_len=%d",
		strings.TrimSpace(toStringAny(record["torrent_id"], "")),
		strings.TrimSpace(toStringAny(record["site_name"], "")),
		teamKey,
		len([]rune(statement)),
	)
	updated, applied, _ := processingack.ApplyTeamAcknowledgmentIfNeeded(statement, teamKey, sites)
	if applied {
		record["statement"] = updated
	}
}
