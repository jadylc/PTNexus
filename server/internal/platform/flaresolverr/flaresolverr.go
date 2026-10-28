// Package flaresolverr 封装 FlareSolverr HTTP API 客户端与全局运行时配置。
//
// 用途：当抓取流程命中 Cloudflare 挑战页时，调用本包通过 FlareSolverr
// 求解挑战，并复用其返回的 Cookie 继续抓取详情页与下载种子。
// 说明：与 netproxy 一致，采用全局运行时配置，设置保存后立即热更新，
// 避免为每次抓取透传配置参数。
package flaresolverr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/pt-nexus/server/internal/config"
	"github.com/pt-nexus/server/internal/platform/logx"
)

const logModule = "FlareSolverr"

// Solution 表示一次 FlareSolverr 求解结果。
// 参数/返回：Body 为求解后的页面内容；Cookies 为求解得到的 Cookie；Headers 为响应头；StatusCode 为最终 HTTP 状态码。
// 失败场景：不适用。
// 副作用：无。
type Solution struct {
	Body       string
	Cookies    []Cookie
	Headers    map[string]string
	StatusCode int
	FinalURL   string
}

// Cookie 表示 FlareSolverr 返回的单条 Cookie。
type Cookie struct {
	Name     string
	Value    string
	Domain   string
	Path     string
	Secure   bool
	HTTPOnly bool
}

// requestPayload 是发送给 FlareSolverr 的请求体。
type requestPayload struct {
	Cmd        string            `json:"cmd"`
	URL        string            `json:"url"`
	MaxTimeout int               `json:"maxTimeout,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Cookies    []cookiePayload   `json:"cookies,omitempty"`
	SessionID  string            `json:"session_id,omitempty"`
	APIKey     string            `json:"apiKey,omitempty"`
}

type cookiePayload struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Secure   bool   `json:"secure"`
	HTTPOnly bool   `json:"httpOnly"`
}

type responsePayload struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	Solution *struct {
		URL     string            `json:"url"`
		Status  int               `json:"status"`
		Body    string            `json:"body"`
		Headers map[string]string `json:"headers"`
		Cookies []cookiePayload   `json:"cookies"`
	} `json:"solution"`
}

var (
	globalMu   sync.RWMutex
	globalCfg  config.FlareSolverrConfig
	globalHTTP *http.Client
)

// Install 初始化全局 FlareSolverr 运行时配置。
// 参数/返回：cfg 为当前应用配置；返回 error 表示配置校验失败。
// 失败场景：FlareSolverr 未启用但配置非法时返回 error。
// 副作用：替换全局运行时配置与 HTTP 客户端。
// 说明：HTTP 客户端超时在求解预算（TimeoutSeconds）之上加 15 秒余量——
// FlareSolverr 用满 maxTimeout 才返回是常态（managed challenge 常需 60 秒+），
// 客户端与预算同刻到期会把"求解完成但响应没收完"误判成超时。
func Install(cfg config.FlareSolverrConfig) error {
	normalized := config.NormalizeFlareSolverrConfig(cfg)
	if err := config.ValidateFlareSolverrConfig(normalized); err != nil {
		return fmt.Errorf("初始化 FlareSolverr 配置失败: %w", err)
	}
	globalMu.Lock()
	defer globalMu.Unlock()
	globalCfg = normalized
	globalHTTP = &http.Client{Timeout: time.Duration(normalized.TimeoutSeconds)*time.Second + httpTimeoutMargin}
	logx.Infof(logModule, "已应用 FlareSolverr 配置 enabled=%t api_url=%s", normalized.Enabled, redactAPIURL(normalized.APIURL))
	return nil
}

// httpTimeoutMargin 是 HTTP 客户端相对求解预算的额外等待余量。
const httpTimeoutMargin = 15 * time.Second

// Update 热更新全局 FlareSolverr 运行时配置。
// 参数/返回：cfg 为新配置；返回 error 表示配置非法。
// 失败场景：配置非法时返回 error，旧配置保持不变。
// 副作用：替换全局运行时配置与 HTTP 客户端。
func Update(cfg config.FlareSolverrConfig) error {
	return Install(cfg)
}

// Enabled 返回 FlareSolverr 是否已启用且配置了 API 地址。
// 参数/返回：无参数；返回是否可用于求解。
// 失败场景：无。
// 副作用：无。
func Enabled() bool {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalCfg.Enabled && strings.TrimSpace(globalCfg.APIURL) != ""
}

// Config 返回当前全局 FlareSolverr 配置快照。
// 参数/返回：无参数；返回值用于日志与诊断。
// 失败场景：未初始化时返回默认空配置。
// 副作用：无。
func Config() config.FlareSolverrConfig {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalCfg
}

// Ping 探测 FlareSolverr 服务连通性（发送 sessions.list 命令，不发起求解）。
// 参数/返回：apiURL 为 FlareSolverr 根地址（可带 /v1）；apiKey 为鉴权密钥（可空）；
// 返回服务返回的说明文本与 error（网络/鉴权/协议错误时非空）。
// 失败场景：地址不可达、非 FlareSolverr 服务、API Key 错误。
// 副作用：发起一次 HTTP 请求。
func Ping(apiURL, apiKey string) (string, error) {
	normalized := config.NormalizeFlareSolverrConfig(config.FlareSolverrConfig{
		APIURL:         apiURL,
		APIKey:         apiKey,
		TimeoutSeconds: 10,
	})
	if strings.TrimSpace(normalized.APIURL) == "" {
		return "", fmt.Errorf("FlareSolverr API 地址不能为空")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	payload := map[string]any{"cmd": "sessions.list"}
	if strings.TrimSpace(normalized.APIKey) != "" {
		payload["apiKey"] = strings.TrimSpace(normalized.APIKey)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("构造探测请求失败: %w", err)
	}

	endpoint, err := resolveEndpoint(normalized.APIURL)
	if err != nil {
		return "", err
	}

	responseBody, err := postJSON(context.Background(), client, endpoint, body, normalized)
	if err != nil {
		if canFallbackToV1(normalized.APIURL, endpoint) {
			if v1Endpoint, v1Err := resolveEndpoint(normalized.APIURL + "/v1"); v1Err == nil {
				if retryBody, retryErr := postJSON(context.Background(), client, v1Endpoint, body, normalized); retryErr == nil {
					responseBody = retryBody
					err = nil
				}
			}
		}
		if err != nil {
			return "", fmt.Errorf("无法连接 FlareSolverr（%s）: %v", redactAPIURL(normalized.APIURL), err)
		}
	}

	var parsed struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		return "", fmt.Errorf("响应不是有效的 FlareSolverr 服务（%s）：请确认地址指向 FlareSolverr", redactAPIURL(normalized.APIURL))
	}
	if strings.ToLower(parsed.Status) != "ok" {
		return "", fmt.Errorf("FlareSolverr 探测失败: %s", firstNonEmpty(parsed.Message, parsed.Status))
	}
	return firstNonEmpty(parsed.Message, "FlareSolverr 服务可用"), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// Solve 调用 FlareSolverr 求解指定 URL 的 Cloudflare 挑战。
// 参数/返回：ctx 为上下文；targetURL 为待求解页面；cookie 为站点现有 Cookie；返回求解结果。
// 失败场景：FlareSolverr 未启用、网络错误、返回状态非 ok 或响应体解析失败。
// 副作用：发起网络请求。
func Solve(ctx context.Context, targetURL, cookie string) (*Solution, error) {
	globalMu.RLock()
	cfg := globalCfg
	client := globalHTTP
	globalMu.RUnlock()

	if !cfg.Enabled {
		return nil, fmt.Errorf("FlareSolverr 未启用")
	}
	if client == nil {
		client = &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds)*time.Second + httpTimeoutMargin}
	}

	payload := buildRequestPayload(cfg, targetURL, cookie)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("构造 FlareSolverr 请求失败: %w", err)
	}

	endpoint, err := resolveEndpoint(cfg.APIURL)
	if err != nil {
		return nil, err
	}

	logx.Infof(logModule, "开始求解 target_url=%s endpoint=%s timeout=%ds", redactURL(targetURL), endpoint, cfg.TimeoutSeconds)
	responseBody, err := postJSON(ctx, client, endpoint, body, cfg)
	if err != nil {
		if canFallbackToV1(cfg.APIURL, endpoint) {
			v1Endpoint, v1Err := resolveEndpoint(cfg.APIURL + "/v1")
			if v1Err == nil {
				logx.Warnf(logModule, "FlareSolverr 根路径求解失败，改用 /v1 重试 endpoint=%s err=%v", endpoint, err)
				responseBody, err = postJSON(ctx, client, v1Endpoint, body, cfg)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("请求 FlareSolverr 失败（%s，请确认服务可达）: %w", redactAPIURL(cfg.APIURL), err)
		}
	}

	var parsed responsePayload
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		return nil, fmt.Errorf("解析 FlareSolverr 响应失败: %w", err)
	}
	if parsed.Status != "" && strings.ToLower(parsed.Status) != "ok" {
		return nil, fmt.Errorf("FlareSolverr 求解失败: status=%s message=%s", parsed.Status, parsed.Message)
	}
	if parsed.Solution == nil {
		return nil, fmt.Errorf("FlareSolverr 响应缺少 solution 字段")
	}

	solution := &Solution{
		Body:       parsed.Solution.Body,
		StatusCode: parsed.Solution.Status,
		FinalURL:   parsed.Solution.URL,
		Headers:    normalizeHeaders(parsed.Solution.Headers),
	}
	for _, item := range parsed.Solution.Cookies {
		if strings.TrimSpace(item.Name) == "" {
			continue
		}
		solution.Cookies = append(solution.Cookies, Cookie{
			Name:     item.Name,
			Value:    item.Value,
			Domain:   item.Domain,
			Path:     item.Path,
			Secure:   item.Secure,
			HTTPOnly: item.HTTPOnly,
		})
	}

	if strings.TrimSpace(solution.Body) == "" {
		return nil, fmt.Errorf("FlareSolverr 未返回页面内容: status=%d", parsed.Solution.Status)
	}
	logx.Infof(logModule, "求解成功 target_url=%s status=%d cookies=%d body_len=%d", redactURL(targetURL), solution.StatusCode, len(solution.Cookies), len(solution.Body))
	return solution, nil
}

func buildRequestPayload(cfg config.FlareSolverrConfig, targetURL, cookie string) requestPayload {
	payload := requestPayload{
		Cmd:        "request.get",
		URL:        targetURL,
		MaxTimeout: cfg.MaxTimeoutMillis,
		Headers:    map[string]string{},
		APIKey:     cfg.APIKey,
	}
	if cfg.CustomUserAgent != "" {
		payload.Headers["User-Agent"] = cfg.CustomUserAgent
	}
	for key, value := range cfg.ExtraHeaders {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		payload.Headers[trimmedKey] = value
	}
	host := ""
	if parsed, parseErr := url.Parse(targetURL); parseErr == nil {
		host = parsed.Hostname()
	}
	for _, item := range parseCookieString(cookie) {
		payload.Cookies = append(payload.Cookies, cookiePayload{
			Name:     item.Name,
			Value:    item.Value,
			Domain:   host,
			Path:     "/",
			Secure:   false,
			HTTPOnly: false,
		})
	}
	return payload
}

func postJSON(ctx context.Context, client *http.Client, endpoint string, body []byte, cfg config.FlareSolverrConfig) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("构建 FlareSolverr 请求失败: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(cfg.APIKey) != "" {
		request.Header.Set("X-Api-Key", cfg.APIKey)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 32*1024*1024))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("FlareSolverr HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return responseBody, nil
}

func resolveEndpoint(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("FlareSolverr API 地址格式错误: %w", err)
	}
	endpoint := strings.TrimRight(parsed.String(), "/")
	if endpoint == "" {
		return "", fmt.Errorf("FlareSolverr API 地址不能为空")
	}
	return endpoint, nil
}

func canFallbackToV1(raw, endpoint string) bool {
	if strings.HasSuffix(endpoint, "/v1") {
		return false
	}
	return strings.TrimSpace(raw) != ""
}

func normalizeHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return map[string]string{}
	}
	normalized := make(map[string]string, len(headers))
	for key, value := range headers {
		normalized[strings.TrimSpace(key)] = value
	}
	return normalized
}

func parseCookieString(raw string) []Cookie {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, ";")
	cookies := make([]Cookie, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		cookies = append(cookies, Cookie{
			Name:  strings.TrimSpace(name),
			Value: strings.TrimSpace(value),
		})
	}
	return cookies
}

func redactAPIURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if parsed.User != nil {
		parsed.User = url.UserPassword(parsed.User.Username(), "****")
	}
	return parsed.String()
}

func redactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if parsed.RawQuery != "" {
		parsed.RawQuery = "***"
	}
	if parsed.Fragment != "" {
		parsed.Fragment = ""
	}
	return parsed.String()
}
