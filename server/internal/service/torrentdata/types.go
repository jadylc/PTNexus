package torrentdata

import (
	"sync"
	"sync/atomic"

	"github.com/pt-nexus/server/internal/config"
	"github.com/pt-nexus/server/internal/repository"
)

type TorrentsDataParams struct {
	Page                      int
	PageSize                  int
	PathFilters               []string
	StateFilters              []string
	DownloaderFilters         []string
	SourceAvailabilityFilters []string
	ExistSiteNames            []string
	NotExistSiteNames         []string
	// ExcludedSourceSites 优先级弹窗中禁用的源站点：这些站点不计入可用源站点，
	// 所有源站点都被禁用的种子直接从列表排除（与前端 getSourceSites 口径一致）。
	ExcludedSourceSites []string
	NameSearch          string
	SortProp            string
	SortOrder           string
	ExcludeExisting     bool
	OnlyCompleted       bool
	SkipMetadata        bool
	MetadataOnly        bool
	// OnlyPublishable 只显示"维护通过"的种子：组内至少一个站点副本的抓取参数
	// 通过可发布判定（字段齐全/简介规范/MediaInfo 规范，口径与跨种列表 is_publishable
	// 同源）。一种多站页面（转种入口）开启；批量获取面板不传（它专取未抓取的）。
	OnlyPublishable bool
}

type siteSummary struct {
	Uploaded  int64
	Comment   *string
	Migration int
	State     string
	Seeders   int64
	// Reviewed 该站点副本的抓取参数是否"维护通过"（可发布），仅在
	// OnlyPublishable 查询中填充；转种源站选择据此限定候选。
	Reviewed bool
}

type torrentSummary struct {
	Name          string
	SavePath      string
	Size          int64
	Progress      float64
	StateSet      map[string]struct{}
	Sites         map[string]*siteSummary
	TotalUploaded int64
	Seeders       int64
	DownloaderIDs []string
}

type TorrentDataService struct {
	repo      *repository.TorrentDataRepository
	cfg       *config.Manager
	iyuuTasks *IYUUTaskService

	refreshMu      sync.Mutex
	refreshRunning bool

	iyuuMu      sync.Mutex
	iyuuRunning atomic.Bool

	iyuuLog func(level string, message string)

	// snapshotHook 数据刷新成功后的回调：用于把最新种子级累计写入站点每日快照，
	// 保证站点统计的"每日增量"与数据刷新时刻对齐（由 bootstrap 注入）。
	snapshotHook func() error
}

func NewTorrentDataService(repo *repository.TorrentDataRepository, cfg *config.Manager) *TorrentDataService {
	return &TorrentDataService{repo: repo, cfg: cfg, iyuuTasks: NewIYUUTaskService()}
}

// SetSnapshotHook 注入数据刷新完成后的站点快照回调。
func (s *TorrentDataService) SetSnapshotHook(hook func() error) {
	s.snapshotHook = hook
}

// SetIYUULogger 设置 IYUU 查询过程的日志回调，便于在设置页展示进度信息。
// 参数/返回：level 为 INFO/WARN/ERROR 等等级，message 为完整日志文本；无返回值。
// 失败场景：无。
// 副作用：将日志写入回调实现方（通常为 SettingsService 的内存日志队列）。
func (s *TorrentDataService) SetIYUULogger(logger func(level string, message string)) {
	s.iyuuMu.Lock()
	s.iyuuLog = logger
	s.iyuuMu.Unlock()
}
