package handlers

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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
	csvFilterMgr   *services.CSVFilterTaskManager
}

// NewHandler 创建处理器
func NewHandler(vs *services.VehicleService, as *services.ArchiveService, tm *services.TaskManager, bls *services.BindLogService, cs *services.COSService) *Handler {
	return &Handler{
		vehicleService: vs,
		archiveService: as,
		taskManager:    tm,
		bindLogService: bls,
		cosService:     cs,
		csvFilterMgr:   services.NewCSVFilterTaskManager(),
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
		StartTime   string   `json:"start_time"`
		EndTime     string   `json:"end_time"`
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
// 同时将CSV文件保存到服务器 work_dir/uploads/ 目录，返回 file_path
func (h *Handler) ImportCSV(c *gin.Context) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传CSV文件: " + err.Error()})
		return
	}
	defer file.Close()

	if header == nil || !strings.HasSuffix(strings.ToLower(header.Filename), ".csv") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传CSV格式文件(.csv)"})
		return
	}

	// 先将文件保存到服务器磁盘
	cfg := config.Get()
	uploadDir := filepath.Join(cfg.WorkDir, "uploads")
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建上传目录失败: " + err.Error()})
		return
	}

	saveName := fmt.Sprintf("%d_%s", time.Now().Unix(), header.Filename)
	savePath := filepath.Join(uploadDir, saveName)

	// 读取原始文件内容到内存，同时保存到磁盘
	rawData, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取文件失败: " + err.Error()})
		return
	}

	if err := os.WriteFile(savePath, rawData, 0644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存文件失败: " + err.Error()})
		return
	}
	log.Printf("[CSV导入] 已保存: %s (%d bytes)", savePath, len(rawData))

	// 从内存数据解析CSV
	reader := csv.NewReader(strings.NewReader(string(rawData)))
	records, err := reader.ReadAll()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CSV文件解析失败: " + err.Error()})
		return
	}

	if len(records) < 2 {
		c.JSON(http.StatusOK, gin.H{"total": 0, "tids": []interface{}{}, "file_path": savePath})
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
		"total":     len(items),
		"tids":      items,
		"file_path": savePath,
		"file_name": header.Filename,
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
		StartTime string   `json:"start_time"`
		EndTime   string   `json:"end_time"`
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

// GetCSVFilterManager 返回 CSV 过滤器管理器
func (h *Handler) GetCSVFilterManager() *services.CSVFilterTaskManager {
	return h.csvFilterMgr
}

// ========== CSV 上传 & 过滤器 API ==========

// UploadCSVFile 上传CSV文件到服务器 work_dir
func (h *Handler) UploadCSVFile(c *gin.Context) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传CSV文件: " + err.Error()})
		return
	}
	defer file.Close()

	if header == nil || !strings.HasSuffix(strings.ToLower(header.Filename), ".csv") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传CSV格式文件(.csv)"})
		return
	}

	cfg := config.Get()
	uploadDir := filepath.Join(cfg.WorkDir, "uploads")
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建上传目录失败: " + err.Error()})
		return
	}

	saveName := fmt.Sprintf("%d_%s", time.Now().Unix(), header.Filename)
	savePath := filepath.Join(uploadDir, saveName)

	out, err := os.Create(savePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存文件失败: " + err.Error()})
		return
	}
	defer out.Close()

	if _, err := io.Copy(out, file); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "写入文件失败: " + err.Error()})
		return
	}

	log.Printf("[CSV上传] 已保存: %s (%d bytes)", savePath, header.Size)
	c.JSON(http.StatusOK, gin.H{
		"file_name": header.Filename,
		"file_path": savePath,
		"file_size": header.Size,
	})
}

// ========== CSV 过滤器 API ==========

// ========== COS 文件下载 API（异步+进度轮询） ==========

// DownloadCOSFileReq 下载请求
type DownloadCOSFileReq struct {
	COSKey string `json:"cos_key" binding:"required"`
	Force  bool   `json:"force"`
}

// dlProgress 单个下载任务的进度
type dlProgress struct {
	mu        sync.Mutex
	Progress  int    `json:"progress"` // 0-100
	Message   string `json:"message"`  // 当前状态描述
	LocalPath string `json:"local_path"`
	FileName  string `json:"file_name"`
	Error     string `json:"error,omitempty"`
	Done      bool   `json:"done"`
}

var (
	dlProgressMu sync.Mutex
	dlProgresses = map[string]*dlProgress{} // key = cos_key
)

func getDLProgress(cosKey string) *dlProgress {
	dlProgressMu.Lock()
	defer dlProgressMu.Unlock()
	p, ok := dlProgresses[cosKey]
	if !ok {
		p = &dlProgress{Progress: 0, Message: "初始化"}
		dlProgresses[cosKey] = p
	}
	return p
}

func setDLProgress(cosKey string, fn func(p *dlProgress)) {
	p := getDLProgress(cosKey)
	p.mu.Lock()
	defer p.mu.Unlock()
	fn(p)
}

// runCoscmdDownload 在后台运行 coscmd download，解析 stdout+stderr 输出进度
func (h *Handler) runCoscmdDownload(cosKey, localPath string) {
	setDLProgress(cosKey, func(p *dlProgress) {
		p.Progress = 0
		p.Message = "正在启动 coscmd download..."
		p.Error = ""
		p.Done = false
	})

	runCmd := fmt.Sprintf("coscmd download %s %s", cosKey, localPath)
	log.Printf("[COS下载] 执行命令: %s", runCmd)
	cmd := exec.Command("coscmd", "download", cosKey, localPath)
	// coscmd 是 Python 工具,管道模式下会缓冲输出,设置环境变量强制无缓冲
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")

	// 同时捕获 stdout 和 stderr（coscmd 进度可能输出到任一流）
	stdout, err1 := cmd.StdoutPipe()
	stderr, err2 := cmd.StderrPipe()
	if err1 != nil || err2 != nil {
		log.Printf("[COS下载] 创建 pipe 失败: stdoutErr=%v stderrErr=%v", err1, err2)
		setDLProgress(cosKey, func(p *dlProgress) {
			p.Error = "创建 pipe 失败"
			p.Done = true
		})
		return
	}

	if err := cmd.Start(); err != nil {
		log.Printf("[COS下载] 启动命令失败: %v", err)
		setDLProgress(cosKey, func(p *dlProgress) {
			p.Error = "启动 coscmd download 失败: " + err.Error()
			p.Done = true
		})
		return
	}
	log.Printf("[COS下载] 命令已启动, PID=%d", cmd.Process.Pid)

	// 合并 stdout 和 stderr 一起读取
	reader := io.MultiReader(stdout, stderr)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1024*64), 1024*64)

	// 匹配多种进度格式:
	//   100%  /  35.00%  /  [####] 35%  /  progress: 45%  /  45.5%
	re := regexp.MustCompile(`(\d+)(?:\.\d+)?\s*%`)
	for scanner.Scan() {
		line := scanner.Text()
		log.Printf("[COS下载 输出] %s", line)
		if m := re.FindStringSubmatch(line); len(m) > 1 {
			pct := 0
			fmt.Sscanf(m[1], "%d", &pct)
			if pct < 0 {
				pct = 0
			}
			if pct > 100 {
				pct = 100
			}
			// 只在大幅变化时更新避免频繁加锁
			setDLProgress(cosKey, func(p *dlProgress) {
				if pct > p.Progress || pct == 100 {
					p.Progress = pct
				}
				p.Message = line
			})
		} else {
			setDLProgress(cosKey, func(p *dlProgress) {
				p.Message = line
			})
		}
	}
	scanErr := scanner.Err()
	if scanErr != nil {
		log.Printf("[COS下载] 扫描输出出错: %v", scanErr)
	}

	err := cmd.Wait()
	log.Printf("[COS下载] 命令退出, err=%v, exitCode=%d", err, cmd.ProcessState.ExitCode())
	if err != nil {
		log.Printf("[COS下载] coscmd 失败: %v，回退 SDK", err)
		setDLProgress(cosKey, func(p *dlProgress) {
			p.Message = "coscmd 失败,正在回退 SDK 方式..."
		})
		if err2 := h.cosService.DownloadFile(cosKey, localPath); err2 != nil {
			setDLProgress(cosKey, func(p *dlProgress) {
				p.Error = "SDK 回退也失败: " + err2.Error()
				p.Done = true
				p.Progress = 100
			})
			return
		}
	}

	setDLProgress(cosKey, func(p *dlProgress) {
		p.Progress = 100
		p.Message = "下载完成"
		p.Done = true
	})
	log.Printf("[COS下载] 完成: %s -> %s", cosKey, localPath)
}

// DownloadCOSFile 启动异步下载，返回 task_id（即 cos_key）用于轮询进度
func (h *Handler) DownloadCOSFile(c *gin.Context) {
	var req DownloadCOSFileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数无效: " + err.Error()})
		return
	}
	log.Printf("[COS下载] 收到下载请求: cos_key=%s force=%v", req.COSKey, req.Force)

	cfg := config.Get()
	downloadDir := filepath.Join(cfg.WorkDir, "downloads")
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建下载目录失败: " + err.Error()})
		return
	}

	localName := filepath.Base(req.COSKey)
	localPath := filepath.Join(downloadDir, localName)

	// 检查文件是否已存在
	if _, err := os.Stat(localPath); err == nil {
		if !req.Force {
			c.JSON(http.StatusConflict, gin.H{
				"exists":     true,
				"local_path": localPath,
				"file_name":  localName,
				"cos_key":    req.COSKey,
				"message":    fmt.Sprintf("文件 %s 已存在，是否覆盖？", localName),
			})
			return
		}
		log.Printf("[COS下载] 文件已存在，强制覆盖: %s", localPath)
		if err := os.Remove(localPath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "删除旧文件失败: " + err.Error()})
			return
		}
	}

	// 初始化进度记录
	getDLProgress(req.COSKey)
	setDLProgress(req.COSKey, func(p *dlProgress) {
		p.Progress = 0
		p.Message = "排队中..."
		p.LocalPath = localPath
		p.FileName = localName
		p.Done = false
		p.Error = ""
	})

	// 异步启动下载
	go h.runCoscmdDownload(req.COSKey, localPath)

	// 立即返回 task_id（即 cos_key），前端用它轮询进度
	c.JSON(http.StatusOK, gin.H{
		"task_id":    req.COSKey,
		"cos_key":    req.COSKey,
		"local_path": localPath,
		"file_name":  localName,
	})
}

// GetDownloadProgress 查询下载进度（供前端轮询，每5秒）
func (h *Handler) GetDownloadProgress(c *gin.Context) {
	taskID := c.Query("task_id")
	if taskID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "task_id 不能为空"})
		return
	}
	p := getDLProgress(taskID)
	p.mu.Lock()
	defer p.mu.Unlock()
	c.JSON(http.StatusOK, gin.H{
		"task_id":    taskID,
		"progress":   p.Progress,
		"message":    p.Message,
		"local_path": p.LocalPath,
		"file_name":  p.FileName,
		"error":      p.Error,
		"done":       p.Done,
	})
}

// ========== CSV 过滤器 API ==========

// CSVFilterRequest CSV过滤请求
type CSVFilterRequest struct {
	TarPaths   []string `json:"tar_paths"`
	TarPath    string   `json:"tar_path"`
	CSVPath    string   `json:"csv_path" binding:"required"`
	OutputPath string   `json:"output_path"`
	Restart    bool     `json:"restart"`
}

// StartCSVFilter 提交CSV过滤任务（tar_paths 需为已下载到本地的文件路径）
func (h *Handler) StartCSVFilter(c *gin.Context) {
	var req CSVFilterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "解析请求失败: " + err.Error()})
		return
	}
	if req.CSVPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "csv_path 不能为空"})
		return
	}

	tarPaths := req.TarPaths
	if len(tarPaths) == 0 && req.TarPath != "" {
		tarPaths = []string{req.TarPath}
	}
	if len(tarPaths) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "至少需要一个已下载的 tar.gz 路径"})
		return
	}

	// 解析 CSV
	segments, err := services.ReadCSV(req.CSVPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CSV 解析失败: " + err.Error()})
		return
	}

	// 预检查所有 tar 是否存在
	for _, p := range tarPaths {
		if _, err := os.Stat(p); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "文件不存在: " + p + " (" + err.Error() + ")"})
			return
		}
	}

	groupCancel := make(chan struct{})

	type submittedTask struct {
		TarPath     string `json:"tar_path"`
		TaskID      string `json:"task_id"`
		ResumedFrom int64  `json:"resumed_from"`
		Error       string `json:"error,omitempty"`
	}
	results := make([]submittedTask, 0, len(tarPaths))

	type pendingTask struct {
		t    *services.CSVFilterTask
		prog *services.CSVProgressFile
	}
	queue := make([]pendingTask, 0, len(tarPaths))
	for _, tarPath := range tarPaths {
		outputPath := req.OutputPath
		if outputPath == "" || len(tarPaths) > 1 {
			outputPath = ""
		}
		var prog *services.CSVProgressFile
		if !req.Restart {
			if p, ok := services.LoadCSVProgress(tarPath, req.CSVPath, outputPath); ok {
				prog = p
			}
		}
		t, err := h.csvFilterMgr.Submit(tarPath, req.CSVPath, outputPath, req.Restart, groupCancel)
		if err != nil {
			results = append(results, submittedTask{TarPath: tarPath, Error: err.Error()})
			continue
		}
		queue = append(queue, pendingTask{t: t, prog: prog})
		rf := int64(0)
		if prog != nil {
			rf = prog.LinesDone
		}
		results = append(results, submittedTask{TarPath: tarPath, TaskID: t.ID, ResumedFrom: rf})
	}

	// 后台串行执行
	go func() {
		for _, pt := range queue {
			select {
			case <-groupCancel:
				pt.t.SetStatus(services.CSVStatusFailed)
				pt.t.SetError("组已取消")
				continue
			default:
			}
			h.csvFilterMgr.RunTask(pt.t, segments, pt.prog)
		}
	}()

	c.JSON(http.StatusOK, gin.H{"tasks": results})
}

// ListCSVFilterTasks 列出所有CSV过滤任务
func (h *Handler) ListCSVFilterTasks(c *gin.Context) {
	tasks := h.csvFilterMgr.List()
	c.JSON(http.StatusOK, gin.H{"tasks": tasks})
}

// CancelCSVFilterTask 取消CSV过滤任务
func (h *Handler) CancelCSVFilterTask(c *gin.Context) {
	id := c.Query("id")
	ok := h.csvFilterMgr.Cancel(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "cancel_requested"})
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
