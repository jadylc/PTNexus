package fetch

import (
	"sync"
	"time"
)

const batchFetchTimeLayout = "2006-01-02 15:04:05"

// BatchFetchTask 表示批量抓取任务的进度快照。
type BatchFetchTask struct {
	Total     int              `json:"total"`
	Processed int              `json:"processed"`
	Success   int              `json:"success"`
	Failed    int              `json:"failed"`
	Skipped   int              `json:"skipped"`
	IsRunning bool             `json:"isRunning"`
	Cancelled bool             `json:"cancelled"`
	StartedAt string           `json:"started_at"`
	UpdatedAt string           `json:"updated_at"`
	Results   []map[string]any `json:"results"`
}

// BatchFetchState 管理批量抓取任务的内存状态。
type BatchFetchState struct {
	mu    sync.RWMutex
	tasks map[string]*BatchFetchTask
}

// NewBatchFetchState 创建批量抓取状态管理器。
func NewBatchFetchState() *BatchFetchState {
	return &BatchFetchState{tasks: map[string]*BatchFetchTask{}}
}

// Start 注册并初始化一个批量抓取任务。
func (s *BatchFetchState) Start(taskID string, total int) {
	if s == nil {
		return
	}
	now := time.Now().Format(batchFetchTimeLayout)
	s.mu.Lock()
	s.tasks[taskID] = &BatchFetchTask{
		Total:     total,
		Processed: 0,
		Success:   0,
		Failed:    0,
		Skipped:   0,
		IsRunning: true,
		StartedAt: now,
		UpdatedAt: now,
		Results:   []map[string]any{},
	}
	s.mu.Unlock()
}

// MarkResult 记录单项抓取结果并更新计数。
func (s *BatchFetchState) MarkResult(taskID string, success bool, result map[string]any) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	task := s.tasks[taskID]
	if task != nil {
		task.Processed++
		if success {
			task.Success++
		} else {
			task.Failed++
		}
		task.UpdatedAt = time.Now().Format(batchFetchTimeLayout)
		task.Results = append(task.Results, cloneAnyMap(result))
	}
	s.mu.Unlock()
	return task != nil
}

// Cancel 请求取消任务：只置标志位，由执行侧在条目边界协作退出（IsCancelled 判定）。
// 参数/返回：taskID 任务标识；返回任务是否存在。
func (s *BatchFetchState) Cancel(taskID string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[taskID]
	if task == nil {
		return false
	}
	task.Cancelled = true
	task.UpdatedAt = time.Now().Format(batchFetchTimeLayout)
	return true
}

// IsCancelled 判断任务是否已被请求取消；任务不存在时视为已取消，避免执行侧空转。
func (s *BatchFetchState) IsCancelled(taskID string) bool {
	if s == nil {
		return true
	}
	s.mu.RLock()
	task := s.tasks[taskID]
	s.mu.RUnlock()
	return task == nil || task.Cancelled
}

// Finish 标记批量抓取任务结束（不改变取消标志，也不中断执行侧）。
func (s *BatchFetchState) Finish(taskID string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	task := s.tasks[taskID]
	if task != nil {
		task.IsRunning = false
		task.UpdatedAt = time.Now().Format(batchFetchTimeLayout)
	}
	s.mu.Unlock()
	return task != nil
}

// Snapshot 返回任务状态副本。
func (s *BatchFetchState) Snapshot(taskID string) (BatchFetchTask, bool) {
	if s == nil {
		return BatchFetchTask{}, false
	}
	s.mu.RLock()
	task := s.tasks[taskID]
	s.mu.RUnlock()
	if task == nil {
		return BatchFetchTask{}, false
	}
	return copyBatchFetchTask(task), true
}

// SnapshotAll 返回全部批量抓取任务快照。
// 参数/返回：无参数；返回 task_id 到任务快照的映射。
// 失败场景：状态管理器为空时返回空映射。
// 副作用：无。
func (s *BatchFetchState) SnapshotAll() map[string]BatchFetchTask {
	if s == nil {
		return map[string]BatchFetchTask{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]BatchFetchTask, len(s.tasks))
	for taskID, task := range s.tasks {
		if task == nil {
			continue
		}
		result[taskID] = copyBatchFetchTask(task)
	}
	return result
}

// copyBatchFetchTask 深拷贝任务快照，避免调用方读到执行中不断增长的 Results 切片。
func copyBatchFetchTask(task *BatchFetchTask) BatchFetchTask {
	copied := *task
	copied.Results = make([]map[string]any, 0, len(task.Results))
	for _, item := range task.Results {
		copied.Results = append(copied.Results, cloneAnyMap(item))
	}
	return copied
}

func cloneAnyMap(source map[string]any) map[string]any {
	if source == nil {
		return map[string]any{}
	}
	copied := make(map[string]any, len(source))
	for key, value := range source {
		copied[key] = value
	}
	return copied
}
