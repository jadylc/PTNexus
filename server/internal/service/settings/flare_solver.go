package settings

import (
	"fmt"

	"github.com/pt-nexus/server/internal/config"
	"github.com/pt-nexus/server/internal/platform/flaresolverr"
)

// normalizeFlareSolverrValue 校验并规范化前端提交的 FlareSolverr 配置。
// 参数/返回：raw 为前端提交的原始配置；返回规范化配置或校验错误。
// 失败场景：配置非法时返回 InvalidSettingsError。
// 副作用：无。
func normalizeFlareSolverrValue(raw any) (config.FlareSolverrConfig, error) {
	cfg := config.ParseFlareSolverrConfig(raw)
	if err := config.ValidateFlareSolverrConfig(cfg); err != nil {
		return config.FlareSolverrConfig{}, &InvalidSettingsError{Message: fmt.Sprintf("FlareSolverr 配置不合法: %v", err)}
	}
	return config.NormalizeFlareSolverrConfig(cfg), nil
}

// applyFlareSolverrRuntime 将 FlareSolverr 配置热更新到运行时。
// 参数/返回：cfg 为已规范化的配置；返回 error 表示运行时更新失败。
// 失败场景：配置非法或运行时初始化失败时返回 error。
// 副作用：更新全局 FlareSolverr 运行时配置。
func applyFlareSolverrRuntime(cfg config.FlareSolverrConfig) error {
	if err := flaresolverr.Update(cfg); err != nil {
		return fmt.Errorf("应用 FlareSolverr 配置失败: %w", err)
	}
	return nil
}
