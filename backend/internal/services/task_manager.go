package services

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"gps-archive-tool/internal/models"
)

// TaskManager 任务管理器
type TaskManager struct {
	mu    sync.RWMutex
	tasks map[string]*models.TaskStatus
}

// NewTaskManager 创建任务管理器
func NewTaskManager() *TaskManager {
	return &TaskManager{
		tasks: make(map[string]*models.TaskStatus),
	}
}

// CreateTask 创建新任务
func (tm *TaskManager) CreateTask(req models.FilterTaskRequest) string {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	taskID := uuid.New().String()
	startAt := time.Now()

	task := &models.TaskStatus{
		TaskID:    taskID,
		Status:    "pending",
		Progress:  0,
		TIDs:      req.TIDs,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		StartAt:   startAt.Format("2006-01-02 15:04:05"),
	}

	tm.tasks[taskID] = task

	// 持久化
	if store := GetTaskStore(); store != nil {
		store.MarkDirty()
	}

	return taskID
}

// UpdateTask 更新任务状态
func (tm *TaskManager) UpdateTask(taskID string, update func(*models.TaskStatus)) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if task, ok := tm.tasks[taskID]; ok {
		update(task)
		// 计算耗时
		startAt, err := time.Parse("2006-01-02 15:04:05", task.StartAt)
		if err == nil {
			elapsed := time.Since(startAt)
			task.Elapsed = fmt.Sprintf("%.1f秒", elapsed.Seconds())
			if elapsed > time.Minute {
				task.Elapsed = fmt.Sprintf("%.0f分%.0f秒", elapsed.Minutes(), float64(int64(elapsed.Seconds())%60))
			}
		}
	}

	// 持久化
	if store := GetTaskStore(); store != nil {
		store.MarkDirty()
	}
}

// GetTask 获取任务状态
func (tm *TaskManager) GetTask(taskID string) (*models.TaskStatus, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	task, ok := tm.tasks[taskID]
	if !ok {
		return nil, false
	}
	return task, true
}

// ListTasks 列出所有任务
func (tm *TaskManager) ListTasks() []*models.TaskStatus {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	tasks := make([]*models.TaskStatus, 0, len(tm.tasks))
	for _, t := range tm.tasks {
		tasks = append(tasks, t)
	}
	return tasks
}

// AddLog 向任务添加日志
func (tm *TaskManager) AddLog(taskID, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	ts := time.Now().Format("15:04:05")
	entry := fmt.Sprintf("[%s] %s", ts, msg)

	tm.mu.Lock()
	defer tm.mu.Unlock()

	if task, ok := tm.tasks[taskID]; ok {
		task.Logs = append(task.Logs, entry)
	}

	// 持久化
	if store := GetTaskStore(); store != nil {
		store.MarkDirty()
	}
}

// DeleteTask 删除任务
func (tm *TaskManager) DeleteTask(taskID string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	delete(tm.tasks, taskID)

	// 持久化
	if store := GetTaskStore(); store != nil {
		store.MarkDirty()
	}
}
