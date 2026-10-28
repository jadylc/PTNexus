package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/service"
)

type CrossSeedHandler struct {
	service *service.CrossSeedService
}

func NewCrossSeedHandler(svc *service.CrossSeedService) *CrossSeedHandler {
	return &CrossSeedHandler{service: svc}
}

func (h *CrossSeedHandler) UniquePaths(c *gin.Context) {
	result, err := h.service.UniquePaths()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *CrossSeedHandler) Data(c *gin.Context) {
	startTime := time.Now()
	includeUniquePaths := true
	if raw := strings.TrimSpace(c.Query("include_unique_paths")); raw != "" {
		includeUniquePaths = boolQuery(c, "include_unique_paths")
	}

	params := service.CrossSeedQueryParams{
		Page:               intQuery(c, "page", 1),
		PageSize:           intQuery(c, "page_size", 20),
		Search:             c.Query("search"),
		PathFilters:        stringSliceQuery(c, "path_filters"),
		IsDeleted:          c.Query("is_deleted"),
		ExcludeTargetSites: c.Query("exclude_target_sites"),
		ReviewStatus:       c.Query("review_status"),
		IncludeUniquePaths: includeUniquePaths,
	}
	result, err := h.service.QueryData(params)
	if err != nil {
		logx.Warnf("跨种查询", "GET /api/cross-seed-data 失败 耗时=%s err=%v", time.Since(startTime), err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	logx.Infof("跨种查询", "GET /api/cross-seed-data 完成 耗时=%s total=%v count=%v",
		time.Since(startTime), result["total"], result["count"])
	c.JSON(http.StatusOK, result)
}

func (h *CrossSeedHandler) TestNoAuth(c *gin.Context) {
	// Python baseline uses str(time.time()).
	seconds := float64(time.Now().UnixNano()) / 1e9
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "无认证测试成功", "timestamp": strconv.FormatFloat(seconds, 'f', -1, 64)})
}

func (h *CrossSeedHandler) DeleteData(c *gin.Context) {
	payload := map[string]any{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "请求体格式错误"})
		return
	}
	result, status := h.service.DeleteCrossSeedData(payload)
	c.JSON(status, result)
}

// ClearDeletedData 一键清除所有"已删除"记录（种子已不在下载器的数据行）。
// 参数/返回：无请求参数；返回 success/message/deleted_count。
// 失败场景：数据库删除失败时返回对应错误码。
// 副作用：删除 seed_parameters 中已不存在未隐藏种子的数据行。
func (h *CrossSeedHandler) ClearDeletedData(c *gin.Context) {
	result, status := h.service.ClearDeletedData()
	c.JSON(status, result)
}
