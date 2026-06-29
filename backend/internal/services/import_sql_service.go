package services

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"

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
// 通过 mysql 命令行客户端执行 source 命令来导入，避免 Go 解析 SQL 的兼容性问题
func ImportSQLToTempDB(sqlPath string, progressFn ImportProgressFn) error {
	cfg := config.Get()

	// 1. 确保目标表存在（通过 database/sql 创建）
	tableName := cfg.TempDB.Table
	if tableName == "" {
		tableName = "gps_archive_data"
	}
	func() {
		db, err := database.Connect(cfg.TempDB)
		if err != nil {
			log.Printf("[SQL导入] 连接临时数据库失败(跳过建表): %v", err)
			return
		}
		defer db.Close()

		// 从SQL文件中动态检测 INSERT 列结构，创建匹配的临时表
		columns, valueCount, detectErr := detectInsertColumns(sqlPath)
		if detectErr == nil && (len(columns) > 0 || valueCount > 0) {
			schema := buildDynamicTableSchema(tableName, columns, valueCount)
			log.Printf("[SQL导入] 动态检测到 %d 列，创建临时表: %s", len(columns), tableName)
			if err := database.EnsureTempTableReplace(db, tableName, schema); err != nil {
				log.Printf("[SQL导入] 动态建表失败(回退): %v", err)
				schema = getImportTableSchema(tableName)
				database.EnsureTempTableReplace(db, tableName, schema)
			}
		} else {
			log.Printf("[SQL导入] 未检测到INSERT列结构(回退默认schema): %v", detectErr)
			schema := getImportTableSchema(tableName)
			if err := database.EnsureTempTableReplace(db, tableName, schema); err != nil {
				log.Printf("[SQL导入] 创建临时表失败(跳过): %v", err)
			}
		}
	}()

	// 2. 检查 SQL 文件是否存在
	if _, err := os.Stat(sqlPath); os.IsNotExist(err) {
		return fmt.Errorf("SQL文件不存在: %s", sqlPath)
	}

	// 3. 通过 mysql 命令行客户端执行导入
	log.Printf("[SQL导入] 开始通过 mysql CLI source 导入: %s", sqlPath)

	// 获取文件大小用于进度报告
	fileInfo, _ := os.Stat(sqlPath)
	fileSize := int64(0)
	if fileInfo != nil {
		fileSize = fileInfo.Size()
	}

	// 报告初始进度
	if progressFn != nil {
		progressFn(fileSize, 0)
	}

	// 构造 mysql 命令
	args := []string{
		"-h", cfg.TempDB.Host,
		"-P", fmt.Sprintf("%d", cfg.TempDB.Port),
		"-u", cfg.TempDB.User,
		fmt.Sprintf("-p%s", cfg.TempDB.Password),
		cfg.TempDB.DBName,
	}

	cmd := exec.Command("mysql", args...)

	f, err := os.Open(sqlPath)
	if err != nil {
		return fmt.Errorf("打开SQL文件失败: %w", err)
	}
	defer f.Close()
	cmd.Stdin = f

	// 捕获 stdout+stderr
	output, err := cmd.CombinedOutput()
	if err != nil {
		errMsg := strings.TrimSpace(string(output))
		if errMsg == "" {
			errMsg = err.Error()
		}
		return fmt.Errorf("mysql source 导入失败: %s", errMsg)
	}

	// 报告完成进度
	if progressFn != nil {
		progressFn(fileSize, fileSize)
	}

	outputStr := strings.TrimSpace(string(output))
	if outputStr != "" && len(outputStr) < 1024 {
		log.Printf("[SQL导入] mysql 输出: %s", outputStr)
	} else if outputStr != "" {
		log.Printf("[SQL导入] mysql 输出: %d bytes", len(outputStr))
	}

	log.Printf("[SQL导入] source 导入完成: 文件=%s, 大小=%d bytes", sqlPath, fileSize)
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

// ========== 动态列检测 ==========

// detectInsertColumns 从SQL文件中检测第一条INSERT语句的列结构
// 返回列名列表和值数量（至少一个有效）
func detectInsertColumns(sqlPath string) (columns []string, valueCount int, err error) {
	f, err := os.Open(sqlPath)
	if err != nil {
		return nil, 0, fmt.Errorf("打开SQL文件失败: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	// 匹配 INSERT INTO `table` 开头
	insertPrefixRe := regexp.MustCompile(`(?i)^\s*INSERT\s+INTO\s+`)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "--") || strings.HasPrefix(line, "/*") || strings.HasPrefix(line, "#") {
			continue
		}
		if !insertPrefixRe.MatchString(line) {
			continue
		}

		// 尝试匹配带列名的格式: INSERT INTO `table` (col1, col2, ...) VALUES ...
		colRe := regexp.MustCompile(`(?i)INSERT\s+INTO\s+` + "`?\\w+`?" + `\s*\(([^)]+)\)\s*VALUES`)
		colMatches := colRe.FindStringSubmatch(line)
		if len(colMatches) >= 2 {
			cols := parseColumnsSimple(colMatches[1])
			if len(cols) > 0 {
				return cols, len(cols), nil
			}
		}

		// 没有列名，从第一个 VALUES tuple 中计算字段数
		upper := strings.ToUpper(line)
		valuesIdx := strings.Index(upper, "VALUES")
		if valuesIdx < 0 {
			continue
		}
		valuesPart := strings.TrimSpace(line[valuesIdx+len("VALUES"):])
		// 找到第一个 (...) tuple
		parenStart := strings.Index(valuesPart, "(")
		if parenStart < 0 {
			continue
		}
		tuple := valuesPart[parenStart:]
		parenEnd := strings.Index(tuple, ")")
		if parenEnd < 0 {
			continue
		}
		tupleContent := tuple[1:parenEnd]
		count := countValuesInTuple(tupleContent)
		if count > 0 {
			return nil, count, nil
		}
		return nil, 0, fmt.Errorf("INSERT语句中无有效值")
	}

	if err := scanner.Err(); err != nil {
		return nil, 0, fmt.Errorf("读取SQL文件失败: %w", err)
	}
	return nil, 0, fmt.Errorf("SQL文件中未找到INSERT语句")
}

// parseColumnsSimple 简单解析列名列表（逗号分隔，去除反引号和空格）
func parseColumnsSimple(colStr string) []string {
	parts := strings.Split(colStr, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, "`\"' ")
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// countValuesInTuple 计算 tuple 中逗号分隔的值的数量（考虑字符串中的逗号和括号）
func countValuesInTuple(tuple string) int {
	count := 0
	inStr := false
	strChar := byte(0)
	depth := 0

	for i := 0; i < len(tuple); i++ {
		ch := tuple[i]

		if inStr {
			if ch == '\\' && i+1 < len(tuple) {
				i++
			} else if ch == strChar {
				inStr = false
			}
			continue
		}

		switch ch {
		case '\'', '"':
			inStr = true
			strChar = ch
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				count++
			}
		}
	}
	return count + 1 // 逗号数+1 = 字段数
}

// buildDynamicTableSchema 根据检测到的列信息动态构建 CREATE TABLE 语句
func buildDynamicTableSchema(tableName string, columns []string, valueCount int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE TABLE IF NOT EXISTS `%s` (\n", tableName))
	sb.WriteString("  `_row_id` BIGINT AUTO_INCREMENT PRIMARY KEY,\n")

	if len(columns) > 0 {
		for i, col := range columns {
			colType := getDynamicColumnType(col)
			sb.WriteString(fmt.Sprintf("  `%s` %s", col, colType))
			if col == "gps_time" || col == "create_time" || col == "upload_time" {
				// 时间列加索引
				sb.WriteString(fmt.Sprintf(",\n  INDEX idx_%s (`%s`)", col, col))
			}
			if i < len(columns)-1 {
				sb.WriteString(",\n")
			}
		}
	} else {
		for i := 0; i < valueCount; i++ {
			sb.WriteString(fmt.Sprintf("  `col_%d` TEXT", i+1))
			if i < valueCount-1 {
				sb.WriteString(",\n")
			}
		}
	}
	sb.WriteString("\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4")
	return sb.String()
}

// getDynamicColumnType 根据列名返回合适的 MySQL 数据类型
func getDynamicColumnType(colName string) string {
	switch strings.ToLower(colName) {
	case "id":
		return "BIGINT"
	case "tid", "device_id", "terminal_id", "deviceid", "terminalid":
		return "VARCHAR(64)"
	case "gps_time", "gpstime", "create_time", "createtime", "upload_time", "uploadtime", "record_time", "recordtime":
		return "DATETIME"
	case "longitude", "lng":
		return "DECIMAL(12,8)"
	case "latitude", "lat":
		return "DECIMAL(12,8)"
	case "speed":
		return "DECIMAL(8,2)"
	case "direction":
		return "INT"
	case "status":
		return "VARCHAR(255)"
	default:
		return "TEXT"
	}
}
