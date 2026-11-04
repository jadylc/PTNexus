package fetch

import (
	"fmt"
	"strings"
	"time"
)

// 本文件为批量抓取任务提供细粒度进度结构（阶段字典 A~E），
// 与 batch_fetch_state.go 中的聚合计数并存、互不影响。

// 阶段 ID 字典（与前端步骤条一一对应）：
//
//	A                定位准备
//	B                抓取源站（顶层，随候选切换重置 running）
//	B.detail         打开详情页
//	B.torrent        下载种子文件
//	B.parse          解析种子元数据
//	B.scrape         解析页面信息（标题/副标题/BBCode）
//	B.repair         抓取修复（校验纠偏总阶段）
//	B.poster         修复海报
//	B.intro          修复简介
//	B.screens        修复截图
//	B.mediainfo      修复媒体信息（MediaInfo/BDInfo）
//	C                写入数据库（标准化+落库）
//	E                完成
const (
	StageLocate    = "A"
	StageFetchTop  = "B"
	StageDetail    = "B.detail"
	StageTorrent   = "B.torrent"
	StageParse     = "B.parse"
	StageScrape    = "B.scrape"
	StageRepair    = "B.repair"
	StagePoster    = "B.poster"
	StageIntro     = "B.intro"
	StageScreens   = "B.screens"
	StageMediainfo = "B.mediainfo"
	StagePersist   = "C"
	StageDone      = "E"
)

// BatchStageRecord 表示一个阶段的执行记录。
type BatchStageRecord struct {
	State   string `json:"state"` // running | done | failed | warning
	Message string `json:"message,omitempty"`
	Site    string `json:"site,omitempty"`
	Started string `json:"started_at,omitempty"`
	Ended   string `json:"ended_at,omitempty"`
	Dms     int64  `json:"duration_ms,omitempty"`
}

// BatchAttemptRecord 表示对某个候选源站的一次完整尝试（含各子阶段）。
type BatchAttemptRecord struct {
	Site    string                       `json:"site"`
	Torrent string                       `json:"torrent_id,omitempty"`
	State   string                       `json:"state"` // running | success | failed
	Reason  string                       `json:"reason,omitempty"`
	Started string                       `json:"started_at,omitempty"`
	Ended   string                       `json:"ended_at,omitempty"`
	Dms     int64                        `json:"duration_ms,omitempty"`
	Stages  map[string]*BatchStageRecord `json:"stages,omitempty"`
}

// BatchItemState 表示批次内单条种子的完整进度。
type BatchItemState struct {
	Name       string                       `json:"name"`
	Index      int                          `json:"index"`
	Status     string                       `json:"status"` // queued | running | success | failed | pending_review
	Started    string                       `json:"started_at,omitempty"`
	Ended      string                       `json:"ended_at,omitempty"`
	DurationMs int64                        `json:"duration_ms,omitempty"`
	Candidates []string                     `json:"candidates,omitempty"`
	Stages     map[string]*BatchStageRecord `json:"stages,omitempty"`
	Attempts   []*BatchAttemptRecord        `json:"attempts,omitempty"`
	Error      string                       `json:"error,omitempty"`
	FinalSite  string                       `json:"final_site,omitempty"`
	LogTail    []string                     `json:"log_tail,omitempty"`
}

// StartWithItems 注册任务并初始化逐项排队状态（细粒度进度面板数据源）。
// 参数/返回：taskID 任务标识；names 批次全部种子名；无返回。
// 失败场景：状态管理器为空时静默。
// 副作用：创建带 Items 的任务。
func (s *BatchFetchState) StartWithItems(taskID string, names []string) {
	if s == nil {
		return
	}
	now := time.Now().Format(batchFetchTimeLayout)
	task := &BatchFetchTask{
		Total:     len(names),
		Processed: 0,
		Success:   0,
		Failed:    0,
		Skipped:   0,
		IsRunning: true,
		StartedAt: now,
		UpdatedAt: now,
		Results:   []map[string]any{},
		Items:     make([]*BatchItemState, 0, len(names)),
	}
	for idx, name := range names {
		task.Items = append(task.Items, &BatchItemState{
			Name:   name,
			Index:  idx + 1,
			Status: "queued",
			Stages: map[string]*BatchStageRecord{},
		})
	}
	s.mu.Lock()
	s.tasks[taskID] = task
	s.mu.Unlock()
}

// MarkItemStart 将某条种子标记为运行中并记录候选站点列表（阶段 A）。
func (s *BatchFetchState) MarkItemStart(taskID, name string, candidates []string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[taskID]
	if task == nil {
		return
	}
	item := findOrCreateBatchItem(task, name)
	now := time.Now().Format(batchFetchTimeLayout)
	item.Status = "running"
	item.Started = now
	item.Candidates = append([]string{}, candidates...)
	setBatchStage(item.Stages, StageLocate, "running", "定位源站点", "", now)
	task.CurrentName = name
	task.UpdatedAt = now
}

// MarkItemAttemptStart 记录对某候选源站的尝试开始（阶段 B 及当前指针）。
func (s *BatchFetchState) MarkItemAttemptStart(taskID, name, site, torrentID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[taskID]
	if task == nil {
		return
	}
	item := findBatchItem(task, name)
	if item == nil {
		return
	}
	now := time.Now().Format(batchFetchTimeLayout)
	if locate := item.Stages[StageLocate]; locate != nil && locate.State == "running" {
		locate.State = "done"
		locate.Ended = now
	}
	item.Attempts = append(item.Attempts, &BatchAttemptRecord{
		Site:    site,
		Torrent: torrentID,
		State:   "running",
		Started: now,
		Stages:  map[string]*BatchStageRecord{},
	})
	setBatchStage(item.Stages, StageFetchTop, "running", "正在抓取源站", site, now)
	task.CurrentName = name
	task.UpdatedAt = now
}

// MarkItemAttemptEnd 结束一次候选尝试并记录结果原因。
func (s *BatchFetchState) MarkItemAttemptEnd(taskID, name, site string, success bool, reason string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[taskID]
	if task == nil {
		return
	}
	item := findBatchItem(task, name)
	if item == nil {
		return
	}
	now := time.Now()
	nowText := now.Format(batchFetchTimeLayout)
	for idx := len(item.Attempts) - 1; idx >= 0; idx-- {
		attempt := item.Attempts[idx]
		if attempt.Site != site || attempt.State != "running" {
			continue
		}
		if success {
			attempt.State = "success"
		} else {
			attempt.State = "failed"
			attempt.Reason = reason
		}
		attempt.Ended = nowText
		attempt.Dms = elapsedMs(attempt.Started, now)
		break
	}
	task.UpdatedAt = nowText
}

// ApplyItemStage 更新某条种子当前尝试的阶段状态；stageID 为阶段字典键。
// 顶层 stages 只保留主阶段（A/B/C/E），B 的子步记在最后一条 attempt 里。
func (s *BatchFetchState) ApplyItemStage(taskID, name, stageID, state, message string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[taskID]
	if task == nil {
		return
	}
	item := findBatchItem(task, name)
	if item == nil {
		return
	}
	now := time.Now()
	nowText := now.Format(batchFetchTimeLayout)
	if strings.HasPrefix(stageID, "B.") && len(item.Attempts) > 0 {
		last := item.Attempts[len(item.Attempts)-1]
		if last.Stages == nil {
			last.Stages = map[string]*BatchStageRecord{}
		}
		applyBatchStage(last.Stages, stageID, state, message, now, nowText)
		setBatchStage(item.Stages, StageFetchTop, "running", fmt.Sprintf("%s %s", shortStageName(stageID), message), last.Site, nowText)
	} else {
		site := ""
		if len(item.Attempts) > 0 {
			site = item.Attempts[len(item.Attempts)-1].Site
		}
		applyBatchStage(item.Stages, stageID, state, message, now, nowText)
		if site != "" {
			if record := item.Stages[stageID]; record != nil {
				record.Site = site
			}
		}
	}
	task.UpdatedAt = nowText
}

// AppendItemLog 追加一条内部子步骤日志到日志尾缓冲（保留最近 20 行）。
func (s *BatchFetchState) AppendItemLog(taskID, name, line string) {
	if s == nil {
		return
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[taskID]
	if task == nil {
		return
	}
	item := findBatchItem(task, name)
	if item == nil {
		return
	}
	item.LogTail = append(item.LogTail, trimmed)
	if len(item.LogTail) > 20 {
		item.LogTail = item.LogTail[len(item.LogTail)-20:]
	}
	task.UpdatedAt = time.Now().Format(batchFetchTimeLayout)
}

// MarkItemResult 记录某条种子的终态（success | failed | pending_review）。
func (s *BatchFetchState) MarkItemResult(taskID, name, status, finalSite, message string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[taskID]
	if task == nil {
		return
	}
	item := findBatchItem(task, name)
	if item == nil {
		return
	}
	now := time.Now()
	nowText := now.Format(batchFetchTimeLayout)
	item.Status = status
	item.Ended = nowText
	item.DurationMs = elapsedMs(item.Started, now)
	if finalSite != "" {
		item.FinalSite = finalSite
	}
	if status == "failed" {
		item.Error = message
	} else {
		setBatchStage(item.Stages, StageDone, "done", message, finalSite, nowText)
	}
	if locate := item.Stages[StageLocate]; locate != nil && locate.State == "running" {
		locate.State = "done"
		locate.Ended = nowText
	}
	if fetchTop := item.Stages[StageFetchTop]; fetchTop != nil && fetchTop.State == "running" && status == "failed" {
		fetchTop.State = "failed"
		fetchTop.Ended = nowText
	}
	if task.CurrentName == name {
		task.CurrentName = ""
	}
	task.UpdatedAt = nowText
}

func findBatchItem(task *BatchFetchTask, name string) *BatchItemState {
	for _, item := range task.Items {
		if item != nil && item.Name == name {
			return item
		}
	}
	return nil
}

func findOrCreateBatchItem(task *BatchFetchTask, name string) *BatchItemState {
	if item := findBatchItem(task, name); item != nil {
		return item
	}
	item := &BatchItemState{
		Name:   name,
		Index:  len(task.Items) + 1,
		Status: "queued",
		Stages: map[string]*BatchStageRecord{},
	}
	task.Items = append(task.Items, item)
	return item
}

func setBatchStage(stages map[string]*BatchStageRecord, stageID, state, message, site, nowText string) {
	if stages == nil {
		return
	}
	record, exists := stages[stageID]
	if !exists {
		record = &BatchStageRecord{Started: nowText, Ended: nowText}
		stages[stageID] = record
	}
	record.State = state
	record.Message = message
	if site != "" {
		record.Site = site
	}
	if state == "running" {
		record.Started = nowText
		record.Ended = ""
		record.Dms = 0
	}
}

func applyBatchStage(stages map[string]*BatchStageRecord, stageID, state string, message string, now time.Time, nowText string) {
	if stages == nil {
		return
	}
	record, exists := stages[stageID]
	if !exists {
		record = &BatchStageRecord{Started: nowText}
		stages[stageID] = record
	}
	if state == "running" {
		record.Started = nowText
		record.Ended = ""
		record.Dms = 0
	} else if record.Ended == "" {
		record.Ended = nowText
		record.Dms = elapsedMs(record.Started, now)
	}
	record.State = state
	if message != "" {
		record.Message = message
	}
}

func elapsedMs(started string, now time.Time) int64 {
	if strings.TrimSpace(started) == "" {
		return 0
	}
	parsed, err := time.ParseInLocation(batchFetchTimeLayout, started, time.Local)
	if err != nil {
		return 0
	}
	delta := now.Sub(parsed).Milliseconds()
	if delta < 0 {
		return 0
	}
	return delta
}

var batchStageShortNames = map[string]string{
	StageLocate:    "定位准备",
	StageFetchTop:  "抓取源站",
	StageDetail:    "详情页",
	StageTorrent:   "下载种子",
	StageParse:     "解析种子",
	StageScrape:    "解析页面",
	StageRepair:    "抓取修复",
	StagePoster:    "海报",
	StageIntro:     "简介",
	StageScreens:   "截图",
	StageMediainfo: "媒体信息",
	StagePersist:   "写入数据库",
	StageDone:      "完成",
}

func shortStageName(stageID string) string {
	if name, ok := batchStageShortNames[stageID]; ok {
		return name
	}
	return stageID
}

// copyBatchItems 深拷贝逐项进度，供 Snapshot 使用。
func copyBatchItems(items []*BatchItemState) []*BatchItemState {
	if items == nil {
		return nil
	}
	copied := make([]*BatchItemState, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		itemCopy := *item
		itemCopy.Stages = copyBatchStages(item.Stages)
		itemCopy.Candidates = append([]string{}, item.Candidates...)
		itemCopy.LogTail = append([]string{}, item.LogTail...)
		itemCopy.Attempts = make([]*BatchAttemptRecord, 0, len(item.Attempts))
		for _, attempt := range item.Attempts {
			if attempt == nil {
				continue
			}
			attemptCopy := *attempt
			attemptCopy.Stages = copyBatchStages(attempt.Stages)
			itemCopy.Attempts = append(itemCopy.Attempts, &attemptCopy)
		}
		copied = append(copied, &itemCopy)
	}
	return copied
}

func copyBatchStages(source map[string]*BatchStageRecord) map[string]*BatchStageRecord {
	copied := make(map[string]*BatchStageRecord, len(source))
	for key, record := range source {
		if record == nil {
			continue
		}
		recordCopy := *record
		copied[key] = &recordCopy
	}
	return copied
}
