package services

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"gps-archive-tool/internal/config"
	"gps-archive-tool/internal/database"
)

// ========== 导入状态 ==========

// CSVImportStatus 导入阶段状态
type CSVImportStatus string

const (
	CSVImportNone    CSVImportStatus = ""
	CSVImportPending CSVImportStatus = "pending"
	CSVImportRunning CSVImportStatus = "importing"
	CSVImportDone    CSVImportStatus = "done"
	CSVImportFailed  CSVImportStatus = "failed"
)

// ========== 导入进度回调 ==========

// ImportProgressFn 导入进度回调(total=总行数, done=已导入行数)
type ImportProgressFn func(total, done int64)

// ImportSQLToTempDB 将过滤后的 SQL 文件导入临时 MySQL 数据库
// 读取 SQL 文件中的 INSERT 语句，解析并批量插入到 gps_archive_data 表
func ImportSQLToTempDB(sqlPath string, progressFn ImportProgressFn) error {
	cfg := config.Get()

	// 1. 连接临时数据库
	db, err := database.Connect(cfg.TempDB)
	if err != nil {
		return fmt.Errorf("连接临时数据库失败: %w", err)
	}
	defer db.Close()

	// 2. 确保目标表存在
	tableName := cfg.TempDB.Table
	if tableName == "" {
		tableName = "gps_archive_data"
	}
	schema := getImportTableSchema(tableName)
	err = database.EnsureTempTable(db, tableName, schema)
	if err != nil {
		return fmt.Errorf("创建临时表失败: %w", err)
	}

	// 3. 读取 SQL 文件，逐行解析 INSERT 语句
	file, err := os.Open(sqlPath)
	if err != nil {
		return fmt.Errorf("打开SQL文件失败: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	// 匹配 INSERT 语句
	insertRe := &insertMatcher{}

	var (
		batchRows      [][]interface{}
		currentColumns []string
		currentTable   string
		maxBatchSize   = 500
		totalRows      int64
		importedRows   int64
		lineCount      int64
		lastReportTime = time.Now()
	)

	for scanner.Scan() {
		line := scanner.Text()
		lineCount++

		// 跳过注释和空行
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if insertRe.MatchString(trimmed) {
			_, columns, rows := parseInsertStatement(trimmed)
			if len(rows) == 0 {
				continue
			}

			// 始终使用配置的目标表名
			currentTable = cfg.TempDB.Table
			if currentTable == "" {
				currentTable = "gps_archive_data"
			}
			currentColumns = columns
			totalRows += int64(len(rows))

			for _, row := range rows {
				batchRows = append(batchRows, row)

				if len(batchRows) >= maxBatchSize {
					err := database.BatchInsert(db, currentTable, currentColumns, batchRows)
					if err != nil {
						return fmt.Errorf("批量插入失败(行 %d): %w", lineCount, err)
					}
					importedRows += int64(len(batchRows))
					batchRows = batchRows[:0]

					// 每 500ms 报告一次进度
					if time.Since(lastReportTime) > 500*time.Millisecond {
						if progressFn != nil {
							progressFn(totalRows, importedRows)
						}
						lastReportTime = time.Now()
					}
				}
			}
		}
	}

	// 插入剩余数据
	if len(batchRows) > 0 {
		err := database.BatchInsert(db, currentTable, currentColumns, batchRows)
		if err != nil {
			return fmt.Errorf("批量插入剩余数据失败: %w", err)
		}
		importedRows += int64(len(batchRows))
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取SQL文件失败: %w", err)
	}

	// 最终进度报告
	if progressFn != nil {
		progressFn(totalRows, importedRows)
	}

	log.Printf("[SQL导入] 完成: 文件=%s, 总行数=%d, 导入行数=%d", sqlPath, totalRows, importedRows)
	return nil
}

// ImportSQLToTempDBWithTask 将过滤后的 SQL 文件导入临时 MySQL，并更新 CSVFilterTask 的导入进度
func ImportSQLToTempDBWithTask(task *CSVFilterTask, sqlPath string) {
	// 已导入完成则跳过
	if task.ImportStatus == CSVImportDone {
		log.Printf("[SQL导入] 跳过: task=%s, 已经导入完成", task.ID)
		return
	}

	// 检查文件是否存在
	if _, err := os.Stat(sqlPath); os.IsNotExist(err) {
		task.setImportError(fmt.Sprintf("SQL文件不存在: %s", sqlPath))
		log.Printf("[SQL导入] 跳过: task=%s, 文件不存在: %s", task.ID, sqlPath)
		return
	}

	task.setImportStatus(CSVImportRunning, 0, 0)
	log.Printf("[SQL导入] 开始: task=%s, file=%s", task.ID, sqlPath)

	err := ImportSQLToTempDB(sqlPath, func(total, done int64) {
		pct := 0
		if total > 0 {
			pct = int(done * 100 / total)
		}
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		task.setImportProgress(pct, total, done)
	})

	if err != nil {
		task.setImportError(fmt.Sprintf("导入SQL失败: %v", err))
		log.Printf("[SQL导入] 失败: task=%s, error=%v", task.ID, err)
		return
	}

	task.setImportStatus(CSVImportDone, 100, 0)
	log.Printf("[SQL导入] 完成: task=%s, output=%s", task.ID, sqlPath)
}

// ========== 表结构 ==========

func getImportTableSchema(tableName string) string {
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

// ========== INSERT 匹配器 ==========

// insertMatcher 轻量 INSERT 匹配器
type insertMatcher struct{}

func (m *insertMatcher) MatchString(s string) bool {
	upper := strings.ToUpper(strings.TrimSpace(s))
	return strings.HasPrefix(upper, "INSERT")
}
