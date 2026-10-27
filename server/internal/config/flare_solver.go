package config

import (
	"fmt"
	"net/url"
	"strings"
)

// FlareSolverrConfig 描述 FlareSolverr 服务配置。
// 说明：开启后，详情页命中 Cloudflare 挑战时改由 FlareSolverr 求解，
// 并复用其返回的 Cookie 继续抓取/下载。
type FlareSolverrConfig struct {
	Enabled          bool              `json:"enabled"`
	APIURL           string            `json:"api_url"`
	APIKey           string            `json:"api_key"`
	TimeoutSeconds   int               `json:"timeout_seconds"`
	MaxTimeoutMillis int               `json:"max_timeout_millis"`
	CustomUserAgent  string            `json:"custom_user_agent"`
	ExtraHeaders     map[string]string `json:"extra_headers"`
}

// DefaultFlareSolverrConfig 返回默认的 FlareSolverr 配置。
// 参数/返回：无参数；默认关闭，本地默认地址 127.0.0.1:8112。
// 失败场景：无。
// 副作用：无。
func DefaultFlareSolverrConfig() FlareSolverrConfig {
	return FlareSolverrConfig{
		Enabled:          false,
		APIURL:           "http://127.0.0.1:8112",
		APIKey:           "",
		TimeoutSeconds:   60,
		MaxTimeoutMillis: 0,
		CustomUserAgent:  "",
		ExtraHeaders:     map[string]string{},
	}
}

// ParseFlareSolverrConfig 将任意配置对象解析为标准 FlareSolverr 配置。
// 参数/返回：value 通常来自 config.json 的 flare_solver 字段；返回规范化后的配置。
// 失败场景：输入结构异常时回退默认值，不返回 error。
// 副作用：兼容读取旧版字段名。
func ParseFlareSolverrConfig(value any) FlareSolverrConfig {
	cfg := DefaultFlareSolverrConfig()
	raw, ok := value.(map[string]any)
	if !ok {
		return cfg
	}

	cfg.Enabled = toBoolAny(raw["enabled"])
	cfg.APIURL = firstNonEmptyString(
		toString(raw["api_url"], ""),
		toString(raw["apiUrl"], ""),
		toString(raw["url"], ""),
	)
	cfg.APIKey = firstNonEmptyString(
		toString(raw["api_key"], ""),
		toString(raw["apiKey"], ""),
		toString(raw["key"], ""),
	)
	cfg.TimeoutSeconds = toIntAny(raw["timeout_seconds"], cfg.TimeoutSeconds)
	cfg.MaxTimeoutMillis = toIntAny(raw["max_timeout_millis"], cfg.MaxTimeoutMillis)
	cfg.CustomUserAgent = toString(raw["custom_user_agent"], "")
	if headers := extraHeadersAny(raw["extra_headers"]); len(headers) > 0 {
		cfg.ExtraHeaders = headers
	}
	return NormalizeFlareSolverrConfig(cfg)
}

// NormalizeFlareSolverrConfig 对 FlareSolverr 配置做空白与缺省归一化。
// 参数/返回：cfg 为原始配置；返回值保证字段可直接持久化和运行时使用。
// 失败场景：无。
// 副作用：无。
func NormalizeFlareSolverrConfig(cfg FlareSolverrConfig) FlareSolverrConfig {
	normalized := cfg
	normalized.APIURL = normalizeFlareSolverrAPIURL(normalized.APIURL)
	normalized.APIKey = strings.TrimSpace(normalized.APIKey)
	if normalized.TimeoutSeconds <= 0 {
		normalized.TimeoutSeconds = DefaultFlareSolverrConfig().TimeoutSeconds
	}
	if normalized.MaxTimeoutMillis <= 0 {
		normalized.MaxTimeoutMillis = normalized.TimeoutSeconds * 1000
	}
	normalized.CustomUserAgent = strings.TrimSpace(normalized.CustomUserAgent)
	if normalized.ExtraHeaders == nil {
		normalized.ExtraHeaders = map[string]string{}
	} else {
		cleaned := map[string]string{}
		for key, value := range normalized.ExtraHeaders {
			trimmedKey := strings.TrimSpace(key)
			if trimmedKey == "" {
				continue
			}
			cleaned[trimmedKey] = value
		}
		normalized.ExtraHeaders = cleaned
	}
	return normalized
}

// ValidateFlareSolverrConfig 校验 FlareSolverr 配置是否合法。
// 参数/返回：cfg 为待校验配置；返回 error 表示前端提交内容非法。
// 失败场景：启用但缺少 API 地址、API 地址格式错误、超时值非法时返回 error。
// 副作用：无。
func ValidateFlareSolverrConfig(cfg FlareSolverrConfig) error {
	normalized := NormalizeFlareSolverrConfig(cfg)
	if !normalized.Enabled {
		return nil
	}
	if strings.TrimSpace(normalized.APIURL) == "" {
		return fmt.Errorf("FlareSolverr API 地址不能为空")
	}
	parsed, err := url.Parse(normalized.APIURL)
	if err != nil {
		return fmt.Errorf("FlareSolverr API 地址格式错误: %w", err)
	}
	if strings.TrimSpace(parsed.Scheme) != "http" && strings.TrimSpace(parsed.Scheme) != "https" {
		return fmt.Errorf("FlareSolverr API 地址仅支持 http/https 协议: %s", parsed.Scheme)
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return fmt.Errorf("FlareSolverr API 地址缺少主机名: %s", normalized.APIURL)
	}
	if normalized.TimeoutSeconds > 600 {
		return fmt.Errorf("FlareSolverr 超时时间不能超过 600 秒: %d", normalized.TimeoutSeconds)
	}
	if normalized.MaxTimeoutMillis > 10*60*1000 {
		return fmt.Errorf("FlareSolverr 最大求解时间不能超过 600000 毫秒: %d", normalized.MaxTimeoutMillis)
	}
	return nil
}

// ToMap 将 FlareSolverr 配置转换为 config.json 可持久化的 map 结构。
// 参数/返回：无参数；返回值可直接写入 Manager.Save。
// 失败场景：无。
// 副作用：无。
func (c FlareSolverrConfig) ToMap() map[string]any {
	normalized := NormalizeFlareSolverrConfig(c)
	headers := make(map[string]string, len(normalized.ExtraHeaders))
	for key, value := range normalized.ExtraHeaders {
		headers[key] = value
	}
	return map[string]any{
		"enabled":            normalized.Enabled,
		"api_url":            normalized.APIURL,
		"api_key":            normalized.APIKey,
		"timeout_seconds":    normalized.TimeoutSeconds,
		"max_timeout_millis": normalized.MaxTimeoutMillis,
		"custom_user_agent":  normalized.CustomUserAgent,
		"extra_headers":      headers,
	}
}

// FlareSolverrConfig 返回当前配置管理器中的 FlareSolverr 配置快照。
// 参数/返回：无参数；返回值用于抓取流程与设置保存后的热更新。
// 失败场景：内存配置缺失时回退默认值。
// 副作用：无。
func (m *Manager) FlareSolverrConfig() FlareSolverrConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return ParseFlareSolverrConfig(m.config["flare_solver"])
}

func normalizeFlareSolverrAPIURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "http://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return trimmed
	}
	// url.URL.String() 会把空主机名的 "http://" 折叠成 "http:"，
	// 导致二次归一化时被误判为无协议地址，这里保留原值交由校验报错。
	if parsed.Host == "" && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" {
		return trimmed
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String()
}

func extraHeadersAny(value any) map[string]string {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	headers := map[string]string{}
	for key, rawValue := range raw {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		headers[trimmedKey] = toString(rawValue, "")
	}
	return headers
}

func toBoolAny(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		lower := strings.ToLower(strings.TrimSpace(typed))
		if lower == "true" || lower == "1" {
			return true
		}
		if lower == "false" || lower == "0" {
			return false
		}
	}
	return false
}

func toIntAny(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		parsed := strings.TrimSpace(typed)
		if parsed == "" {
			return fallback
		}
		n := 0
		for _, ch := range parsed {
			if ch < '0' || ch > '9' {
				return fallback
			}
			n = n*10 + int(ch-'0')
		}
		return n
	default:
		return fallback
	}
}
