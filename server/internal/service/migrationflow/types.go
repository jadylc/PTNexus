package migrationflow

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/pt-nexus/server/internal/config"
	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/repository"
	extract "github.com/pt-nexus/server/internal/service/acquire/extract"
	acquirefetch "github.com/pt-nexus/server/internal/service/acquire/fetch"
	processingbdflow "github.com/pt-nexus/server/internal/service/processing/bdflow"
	publishworkflow "github.com/pt-nexus/server/internal/service/publish/workflow"
)

type MigrateService struct {
	repo *repository.MigrateRepository
	cfg  *config.Manager

	extractorEngine *extract.Engine

	contextState    *publishworkflow.ContextState
	logStreamState  *acquirefetch.LogStreamState
	batchFetchState *acquirefetch.BatchFetchState
	publishState    *publishworkflow.BatchState
	bdinfoState     *processingbdflow.BDInfoState

	queueRepo      *repository.PublishQueueRepository
	publishLogRepo *repository.PublishLogRepository
	statsRepo      *repository.StatsRepository

	// taskRunRepo 让批量发布/批量获取的批次与条目落库，从而支持进程重启后续跑。
	taskRunRepo *repository.TaskRunRepository

	queueStartOnce sync.Once
	queueStopCh    chan struct{}
	queueDoneCh    chan struct{}

	// seedParamsChangedHook 在批量写 seed_parameters 后触发（如标题重算），
	// 用于通知跨种服务失效查询缓存，避免列表在 TTL 内展示旧数据。
	seedParamsChangedHook func()
}

// SetSeedParametersChangedHook 注入 seed_parameters 批量写库后的回调（由启动流程接线到跨种缓存失效）。
// 参数/返回：hook 为回调函数；无返回值。
// 失败场景：传入 nil 时忽略。
// 副作用：写入服务内部依赖引用。
func (s *MigrateService) SetSeedParametersChangedHook(hook func()) {
	if s == nil || hook == nil {
		return
	}
	s.seedParamsChangedHook = hook
}

// notifySeedParametersChanged 触发 seed_parameters 变更回调（存在时）。
func (s *MigrateService) notifySeedParametersChanged() {
	if s == nil || s.seedParamsChangedHook == nil {
		return
	}
	s.seedParamsChangedHook()
}

// InitTaskRuns 注入批次任务仓储（由启动流程调用，未注入时批次只保留内存态）。
// 参数/返回：repo 为 task_runs/task_run_items 仓储；无返回值。
// 失败场景：传入 nil 时保持原状并记录日志，不影响主服务启动。
// 副作用：写入服务内部依赖引用（不启动线程）。
func (s *MigrateService) InitTaskRuns(repo *repository.TaskRunRepository) {
	if s == nil {
		return
	}
	if repo == nil {
		logx.Warnf(taskRunLogModule, "注入批次任务仓储失败：repo 为空")
		return
	}
	s.taskRunRepo = repo
}

func NewMigrateService(repo *repository.MigrateRepository, cfg *config.Manager) *MigrateService {
	return &MigrateService{
		repo:            repo,
		cfg:             cfg,
		extractorEngine: extract.NewPageExtractorEngine(),
		contextState:    publishworkflow.NewContextState(),
		logStreamState:  acquirefetch.NewLogStreamState(),
		batchFetchState: acquirefetch.NewBatchFetchState(),
		publishState:    publishworkflow.NewBatchState(),
		bdinfoState:     processingbdflow.NewBDInfoState(),
	}
}

func (s *MigrateService) newID(prefix string) string {
	return fmt.Sprintf("%s-%d-%06d", prefix, time.Now().UnixNano(), rand.Intn(1000000))
}
