package settings

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pt-nexus/server/internal/platform/flaresolverr"
)

// TestFlareSolverrConnection 探测 FlareSolverr 服务连通性（不发起求解）。
// 参数/返回：请求体含 api_url（必填）与 api_key（可选）；成功返回 success/message。
// 失败场景：缺少地址、地址不可达、目标不是 FlareSolverr 或鉴权失败时返回 400/502。
// 副作用：向目标地址发起一次 HTTP 探测请求。
func (h *Handler) TestFlareSolverrConnection(c *gin.Context) {
	payload := struct {
		APIURL string `json:"api_url"`
		APIKey string `json:"api_key"`
	}{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求体格式错误"})
		return
	}

	apiURL := strings.TrimSpace(payload.APIURL)
	if apiURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请先填写 FlareSolverr API 地址"})
		return
	}

	message, err := flaresolverr.Ping(apiURL, strings.TrimSpace(payload.APIKey))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": message})
}
