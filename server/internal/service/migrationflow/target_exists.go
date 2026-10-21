package migrationflow

import (
	"strings"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/repository"
	processingshared "github.com/pt-nexus/server/internal/service/processing/shared"
)

// TargetExistsCheckInput 定义“目标站已存在种子”检测输入（转种/发布前置使用）。
// Hash 为内容指纹；Name+Size 为组指纹；TorrentID+SourceSite 为源站种子标识。
type TargetExistsCheckInput struct {
	Hash       string
	Name       string
	Size       int64 // -1 表示未知
	TorrentID  string
	SourceSite string
	TargetSite string
}

// TargetExistsCheckResult 定义“目标站已存在种子”检测结果。
// Exists 为 true 时 Source 标识命中来源：snapshot（下载器快照）/queue（发布队列）/publish_log（发种日志）。
type TargetExistsCheckResult struct {
	Exists bool
	Source string
	Detail string
}

// CheckTargetExists 在本地数据中综合检测目标站是否已存在该种子（统一入口）。
// 检测顺序：下载器快照 → 发布队列 → 发种日志；命中任一即返回。
// 参数/返回：input 需包含目标站与至少一种指纹；返回命中结果与 source/detail。
// 失败场景：仓库未初始化或 SQL 错误时记录日志并返回未命中，避免阻塞转种/发布主流程。
// 副作用：仅读取数据库。
func (s *MigrateService) CheckTargetExists(input TargetExistsCheckInput) TargetExistsCheckResult {
	if s == nil || s.repo == nil {
		return TargetExistsCheckResult{}
	}
	if strings.TrimSpace(input.TargetSite) == "" {
		return TargetExistsCheckResult{}
	}

	source, detail, err := s.repo.CheckTargetExists(repository.TargetExistsQuery{
		Hash:       strings.TrimSpace(input.Hash),
		Name:       strings.TrimSpace(input.Name),
		Size:       input.Size,
		TorrentID:  strings.TrimSpace(input.TorrentID),
		SourceSite: strings.TrimSpace(input.SourceSite),
		TargetSite: strings.TrimSpace(input.TargetSite),
	})
	if err != nil {
		logx.Warnf(publishLogModule, "目标站已存在检测失败 target=%s torrent_id=%s err=%v",
			strings.TrimSpace(input.TargetSite), strings.TrimSpace(input.TorrentID), err)
		return TargetExistsCheckResult{}
	}
	if source == "" {
		return TargetExistsCheckResult{}
	}
	return TargetExistsCheckResult{
		Exists: true,
		Source: source,
		Detail: detail,
	}
}

// CheckTargetExistsWithAliases 是 CheckTargetExists 的带别名版本，用于批量入队等循环场景。
// aliases 由调用方预先解析一次（ResolveTargetSiteAliases），循环内避免对每个种子重复查询 sites 表。
func (s *MigrateService) CheckTargetExistsWithAliases(input TargetExistsCheckInput, aliases []string) TargetExistsCheckResult {
	if s == nil || s.repo == nil {
		return TargetExistsCheckResult{}
	}
	if strings.TrimSpace(input.TargetSite) == "" {
		return TargetExistsCheckResult{}
	}

	source, detail, err := s.repo.CheckTargetExistsWithAliases(repository.TargetExistsQuery{
		Hash:       strings.TrimSpace(input.Hash),
		Name:       strings.TrimSpace(input.Name),
		Size:       input.Size,
		TorrentID:  strings.TrimSpace(input.TorrentID),
		SourceSite: strings.TrimSpace(input.SourceSite),
		TargetSite: strings.TrimSpace(input.TargetSite),
	}, aliases)
	if err != nil {
		logx.Warnf(publishLogModule, "目标站已存在检测失败 target=%s torrent_id=%s err=%v",
			strings.TrimSpace(input.TargetSite), strings.TrimSpace(input.TorrentID), err)
		return TargetExistsCheckResult{}
	}
	if source == "" {
		return TargetExistsCheckResult{}
	}
	return TargetExistsCheckResult{
		Exists: true,
		Source: source,
		Detail: detail,
	}
}

// ResolveTargetSiteAliases 解析目标站站点昵称/代码的全部别名（批量循环复用）。
func (s *MigrateService) ResolveTargetSiteAliases(targetSite string) []string {
	if s == nil || s.repo == nil {
		return nil
	}
	aliases, err := s.repo.ResolveSiteAliases(strings.TrimSpace(targetSite))
	if err != nil {
		logx.Warnf(publishLogModule, "解析目标站别名失败 target=%s err=%v", strings.TrimSpace(targetSite), err)
		return nil
	}
	if len(aliases) == 0 {
		return []string{strings.TrimSpace(targetSite)}
	}
	return aliases
}

// publishTargetUpdateIntent 判断用户是否明确要更新/添加已存在种子（此时不应拦截发种）。
// 开启 auto_update_existing_torrent 或 auto_add_existing_to_downloader 视为更新意图。
func publishTargetUpdateIntent(payload map[string]any) bool {
	if payload == nil {
		return false
	}
	extract := func(keys ...string) bool {
		for _, key := range keys {
			if value, ok := payload[key]; ok {
				switch typed := value.(type) {
				case bool:
					return typed
				case string:
					return strings.TrimSpace(typed) == "true"
				}
			}
		}
		return false
	}
	return extract("auto_update_existing_torrent", "autoUpdateExistingTorrent") ||
		extract("auto_add_existing_to_downloader", "autoAddExistingToDownloader")
}

// targetExistsCheckPayloadFingerprint 从 payload 中提取检测所需指纹字段。
// 返回（hash, name, size, torrentID, sourceSite）。
func targetExistsCheckPayloadFingerprint(payload map[string]any) (string, string, int64, string, string) {
	if payload == nil {
		return "", "", -1, "", ""
	}
	toString := func(keys ...string) string {
		for _, key := range keys {
			if value, ok := payload[key]; ok {
				if text := strings.TrimSpace(processingshared.ToString(value, "")); text != "" {
					return text
				}
			}
		}
		return ""
	}
	hash := toString("hash")
	name := toString("name", "title")
	size := int64(processingshared.ToFloat(payload["size"]))
	if size <= 0 {
		size = -1
	}
	torrentID := toString("torrent_id", "torrentId")
	sourceSite := toString("sourceSite", "source_site", "source_site_name", "nickname")
	return hash, name, size, torrentID, sourceSite
}
