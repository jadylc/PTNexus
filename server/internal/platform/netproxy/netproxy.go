package netproxy

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/pt-nexus/server/internal/config"
	"github.com/pt-nexus/server/internal/platform/logx"
	"golang.org/x/net/http/httpproxy"
)

const logModule = "网络代理"

type proxyResolverState struct {
	fn      func(*http.Request) (*url.URL, error)
	summary string
}

type runtimeManager struct {
	resolver atomic.Value

	mu         sync.Mutex
	transports map[*http.Transport]struct{}
}

var (
	globalMu      sync.Mutex
	globalManager *runtimeManager
)

// Install 初始化全局网络代理管理器，并把默认 HTTP transport 接入配置代理逻辑。
// 参数/返回：cfg 为当前应用配置中的网络代理设置；返回 error 表示默认 transport 无法接管。
// 失败场景：默认 transport 类型异常时返回 error。
// 副作用：替换 http.DefaultTransport，并注册后续热更新所需的 transport 追踪。
func Install(cfg config.NetworkProxyConfig) error {
	globalMu.Lock()
	defer globalMu.Unlock()
	return installLocked(cfg)
}

// Update 热更新全局网络代理配置，并关闭已注册 transport 的空闲连接。
// 参数/返回：cfg 为新的网络代理设置；返回 error 表示配置非法或全局代理尚未初始化失败。
// 失败场景：配置非法、默认 transport 不支持接管时返回 error。
// 副作用：更新代理决策函数，并对默认/custom transport 执行 CloseIdleConnections。
func Update(cfg config.NetworkProxyConfig) error {
	globalMu.Lock()
	defer globalMu.Unlock()
	if globalManager == nil {
		return installLocked(cfg)
	}
	return globalManager.update(cfg)
}

// ConfigureTransport 将指定 transport 接入全局网络代理管理器。
// 参数/返回：transport 为调用方自定义的 HTTP transport；原对象会被原地修改并返回。
// 失败场景：transport 为空或全局代理尚未初始化时直接返回原对象。
// 副作用：覆盖 transport.Proxy，并纳入热更新的空闲连接清理集合。
func ConfigureTransport(transport *http.Transport) *http.Transport {
	if transport == nil {
		return transport
	}

	globalMu.Lock()
	manager := globalManager
	globalMu.Unlock()
	if manager == nil {
		return transport
	}
	manager.configureTransport(transport)
	return transport
}

func installLocked(cfg config.NetworkProxyConfig) error {
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return fmt.Errorf("默认 HTTP transport 类型不支持接管: %T", http.DefaultTransport)
	}

	manager := globalManager
	if manager == nil {
		manager = &runtimeManager{transports: map[*http.Transport]struct{}{}}
		globalManager = manager
	}
	if err := manager.update(cfg); err != nil {
		return err
	}

	cloned := defaultTransport.Clone()
	manager.configureTransport(cloned)
	http.DefaultTransport = cloned
	return nil
}

func (m *runtimeManager) update(cfg config.NetworkProxyConfig) error {
	state, err := buildResolverState(cfg)
	if err != nil {
		return err
	}

	m.resolver.Store(state)
	logx.Infof(logModule, "已应用网络代理配置 %s", state.summary)
	m.closeIdleConnections()
	return nil
}

func (m *runtimeManager) configureTransport(transport *http.Transport) {
	if transport == nil {
		return
	}
	transport.Proxy = m.proxy

	m.mu.Lock()
	defer m.mu.Unlock()
	m.transports[transport] = struct{}{}
}

func (m *runtimeManager) proxy(req *http.Request) (*url.URL, error) {
	raw := m.resolver.Load()
	if raw == nil {
		return nil, nil
	}
	state, ok := raw.(proxyResolverState)
	if !ok || state.fn == nil {
		return nil, nil
	}
	return state.fn(req)
}

func (m *runtimeManager) closeIdleConnections() {
	m.mu.Lock()
	transports := make([]*http.Transport, 0, len(m.transports))
	for transport := range m.transports {
		transports = append(transports, transport)
	}
	m.mu.Unlock()

	for _, transport := range transports {
		if transport == nil {
			continue
		}
		transport.CloseIdleConnections()
	}
}

func buildResolverState(cfg config.NetworkProxyConfig) (proxyResolverState, error) {
	normalized := config.NormalizeNetworkProxyConfig(cfg)
	if err := config.ValidateNetworkProxyConfig(normalized); err != nil {
		return proxyResolverState{}, err
	}

	var base *httpproxy.Config
	source := ""
	if strings.TrimSpace(normalized.ProxyURL) != "" {
		base = &httpproxy.Config{
			HTTPProxy:  normalized.ProxyURL,
			HTTPSProxy: normalized.ProxyURL,
			NoProxy:    ensureLoopbackNoProxy(normalized.NoProxy),
		}
		source = "frontend"
	} else {
		envCfg := httpproxy.FromEnvironment()
		if strings.TrimSpace(envCfg.HTTPProxy) == "" && strings.TrimSpace(envCfg.HTTPSProxy) == "" {
			return proxyResolverState{
				summary: "proxy=false",
				fn: func(req *http.Request) (*url.URL, error) {
					return nil, nil
				},
			}, nil
		}
		envCfg.NoProxy = ensureLoopbackNoProxy(envCfg.NoProxy)
		base = envCfg
		source = "env"
	}

	resolver := base.ProxyFunc()
	// 分类路由：只有命中"允许走代理"的功能类别或站点白名单的请求才实际走代理，
	// 其余目标（含未白名单的 PT 站点）强制直连，避免代理节点故障或 IP 漂移影响全局。
	categories := normalized.Categories
	proxySites := normalized.ProxySites
	state := proxyResolverState{
		summary: fmt.Sprintf(
			"proxy=%s media=%t image=%t iyuu=%t other=%t proxy_sites=%d",
			source,
			categories.Media,
			categories.ImageHost,
			categories.IYUU,
			categories.Other,
			len(proxySites),
		),
		fn: func(req *http.Request) (*url.URL, error) {
			if req == nil || req.URL == nil {
				return nil, nil
			}
			host := requestHost(req.URL)
			if !hostUsesProxy(host, categories, proxySites) {
				return nil, nil
			}
			return resolver(req.URL)
		},
	}
	return state, nil
}

// hostUsesProxy 判断目标主机是否允许通过配置的代理出网。
// 参数/返回：host 为小写主机名；categories 为功能开关；proxySites 为按站点白名单（主机名）。
// 失败场景：不适用；白名单命中时优先于分类开关。
// 副作用：无。
func hostUsesProxy(host string, categories config.NetworkProxyCategories, proxySites []string) bool {
	if host == "" {
		return false
	}
	for _, site := range proxySites {
		if matchDomain(host, site) {
			return true
		}
	}
	switch classifyHost(host) {
	case categoryMedia:
		return categories.Media
	case categoryImageHost:
		return categories.ImageHost
	case categoryIYUU:
		return categories.IYUU
	default:
		return categories.Other
	}
}

type hostCategory int

const (
	categoryOther hostCategory = iota
	categoryMedia
	categoryImageHost
	categoryIYUU
)

var (
	mediaSuffixes = []string{".themoviedb.org", ".tmdb.org", ".imdb.com", ".douban.com", ".cspt.top", ".homeqian.top", ".tju.pt"}
	mediaHosts    = []string{"themoviedb.org", "tmdb.org", "imdb.com", "douban.com", "cspt.top", "homeqian.top", "tju.pt"}
	imageSuffixes = []string{".pixhost.to", ".workers.dev"}
	imageHosts    = []string{"pixhost.to"}
	iyuuSuffixes  = []string{".iyuu.cn"}
	iyuuHosts     = []string{"iyuu.cn"}
)

// classifyHost 将目标主机归类为 媒体信息/图床/IYUU/其他 四类。
// 参数/返回：host 为小写主机名（不含端口）；返回功能类别。
// 失败场景：未知主机归为 other。
// 副作用：无。
// 说明：workers.dev 上的 pt-nexus-ptgen 转发归媒体、pt-nexus-proxy 图床转发归图床，
// 按子域名前缀区分；其余 *.workers.dev 归 other。
func classifyHost(host string) hostCategory {
	for _, item := range mediaHosts {
		if host == item {
			return categoryMedia
		}
	}
	for _, suffix := range mediaSuffixes {
		if strings.HasSuffix(host, suffix) {
			return categoryMedia
		}
	}
	for _, item := range iyuuHosts {
		if host == item {
			return categoryIYUU
		}
	}
	for _, suffix := range iyuuSuffixes {
		if strings.HasSuffix(host, suffix) {
			return categoryIYUU
		}
	}
	for _, item := range imageHosts {
		if host == item {
			return categoryImageHost
		}
	}
	for _, suffix := range imageSuffixes {
		if strings.HasSuffix(host, suffix) {
			if strings.Contains(host, "ptgen") || strings.Contains(host, "imdb2douban") {
				return categoryMedia
			}
			if strings.Contains(host, "proxy") || strings.Contains(host, "pixhost") {
				return categoryImageHost
			}
			return categoryOther
		}
	}
	return categoryOther
}

// matchDomain 判断 host 是否等于 pattern 或其子域（pattern 允许带前导点或协议头）。
func matchDomain(host, pattern string) bool {
	cleaned := strings.ToLower(strings.TrimSpace(pattern))
	if cleaned == "" {
		return false
	}
	if parsed, err := url.Parse(cleaned); err == nil && parsed.Host != "" {
		cleaned = strings.ToLower(parsed.Hostname())
	} else {
		cleaned = strings.TrimPrefix(cleaned, ".")
	}
	if cleaned == "" {
		return false
	}
	return host == cleaned || strings.HasSuffix(host, "."+cleaned)
}

func requestHost(u *url.URL) string {
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	return host
}

func ensureLoopbackNoProxy(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "*" {
		return trimmed
	}

	values := make([]string, 0, 4)
	seen := map[string]struct{}{}
	appendValue := func(value string) {
		cleaned := strings.TrimSpace(value)
		if cleaned == "" {
			return
		}
		key := strings.ToLower(cleaned)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		values = append(values, cleaned)
	}

	for _, item := range strings.Split(trimmed, ",") {
		appendValue(item)
	}
	appendValue("localhost")
	appendValue("127.0.0.1")
	appendValue("::1")
	return strings.Join(values, ",")
}
