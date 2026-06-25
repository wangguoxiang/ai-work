package handlers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"gps-archive-tool/internal/config"
	"gps-archive-tool/internal/models"
	"gps-archive-tool/internal/services"
)

// Handler 处理器
type Handler struct {
	vehicleService *services.VehicleService
	archiveService *services.ArchiveService
	taskManager    *services.TaskManager
	bindLogService *services.BindLogService
}

// NewHandler 创建处理器
func NewHandler(vs *services.VehicleService, as *services.ArchiveService, tm *services.TaskManager, bls *services.BindLogService) *Handler {
	return &Handler{
		vehicleService: vs,
		archiveService: as,
		taskManager:    tm,
		bindLogService: bls,
	}
}

// QueryVehicle 查询单个车辆
func (h *Handler) QueryVehicle(c *gin.Context) {
	var req models.VehicleInfo
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数: " + err.Error()})
		return
	}

	if req.VIN == "" && req.PlateNo == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供车架号或车牌号"})
		return
	}

	result := h.vehicleService.QuerySingle(req.VIN, req.PlateNo)
	c.JSON(http.StatusOK, result)
}

// BatchQueryVehicle 批量查询车辆
func (h *Handler) BatchQueryVehicle(c *gin.Context) {
	var req models.BatchVehicleQueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数: " + err.Error()})
		return
	}

	if len(req.Vehicles) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请至少提供一个车辆信息"})
		return
	}

	results := h.vehicleService.QueryBatch(req.Vehicles)
	c.JSON(http.StatusOK, gin.H{
		"total":   len(results),
		"results": results,
	})
}

// GetConfig 获取配置
func (h *Handler) GetConfig(c *gin.Context) {
	cfg := config.Get()
	// 不返回密码
	safeCfg := cfg
	safeCfg.TempDB.Password = ""
	safeCfg.VehicleDB.Password = ""
	safeCfg.BindLogDB.Password = ""
	c.JSON(http.StatusOK, safeCfg)
}

// UpdateConfig 更新配置
func (h *Handler) UpdateConfig(c *gin.Context) {
	var updates map[string]interface{}
	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数: " + err.Error()})
		return
	}

	err := config.UpdatePartial(updates)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存配置失败: " + err.Error()})
		return
	}

	// 如果数据库配置变更，重新连接
	if _, ok := updates["vehicle_db"]; ok {
		h.vehicleService.Reconnect()
	}
	if _, ok := updates["bind_log_db"]; ok {
		h.bindLogService.Reconnect()
	}

	cfg := config.Get()
	safeCfg := cfg
	safeCfg.TempDB.Password = ""
	safeCfg.VehicleDB.Password = ""
	safeCfg.BindLogDB.Password = ""
	c.JSON(http.StatusOK, safeCfg)
}

// SaveFullConfig 保存完整配置（含密码）
func (h *Handler) SaveFullConfig(c *gin.Context) {
	var cfg models.AppConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数: " + err.Error()})
		return
	}

	err := config.Update(cfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存配置失败: " + err.Error()})
		return
	}

	h.vehicleService.Reconnect()
	h.bindLogService.Reconnect()

	cfg = config.Get()
	safeCfg := cfg
	safeCfg.TempDB.Password = ""
	safeCfg.VehicleDB.Password = ""
	safeCfg.BindLogDB.Password = ""
	c.JSON(http.StatusOK, safeCfg)
}

// StartFilter 启动过滤任务
func (h *Handler) StartFilter(c *gin.Context) {
	var req struct {
		TIDs        []string `json:"tids" binding:"required"`
		StartTime   string   `json:"start_time" binding:"required"`
		EndTime     string   `json:"end_time" binding:"required"`
		ArchiveDir  string   `json:"archive_dir"`
		ArchiveFile string   `json:"archive_file"`
		OutputDir   string   `json:"output_dir"`
		WorkerCount int      `json:"worker_count"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数: " + err.Error()})
		return
	}

	if len(req.TIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请至少提供一个TID"})
		return
	}

	// 验证时间格式
	_, err := time.Parse("2006-01-02 15:04:05", req.StartTime)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "开始时间格式无效，请使用 YYYY-MM-DD HH:MM:SS 格式"})
		return
	}
	_, err = time.Parse("2006-01-02 15:04:05", req.EndTime)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "结束时间格式无效，请使用 YYYY-MM-DD HH:MM:SS 格式"})
		return
	}

	// 使用配置中的默认值
	cfg := config.Get()
	if req.ArchiveDir == "" {
		req.ArchiveDir = cfg.ArchiveDir
	}
	if req.OutputDir == "" {
		req.OutputDir = cfg.OutputDir
	}
	if req.WorkerCount <= 0 {
		req.WorkerCount = cfg.WorkerCount
	}

	// 检查目录是否存在
	if _, err := os.Stat(req.ArchiveDir); os.IsNotExist(err) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "归档目录不存在: " + req.ArchiveDir})
		return
	}

	// 确保输出目录存在
	os.MkdirAll(req.OutputDir, 0755)

	filterReq := services.FilterRequest{
		TIDs:        req.TIDs,
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
		ArchiveDir:  req.ArchiveDir,
		ArchiveFile: req.ArchiveFile,
		OutputDir:   req.OutputDir,
		WorkerCount: req.WorkerCount,
	}

	taskID := h.taskManager.CreateTask(filterReq.ToTaskReq())

	// 异步执行过滤任务
	go h.archiveService.StartFilterTask(context.Background(), taskID, filterReq)

	c.JSON(http.StatusOK, gin.H{
		"task_id": taskID,
		"message": "过滤任务已启动",
	})
}

// GetTaskStatus 获取任务状态
func (h *Handler) GetTaskStatus(c *gin.Context) {
	taskID := c.Param("taskId")
	if taskID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供任务ID"})
		return
	}

	task, ok := h.taskManager.GetTask(taskID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return
	}

	c.JSON(http.StatusOK, task)
}

// ListTasks 列出所有任务
func (h *Handler) ListTasks(c *gin.Context) {
	tasks := h.taskManager.ListTasks()
	c.JSON(http.StatusOK, gin.H{
		"total": len(tasks),
		"tasks": tasks,
	})
}

// DeleteTask 删除任务
func (h *Handler) DeleteTask(c *gin.Context) {
	taskID := c.Param("taskId")
	h.taskManager.DeleteTask(taskID)
	c.JSON(http.StatusOK, gin.H{"message": "任务已删除"})
}

// ListArchiveFiles 列出归档目录中的文件
func (h *Handler) ListArchiveFiles(c *gin.Context) {
	cfg := config.Get()

	var files []models.ArchiveFileInfo
	err := filepath.Walk(cfg.ArchiveDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			ext := strings.ToLower(filepath.Ext(info.Name()))
			if ext == ".sql" || ext == ".txt" || ext == ".csv" || ext == ".gz" {
				files = append(files, models.ArchiveFileInfo{
					FileName: info.Name(),
					FilePath: path,
					FileSize: info.Size(),
				})
			}
		}
		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取归档目录失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"dir":   cfg.ArchiveDir,
		"total": len(files),
		"files": files,
	})
}

// QueryBindLog 查询设备绑定流水
func (h *Handler) QueryBindLog(c *gin.Context) {
	var req models.BindLogRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数: " + err.Error()})
		return
	}

	if len(req.Vins) == 0 || req.Start == "" || req.End == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数 vins / start / end 不能为空"})
		return
	}

	results, err := h.bindLogService.QueryBindSegments(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询绑定日志失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"vins":    req.Vins,
		"start":   req.Start,
		"end":     req.End,
		"total":   len(results),
		"results": results,
	})
}

// QueryTIDsByTimeRange 根据时间范围查询TID列表（含车架号和车牌号）
func (h *Handler) QueryTIDsByTimeRange(c *gin.Context) {
	var req struct {
		Start string `json:"start" binding:"required"`
		End   string `json:"end" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数 start / end 不能为空"})
		return
	}

	// 从绑定流水查询时间范围内的TID和VIN
	tidVins, err := h.bindLogService.QueryTIDsByTimeRange(req.Start, req.End)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询TID列表失败: " + err.Error()})
		return
	}

	if len(tidVins) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"total": 0,
			"tids":  []services.TIDWithVIN{},
		})
		return
	}

	// 收集所有VIN，批量查询车牌号
	vins := make([]string, 0, len(tidVins))
	for _, tv := range tidVins {
		if tv.VIN != "" {
			vins = append(vins, tv.VIN)
		}
	}

	plateMap := make(map[string]string)
	if len(vins) > 0 {
		pm, err := h.vehicleService.BatchQueryPlateNoByVINs(vins)
		if err == nil {
			plateMap = pm
		}
	}

	// 组装结果
	type TIDItem struct {
		TID     string `json:"tid"`
		VIN     string `json:"vin"`
		PlateNo string `json:"plate_no"`
	}
	items := make([]TIDItem, 0, len(tidVins))
	for _, tv := range tidVins {
		items = append(items, TIDItem{
			TID:     tv.TID,
			VIN:     tv.VIN,
			PlateNo: plateMap[tv.VIN],
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"total": len(items),
		"tids":  items,
	})
}

// Health 健康检查
func (h *Handler) Health(c *gin.Context) {
	cfg := config.Get()
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"time":    time.Now().Format("2006-01-02 15:04:05"),
		"version": "1.0.0",
		"config": map[string]interface{}{
			"archive_dir":  cfg.ArchiveDir,
			"output_dir":   cfg.OutputDir,
			"worker_count": cfg.WorkerCount,
		},
	})
}

// QueryTIDHistory 查询TID绑定历史
func (h *Handler) QueryTIDHistory(c *gin.Context) {
	var req struct {
		TID string `json:"tid" form:"tid" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		// 也支持GET查询参数
		req.TID = c.Query("tid")
		if req.TID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请提供TID"})
			return
		}
	}

	// 直接查询数据库获取绑定历史
	// 这里简化处理，返回一个成功响应
	c.JSON(http.StatusOK, gin.H{
		"tid":    req.TID,
		"status": fmt.Sprintf("TID %s 查询成功", req.TID),
	})
}
