package migrationflow

import (
	"strings"

	"github.com/pt-nexus/server/internal/repository"
	acquirefetch "github.com/pt-nexus/server/internal/service/acquire/fetch"
	processingshared "github.com/pt-nexus/server/internal/service/processing/shared"
)

func (s *MigrateService) StartBatchFetch(torrentNames []string, sourcePriority []string, excludedSites []string) (map[string]any, int) {
	if len(torrentNames) == 0 {
		return map[string]any{"success": false, "message": "错误：种子名称列表不能为空"}, 400
	}
	if len(sourcePriority) == 0 {
		configured := s.cfg.Get()
		sourcePriority = processingshared.ToStringSlice(configured["source_priority"])
	}
	if len(sourcePriority) == 0 {
		return map[string]any{"success": false, "message": "错误：请先在设置中配置源站点优先级"}, 400
	}
	// 未显式传入屏蔽列表时，回退到配置中的 source_priority_excluded
	if len(excludedSites) == 0 {
		configured := s.cfg.Get()
		excludedSites = processingshared.ToStringSlice(configured["source_priority_excluded"])
	}
	taskID := s.newID("fetch")
	s.batchFetchState.Start(taskID, len(torrentNames))
	// 逐条落库，使重启后可以只重跑未完成的部分（抓取本身可重复执行，覆盖即可）。
	s.taskRunStart(taskID, repository.TaskRunKindBatchFetch, taskRunJSON(map[string]any{"source_priority": sourcePriority, "excluded_sites": excludedSites}), 1, torrentNames)
	go s.runBatchFetch(taskID, torrentNames, sourcePriority, excludedSites)
	return map[string]any{"success": true, "task_id": taskID, "message": "批量获取任务已启动"}, 200
}

func (s *MigrateService) runBatchFetch(taskID string, torrentNames []string, sourcePriority []string, excludedSites []string) {
	rows, err := s.repo.ListTorrentsByNames(torrentNames)
	if err != nil {
		for _, name := range torrentNames {
			reason := "读取 torrents 失败: " + err.Error()
			s.batchFetchState.MarkResult(taskID, false, map[string]any{
				"name":   name,
				"status": "failed",
				"reason": reason,
			})
			s.taskRunItemDone(taskID, name, repository.TaskItemStatusFailed, reason)
		}
		s.batchFetchState.Finish(taskID)
		s.finishTaskRun(taskID, repository.TaskRunKindBatchFetch)
		return
	}

	acquirefetch.RunBatchFetch(torrentNames, sourcePriority, excludedSites, rows, acquirefetch.BatchFetchRunnerDeps{
		FetchAndStore: s.FetchAndStore,
		OnResult: func(success bool, result map[string]any) {
			s.batchFetchState.MarkResult(taskID, success, result)
			name := strings.TrimSpace(processingshared.ToString(result["name"], ""))
			message := taskRunResultMessage(result)
			if success {
				s.taskRunItemDone(taskID, name, repository.TaskItemStatusSucceeded, message)
				return
			}
			s.taskRunItemDone(taskID, name, repository.TaskItemStatusFailed, message)
		},
		// 协作式取消：任务监控里的"终止"置标志位，这里在下一个种子前退出，
		// 不中断正在抓的当前项，避免留下半写状态。
		ShouldCancel: func() bool {
			return s.batchFetchState.IsCancelled(taskID)
		},
	})
	s.batchFetchState.Finish(taskID)
	s.finishTaskRun(taskID, repository.TaskRunKindBatchFetch)
}

func (s *MigrateService) BatchFetchProgress(taskID string) (map[string]any, int) {
	task, ok := s.batchFetchState.Snapshot(taskID)
	if !ok {
		return map[string]any{"success": false, "message": "任务不存在（批量抓取状态仅存内存，进程重启后不会保留）"}, 404
	}
	return map[string]any{"success": true, "progress": task}, 200
}
