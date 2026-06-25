package services

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"gps-archive-tool/internal/config"
	"gps-archive-tool/internal/database"
	"gps-archive-tool/internal/models"
)

// ArchiveService 归档文件处理服务
type ArchiveService struct {
	taskManager *TaskManager
}

// NewArchiveService 创建归档服务
func NewArchiveService(taskManager *TaskManager) *ArchiveService {
	return &ArchiveService{
		taskManager: taskManager,
	}
}

// StartFilterTask 启动过滤任务
func (as *ArchiveService) StartFilterTask(ctx context.Context, taskID string, req FilterRequest) {
	as.runFilterTask(ctx, taskID, req)
}

// FilterRequest 过滤请求（内部使用）
type FilterRequest struct {
	TIDs        []string
	StartTime   string
	EndTime     string
	ArchiveDir  string
	ArchiveFile string
	OutputDir   string
	WorkerCount int
}

// ToTaskReq 转换为FilterTaskRequest
func (r FilterRequest) ToTaskReq() models.FilterTaskRequest {
	return models.FilterTaskRequest{
		TIDs:        r.TIDs,
		StartTime:   r.StartTime,
		EndTime:     r.EndTime,
		ArchiveDir:  r.ArchiveDir,
		ArchiveFile: r.ArchiveFile,
		OutputDir:   r.OutputDir,
		WorkerCount: r.WorkerCount,
	}
}

// runFilterTask 执行过滤任务
func (as *ArchiveService) runFilterTask(ctx context.Context, taskID string, req FilterRequest) {
	cfg := config.Get()

	// 创建TID集合用于快速查找
	tidSet := make(map[string]bool)
	for _, t := range req.TIDs {
		tidSet[t] = true
	}

	// 步骤1: 查找归档文件
	as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
		t.Status = "running"
		t.Status = "查找归档文件中..."
	})

	files, err := as.findArchiveFiles(req.ArchiveDir, req.ArchiveFile)
	if err != nil {
		as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
			t.Status = "failed"
			t.Error = fmt.Sprintf("查找归档文件失败: %v", err)
		})
		return
	}

	if len(files) == 0 {
		as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
			t.Status = "failed"
			t.Error = "未找到归档文件"
		})
		return
	}

	as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
		t.TotalFiles = len(files)
	})

	// 步骤2: 连接临时数据库
	tempDB, err := database.Connect(cfg.TempDB)
	if err != nil {
		as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
			t.Status = "failed"
			t.Error = fmt.Sprintf("连接临时数据库失败: %v", err)
		})
		return
	}
	defer tempDB.Close()

	// 确保目标表存在
	err = database.EnsureTempTable(tempDB, "gps_archive_data", as.getTableSchema())
	if err != nil {
		as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
			t.Status = "failed"
			t.Error = fmt.Sprintf("创建临时表失败: %v", err)
		})
		return
	}

	// 步骤3: 解析归档文件并过滤导入临时数据库
	as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
		t.Status = "正在过滤并导入临时数据库..."
	})

	workerCount := req.WorkerCount
	if workerCount <= 0 {
		workerCount = 4
	}

	totalFiltered, err := as.processArchiveFiles(ctx, taskID, files, tidSet, req.StartTime, req.EndTime, tempDB, workerCount)
	if err != nil {
		as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
			t.Status = "failed"
			t.Error = fmt.Sprintf("处理归档文件失败: %v", err)
		})
		return
	}

	as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
		t.FilteredRecords = totalFiltered
	})

	// 步骤4: 从临时数据库导出到SQL文件
	as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
		t.Status = "正在导出到SQL文件..."
	})

	err = as.exportToSQLFiles(ctx, taskID, tempDB, req.TIDs, req.OutputDir)
	if err != nil {
		as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
			t.Status = "failed"
			t.Error = fmt.Sprintf("导出SQL文件失败: %v", err)
		})
		return
	}

	// 完成
	as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
		t.Status = "completed"
		t.Progress = 100
	})
}

// findArchiveFiles 查找归档文件
func (as *ArchiveService) findArchiveFiles(dir, specificFile string) ([]string, error) {
	if specificFile != "" {
		// 处理单个文件
		if strings.HasPrefix(specificFile, "/") || strings.HasPrefix(specificFile, "\\") || (len(specificFile) > 1 && specificFile[1] == ':') {
			// 绝对路径
			if _, err := os.Stat(specificFile); err == nil {
				return []string{specificFile}, nil
			}
			return nil, fmt.Errorf("文件不存在: %s", specificFile)
		}
		// 相对路径
		fullPath := filepath.Join(dir, specificFile)
		if _, err := os.Stat(fullPath); err == nil {
			return []string{fullPath}, nil
		}
		return nil, fmt.Errorf("文件不存在: %s", fullPath)
	}

	// 查找目录下所有.sql文件
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (strings.HasSuffix(info.Name(), ".sql") || strings.HasSuffix(info.Name(), ".txt") || strings.HasSuffix(info.Name(), ".csv")) {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// processArchiveFiles 处理归档文件（多worker并行）
func (as *ArchiveService) processArchiveFiles(
	ctx context.Context,
	taskID string,
	files []string,
	tidSet map[string]bool,
	startTime, endTime string,
	tempDB *sqlx.DB,
	workerCount int,
) (int64, error) {

	var totalFiltered int64
	var mu sync.Mutex
	fileChan := make(chan string, len(files))
	errChan := make(chan error, workerCount)
	var wg sync.WaitGroup

	// 启动worker
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for filePath := range fileChan {
				select {
				case <-ctx.Done():
					return
				default:
				}

				as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
					t.CurrentFile = filepath.Base(filePath)
				})

				filtered, err := as.processSingleFile(filePath, tidSet, startTime, endTime, tempDB)
				if err != nil {
					errChan <- fmt.Errorf("处理文件 %s 失败: %w", filePath, err)
					return
				}

				mu.Lock()
				totalFiltered += filtered
				processed := 0
				as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
					t.ProcessedFiles++
					t.FilteredRecords = totalFiltered
					if t.TotalFiles > 0 {
						t.Progress = float64(t.ProcessedFiles) / float64(t.TotalFiles) * 50 // 前50%进度用于导入
					}
				})
				_ = processed
				mu.Unlock()
			}
		}(i)
	}

	// 发送文件路径到channel
	go func() {
		for _, f := range files {
			fileChan <- f
		}
		close(fileChan)
	}()

	wg.Wait()
	close(errChan)

	// 检查错误
	for err := range errChan {
		if err != nil {
			return totalFiltered, err
		}
	}

	return totalFiltered, nil
}

// processSingleFile 处理单个归档文件
func (as *ArchiveService) processSingleFile(
	filePath string,
	tidSet map[string]bool,
	startTime, endTime string,
	tempDB *sqlx.DB,
) (int64, error) {

	file, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("打开文件失败: %w", err)
	}
	defer file.Close()

	// 编译正则表达式匹配INSERT语句
	// 匹配: INSERT INTO `table` (`col1`, `col2`, ...) VALUES (val1, val2, ...), (val1, val2, ...), ...
	insertRe := regexp.MustCompile(`(?is)^\s*INSERT\s+INTO\s+`)

	scanner := bufio.NewScanner(file)
	// 增大缓冲区以处理长行
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	var filteredCount int64
	var batchRows [][]interface{}
	var currentColumns []string
	var currentTable string
	maxBatchSize := 500

	for scanner.Scan() {
		line := scanner.Text()

		// 跳过注释和空行
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if insertRe.MatchString(trimmed) {
			// 解析INSERT语句
			tableName, columns, rows := parseInsertStatement(trimmed)
			if tableName == "" || len(rows) == 0 {
				continue
			}

			currentTable = tableName
			currentColumns = columns

			for _, row := range rows {
				// 检查TID是否在目标集合中
				tidValue := extractTIDFromRow(row, columns)
				if tidValue == "" || !tidSet[tidValue] {
					continue
				}

				// 检查时间范围
				if startTime != "" && endTime != "" {
					timeValue := extractTimeFromRow(row, columns)
					if timeValue != "" && !isTimeInRange(timeValue, startTime, endTime) {
						continue
					}
				}

				batchRows = append(batchRows, row)
				filteredCount++

				// 批量插入
				if len(batchRows) >= maxBatchSize {
					err := database.BatchInsert(tempDB, currentTable, currentColumns, batchRows)
					if err != nil {
						return filteredCount, fmt.Errorf("批量插入失败: %w", err)
					}
					batchRows = batchRows[:0]
				}
			}
		}
	}

	// 插入剩余数据
	if len(batchRows) > 0 {
		err := database.BatchInsert(tempDB, currentTable, currentColumns, batchRows)
		if err != nil {
			return filteredCount, fmt.Errorf("批量插入剩余数据失败: %w", err)
		}
	}

	if err := scanner.Err(); err != nil {
		return filteredCount, fmt.Errorf("读取文件失败: %w", err)
	}

	return filteredCount, nil
}

// parseInsertStatement 解析INSERT语句
func parseInsertStatement(line string) (string, []string, [][]interface{}) {
	// 匹配: INSERT INTO `table` (`col1`, `col2`, ...) VALUES ...
	re := regexp.MustCompile(`(?is)^\s*INSERT\s+INTO\s+` + "`?\\w+`?" + `\s*\(([^)]+)\)\s*VALUES\s*(.*)`)
	matches := re.FindStringSubmatch(line)
	if len(matches) < 3 {
		// 尝试不带列名的格式: INSERT INTO `table` VALUES ...
		re2 := regexp.MustCompile(`(?is)^\s*INSERT\s+INTO\s+` + "`?(\\w+)`?" + `\s*VALUES\s*(.*)`)
		matches2 := re2.FindStringSubmatch(line)
		if len(matches2) < 3 {
			return "", nil, nil
		}
		// 没有列名信息，跳过（需要列名来匹配TID）
		return "", nil, nil
	}

	tableName := extractTableName(line)
	columns := parseColumns(matches[1])
	valuesPart := matches[len(matches)-1]

	rows := parseValues(valuesPart, len(columns))
	return tableName, columns, rows
}

// extractTableName 提取表名
func extractTableName(line string) string {
	re := regexp.MustCompile(`(?is)INSERT\s+INTO\s+` + "`?(\\w+)`?")
	matches := re.FindStringSubmatch(line)
	if len(matches) > 1 {
		return matches[1]
	}
	return "gps_archive_data"
}

// parseColumns 解析列名
func parseColumns(colStr string) []string {
	cols := strings.Split(colStr, ",")
	result := make([]string, 0, len(cols))
	for _, c := range cols {
		c = strings.TrimSpace(c)
		c = strings.Trim(c, "`\"' ")
		if c != "" {
			result = append(result, c)
		}
	}
	return result
}

// parseValues 解析VALUES部分
func parseValues(valuesPart string, colCount int) [][]interface{} {
	var rows [][]interface{}

	// 处理多行VALUES
	// 匹配括号内的内容，处理嵌套括号
	depth := 0
	current := strings.Builder{}
	inStr := false
	strChar := byte(0)

	for i := 0; i < len(valuesPart); i++ {
		ch := valuesPart[i]

		if inStr {
			current.WriteByte(ch)
			if ch == '\\' && i+1 < len(valuesPart) {
				i++
				current.WriteByte(valuesPart[i])
			} else if ch == strChar {
				inStr = false
			}
			continue
		}

		if ch == '\'' || ch == '"' {
			inStr = true
			strChar = ch
			current.WriteByte(ch)
			continue
		}

		if ch == '(' {
			if depth > 0 {
				current.WriteByte(ch)
			}
			depth++
		} else if ch == ')' {
			depth--
			if depth == 0 {
				// 完成一个row
				rowStr := current.String()
				current.Reset()
				row := parseValueRow(rowStr, colCount)
				if len(row) > 0 {
					rows = append(rows, row)
				}
			} else {
				current.WriteByte(ch)
			}
		} else if depth > 0 {
			current.WriteByte(ch)
		}
	}

	return rows
}

// parseValueRow 解析单行值
func parseValueRow(rowStr string, colCount int) []interface{} {
	if rowStr == "" {
		return nil
	}

	var values []interface{}
	current := strings.Builder{}
	inStr := false
	strChar := byte(0)
	fieldCount := 0

	for i := 0; i < len(rowStr); i++ {
		ch := rowStr[i]

		if inStr {
			current.WriteByte(ch)
			if ch == '\\' && i+1 < len(rowStr) {
				i++
				current.WriteByte(rowStr[i])
			} else if ch == strChar {
				inStr = false
			}
			continue
		}

		if ch == '\'' {
			inStr = true
			strChar = ch
			continue
		}

		if ch == ',' {
			val := strings.TrimSpace(current.String())
			values = append(values, val)
			current.Reset()
			fieldCount++
			continue
		}

		current.WriteByte(ch)
	}

	// 最后一个值
	val := strings.TrimSpace(current.String())
	values = append(values, val)
	fieldCount++

	return values
}

// extractTIDFromRow 从行数据中提取TID值
func extractTIDFromRow(row []interface{}, columns []string) string {
	tidColNames := []string{"tid", "device_id", "deviceid", "terminal_id", "terminalid"}
	for _, colName := range tidColNames {
		for i, col := range columns {
			if strings.EqualFold(col, colName) {
				if i < len(row) {
					if s, ok := row[i].(string); ok {
						return s
					}
				}
			}
		}
	}
	return ""
}

// extractTimeFromRow 从行数据中提取时间值
func extractTimeFromRow(row []interface{}, columns []string) string {
	timeColNames := []string{"gps_time", "gpstime", "create_time", "createtime", "upload_time", "uploadtime", "record_time", "recordtime", "dt"}
	for _, colName := range timeColNames {
		for i, col := range columns {
			if strings.EqualFold(col, colName) {
				if i < len(row) {
					if s, ok := row[i].(string); ok {
						return strings.Trim(s, "'\"")
					}
				}
			}
		}
	}
	return ""
}

// isTimeInRange 检查时间是否在范围内
func isTimeInRange(timeStr, startTime, endTime string) bool {
	// 尝试多种时间格式
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05.000",
		"2006-01-02T15:04:05",
		"2006/01/02 15:04:05",
		"2006-01-02",
	}

	var t time.Time
	var err error
	for _, format := range formats {
		t, err = time.Parse(format, timeStr)
		if err == nil {
			break
		}
	}
	if err != nil {
		return true // 无法解析时间，不过滤
	}

	start, err1 := time.Parse("2006-01-02 15:04:05", startTime)
	end, err2 := time.Parse("2006-01-02 15:04:05", endTime)
	if err1 != nil || err2 != nil {
		return true
	}

	return (t.Equal(start) || t.After(start)) && (t.Equal(end) || t.Before(end))
}

// getTableSchema 获取临时表结构
func (as *ArchiveService) getTableSchema() string {
	return `CREATE TABLE IF NOT EXISTS gps_archive_data (
		id BIGINT AUTO_INCREMENT PRIMARY KEY,
		tid VARCHAR(64) NOT NULL,
		gps_time DATETIME,
		longitude DECIMAL(10,6),
		latitude DECIMAL(10,6),
		speed DECIMAL(6,2),
		direction INT,
		status VARCHAR(255),
		create_time DATETIME DEFAULT CURRENT_TIMESTAMP,
		INDEX idx_tid (tid),
		INDEX idx_gps_time (gps_time),
		INDEX idx_tid_time (tid, gps_time)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`
}

// exportToSQLFiles 从临时数据库导出到SQL文件
func (as *ArchiveService) exportToSQLFiles(
	ctx context.Context,
	taskID string,
	tempDB *sqlx.DB,
	tids []string,
	outputDir string,
) error {

	// 确保输出目录存在
	err := os.MkdirAll(outputDir, 0755)
	if err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}

	totalTIDs := len(tids)
	for idx, tid := range tids {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 更新进度
		as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
			t.CurrentFile = fmt.Sprintf("导出TID=%s", tid)
			if totalTIDs > 0 {
				t.Progress = 50 + (float64(idx)/float64(totalTIDs))*50 // 后50%进度用于导出
			}
		})

		// 查询该TID的所有数据
		rows, err := tempDB.Queryx("SELECT * FROM gps_archive_data WHERE tid = ? ORDER BY gps_time ASC", tid)
		if err != nil {
			return fmt.Errorf("查询TID=%s数据失败: %w", tid, err)
		}

		// 获取列名
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return fmt.Errorf("获取列名失败: %w", err)
		}

		// 创建输出文件
		outputFile := filepath.Join(outputDir, fmt.Sprintf("%s.sql", tid))
		f, err := os.Create(outputFile)
		if err != nil {
			rows.Close()
			return fmt.Errorf("创建输出文件失败: %w", err)
		}

		// 写入文件头
		fmt.Fprintf(f, "-- GPS Archive Export - TID: %s\n", tid)
		fmt.Fprintf(f, "-- Export Time: %s\n", time.Now().Format("2006-01-02 15:04:05"))
		fmt.Fprintf(f, "-- Total Records: %s\n\n", "?")
		fmt.Fprintf(f, "USE `gps_archive_export`;\n\n")

		// 逐行读取并写入
		recordCount := 0
		batchSize := 500

		for rows.Next() {
			// 读取行数据
			values := make([]interface{}, len(columns))
			valuePtrs := make([]interface{}, len(columns))
			for i := range columns {
				valuePtrs[i] = &values[i]
			}

			err = rows.Scan(valuePtrs...)
			if err != nil {
				rows.Close()
				f.Close()
				return fmt.Errorf("读取数据行失败: %w", err)
			}

			// 构建INSERT语句
			if recordCount%batchSize == 0 {
				if recordCount > 0 {
					fmt.Fprintf(f, ";\n")
				}
				fmt.Fprintf(f, "INSERT INTO `gps_archive_data` (`%s`) VALUES\n", strings.Join(columns, "`, `"))
			} else {
				fmt.Fprintf(f, ",\n")
			}

			fmt.Fprintf(f, "(")
			for i, v := range values {
				if i > 0 {
					fmt.Fprintf(f, ", ")
				}
				if v == nil {
					fmt.Fprintf(f, "NULL")
				} else {
					switch val := v.(type) {
					case []byte:
						fmt.Fprintf(f, "'%s'", escapeSQLString(string(val)))
					case string:
						fmt.Fprintf(f, "'%s'", escapeSQLString(val))
					case time.Time:
						fmt.Fprintf(f, "'%s'", val.Format("2006-01-02 15:04:05"))
					default:
						fmt.Fprintf(f, "'%v'", v)
					}
				}
			}
			fmt.Fprintf(f, ")")
			recordCount++

			as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
				t.ExportedRecords++
			})
		}

		if recordCount > 0 {
			fmt.Fprintf(f, ";\n")
		}

		fmt.Fprintf(f, "\n-- Total Records: %d\n", recordCount)
		rows.Close()
		f.Close()

		_ = recordCount
	}

	return nil
}

// escapeSQLString 转义SQL字符串
func escapeSQLString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return s
}
