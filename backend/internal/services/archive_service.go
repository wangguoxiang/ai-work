package services

import (
	"bufio"
	"compress/gzip"
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
	tableName := as.getTableName()
	err = database.EnsureTempTable(tempDB, tableName, as.getTableSchema(tableName))
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
		// 没有列名信息，跳过（需要列名来匹配TID/时间）
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
	// 从配置获取默认表名
	cfg := config.Get()
	if cfg.TempDB.Table != "" {
		return cfg.TempDB.Table
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

// getTableName 获取临时表名（从配置读取）
func (as *ArchiveService) getTableName() string {
	cfg := config.Get()
	if cfg.TempDB.Table != "" {
		return cfg.TempDB.Table
	}
	return "gps_archive_data"
}

// getTableSchema 获取临时表结构
func (as *ArchiveService) getTableSchema(tableName string) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
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
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`, tableName)
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

	cfg := config.Get()
	as.taskManager.AddLog(taskID, "  📝 导出 %d 个TID的SQL文件到 %s", len(tids), outputDir)
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
		tableName := as.getTableName()
		rows, err := tempDB.Queryx(fmt.Sprintf("SELECT * FROM %s WHERE tid = ? ORDER BY gps_time ASC", tableName), tid)
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
		// 切换到目标数据库（从配置读取，而非硬编码）
		dbName := cfg.TempDB.DBName
		if dbName != "" {
			fmt.Fprintf(f, "USE `%s`;\n\n", dbName)
		}

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
				fmt.Fprintf(f, "INSERT INTO `%s` (`%s`) VALUES\n", tableName, strings.Join(columns, "`, `"))
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

// ========== COS Pipeline 新任务流程 ==========

// COSPipelineRequest COS管道任务请求
type COSPipelineRequest struct {
	TIDs        []string
	StartTime   string
	EndTime     string
	COSFiles    []string // COS中的文件路径列表
	WorkDir     string   // 本地工作目录
	WorkerCount int
	COSService  *COSService
}

// SaveTIDFile 将TID列表保存到文件
func SaveTIDFile(workDir string, tids []TIDWithPlate) (string, error) {
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return "", fmt.Errorf("创建工作目录失败: %w", err)
	}
	tidFilePath := filepath.Join(workDir, "tid_list.txt")
	f, err := os.Create(tidFilePath)
	if err != nil {
		return "", fmt.Errorf("创建TID文件失败: %w", err)
	}
	defer f.Close()
	for _, t := range tids {
		line := t.TID
		if t.VIN != "" {
			line += "," + t.VIN
		}
		if t.PlateNo != "" {
			line += "," + t.PlateNo
		}
		fmt.Fprintln(f, line)
	}
	return tidFilePath, nil
}

// TIDWithPlate TID信息（含车牌）
type TIDWithPlate struct {
	TID     string
	VIN     string
	PlateNo string
}

// StartCOSPipeline 启动COS管道任务：下载→过滤→导出SQL→导入MySQL
func (as *ArchiveService) StartCOSPipeline(ctx context.Context, taskID string, req COSPipelineRequest) {
	as.taskManager.AddLog(taskID, "🚀 任务启动，共 %d 个COS文件，%d 个TID", len(req.COSFiles), len(req.TIDs))
	as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
		t.Status = "running"
		t.Stage = "正在从COS下载文件..."
		t.TotalFiles = len(req.COSFiles)
	})

	cfg := config.Get()
	workDir := req.WorkDir
	if workDir == "" {
		workDir = cfg.WorkDir
	}
	downloadDir := filepath.Join(workDir, "downloads")
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		as.failTask(taskID, fmt.Sprintf("创建下载目录失败: %v", err))
		return
	}
	as.taskManager.AddLog(taskID, "📁 工作目录: %s", workDir)

	// === 步骤1: 下载COS文件到本地 ===
	as.taskManager.AddLog(taskID, "⬇️ 【步骤1/4】开始从COS下载 %d 个文件...", len(req.COSFiles))
	localFiles := make([]string, 0, len(req.COSFiles))
	for i, cosKey := range req.COSFiles {
		select {
		case <-ctx.Done():
			as.failTask(taskID, "任务已取消")
			return
		default:
		}

		localName := filepath.Base(cosKey)
		localPath := filepath.Join(downloadDir, localName)

		as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
			t.CurrentFile = fmt.Sprintf("下载: %s", localName)
			t.Stage = fmt.Sprintf("正在从COS下载文件 (%d/%d)", i+1, len(req.COSFiles))
		})

		as.taskManager.AddLog(taskID, "  ↓ 下载 [%d/%d] %s", i+1, len(req.COSFiles), localName)
		if err := req.COSService.DownloadFile(cosKey, localPath); err != nil {
			as.failTask(taskID, fmt.Sprintf("下载COS文件失败 %s: %v", cosKey, err))
			return
		}

		localFiles = append(localFiles, localPath)
		as.taskManager.AddLog(taskID, "  ✓ 完成: %s", localName)
		as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
			t.ProcessedFiles = i + 1
			t.Progress = float64(i+1) / float64(len(req.COSFiles)+2) * 30
		})
	}
	as.taskManager.AddLog(taskID, "✅ 【步骤1/4】全部 %d 个文件下载完成", len(localFiles))

	// === 步骤2: 过滤归档文件并导入临时数据库 ===
	as.taskManager.AddLog(taskID, "🔍 【步骤2/4】开始过滤数据并导入临时数据库...")
	as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
		t.Stage = "正在过滤数据并导入临时数据库..."
		t.CurrentFile = ""
		t.TotalFiles = len(localFiles)
		t.ProcessedFiles = 0
	})

	// 连接临时数据库
	as.taskManager.AddLog(taskID, "  🔗 连接临时数据库 %s@%s:%d/%s", cfg.TempDB.User, cfg.TempDB.Host, cfg.TempDB.Port, cfg.TempDB.DBName)
	tempDB, err := database.Connect(cfg.TempDB)
	if err != nil {
		as.failTask(taskID, fmt.Sprintf("连接临时数据库失败: %v", err))
		return
	}
	defer tempDB.Close()
	as.taskManager.AddLog(taskID, "  ✓ 数据库连接成功")

	tableName := as.getTableName()
	err = database.EnsureTempTable(tempDB, tableName, as.getTableSchema(tableName))
	if err != nil {
		as.failTask(taskID, fmt.Sprintf("创建临时表失败: %v", err))
		return
	}
	as.taskManager.AddLog(taskID, "  ✓ 临时表已就绪")

	// 构建TID集合
	tidSet := make(map[string]bool)
	for _, t := range req.TIDs {
		tidSet[t] = true
	}
	as.taskManager.AddLog(taskID, "  📋 TID集合: %d 个设备", len(tidSet))

	totalFiltered, err := as.processArchiveFilesGZip(ctx, taskID, localFiles, tidSet, req.StartTime, req.EndTime, tempDB, req.WorkerCount)
	if err != nil {
		as.failTask(taskID, fmt.Sprintf("处理归档文件失败: %v", err))
		return
	}

	as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
		t.FilteredRecords = totalFiltered
	})
	as.taskManager.AddLog(taskID, "✅ 【步骤2/4】过滤完成，共匹配 %d 条记录", totalFiltered)

	// === 步骤3: 导出到SQL文件 ===
	as.taskManager.AddLog(taskID, "📝 【步骤3/4】开始导出SQL文件...")
	as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
		t.Stage = "正在导出到SQL文件..."
	})

	outputDir := filepath.Join(workDir, "output")
	err = as.exportToSQLFiles(ctx, taskID, tempDB, req.TIDs, outputDir)
	if err != nil {
		as.failTask(taskID, fmt.Sprintf("导出SQL文件失败: %v", err))
		return
	}
	as.taskManager.AddLog(taskID, "✅ 【步骤3/4】SQL文件导出完成，输出目录: %s", outputDir)

	// === 步骤4: 导入SQL到MySQL（自动执行SQL文件） ===
	as.taskManager.AddLog(taskID, "💾 【步骤4/4】开始导入SQL到临时数据库...")
	as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
		t.Stage = "正在导入SQL到临时数据库..."
	})

	importCount := 0
	for idx, tid := range req.TIDs {
		select {
		case <-ctx.Done():
			as.failTask(taskID, "任务已取消")
			return
		default:
		}

		sqlFile := filepath.Join(outputDir, fmt.Sprintf("%s.sql", tid))
		if _, err := os.Stat(sqlFile); os.IsNotExist(err) {
			as.taskManager.AddLog(taskID, "  ⚠ TID=%s 无匹配数据，跳过", tid)
			continue
		}

		as.taskManager.AddLog(taskID, "  ↑ 导入 [%d/%d] %s.sql", importCount+1, len(req.TIDs), tid)
		as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
			t.CurrentFile = fmt.Sprintf("导入SQL: %s.sql", tid)
		})

		sqlContent, err := os.ReadFile(sqlFile)
		if err != nil {
			as.failTask(taskID, fmt.Sprintf("读取SQL文件失败 %s: %v", sqlFile, err))
			return
		}

		stmtCount := 0
		statements := strings.Split(string(sqlContent), ";\n")
		for _, stmt := range statements {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" || strings.HasPrefix(stmt, "--") {
				continue
			}
			if _, err := tempDB.Exec(stmt); err != nil {
				as.failTask(taskID, fmt.Sprintf("执行SQL失败(TID=%s): %v", tid, err))
				return
			}
			stmtCount++
		}
		importCount++
		as.taskManager.AddLog(taskID, "  ✓ %s.sql 导入完成 (%d 条语句)", tid, stmtCount)

		as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
			t.ExportedRecords++
			if len(req.TIDs) > 0 {
				t.Progress = 70 + (float64(idx+1)/float64(len(req.TIDs)))*30
			}
		})
	}

	// 完成
	as.taskManager.AddLog(taskID, "✅ 全部步骤完成！共导入 %d 个TID的数据到临时数据库", importCount)
	as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
		t.Status = "completed"
		t.Stage = "任务完成"
		t.Progress = 100
	})
}

// processArchiveFilesGZip 处理归档文件（支持.gz压缩文件）
func (as *ArchiveService) processArchiveFilesGZip(
	ctx context.Context,
	taskID string,
	files []string,
	tidSet map[string]bool,
	startTime, endTime string,
	tempDB *sqlx.DB,
	workerCount int,
) (int64, error) {
	as.taskManager.AddLog(taskID, "  📂 开始处理 %d 个归档文件，%d 个worker并行", len(files), workerCount)
	var totalFiltered int64
	var mu sync.Mutex
	fileChan := make(chan string, len(files))
	errChan := make(chan error, workerCount)
	var wg sync.WaitGroup

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func(id int) {
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

				as.taskManager.AddLog(taskID, "  🔄 worker#%d 处理: %s", id, filepath.Base(filePath))
				filtered, err := as.processSingleFileGZip(filePath, tidSet, startTime, endTime, tempDB)
				if err != nil {
					errChan <- fmt.Errorf("处理文件 %s 失败: %w", filePath, err)
					return
				}
				as.taskManager.AddLog(taskID, "  ✓ worker#%d 完成: %s (匹配 %d 条)", id, filepath.Base(filePath), filtered)

				mu.Lock()
				totalFiltered += filtered
				as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
					t.ProcessedFiles++
					t.FilteredRecords = totalFiltered
					if t.TotalFiles > 0 {
						t.Progress = 30 + (float64(t.ProcessedFiles)/float64(t.TotalFiles))*40
					}
				})
				mu.Unlock()
			}
		}(i)
	}

	go func() {
		for _, f := range files {
			fileChan <- f
		}
		close(fileChan)
	}()

	wg.Wait()
	close(errChan)

	for err := range errChan {
		if err != nil {
			return totalFiltered, err
		}
	}

	return totalFiltered, nil
}

// processSingleFileGZip 处理单个归档文件（支持.gz压缩）
func (as *ArchiveService) processSingleFileGZip(
	filePath string,
	tidSet map[string]bool,
	startTime, endTime string,
	tempDB *sqlx.DB,
) (int64, error) {
	var file *os.File
	var err error

	file, err = os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("打开文件失败: %w", err)
	}
	defer file.Close()

	var reader *bufio.Scanner
	isGzip := strings.HasSuffix(strings.ToLower(filePath), ".gz")

	if isGzip {
		gzReader, err := gzip.NewReader(file)
		if err != nil {
			return 0, fmt.Errorf("解压gzip失败: %w", err)
		}
		defer gzReader.Close()
		scanner := bufio.NewScanner(gzReader)
		scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)
		reader = scanner
	} else {
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)
		reader = scanner
	}

	insertRe := regexp.MustCompile(`(?is)^\s*INSERT\s+INTO\s+`)
	var filteredCount int64
	var batchRows [][]interface{}
	var currentColumns []string
	var currentTable string
	maxBatchSize := 500

	for reader.Scan() {
		line := reader.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if insertRe.MatchString(trimmed) {
			tableName, columns, rows := parseInsertStatement(trimmed)
			if tableName == "" || len(rows) == 0 {
				continue
			}
			currentTable = tableName
			currentColumns = columns

			for _, row := range rows {
				tidValue := extractTIDFromRow(row, columns)
				if tidValue == "" || !tidSet[tidValue] {
					continue
				}
				if startTime != "" && endTime != "" {
					timeValue := extractTimeFromRow(row, columns)
					if timeValue != "" && !isTimeInRange(timeValue, startTime, endTime) {
						continue
					}
				}
				batchRows = append(batchRows, row)
				filteredCount++
				if len(batchRows) >= maxBatchSize {
					if err := database.BatchInsert(tempDB, currentTable, currentColumns, batchRows); err != nil {
						return filteredCount, fmt.Errorf("批量插入失败: %w", err)
					}
					batchRows = batchRows[:0]
				}
			}
		}
	}

	if len(batchRows) > 0 {
		if err := database.BatchInsert(tempDB, currentTable, currentColumns, batchRows); err != nil {
			return filteredCount, fmt.Errorf("批量插入剩余数据失败: %w", err)
		}
	}

	if err := reader.Err(); err != nil {
		return filteredCount, fmt.Errorf("读取文件失败: %w", err)
	}

	return filteredCount, nil
}

func (as *ArchiveService) failTask(taskID string, errMsg string) {
	as.taskManager.AddLog(taskID, "❌ 任务失败: %s", errMsg)
	as.taskManager.UpdateTask(taskID, func(t *models.TaskStatus) {
		t.Status = "failed"
		t.Stage = "失败"
		t.Error = errMsg
	})
}
