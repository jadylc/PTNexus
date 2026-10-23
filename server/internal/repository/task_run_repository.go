package repository

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// 任务运行（task_runs / task_run_items）用于让"批量发布""批量获取"这类
// 原先只活在内存里的批次任务具备：跨进程重启不丢、可续跑、进度可查。
//
// 设计要点：
//   - 一张头表 + 一张条目表。条目的幂等粒度就是"一次可重放的最小动作"
//     （批量发布 = 一个目标站点；批量获取 = 一个种子名）。
//   - 进度里的 total/processed/success/failed 一律由条目表聚合得出，
//     不存冗余计数列，避免头表计数和条目真实状态不同步（第二份真相）。
//   - 重启续跑 = 只重放非终态条目；已 succeeded/failed 的条目不再执行，
//     因此天然幂等，不会重复发种（发布侧另有 ExistsSucceededPublish 兜底）。
const (
	TaskRunKindPublishBatch = "publish_batch"
	TaskRunKindBatchFetch   = "batch_fetch"
)

const (
	TaskRunStatusRunning     = "running"
	TaskRunStatusCompleted   = "completed"
	TaskRunStatusFailed      = "failed"
	TaskRunStatusCancelled   = "cancelled"
	TaskRunStatusInterrupted = "interrupted"
)

const (
	TaskItemStatusPending   = "pending"
	TaskItemStatusRunning   = "running"
	TaskItemStatusSucceeded = "succeeded"
	TaskItemStatusFailed    = "failed"
	TaskItemStatusSkipped   = "skipped"
)

// TaskRun 表示一个批次任务的头信息。
type TaskRun struct {
	ID          int64  `gorm:"column:id;primaryKey" json:"id"`
	RunID       string `gorm:"column:run_id" json:"run_id"`
	Kind        string `gorm:"column:kind" json:"kind"`
	Status      string `gorm:"column:status" json:"status"`
	PayloadJSON string `gorm:"column:payload_json" json:"-"`
	Concurrency int    `gorm:"column:concurrency" json:"concurrency"`
	Message     string `gorm:"column:message" json:"message"`
	StartedAt   string `gorm:"column:started_at" json:"started_at"`
	FinishedAt  string `gorm:"column:finished_at" json:"finished_at"`
	CreatedAt   string `gorm:"column:created_at" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at" json:"updated_at"`
}

// TaskRunItem 表示批次内一个可独立重放的条目。
type TaskRunItem struct {
	ID         int64  `gorm:"column:id;primaryKey" json:"id"`
	RunID      string `gorm:"column:run_id" json:"run_id"`
	Seq        int    `gorm:"column:seq" json:"seq"`
	Target     string `gorm:"column:target" json:"target"`
	Status     string `gorm:"column:status" json:"status"`
	Message    string `gorm:"column:message" json:"message"`
	StartedAt  string `gorm:"column:started_at" json:"started_at"`
	FinishedAt string `gorm:"column:finished_at" json:"finished_at"`
	CreatedAt  string `gorm:"column:created_at" json:"created_at"`
	UpdatedAt  string `gorm:"column:updated_at" json:"updated_at"`
}

// TaskRunSummary 是给任务监控面板用的"头 + 聚合进度"视图。
type TaskRunSummary struct {
	TaskRun
	Total     int `json:"total"`
	Processed int `json:"processed"`
	Success   int `json:"success"`
	Failed    int `json:"failed"`
}

// TaskRunRepository 负责批次任务的落库与查询。
type TaskRunRepository struct {
	store *Store
}

// NewTaskRunRepository 创建批次任务仓储。
func NewTaskRunRepository(store *Store) *TaskRunRepository {
	return &TaskRunRepository{store: store}
}

func (r *TaskRunRepository) db() (*gorm.DB, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil, errors.New("task run repo is not initialized")
	}
	return r.store.DB, nil
}

// CreateRun 落库一个批次头 + 其全部条目（条目初始为 pending）。
// 参数/返回：runID 对外标识（沿用前端 batch_id/task_id 语义）；targets 为条目列表，顺序即执行顺序；
// payloadJSON 为续跑所需入参；concurrency 为该批次并发度。
// 失败场景：仓储未初始化或写入失败返回 error。
// 副作用：写入 task_runs 与 task_run_items。
func (r *TaskRunRepository) CreateRun(runID, kind, payloadJSON string, concurrency int, targets []string) error {
	db, err := r.db()
	if err != nil {
		return err
	}
	runID = strings.TrimSpace(runID)
	if runID == "" || len(targets) == 0 {
		return errors.New("run_id 或条目列表为空")
	}
	if concurrency < 1 {
		concurrency = 1
	}

	now := time.Now().Format(TaskRunTimeLayout)
	if err := db.Table("task_runs").Create(map[string]any{
		"run_id":       runID,
		"kind":         strings.TrimSpace(kind),
		"status":       TaskRunStatusRunning,
		"payload_json": payloadJSON,
		"concurrency":  concurrency,
		"message":      "",
		"started_at":   now,
		"finished_at":  nil,
		"created_at":   now,
		"updated_at":   now,
	}).Error; err != nil {
		return fmt.Errorf("创建 task_runs 失败: %w", err)
	}

	rows := make([]map[string]any, 0, len(targets))
	for index, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		rows = append(rows, map[string]any{
			"run_id": runID, "seq": index, "target": target,
			"status": TaskItemStatusPending, "message": "",
			"created_at": now, "updated_at": now,
		})
	}
	if len(rows) == 0 {
		return errors.New("条目全部为空，无法创建批次任务")
	}
	if err := db.Table("task_run_items").Create(rows).Error; err != nil {
		return fmt.Errorf("创建 task_run_items 失败: %w", err)
	}
	return nil
}

// MarkItemRunning 把条目从 pending 置为 running（不动已终态的条目）。
func (r *TaskRunRepository) MarkItemRunning(runID, target string) error {
	db, err := r.db()
	if err != nil {
		return err
	}
	now := time.Now().Format(TaskRunTimeLayout)
	return db.Table("task_run_items").
		Where("run_id = ? AND target = ? AND status IN ?", strings.TrimSpace(runID), strings.TrimSpace(target),
			[]string{TaskItemStatusPending, TaskItemStatusRunning}).
		Updates(map[string]any{"status": TaskItemStatusRunning, "started_at": now, "updated_at": now}).Error
}

// FinishItem 记录条目终态。status 仅接受 succeeded/failed/skipped。
func (r *TaskRunRepository) FinishItem(runID, target, status, message string) error {
	db, err := r.db()
	if err != nil {
		return err
	}
	switch status {
	case TaskItemStatusSucceeded, TaskItemStatusFailed, TaskItemStatusSkipped:
	default:
		return fmt.Errorf("非法的条目终态: %s", status)
	}

	now := time.Now().Format(TaskRunTimeLayout)
	return db.Table("task_run_items").
		Where("run_id = ? AND target = ?", strings.TrimSpace(runID), strings.TrimSpace(target)).
		Updates(map[string]any{"status": status, "message": strings.TrimSpace(message), "finished_at": now, "updated_at": now}).Error
}

// FinishRun 结束批次；cancelled/interrupted 会顺带把未执行条目置为 skipped，避免被续跑。
func (r *TaskRunRepository) FinishRun(runID, status, message string) error {
	db, err := r.db()
	if err != nil {
		return err
	}
	status = strings.TrimSpace(status)
	switch status {
	case TaskRunStatusCompleted, TaskRunStatusFailed, TaskRunStatusCancelled, TaskRunStatusInterrupted:
	default:
		return fmt.Errorf("非法的批次终态: %s", status)
	}

	now := time.Now().Format(TaskRunTimeLayout)
	if err := db.Table("task_runs").
		Where("run_id = ?", strings.TrimSpace(runID)).
		Updates(map[string]any{"status": status, "message": strings.TrimSpace(message), "finished_at": now, "updated_at": now}).Error; err != nil {
		return err
	}
	if status == TaskRunStatusCancelled || status == TaskRunStatusInterrupted {
		return db.Table("task_run_items").
			Where("run_id = ? AND status IN ?", strings.TrimSpace(runID), []string{TaskItemStatusPending, TaskItemStatusRunning}).
			Updates(map[string]any{"status": TaskItemStatusSkipped, "message": "批次已结束，未执行", "updated_at": now}).Error
	}
	return nil
}

// GetRun 按 run_id 读取批次头。
func (r *TaskRunRepository) GetRun(runID string) (TaskRun, bool, error) {
	db, err := r.db()
	if err != nil {
		return TaskRun{}, false, err
	}
	rows := make([]TaskRun, 0, 1)
	if err := db.Table("task_runs").Where("run_id = ?", strings.TrimSpace(runID)).Limit(1).Find(&rows).Error; err != nil {
		return TaskRun{}, false, err
	}
	if len(rows) == 0 {
		return TaskRun{}, false, nil
	}
	return rows[0], true, nil
}

// ListItems 返回某批次的全部条目（按 seq 升序）。
func (r *TaskRunRepository) ListItems(runID string) ([]TaskRunItem, error) {
	db, err := r.db()
	if err != nil {
		return nil, err
	}
	items := make([]TaskRunItem, 0)
	if err := db.Table("task_run_items").
		Where("run_id = ?", strings.TrimSpace(runID)).
		Order("seq ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// ListResumableRuns 返回仍在 running 的批次（进程重启后可续跑）。
func (r *TaskRunRepository) ListResumableRuns() ([]TaskRun, error) {
	db, err := r.db()
	if err != nil {
		return nil, err
	}
	runs := make([]TaskRun, 0)
	if err := db.Table("task_runs").
		Where("status = ?", TaskRunStatusRunning).
		Order("id ASC").Limit(TaskRunResumeBatchLimit).Find(&runs).Error; err != nil {
		return nil, err
	}
	return runs, nil
}

// ResetRunningItems 把批次里卡在 running 的条目退回 pending，供重启后续跑。
// 参数/返回：runID 批次标识；返回被退回的条目数与 error。
func (r *TaskRunRepository) ResetRunningItems(runID string) (int64, error) {
	db, err := r.db()
	if err != nil {
		return 0, err
	}
	now := time.Now().Format(TaskRunTimeLayout)
	result := db.Table("task_run_items").
		Where("run_id = ? AND status = ?", strings.TrimSpace(runID), TaskItemStatusRunning).
		Updates(map[string]any{"status": TaskItemStatusPending, "started_at": nil, "updated_at": now})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

// ListRecentRunSummaries 返回最近若干批次的"头 + 聚合进度"，供任务监控面板消费。
// running 的批次始终排在前面，保证重启前发起的任务依然看得见。
func (r *TaskRunRepository) ListRecentRunSummaries(limit int) ([]TaskRunSummary, error) {
	db, err := r.db()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}

	runs := make([]TaskRun, 0, limit)
	if err := db.Table("task_runs").
		Where("status = ?", TaskRunStatusRunning).
		Order("id DESC").Limit(limit).Find(&runs).Error; err != nil {
		return nil, err
	}
	if remaining := limit - len(runs); remaining > 0 {
		finished := make([]TaskRun, 0, remaining)
		if err := db.Table("task_runs").
			Where("status <> ?", TaskRunStatusRunning).
			Order("id DESC").Limit(remaining).Find(&finished).Error; err != nil {
			return nil, err
		}
		runs = append(runs, finished...)
	}
	if len(runs) == 0 {
		return []TaskRunSummary{}, nil
	}

	runIDs := make([]string, 0, len(runs))
	for _, run := range runs {
		runIDs = append(runIDs, run.RunID)
	}

	type aggRow struct {
		RunID     string `gorm:"column:run_id"`
		Total     int    `gorm:"column:total"`
		Processed int    `gorm:"column:processed"`
		Success   int    `gorm:"column:success"`
		Failed    int    `gorm:"column:failed"`
	}
	aggs := make([]aggRow, 0, len(runIDs))
	if err := db.Table("task_run_items").
		Select(`run_id,
			COUNT(1) AS total,
			SUM(CASE WHEN status IN ('succeeded','failed','skipped') THEN 1 ELSE 0 END) AS processed,
			SUM(CASE WHEN status = 'succeeded' THEN 1 ELSE 0 END) AS success,
			SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) AS failed`).
		Where("run_id IN ?", runIDs).
		Group("run_id").Find(&aggs).Error; err != nil {
		return nil, err
	}

	byRun := make(map[string]aggRow, len(aggs))
	for _, row := range aggs {
		byRun[row.RunID] = row
	}

	summaries := make([]TaskRunSummary, 0, len(runs))
	for _, run := range runs {
		item := byRun[run.RunID]
		summaries = append(summaries, TaskRunSummary{
			TaskRun:   run,
			Total:     item.Total,
			Processed: item.Processed,
			Success:   item.Success,
			Failed:    item.Failed,
		})
	}
	return summaries, nil
}

// DeleteFinishedRunsBefore 按保留策略清理历史批次（头与条目一并删除）。
// 参数/返回：cutoff 为截止时间；返回删除的批次数与 error。
// 失败场景：仓储未初始化或删除失败返回 error。
func (r *TaskRunRepository) DeleteFinishedRunsBefore(cutoff time.Time) (int64, error) {
	db, err := r.db()
	if err != nil {
		return 0, err
	}
	cutoffText := cutoff.Format(TaskRunTimeLayout)

	expired := make([]string, 0)
	if err := db.Table("task_runs").
		Where("status <> ?", TaskRunStatusRunning).
		Where("updated_at < ?", cutoffText).
		Pluck("run_id", &expired).Error; err != nil {
		return 0, err
	}
	if len(expired) == 0 {
		return 0, nil
	}

	if err := db.Table("task_run_items").Where("run_id IN ?", expired).Delete(map[string]any{}).Error; err != nil {
		return 0, err
	}
	result := db.Table("task_runs").Where("run_id IN ?", expired).Delete(map[string]any{})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

// TaskRunTimeLayout 与发布队列保持同一时间格式，便于跨表按字符串比较排序。
const TaskRunTimeLayout = "2006-01-02 15:04:05"

// TaskRunResumeBatchLimit 限制单次启动恢复处理的批次数，避免异常数据把启动卡住。
const TaskRunResumeBatchLimit = 20
