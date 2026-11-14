package repair

import "testing"

// TestCSPTTokenIssueRegex 回归 2026.11 误报：PTGen 超时业务错误的文案
// 「GET响应无效: api failed: 查询超时…」曾因含「响应无效」被误判为令牌异常。
func TestCSPTTokenIssueRegex(t *testing.T) {
	tokenIssueCases := []string{
		"GET响应无效: api failed: 令牌无效",
		"api failed: token invalid",
		"unauthorized",
		"HTTP 401",
		"HTTP 403 forbidden",
		"未授权，请检查令牌",
		"apikey not valid",
	}
	for _, text := range tokenIssueCases {
		if !reCSPTTokenIssue.MatchString(text) {
			t.Errorf("应判定为令牌异常但未命中: %q", text)
		}
	}

	// 超时/网络/业务失败都不该算令牌问题
	notTokenIssueCases := []string{
		"GET响应无效: api failed: 查询超时，请稍后重试或尝试其他资源",
		"api failed: 查询超时",
		"context deadline exceeded",
		"connection reset by peer",
		"GET响应无效: api failed: 资源不存在",
	}
	for _, text := range notTokenIssueCases {
		if reCSPTTokenIssue.MatchString(text) {
			t.Errorf("不应判定为令牌异常但命中了: %q", text)
		}
	}
}
