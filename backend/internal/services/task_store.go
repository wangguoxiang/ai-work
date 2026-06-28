package services

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gps-archive-tool/internal/config"
	"gps-archive-tool/internal/models"
)

// TaskDB 持久化文件顶层结构
type TaskDB struct {
	Version   string               `json:"version"`
	UpdatedAt int64                `json:"updated_at"`
	Tasks     []*models.TaskStatus `json:"tasks,omitempty"`
	Pipelines []PipelineTask       `json:"pipelines,omitempty"`
}

// ========== 全局存储引用（用于在 manager 中触发保存） ==========

var (
	globalTaskStore     *TaskStore
	globalTaskStoreOnce sync.Once
)

// TaskStore 任务持久化存储
type TaskStore struct {
	taskManager     *TaskManager
	pipelineManager *PipelineTaskManager

	mu     sync.Mutex
	saveCh chan struct{}
	closed bool
}

// InitTaskStore 初始化全局任务存储
func InitTaskStore(tm *TaskManager, pm *PipelineTaskManager) *TaskStore {
	globalTaskStoreOnce.Do(func() {
		globalTaskStore = &TaskStore{
			taskManager:     tm,
			pipelineManager: pm,
			saveCh:          make(chan struct{}, 64),
		}
		go globalTaskStore.saveLoop()
	})
	return globalTaskStore
}

// GetTaskStore 获取全局任务存储实例
func GetTaskStore() *TaskStore {
	return globalTaskStore
}

// saveLoop 后台协程：去抖保存（500ms 内多次变更合并为一次写入）
func (s *TaskStore) saveLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var pending bool
	for {
		select {
		case <-s.saveCh:
			pending = true
		case <-ticker.C:
			if pending {
				pending = false
				s.flush()
			}
		}
	}
}

// flush 执行实际写入
func (s *TaskStore) flush() {
	s.mu.Lock()
	dbPath := config.Get().TaskDBFile
	s.mu.Unlock()

	if dbPath == "" {
		return
	}

	// 收集 TaskManager 的任务快照
	tasks := s.taskManager.ListTasks()

	// 收集 PipelineTaskManager 的任务快照
	pipelines := s.pipelineManager.List()

	db := TaskDB{
		Version:   "1",
		UpdatedAt: time.Now().Unix(),
		Tasks:     tasks,
		Pipelines: pipelines,
	}

	data, err := json.MarshalIndent(db, "", "  ")
	if err != nil {
		log.Printf("[TaskStore] 序列化失败: %v", err)
		return
	}

	// 确保目录存在
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[TaskStore] 创建目录失败: %v", err)
		return
	}

	// 原子写入：先写临时文件再重命名
	tmpPath := dbPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		log.Printf("[TaskStore] 写入临时文件失败: %v", err)
		return
	}
	if err := os.Rename(tmpPath, dbPath); err != nil {
		log.Printf("[TaskStore] 重命名文件失败: %v", err)
		return
	}
}

// MarkDirty 标记数据已变更，触发延迟保存
func (s *TaskStore) MarkDirty() {
	select {
	case s.saveCh <- struct{}{}:
	default:
		// 队列满时丢弃，避免阻塞
	}
}

// LoadAndRestore 从持久化文件加载任务，恢复到管理器中
func LoadAndRestore(tm *TaskManager, pm *PipelineTaskManager) {
	dbPath := config.Get().TaskDBFile
	if dbPath == "" {
		return
	}

	data, err := os.ReadFile(dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("[TaskStore] 持久化文件不存在，跳过加载: %s", dbPath)
			return
		}
		log.Printf("[TaskStore] 读取文件失败: %v", err)
		return
	}

	var db TaskDB
	if err := json.Unmarshal(data, &db); err != nil {
		log.Printf("[TaskStore] 解析失败: %v", err)
		return
	}

	log.Printf("[TaskStore] 加载任务: %d 个过滤任务, %d 个管道任务", len(db.Tasks), len(db.Pipelines))

	// 恢复 TaskManager 任务
	for _, t := range db.Tasks {
		if t == nil {
			continue
		}
		tm.mu.Lock()
		// 不覆盖已存在的任务
		if _, exists := tm.tasks[t.TaskID]; !exists {
			tm.tasks[t.TaskID] = t
		}
		tm.mu.Unlock()
	}

	// 恢复 PipelineTaskManager 任务
	for i := range db.Pipelines {
		task := &db.Pipelines[i]
		task.cosSvc = nil
		task.filterMgr = nil

		pm.mu.Lock()
		if _, exists := pm.tasks[task.ID]; !exists {
			pm.tasks[task.ID] = task
		}
		pm.mu.Unlock()
	}

	log.Printf("[TaskStore] 任务恢复完成")
}
