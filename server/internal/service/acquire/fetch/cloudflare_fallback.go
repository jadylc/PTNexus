package fetch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/flaresolverr"
	"github.com/pt-nexus/server/internal/platform/logx"
)

const cloudflareFallbackLogModule = "迁移-Cloudflare挑战"

// ErrCloudflareChallenge 表示详情页或下载请求命中 Cloudflare 挑战页且未成功求解。
var ErrCloudflareChallenge = errors.New("详情页命中 Cloudflare 挑战页")

// httpStatusError 携带非 2xx 响应的状态码与响应体，便于上层识别 Cloudflare 挑战页。
type httpStatusError struct {
	status int
	body   []byte
}

// Error 返回与原实现一致的错误文本，保证日志与前端提示不变。
func (e *httpStatusError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.status, strings.TrimSpace(string(e.body)))
}

// isCloudflareChallengeBody 判断响应体是否为 Cloudflare 挑战页。
// 参数/返回：body 为响应体；返回是否命中挑战特征。
// 失败场景：不适用。
// 副作用：无。
func isCloudflareChallengeBody(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	sample := strings.ToLower(string(body[:minInt(len(body), 32768)]))
	markers := []string{
		"just a moment",
		"challenges.cloudflare.com",
		"__cf_chl_opt",
		"cf_chl_opt",
		"cf-browser-verification",
		"enable javascript and cookies to continue",
		"attention required! | cloudflare",
		"checking your browser before accessing",
		"please wait while we verify your request",
		"cf_chl_runtime_hint",
	}
	for _, marker := range markers {
		if strings.Contains(sample, marker) {
			return true
		}
	}
	return false
}

// fetchBinarySmart 抓取二进制内容；命中 Cloudflare 挑战时通过 FlareSolverr 求解后重试。
// 参数/返回：targetURL 为目标地址，referer 为来源页，cookie 为站点登录态，timeout 为超时；
// 返回响应体、合并后的 Cookie 与错误。
// 失败场景：非挑战类错误直接返回原错误；挑战页且 FlareSolverr 未启用或求解失败时返回对应错误。
// 副作用：可能调用 FlareSolverr 发起网络请求。
func fetchBinarySmart(targetURL, referer, cookie string, timeout time.Duration) ([]byte, string, error) {
	body, err := fetchBinaryWithCookie(targetURL, referer, cookie, timeout)
	if err == nil {
		if !isCloudflareChallengeBody(body) {
			return body, cookie, nil
		}
	} else {
		var statusErr *httpStatusError
		if !errors.As(err, &statusErr) || !isCloudflareChallengeBody(statusErr.body) {
			return body, cookie, err
		}
	}

	if !flaresolverr.Enabled() {
		if err != nil {
			return body, cookie, err
		}
		return body, cookie, ErrCloudflareChallenge
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	solution, solveErr := flaresolverr.Solve(ctx, targetURL, cookie)
	if solveErr != nil {
		logx.Warnf(cloudflareFallbackLogModule, "FlareSolverr 求解失败 target_url=%s err=%v", redactURLQuery(targetURL), solveErr)
		if err != nil {
			return body, cookie, err
		}
		return body, cookie, fmt.Errorf("%w: %v", ErrCloudflareChallenge, solveErr)
	}

	merged := mergeCookieStrings(cookie, solution.Cookies)
	logx.Infof(cloudflareFallbackLogModule, "FlareSolverr 求解成功 target_url=%s cookies=%d merged=%t", redactURLQuery(targetURL), len(solution.Cookies), merged != cookie)

	// 优先用求解得到的 Cookie 重新直接抓取，避免二进制内容经 JSON 字符串转换损坏。
	if merged != cookie {
		if retried, retryErr := fetchBinaryWithCookie(targetURL, referer, merged, timeout); retryErr == nil {
			return retried, merged, nil
		} else {
			logx.Warnf(cloudflareFallbackLogModule, "使用 FlareSolverr Cookie 重试失败 target_url=%s err=%v", redactURLQuery(targetURL), retryErr)
		}
	}

	if strings.TrimSpace(solution.Body) == "" {
		return body, merged, ErrCloudflareChallenge
	}
	if isCloudflareChallengeBody([]byte(solution.Body)) {
		return []byte(solution.Body), merged, ErrCloudflareChallenge
	}
	return []byte(solution.Body), merged, nil
}

// mergeCookieStrings 合并站点 Cookie 与 FlareSolverr 返回的 Cookie。
// 参数/返回：existing 为站点原始 Cookie 串，cookies 为求解得到的 Cookie；返回合并后的 Cookie 串。
// 失败场景：不适用。
// 副作用：无。
func mergeCookieStrings(existing string, cookies []flaresolverr.Cookie) string {
	order := make([]string, 0)
	values := map[string]string{}
	for _, part := range strings.Split(existing, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, exists := values[name]; exists {
			continue
		}
		values[name] = strings.TrimSpace(value)
		order = append(order, name)
	}
	for _, item := range cookies {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		if _, exists := values[name]; !exists {
			order = append(order, name)
		}
		values[name] = item.Value
	}

	parts := make([]string, 0, len(order))
	for _, name := range order {
		parts = append(parts, name+"="+values[name])
	}
	return strings.Join(parts, "; ")
}
