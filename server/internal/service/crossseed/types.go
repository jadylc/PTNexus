package crossseed

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pt-nexus/server/internal/repository"
)

type CrossSeedQueryParams struct {
	Page               int
	PageSize           int
	Search             string
	PathFilters        []string
	IsDeleted          string
	ExcludeTargetSites string
	ReviewStatus       string
	IncludeUniquePaths bool
}

// crossSeedQueryCacheTTL 控制查询结果缓存时长。维护种子/跨种页 data 接口
// 每次请求都会触发无 LIMIT 全量分类查询与全 torrents 聚合，数据又在服务端批量
// 写入，缓存 30s 即可大幅降低重复请求的数据库压力，同时不至于展示明显过期数据。
const crossSeedQueryCacheTTL = 30 * time.Second

// crossSeedCacheEntry 缓存单条查询结果与其写入时间。
type crossSeedCacheEntry struct {
	cachedAt time.Time
	result   map[string]any
}

type CrossSeedService struct {
	repo *repository.CrossSeedRepository

	cacheMu sync.Mutex
	cache   map[string]crossSeedCacheEntry
}

func NewCrossSeedService(repo *repository.CrossSeedRepository) *CrossSeedService {
	return &CrossSeedService{
		repo:  repo,
		cache: make(map[string]crossSeedCacheEntry),
	}
}

// queryCacheKey 生成查询结果缓存键，覆盖所有影响返回内容的参数。
func queryCacheKey(params CrossSeedQueryParams) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%v\x00%d\x00%d\x00%t",
		strings.TrimSpace(params.ReviewStatus),
		strings.TrimSpace(params.IsDeleted),
		strings.TrimSpace(params.ExcludeTargetSites),
		strings.TrimSpace(params.Search),
		params.PathFilters,
		params.Page,
		params.PageSize,
		params.IncludeUniquePaths,
	)
}

// invalidateCache 使所有查询结果缓存失效，供删除等写操作后调用。
func (s *CrossSeedService) invalidateCache() {
	s.cacheMu.Lock()
	s.cache = make(map[string]crossSeedCacheEntry)
	s.cacheMu.Unlock()
}

// getCachedQuery 返回未过期的缓存结果；不存在或已过期时返回 nil。
func (s *CrossSeedService) getCachedQuery(key string) map[string]any {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	entry, ok := s.cache[key]
	if !ok || time.Since(entry.cachedAt) >= crossSeedQueryCacheTTL {
		return nil
	}
	return entry.result
}

// setCachedQuery 写入查询结果缓存，并防止缓存条目无限增长。
func (s *CrossSeedService) setCachedQuery(key string, result map[string]any) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if len(s.cache) >= 256 {
		// 超限时整体丢弃，避免长时间运行后缓存无界增长；
		// 重新填充的开销远小于一次慢查询。
		s.cache = make(map[string]crossSeedCacheEntry)
	}
	s.cache[key] = crossSeedCacheEntry{cachedAt: time.Now(), result: result}
}
