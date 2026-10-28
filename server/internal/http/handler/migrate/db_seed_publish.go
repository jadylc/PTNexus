package migrate

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pt-nexus/server/internal/platform/logx"
)

const (
	dbSeedInfoHandlerLogModule      = "迁移接口-种子查询"
	fetchStoreHandlerLogModule      = "迁移接口-源站抓取"
	updateDBSeedHandlerLogModule    = "迁移接口-种子更新"
	updateScreenshotReviewLogModule = "迁移接口-截图审查"
)

func (h *Handler) GetDBSeedInfo(c *gin.Context) {
	torrentID := strings.TrimSpace(c.Query("torrent_id"))
	siteName := strings.TrimSpace(c.Query("site_name"))
	taskID := strings.TrimSpace(c.Query("task_id"))
	requestID := c.GetString("request_id")
	logx.Infof(dbSeedInfoHandlerLogModule, "收到请求 请求ID=%s torrent_id=%s site_name=%s task_id=%s", requestID, torrentID, siteName, taskID)
	result, status := h.service.GetDBSeedInfo(torrentID, siteName, taskID)
	if success, ok := result["success"].(bool); ok && !success {
		logx.Warnf(dbSeedInfoHandlerLogModule, "请求结束 请求ID=%s 状态码=%d success=%t error_code=%s", requestID, status, success, handlerToString(result["error_code"], ""))
	} else if status >= http.StatusBadRequest {
		logx.Warnf(dbSeedInfoHandlerLogModule, "请求结束 请求ID=%s 状态码=%d", requestID, status)
	} else {
		logx.Infof(dbSeedInfoHandlerLogModule, "请求结束 请求ID=%s 状态码=%d", requestID, status)
	}
	c.JSON(status, result)
}

func (h *Handler) FetchAndStore(c *gin.Context) {
	requestID := c.GetString("request_id")
	payload, ok := bindMapPayload(c)
	if !ok {
		logx.Warnf(fetchStoreHandlerLogModule, "请求体绑定失败 请求ID=%s", requestID)
		return
	}
	sourceSite := strings.TrimSpace(handlerToString(payload["sourceSite"], ""))
	searchTerm := strings.TrimSpace(handlerToString(payload["searchTerm"], ""))
	taskID := strings.TrimSpace(handlerToString(payload["task_id"], ""))
	logx.Infof(fetchStoreHandlerLogModule, "收到请求 请求ID=%s source_site=%s search_term=%s task_id=%s", requestID, sourceSite, searchTerm, taskID)
	result, status := h.service.FetchAndStore(payload)
	if success, ok := result["success"].(bool); ok && !success {
		logx.Warnf(fetchStoreHandlerLogModule, "请求结束 请求ID=%s source_site=%s search_term=%s 状态码=%d success=%t error_code=%s", requestID, sourceSite, searchTerm, status, success, handlerToString(result["error_code"], ""))
	} else if status >= http.StatusBadRequest {
		logx.Warnf(fetchStoreHandlerLogModule, "请求结束 请求ID=%s source_site=%s search_term=%s 状态码=%d", requestID, sourceSite, searchTerm, status)
	} else {
		logx.Infof(fetchStoreHandlerLogModule, "请求结束 请求ID=%s source_site=%s search_term=%s 状态码=%d", requestID, sourceSite, searchTerm, status)
	}
	c.JSON(status, result)
}

// RefetchMaintenanceSeed 维护页"重新拉取数据"：按源站与种子 ID 重跑抓取入库流程。
// 参数/返回：请求体与 fetch_and_store 一致（sourceSite/searchTerm/torrentName/savePath/
// downloaderId/task_id/screenshotReviewMode）；返回抓取入库结果。
// 失败场景：参数缺失或抓取失败时返回对应错误码。
// 副作用：重新请求源站详情页并更新 seed_parameters 对应行。
// 说明：前端维护详情页此前调用该路径但后端无实现（404"接口不存在"），
// 现在委托与跨种面板相同的抓取入库流程，保证两者行为一致。
func (h *Handler) RefetchMaintenanceSeed(c *gin.Context) {
	requestID := c.GetString("request_id")
	payload, ok := bindMapPayload(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求体格式错误"})
		return
	}
	sourceSite := strings.TrimSpace(handlerToString(payload["sourceSite"], ""))
	searchTerm := strings.TrimSpace(handlerToString(payload["searchTerm"], ""))
	logx.Infof(fetchStoreHandlerLogModule, "维护页重新拉取 请求ID=%s source_site=%s search_term=%s", requestID, sourceSite, searchTerm)
	result, status := h.service.FetchAndStore(payload)
	c.JSON(status, result)
}

func (h *Handler) UpdateDBSeedInfo(c *gin.Context) {
	requestID := c.GetString("request_id")
	payload, ok := bindMapPayload(c)
	if !ok {
		logx.Warnf(updateDBSeedHandlerLogModule, "请求体绑定失败 请求ID=%s", requestID)
		return
	}
	torrentID := strings.TrimSpace(handlerToString(payload["torrent_id"], ""))
	siteName := strings.TrimSpace(handlerToString(payload["site_name"], ""))
	logx.Infof(updateDBSeedHandlerLogModule, "收到请求 请求ID=%s torrent_id=%s site_name=%s", requestID, torrentID, siteName)
	result, status := h.service.UpdateDBSeedInfo(payload)
	if status >= http.StatusBadRequest {
		logx.Warnf(updateDBSeedHandlerLogModule, "请求结束 请求ID=%s torrent_id=%s site_name=%s 状态码=%d", requestID, torrentID, siteName, status)
	} else {
		logx.Infof(updateDBSeedHandlerLogModule, "请求结束 请求ID=%s torrent_id=%s site_name=%s 状态码=%d", requestID, torrentID, siteName, status)
	}
	c.JSON(status, result)
}

// UpdateScreenshotReviewStatus 更新指定种子的截图人工确认状态。
// 参数/返回：请求体包含 torrent_id、site_name 与 screenshot_review_status；成功时返回最新状态。
// 失败场景：请求体无效、参数缺失或数据库写入失败时返回对应错误码。
// 副作用：会更新 seed_parameters 的 screenshot_review_status 字段。
func (h *Handler) UpdateScreenshotReviewStatus(c *gin.Context) {
	requestID := c.GetString("request_id")
	payload, ok := bindMapPayload(c)
	if !ok {
		logx.Warnf(updateScreenshotReviewLogModule, "请求体绑定失败 请求ID=%s", requestID)
		return
	}
	torrentID := strings.TrimSpace(handlerToString(payload["torrent_id"], ""))
	siteName := strings.TrimSpace(handlerToString(payload["site_name"], ""))
	logx.Infof(updateScreenshotReviewLogModule, "收到请求 请求ID=%s torrent_id=%s site_name=%s", requestID, torrentID, siteName)
	result, status := h.service.UpdateScreenshotReviewStatus(payload)
	if status >= http.StatusBadRequest {
		logx.Warnf(updateScreenshotReviewLogModule, "请求结束 请求ID=%s torrent_id=%s site_name=%s 状态码=%d", requestID, torrentID, siteName, status)
	} else {
		logx.Infof(updateScreenshotReviewLogModule, "请求结束 请求ID=%s torrent_id=%s site_name=%s 状态码=%d", requestID, torrentID, siteName, status)
	}
	c.JSON(status, result)
}

// ReparseTitleComponents 按当前解析规则重建标题组件并写回（存量数据修正口子）。
// 参数/返回：请求体可选 site_name / torrent_id 限定范围（均缺省为全量）；
// 返回 success/message/updated/skipped/failed/total。
// 失败场景：数据库读取或写入失败时返回对应错误码。
// 副作用：批量更新 seed_parameters.title_components。
// 说明：标题解析规则升级（如 HDR10+/PMTP/HDTVRip 识别修复）后，存量数据需
// 显式重算才会刷新；维护详情页可传 torrent_id+site_name 重算单条。
func (h *Handler) ReparseTitleComponents(c *gin.Context) {
	requestID := c.GetString("request_id")
	payload, ok := bindMapPayload(c)
	if !ok {
		payload = map[string]any{}
	}
	siteName := strings.TrimSpace(handlerToString(payload["site_name"], ""))
	torrentID := strings.TrimSpace(handlerToString(payload["torrent_id"], ""))
	logx.Infof(updateDBSeedHandlerLogModule, "标题重算 请求ID=%s site_name=%s torrent_id=%s", requestID, siteName, torrentID)
	result, status := h.service.ReparseTitleComponents(payload)
	c.JSON(status, result)
}

func (h *Handler) Publish(c *gin.Context) {
	payload, ok := bindMapPayload(c)
	if !ok {
		return
	}
	result, status := h.service.Publish(payload)
	c.JSON(status, result)
}

func (h *Handler) PublishBatchStart(c *gin.Context) {
	payload, ok := bindMapPayload(c)
	if !ok {
		return
	}
	result, status := h.service.StartPublishBatch(payload)
	c.JSON(status, result)
}

func (h *Handler) PublishBatchStatus(c *gin.Context) {
	batchID := strings.TrimSpace(c.Param("batch_id"))
	if batchID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "缺少 batch_id 参数"})
		return
	}
	result, status := h.service.PublishBatchStatus(batchID)
	c.JSON(status, result)
}

func (h *Handler) PublishBatchCancel(c *gin.Context) {
	batchID := strings.TrimSpace(c.Param("batch_id"))
	if batchID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "缺少 batch_id 参数"})
		return
	}
	result, status := h.service.PublishBatchCancel(batchID)
	c.JSON(status, result)
}
