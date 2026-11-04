package migrationflow

import (
	"strings"

	acquirefetch "github.com/pt-nexus/server/internal/service/acquire/fetch"
)

// 本文件把抓取链路内部的中文步骤名映射为批量任务的阶段事件（阶段字典见 batch_fetch_detail.go）。

// batchStageIDForStep 将抓取内部 emitLog 的步骤名映射为阶段 ID。
// 参数/返回：step 为内部步骤名（"开始抓取"/"下载种子"/"修复截图" 等）；
// 返回阶段字典键；未识别的步骤返回空串（只进日志尾，不上步骤条）。
// 失败场景：不适用。
// 副作用：无。
func batchStageIDForStep(step string) string {
	switch strings.TrimSpace(step) {
	case "开始抓取", "站点校验":
		return acquirefetch.StageDetail
	case "下载种子":
		return acquirefetch.StageTorrent
	case "解析种子":
		return acquirefetch.StageParse
	case "抓取修复":
		return acquirefetch.StageRepair
	case "修复海报":
		return acquirefetch.StagePoster
	case "修复简介":
		return acquirefetch.StageIntro
	case "修复截图":
		return acquirefetch.StageScreens
	case "修复媒体信息":
		return acquirefetch.StageMediainfo
	case "数据库查询", "写入数据库":
		return acquirefetch.StagePersist
	case "完成":
		return acquirefetch.StageDone
	default:
		return ""
	}
}

// batchStageStateForStatus 将内部日志状态映射为阶段状态。
func batchStageStateForStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "processing":
		return "running"
	case "success":
		return "done"
	case "error":
		return "failed"
	case "warning":
		return "warning"
	default:
		return "running"
	}
}

// newBatchStageHook 构造透传给 FetchAndStore 的阶段钩子：
// 将内部步骤事件写入批量任务细粒度状态（阶段条 + 日志尾）。
// 参数/返回：state 为批量状态管理器、taskID 为批量任务标识；返回钩子函数。
// 失败场景：state/taskID 无效时钩子内部静默。
// 副作用：无（写入由状态管理器完成）。
func (s *MigrateService) newBatchStageHook(taskID string) func(name, step, message, status string) {
	return func(name, step, message, status string) {
		if s == nil || s.batchFetchState == nil {
			return
		}
		stageID := batchStageIDForStep(step)
		stageState := batchStageStateForStatus(status)
		if stageID != "" {
			s.batchFetchState.ApplyItemStage(taskID, name, stageID, stageState, message)
		}
		if strings.TrimSpace(message) != "" {
			s.batchFetchState.AppendItemLog(taskID, name, step+": "+message)
		}
	}
}
