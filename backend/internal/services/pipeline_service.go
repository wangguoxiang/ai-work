package services

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"gps-archive-tool/internal/config"
)

// ========== 管道阶段定义 ==========

// PipelineStage 管道任务阶段
type PipelineStage string

const (
	StagePending   PipelineStage = "pending"
	StageDownload  PipelineStage = "downloading"
	StageFilter    PipelineStage = "filtering"
	StageImport    PipelineStage = "importing"
	StageCompleted PipelineStage = "completed"
	StageFailed    PipelineStage = "failed"
)

// ========== 文件下载进度（管道内使用） ==========

// FileDownloadInfo 单个文件下载进度（管道用）
type FileDownloadInfo struct {
	COSKey    string `json:"cos_key"`
	FileName  string `json:"file_name"`
	Progress  int    `json:"progress"`
	Message   string `json:"message"`
	Done      bool   `json:"done"`
	Error     string `json:"error,omitempty"`
	LocalPath string `json:"local_path,omitempty"`
}

// ========== 管道任务 ==========

// PipelineTask 一个完整的管道任务（下载 → 过滤 → 导入）
type PipelineTask struct {
	ID        string        `json:"id"`
	Status    PipelineStage `json:"status"`
	Progress  int           `json:"progress"` // 整体进度 0-100
	Error     string        `json:"error,omitempty"`
	StartAt   int64         `json:"start_at"`
	UpdatedAt int64         `json:"updated_at"`
	Elapsed   string        `json:"elapsed"`

	// 输入参数
	COSKeys  []string `json:"cos_keys"`
	TIDs     []string `json:"tids"`
	VINs     []string `json:"vins"`
	PlateNos []string `json:"plate_nos"`
	CSVPath  string   `json:"csv_path"`

	// 阶段1: 下载
	Downloads        []FileDownloadInfo `json:"downloads"`
	DownloadProgress int                `json:"download_progress"` // 0-100

	// 阶段2: 过滤
	FilterStatus    string `json:"filter_status"`
	FilterProgress  int    `json:"filter_progress"`
	FilterTaskID    string `json:"filter_task_id"`
	FilterKeptLines int64  `json:"filter_lines_kept"`
	FilterRawLines  int64  `json:"filter_lines_raw"`

	// 阶段3: 导入
	ImportStatus   string `json:"import_status"`
	ImportProgress int    `json:"import_progress"`
	ImportDone     int64  `json:"import_done"`
	ImportTotal    int64  `json:"import_total"`
	ImportError    string `json:"import_error,omitempty"`

	mu        sync.Mutex
	cosSvc    *COSService
	filterMgr *CSVFilterTaskManager
}

func (t *PipelineTask) lock()   { t.mu.Lock() }
func (t *PipelineTask) unlock() { t.mu.Unlock() }

// getSnapshot 获取线程安全的快照（复制所有字段、不包含互斥锁）
func (t *PipelineTask) getSnapshot() PipelineTask {
	t.lock()
	defer t.unlock()
	return PipelineTask{
		ID: t.ID, Status: t.Status, Progress: t.Progress, Error: t.Error,
		StartAt: t.StartAt, UpdatedAt: t.UpdatedAt, Elapsed: t.Elapsed,
		COSKeys: t.COSKeys, TIDs: t.TIDs, VINs: t.VINs, PlateNos: t.PlateNos, CSVPath: t.CSVPath,
		Downloads: t.Downloads, DownloadProgress: t.DownloadProgress,
		FilterStatus: t.FilterStatus, FilterProgress: t.FilterProgress,
		FilterTaskID: t.FilterTaskID, FilterKeptLines: t.FilterKeptLines, FilterRawLines: t.FilterRawLines,
		ImportStatus: t.ImportStatus, ImportProgress: t.ImportProgress,
		ImportDone: t.ImportDone, ImportTotal: t.ImportTotal, ImportError: t.ImportError,
	}
}

func (t *PipelineTask) setStage(stage PipelineStage) {
	t.lock()
	t.Status = stage
	t.UpdatedAt = time.Now().Unix()
	t.updateElapsed()
	t.unlock()
	if store := GetTaskStore(); store != nil {
		store.MarkDirty()
	}
}

func (t *PipelineTask) setError(err string) {
	t.lock()
	t.Status = StageFailed
	t.Error = err
	t.UpdatedAt = time.Now().Unix()
	t.updateElapsed()
	t.unlock()
	if store := GetTaskStore(); store != nil {
		store.MarkDirty()
	}
}

func (t *PipelineTask) updateElapsed() {
	if t.StartAt > 0 {
		elapsed := time.Since(time.Unix(t.StartAt, 0))
		t.Elapsed = fmt.Sprintf("%.0fs", elapsed.Seconds())
		if elapsed > time.Minute {
			t.Elapsed = fmt.Sprintf("%.0fmin%.0fs", elapsed.Minutes(), float64(int64(elapsed.Seconds())%60))
		}
	}
}

// recalcProgress 重新计算整体进度
func (t *PipelineTask) recalcProgress() {
	switch t.Status {
	case StagePending:
		t.Progress = 0
	case StageDownload:
		// 下载占 0~50%
		t.Progress = t.DownloadProgress * 50 / 100
	case StageFilter:
		// 过滤占 50~80%
		t.Progress = 50 + t.FilterProgress*30/100
	case StageImport:
		// 导入占 80~100%
		t.Progress = 80 + t.ImportProgress*20/100
	case StageCompleted:
		t.Progress = 100
	case StageFailed:
		if t.Progress < 0 {
			t.Progress = 0
		}
	}
	t.UpdatedAt = time.Now().Unix()
	t.updateElapsed()
	if store := GetTaskStore(); store != nil {
		store.MarkDirty()
	}
}

// ========== 管道任务管理器 ==========

// PipelineTaskManager 管理所有管道任务
type PipelineTaskManager struct {
	mu    sync.RWMutex
	tasks map[string]*PipelineTask
}

// NewPipelineTaskManager 创建管理器
func NewPipelineTaskManager() *PipelineTaskManager {
	return &PipelineTaskManager{
		tasks: make(map[string]*PipelineTask),
	}
}

// Create 创建管道任务并返回
func (pm *PipelineTaskManager) Create(req *PipelineCreateRequest) *PipelineTask {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	now := time.Now()
	task := &PipelineTask{
		ID:        uuid.New().String(),
		Status:    StagePending,
		StartAt:   now.Unix(),
		UpdatedAt: now.Unix(),
		COSKeys:   req.COSKeys,
		TIDs:      req.TIDs,
		VINs:      req.VINs,
		PlateNos:  req.PlateNos,
		CSVPath:   req.CSVPath,
	}

	// 初始化下载文件列表
	for _, key := range req.COSKeys {
		task.Downloads = append(task.Downloads, FileDownloadInfo{
			COSKey:   key,
			FileName: filepath.Base(key),
			Progress: 0,
			Message:  "等待下载",
		})
	}

	pm.tasks[task.ID] = task

	// 持久化
	if store := GetTaskStore(); store != nil {
		store.MarkDirty()
	}

	return task
}

// Get 查询单个管道任务
func (pm *PipelineTaskManager) Get(id string) (*PipelineTask, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	t, ok := pm.tasks[id]
	if !ok {
		return nil, false
	}
	// 同步导入阶段的进度（从 CSVFilterTask 读取）
	t.syncImportProgress()
	return t, true
}

// List 列出所有管道任务（按创建时间降序）
func (pm *PipelineTaskManager) List() []PipelineTask {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	res := make([]PipelineTask, 0, len(pm.tasks))
	for _, t := range pm.tasks {
		t.syncImportProgress()
		res = append(res, t.getSnapshot())
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].StartAt > res[j].StartAt
	})
	return res
}

// syncImportProgress 从 CSVFilterTask 同步导入进度
func (t *PipelineTask) syncImportProgress() {
	t.lock()
	defer t.unlock()

	if t.FilterTaskID == "" || t.Status != StageFilter && t.Status != StageImport {
		return
	}
	filterMgr := t.filterMgr
	if filterMgr == nil {
		return
	}
	ft, ok := filterMgr.Get(t.FilterTaskID)
	if !ok {
		return
	}
	snap := ft.Snapshot()

	t.FilterStatus = string(snap.Status)
	t.FilterKeptLines = snap.KeptLines
	t.FilterRawLines = snap.RawLines

	if snap.Status == CSVStatusDone {
		t.FilterProgress = 100
	} else if snap.Pct > 0 {
		t.FilterProgress = snap.Pct
	}

	// 同步导入进度
	if snap.ImportStatus != "" {
		t.ImportStatus = string(snap.ImportStatus)
		t.ImportProgress = snap.ImportProgress
		t.ImportDone = snap.ImportDone
		t.ImportTotal = snap.ImportTotal
		if snap.ImportError != "" {
			t.ImportError = snap.ImportError
		}

		if snap.ImportStatus == CSVImportDone {
			t.Status = StageCompleted
			t.Progress = 100
		} else if snap.ImportStatus == CSVImportRunning {
			t.Status = StageImport
		} else if snap.ImportStatus == CSVImportFailed {
			t.ImportError = snap.ImportError
		}
	}

	t.recalcProgress()
}

// ========== 管道请求 ==========

// PipelineCreateRequest 创建管道请求
type PipelineCreateRequest struct {
	COSKeys  []string `json:"cos_keys"`
	TIDs     []string `json:"tids"`
	VINs     []string `json:"vins"`
	PlateNos []string `json:"plate_nos"`
	CSVPath  string   `json:"csv_path"`
}

// ========== 管道执行 ==========

// RunPipeline 在后台执行完整的管道任务
func (pm *PipelineTaskManager) RunPipeline(
	task *PipelineTask,
	cosService *COSService,
	filterMgr *CSVFilterTaskManager,
) {
	task.cosSvc = cosService
	task.filterMgr = filterMgr

	// 标记开始
	task.setStage(StageDownload)
	log.Printf("[管道 %s] 开始执行: %d 个COS文件, %d 个TID", task.ID, len(task.COSKeys), len(task.TIDs))

	// ======== 阶段1: 下载 ========
	downloadedPaths, err := pm.runDownloadStage(task, cosService)
	if err != nil {
		task.setError(fmt.Sprintf("下载阶段失败: %v", err))
		log.Printf("[管道 %s] 下载阶段失败: %v", task.ID, err)
		return
	}
	log.Printf("[管道 %s] 下载完成: %d 个文件", task.ID, len(downloadedPaths))

	// ======== 阶段2: 过滤 + 导入 ========
	pm.runFilterAndImportStage(task, filterMgr, downloadedPaths)
	log.Printf("[管道 %s] 管道执行完毕", task.ID)

	// 清理下载的 tar.gz 文件
	pm.cleanupDownloadedFiles(task, downloadedPaths)
}

// cleanupDownloadedFiles 清理管道下载的 tar.gz 文件
func (pm *PipelineTaskManager) cleanupDownloadedFiles(task *PipelineTask, paths []string) {
	for _, path := range paths {
		if err := os.Remove(path); err != nil {
			log.Printf("[管道 %s] 清理下载文件失败: %s (%v)", task.ID, path, err)
		} else {
			log.Printf("[管道 %s] 已清理下载文件: %s", task.ID, path)
		}
	}
}

// runDownloadStage 执行下载阶段
func (pm *PipelineTaskManager) runDownloadStage(task *PipelineTask, cosService *COSService) ([]string, error) {
	cfg := config.Get()
	downloadDir := filepath.Join(cfg.WorkDir, "downloads")

	var downloadedPaths []string

	for i, key := range task.COSKeys {
		// 更新当前文件状态
		pm.updateDownloadItem(task.ID, i, func(d *FileDownloadInfo) {
			d.Message = "下载中..."
			d.Progress = 0
		})

		localName := filepath.Base(key)
		localPath := filepath.Join(downloadDir, localName)

		// 检查文件是否已存在
		if _, err := os.Stat(localPath); err == nil {
			// 已存在，跳过
			downloadedPaths = append(downloadedPaths, localPath)
			pm.updateDownloadItem(task.ID, i, func(d *FileDownloadInfo) {
				d.Progress = 100
				d.Message = "已存在(跳过)"
				d.Done = true
				d.LocalPath = localPath
			})
			log.Printf("[管道 %s] 文件已存在, 跳过: %s", task.ID, localName)
			continue
		}

		// 下载文件
		err := cosService.DownloadFileWithProgress(key, localPath, func(downloaded, total int64) {
			pct := 0
			if total > 0 {
				pct = int(downloaded * 100 / total)
			}
			if pct < 0 {
				pct = 0
			}
			if pct > 100 {
				pct = 100
			}
			pm.updateDownloadItem(task.ID, i, func(d *FileDownloadInfo) {
				d.Progress = pct
				d.Message = fmt.Sprintf("%d%% (%d/%d MB)", pct, downloaded/1048576, total/1048576)
			})
			// 更新整体下载进度
			pm.updateTaskProgress(task.ID, func(t *PipelineTask) {
				totalPct := 0
				for j, dd := range t.Downloads {
					if j == i {
						totalPct += pct
					} else if dd.Done {
						totalPct += 100
					}
				}
				t.DownloadProgress = totalPct / len(t.COSKeys)
				t.recalcProgress()
			})
		})

		if err != nil {
			pm.updateDownloadItem(task.ID, i, func(d *FileDownloadInfo) {
				d.Error = err.Error()
				d.Done = true
			})
			return downloadedPaths, fmt.Errorf("下载 %s 失败: %w", localName, err)
		}

		downloadedPaths = append(downloadedPaths, localPath)
		pm.updateDownloadItem(task.ID, i, func(d *FileDownloadInfo) {
			d.Progress = 100
			d.Message = "下载完成"
			d.Done = true
			d.LocalPath = localPath
		})
		log.Printf("[管道 %s] 下载完成: %s", task.ID, localName)
	}

	// 最终下载进度
	pm.updateTaskProgress(task.ID, func(t *PipelineTask) {
		t.DownloadProgress = 100
		t.recalcProgress()
	})

	return downloadedPaths, nil
}

// runFilterAndImportStage 执行过滤+导入阶段
func (pm *PipelineTaskManager) runFilterAndImportStage(
	task *PipelineTask,
	filterMgr *CSVFilterTaskManager,
	tarPaths []string,
) {
	task.setStage(StageFilter)
	log.Printf("[管道 %s] 开始过滤: %d 个文件", task.ID, len(tarPaths))

	// 读取 CSV 获取绑定段
	segments, err := ReadCSV(task.CSVPath)
	if err != nil {
		task.setError(fmt.Sprintf("读取CSV失败: %v", err))
		return
	}
	log.Printf("[管道 %s] CSV解析成功: %d 个TID", task.ID, len(segments))

	// 为每个 tar 文件创建过滤任务并串行执行
	groupCancel := make(chan struct{})
	for _, tarPath := range tarPaths {
		select {
		case <-groupCancel:
			task.setError("管道已取消")
			return
		default:
		}

		ft, err := filterMgr.Submit(tarPath, task.CSVPath, "", false, groupCancel)
		if err != nil {
			task.setError(fmt.Sprintf("提交过滤任务失败: %v", err))
			return
		}

		// 记录过滤任务ID
		task.lock()
		task.FilterTaskID = ft.ID
		task.unlock()

		// 检查是否需要续传
		var prog *CSVProgressFile
		if p, ok := LoadCSVProgress(tarPath, task.CSVPath, ""); ok {
			prog = p
			log.Printf("[管道 %s] 续传过滤: %s (已处理 %d 行)", task.ID, tarPath, prog.LinesDone)
		}

		// 执行过滤（同步阻塞）
		log.Printf("[管道 %s] 开始过滤文件: %s", task.ID, filepath.Base(tarPath))
		filterMgr.RunTask(ft, segments, prog)

		// 同步过滤状态
		snap := ft.Snapshot()
		task.lock()
		task.FilterKeptLines = snap.KeptLines
		task.FilterRawLines = snap.RawLines
		task.FilterStatus = string(snap.Status)
		task.FilterProgress = 100
		task.ImportStatus = string(snap.ImportStatus)
		task.ImportProgress = snap.ImportProgress
		task.ImportDone = snap.ImportDone
		task.ImportTotal = snap.ImportTotal
		task.recalcProgress()
		task.unlock()

		if snap.Status == CSVStatusFailed {
			task.setError(fmt.Sprintf("过滤失败: %s", snap.Error))
			return
		}

		// 过滤成功，将过滤后的 SQL 文件导入临时 MySQL 数据库
		log.Printf("[管道 %s] 过滤完成，开始导入MySQL: task=%s, output=%s", task.ID, ft.ID, ft.OutputPath)
		ImportSQLToTempDBWithTask(ft, ft.OutputPath)

		// 如果导入还在进行中，轮询等待完成
		if snap.ImportStatus == CSVImportRunning {
			task.setStage(StageImport)
			log.Printf("[管道 %s] 导入进行中, 等待完成...", task.ID)
			waitForImportDone(filterMgr, ft.ID, task)
		}

		// 最终状态检查
		task.lock()
		snap2 := ft.Snapshot()
		task.ImportStatus = string(snap2.ImportStatus)
		task.ImportProgress = snap2.ImportProgress
		task.ImportDone = snap2.ImportDone
		task.ImportTotal = snap2.ImportTotal
		if snap2.ImportError != "" {
			task.ImportError = snap2.ImportError
		}
		task.unlock()

		if snap2.ImportStatus == CSVImportFailed {
			task.setError(fmt.Sprintf("导入失败: %s", snap2.ImportError))
			return
		}
	}

	task.setStage(StageCompleted)
	log.Printf("[管道 %s] 全部完成!", task.ID)
}

// waitForImportDone 轮询等待导入完成
func waitForImportDone(filterMgr *CSVFilterTaskManager, filterTaskID string, task *PipelineTask) {
	for {
		time.Sleep(2 * time.Second)
		ft, ok := filterMgr.Get(filterTaskID)
		if !ok {
			return
		}
		snap := ft.Snapshot()

		task.lock()
		task.ImportStatus = string(snap.ImportStatus)
		task.ImportProgress = snap.ImportProgress
		task.ImportDone = snap.ImportDone
		task.ImportTotal = snap.ImportTotal
		task.recalcProgress()
		task.unlock()

		if snap.ImportStatus == CSVImportDone || snap.ImportStatus == CSVImportFailed {
			return
		}
		if snap.Status == CSVStatusFailed {
			return
		}
	}
}

// ========== 辅助方法 ==========

func (pm *PipelineTaskManager) updateDownloadItem(taskID string, idx int, fn func(d *FileDownloadInfo)) {
	pm.mu.RLock()
	t, ok := pm.tasks[taskID]
	pm.mu.RUnlock()
	if !ok {
		return
	}
	t.lock()
	if idx >= 0 && idx < len(t.Downloads) {
		fn(&t.Downloads[idx])
	}
	t.unlock()
	if store := GetTaskStore(); store != nil {
		store.MarkDirty()
	}
}

func (pm *PipelineTaskManager) updateTaskProgress(taskID string, fn func(t *PipelineTask)) {
	pm.mu.RLock()
	t, ok := pm.tasks[taskID]
	pm.mu.RUnlock()
	if !ok {
		return
	}
	t.lock()
	fn(t)
	t.unlock()
	if store := GetTaskStore(); store != nil {
		store.MarkDirty()
	}
}

// 确保 os 包被使用
var _ = os.Stat
