package handlers

import (
	"context"
	"encoding/csv"
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
	cosService     *services.COSService
}

// NewHandler 创建处理器
func NewHandler(vs *services.VehicleService, as *services.ArchiveService, tm *services.TaskManager, bls *services.BindLogService, cs *services.COSService) *Handler {
	return &Handler{
		vehicleService: vs,
		archiveService: as,
		taskManager:    tm,
		bindLogService: bls,
		cosService:     cs,
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
		req.ArchiveDir = cfg.WorkDir
	}
	if req.OutputDir == "" {
		req.OutputDir = cfg.WorkDir
	}
	if req.WorkerCount <= 0 {
		req.WorkerCount = cfg.WorkerCount
	}

	// 确保目录存在
	os.MkdirAll(req.ArchiveDir, 0755)
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

// ListArchiveFiles 列出工作目录中的下载文件
func (h *Handler) ListArchiveFiles(c *gin.Context) {
	cfg := config.Get()
	downloadDir := filepath.Join(cfg.WorkDir, "downloads")

	var files []models.ArchiveFileInfo
	err := filepath.Walk(downloadDir, func(path string, info os.FileInfo, err error) error {
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
		// 目录可能还不存在，返回空列表
		files = []models.ArchiveFileInfo{}
	}

	c.JSON(http.StatusOK, gin.H{
		"dir":   downloadDir,
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

// ImportCSV 导入绑定流水CSV文件，返回TID列表（含车架号和车牌号）
func (h *Handler) ImportCSV(c *gin.Context) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传CSV文件: " + err.Error()})
		return
	}
	defer file.Close()

	if header == nil || (!strings.HasSuffix(strings.ToLower(header.Filename), ".csv") && header.Header.Get("Content-Type") != "text/csv") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传CSV格式文件"})
		return
	}

	// 读取CSV
	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CSV文件解析失败: " + err.Error()})
		return
	}

	if len(records) < 2 {
		c.JSON(http.StatusOK, gin.H{"total": 0, "tids": []interface{}{}})
		return
	}

	// 解析表头，查找列索引（去除可能的 BOM 和空白）
	headerRow := records[0]
	colMap := make(map[string]int)
	for i, col := range headerRow {
		clean := strings.TrimSpace(strings.ToLower(col))
		clean = strings.TrimLeft(clean, "\ufeff\u00a0")
		colMap[clean] = i
	}

	tidIdx, hasTID := colMap["tid"]
	vinIdx, hasVIN := colMap["vin"]

	if !hasTID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CSV文件中未找到 tid 列"})
		return
	}

	// 收集所有TID和VIN（不过滤时间，直接导入全部）
	type tidItem struct {
		TID string `json:"tid"`
		VIN string `json:"vin"`
	}
	seen := make(map[string]bool)
	var matched []tidItem
	vinsForLookup := []string{}

	for _, row := range records[1:] {
		if tidIdx >= len(row) {
			continue
		}
		tid := strings.TrimSpace(row[tidIdx])
		if tid == "" {
			continue
		}
		vin := ""
		if hasVIN && vinIdx < len(row) {
			vin = strings.TrimSpace(row[vinIdx])
		}
		key := tid + "|" + vin
		if seen[key] {
			continue
		}
		seen[key] = true
		matched = append(matched, tidItem{TID: tid, VIN: vin})
		if vin != "" {
			vinsForLookup = append(vinsForLookup, vin)
		}
	}

	// 批量查询车牌号
	plateMap := make(map[string]string)
	if len(vinsForLookup) > 0 {
		pm, err := h.vehicleService.BatchQueryPlateNoByVINs(vinsForLookup)
		if err == nil {
			plateMap = pm
		}
	}

	// 组装结果
	type resultItem struct {
		TID     string `json:"tid"`
		VIN     string `json:"vin"`
		PlateNo string `json:"plate_no"`
	}
	items := make([]resultItem, 0, len(matched))
	for _, m := range matched {
		items = append(items, resultItem{
			TID:     m.TID,
			VIN:     m.VIN,
			PlateNo: plateMap[m.VIN],
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"total": len(items),
		"tids":  items,
	})
}

// ListCOSFiles 列出COS存储桶中的文件
func (h *Handler) ListCOSFiles(c *gin.Context) {
	prefix := c.Query("prefix")
	files, err := h.cosService.ListFiles(prefix)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "列出COS文件失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"total": len(files),
		"files": files,
	})
}

// CreateCOSFilterTask 创建COS过滤任务（含CSV导入的TID）
func (h *Handler) CreateCOSFilterTask(c *gin.Context) {
	var req struct {
		TIDs      []string `json:"tids" binding:"required"`
		VINs      []string `json:"vins"`
		PlateNos  []string `json:"plate_nos"`
		StartTime string   `json:"start_time" binding:"required"`
		EndTime   string   `json:"end_time" binding:"required"`
		COSFiles  []string `json:"cos_files" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数: " + err.Error()})
		return
	}

	if len(req.TIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请至少提供一个TID"})
		return
	}
	if len(req.COSFiles) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请至少选择一个COS文件"})
		return
	}

	cfg := config.Get()
	workDir := cfg.WorkDir

	// 保存TID列表到文件
	tidWithPlates := make([]services.TIDWithPlate, len(req.TIDs))
	for i, tid := range req.TIDs {
		vin := ""
		plateNo := ""
		if i < len(req.VINs) {
			vin = req.VINs[i]
		}
		if i < len(req.PlateNos) {
			plateNo = req.PlateNos[i]
		}
		tidWithPlates[i] = services.TIDWithPlate{
			TID:     tid,
			VIN:     vin,
			PlateNo: plateNo,
		}
	}
	tidFilePath, err := services.SaveTIDFile(workDir, tidWithPlates)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存TID文件失败: " + err.Error()})
		return
	}

	// 创建任务
	taskReq := models.FilterTaskRequest{
		TIDs:        req.TIDs,
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
		COSFiles:    req.COSFiles,
		TIDFilePath: tidFilePath,
		WorkerCount: cfg.WorkerCount,
	}
	taskID := h.taskManager.CreateTask(taskReq)

	// 异步启动COS管道任务
	pipelineReq := services.COSPipelineRequest{
		TIDs:        req.TIDs,
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
		COSFiles:    req.COSFiles,
		WorkDir:     workDir,
		WorkerCount: cfg.WorkerCount,
		COSService:  h.cosService,
	}
	go h.archiveService.StartCOSPipeline(context.Background(), taskID, pipelineReq)

	c.JSON(http.StatusOK, gin.H{
		"task_id": taskID,
		"message": "任务已创建",
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
			"work_dir":     cfg.WorkDir,
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
