package migrationflow

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/repository"
	acquirefetch "github.com/pt-nexus/server/internal/service/acquire/fetch"
	processingshared "github.com/pt-nexus/server/internal/service/processing/shared"
	publishworkflow "github.com/pt-nexus/server/internal/service/publish/workflow"
)

const taskRunLogModule = "批次任务-续跑"

// memoryTaskRetention 是内存态批次的保留时长：历史批次已经落库，内存只需要覆盖
// "刚结束、前端可能还在查看结果"的窗口。运行中的批次不会被清理。
const memoryTaskRetention = 2 * time.Hour

// pruneMemoryTaskStates 清理过期内存态批次，避免批量发布/批量获取的历史长期占内存，
// 同时避免每次轮询面板都全量深拷贝这些历史任务的明细。
func (s *MigrateService) pruneMemoryTaskStates() {
	if s == nil {
		return
	}

	cutoff := time.Now().Add(-memoryTaskRetention)
	if removed := s.publishState.PruneFinished(cutoff); removed > 0 {
		logx.Infof(taskRunLogModule, "已清理内存态批量发布批次 %d 个", removed)
	}
	if removed := s.batchFetchState.PruneFinished(cutoff); removed > 0 {
		logx.Infof(taskRunLogModule, "已清理内存态批量获取任务 %d 个", removed)
	}
}

// taskRunJSON 序列化续跑所需的入参；失败只记日志，不影响主流程。
func taskRunJSON(payload map[string]any) string {
	if payload == nil {
		return "{}"
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// taskRunStart 登记批次及其条目（全部初始为 pending），使任务可跨重启续跑。
// 副作用：写入 task_runs / task_run_items。仓储未注入时静默跳过（不影响原有内存态执行）。
func (s *MigrateService) taskRunStart(runID, kind, payloadJSON string, concurrency int, targets []string) {
	if s == nil || s.taskRunRepo == nil {
		return
	}
	if err := s.taskRunRepo.CreateRun(runID, kind, payloadJSON, concurrency, targets); err != nil {
		logx.Warnf(taskRunLogModule, "登记批次失败 kind=%s run_id=%s err=%v", kind, runID, err)
	}
}

// taskRunItemDone 记录单个条目的终态（succeeded/failed/skipped）。
func (s *MigrateService) taskRunItemDone(runID, target, status, message string) {
	if s == nil || s.taskRunRepo == nil {
		return
	}
	if strings.TrimSpace(target) == "" {
		return
	}
	if err := s.taskRunRepo.FinishItem(runID, target, status, message); err != nil {
		logx.Warnf(taskRunLogModule, "写入条目结果失败 run_id=%s target=%s err=%v", runID, target, err)
	}
}

// taskRunDone 结束批次。
func (s *MigrateService) taskRunDone(runID, status, message string) {
	if s == nil || s.taskRunRepo == nil {
		return
	}
	if err := s.taskRunRepo.FinishRun(runID, status, message); err != nil {
		logx.Warnf(taskRunLogModule, "结束批次失败 run_id=%s err=%v", runID, err)
	}
}

// pendingTargets 返回某批次尚未执行的条目（pending/running 都算未完成：
// running 只可能是崩溃残留，重放它才不会永远停在"处理中"）。
func (s *MigrateService) pendingTargets(runID string) []string {
	if s == nil || s.taskRunRepo == nil {
		return []string{}
	}
	items, err := s.taskRunRepo.ListItems(runID)
	if err != nil {
		logx.Warnf(taskRunLogModule, "读取批次条目失败 run_id=%s err=%v", runID, err)
		return []string{}
	}
	targets := make([]string, 0, len(items))
	for _, item := range items {
		if item.Status == repository.TaskItemStatusSucceeded ||
			item.Status == repository.TaskItemStatusFailed ||
			item.Status == repository.TaskItemStatusSkipped {
			continue
		}
		if target := strings.TrimSpace(item.Target); target != "" {
			targets = append(targets, target)
		}
	}
	return targets
}

// StartTaskRunResume 在进程启动后续跑被中断的批次。
// 参数/返回：无；不阻塞调用方（后台 goroutine 执行）。
// 失败场景：仓储未注入直接返回；单个批次入参解析失败只把该批次标为 interrupted，不影响其他批次。
// 副作用：更新 task_runs / task_run_items，并重新拉起对应的内存态任务与执行 goroutine。
func (s *MigrateService) StartTaskRunResume() {
	if s == nil || s.taskRunRepo == nil {
		return
	}

	runs, err := s.taskRunRepo.ListResumableRuns()
	if err != nil {
		logx.Warnf(taskRunLogModule, "查询待续跑批次失败 err=%v", err)
		return
	}
	if len(runs) == 0 {
		return
	}

	logx.Infof(taskRunLogModule, "发现 %d 个中断批次，准备续跑", len(runs))
	for _, run := range runs {
		s.resumeTaskRun(run)
	}
}

// resumeTaskRun 续跑单个批次：只执行未完成的条目，已成功的不会重发（条目级幂等）。
func (s *MigrateService) resumeTaskRun(run repository.TaskRun) {
	targets := s.pendingTargets(run.RunID)
	if len(targets) == 0 {
		// 条目都已终态，只是头没来得及结束：按聚合结果收尾。
		summaries, err := s.taskRunRepo.ListRecentRunSummaries(200)
		status := repository.TaskRunStatusCompleted
		if err == nil {
			for _, item := range summaries {
				if item.RunID != run.RunID {
					continue
				}
				if item.Failed > 0 {
					status = repository.TaskRunStatusFailed
				}
				break
			}
		}
		s.taskRunDone(run.RunID, status, "所有条目已执行完毕，续跑时收尾")
		return
	}

	payload := map[string]any{}
	if text := strings.TrimSpace(run.PayloadJSON); text != "" {
		if err := json.Unmarshal([]byte(text), &payload); err != nil || payload == nil {
			logx.Warnf(taskRunLogModule, "续跑入参解析失败，标记为中断 run_id=%s err=%v", run.RunID, err)
			s.taskRunDone(run.RunID, repository.TaskRunStatusInterrupted, "入参无法解析，无法自动续跑")
			return
		}
	}

	concurrency := run.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}

	switch strings.TrimSpace(run.Kind) {
	case repository.TaskRunKindPublishBatch:
		payload["batch_id"] = run.RunID
		// 内存态的 total 用条目总数而不是"剩余数"，否则续跑后面板会显示 0/2 这种失真进度。
		s.publishState.Start(run.RunID, s.taskRunItemCount(run.RunID), concurrency, time.Now())
		logx.Infof(taskRunLogModule, "续跑批量发布 run_id=%s 剩余=%d 并发=%d", run.RunID, len(targets), concurrency)
		go s.resumePublishBatch(run, payload, targets, concurrency)
	case repository.TaskRunKindBatchFetch:
		s.batchFetchState.Start(run.RunID, s.taskRunItemCount(run.RunID))
		logx.Infof(taskRunLogModule, "续跑批量获取 run_id=%s 剩余=%d", run.RunID, len(targets))
		go s.resumeBatchFetch(run, payload, targets)
	default:
		logx.Warnf(taskRunLogModule, "未知批次类型，标记为中断 run_id=%s kind=%s", run.RunID, run.Kind)
		s.taskRunDone(run.RunID, repository.TaskRunStatusInterrupted, "未知的批次类型: "+run.Kind)
	}
}

// taskRunItemCount 返回批次的条目总数（用于恢复内存态进度分母）。
func (s *MigrateService) taskRunItemCount(runID string) int {
	if s == nil || s.taskRunRepo == nil {
		return 0
	}
	items, err := s.taskRunRepo.ListItems(runID)
	if err != nil {
		logx.Warnf(taskRunLogModule, "读取批次条目总数失败 run_id=%s err=%v", runID, err)
		return 0
	}
	return len(items)
}

// resumePublishBatch 继续发布剩余目标站点。
func (s *MigrateService) resumePublishBatch(run repository.TaskRun, payload map[string]any, targets []string, concurrency int) {
	var mu sync.Mutex
	publishworkflow.RunManagedBatchPublishFromPayload(
		publishworkflow.ManagedBatchFromPayloadInput{
			BatchID:     run.RunID,
			Targets:     targets,
			Concurrency: concurrency,
			Payload:     payload,
		},
		publishworkflow.ManagedBatchFromPayloadDeps{
			State:          s.publishState,
			PublishPayload: s.Publish,
			OnSiteFinished: func(siteName string, result map[string]any) {
				mu.Lock()
				defer mu.Unlock()
				message := taskRunResultMessage(result)
				if taskRunResultSuccess(result) {
					s.taskRunItemDone(run.RunID, siteName, repository.TaskItemStatusSucceeded, message)
					return
				}
				s.taskRunItemDone(run.RunID, siteName, repository.TaskItemStatusFailed, message)
			},
		},
	)
	s.finishTaskRun(run.RunID, repository.TaskRunKindPublishBatch)
}

// resumeBatchFetch 继续抓取剩余种子。
func (s *MigrateService) resumeBatchFetch(run repository.TaskRun, payload map[string]any, targets []string) {
	sourcePriority := processingshared.ToStringSlice(payload["source_priority"])
	excludedSites := processingshared.ToStringSlice(payload["excluded_sites"])
	rows, err := s.repo.ListTorrentsByNames(targets)
	if err != nil {
		for _, name := range targets {
			s.taskRunItemDone(run.RunID, name, repository.TaskItemStatusFailed, "读取 torrents 失败: "+err.Error())
		}
		s.finishTaskRun(run.RunID, repository.TaskRunKindBatchFetch)
		return
	}

	acquirefetch.RunBatchFetch(targets, sourcePriority, excludedSites, rows, acquirefetch.BatchFetchRunnerDeps{
		FetchAndStore: s.FetchAndStore,
		OnResult: func(success bool, result map[string]any) {
			name := strings.TrimSpace(processingshared.ToString(result["name"], ""))
			message := taskRunResultMessage(result)
			if success {
				s.taskRunItemDone(run.RunID, name, repository.TaskItemStatusSucceeded, message)
				return
			}
			s.taskRunItemDone(run.RunID, name, repository.TaskItemStatusFailed, message)
		},
		ShouldCancel: func() bool {
			return s.batchFetchState.IsCancelled(run.RunID)
		},
	})
	s.finishTaskRun(run.RunID, repository.TaskRunKindBatchFetch)
}

// finishTaskRun 按条目聚合结果决定批次终态。
func (s *MigrateService) finishTaskRun(runID, kind string) {
	if s == nil || s.taskRunRepo == nil {
		return
	}
	status := repository.TaskRunStatusCompleted
	message := ""
	if summaries, err := s.taskRunRepo.ListRecentRunSummaries(200); err == nil {
		for _, item := range summaries {
			if item.RunID != runID {
				continue
			}
			if item.Failed > 0 {
				status = repository.TaskRunStatusFailed
				message = "存在失败条目"
			}
			break
		}
	}
	s.taskRunDone(runID, status, message)
}

// taskRunResultMessage 从执行结果里挑一条可读的说明用于落库。
func taskRunResultMessage(result map[string]any) string {
	if result == nil {
		return ""
	}
	for _, key := range []string{"logs", "message", "reason", "error"} {
		if text := strings.TrimSpace(processingshared.ToString(result[key], "")); text != "" {
			return text
		}
	}
	return ""
}

// taskRunResultSuccess 判断结果是否成功（兼容 bool 与字符串两种写法）。
func taskRunResultSuccess(result map[string]any) bool {
	if result == nil {
		return false
	}
	return processingshared.ToBool(result["success"])
}
