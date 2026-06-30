package services

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"gps-archive-tool/internal/config"
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

	// 检查 SQL 文件是否存在
	if _, err := os.Stat(sqlPath); os.IsNotExist(err) {
		return fmt.Errorf("SQL文件不存在: %s", sqlPath)
	}

	// 通过 mysql 命令行客户端执行 source 导入
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

	// 统计 INSERT 行数用于更有意义的进度展示
	totalLines := countInsertLines(sqlPath)
	log.Printf("[SQL导入] 统计: %s, INSERT语句数=%d", sqlPath, totalLines)

	// 初始化进度（总行数）
	pct := 0
	task.setImportProgress(pct, totalLines, 0)

	err := ImportSQLToTempDB(sqlPath, func(total, done int64) {
		// total/done 是文件字节数，这里用总行数换算进度
		p := 0
		if total > 0 {
			p = int(done * 100 / total)
		}
		if p < 0 {
			p = 0
		}
		if p > 100 {
			p = 100
		}
		// 换算成按行数的进度
		lineDone := totalLines * int64(p) / 100
		task.setImportProgress(p, totalLines, lineDone)
	})

	if err != nil {
		task.setImportError(fmt.Sprintf("导入SQL失败: %v", err))
		log.Printf("[SQL导入] 失败: task=%s, error=%v", task.ID, err)
		return
	}

	task.setImportDone(totalLines)
	log.Printf("[SQL导入] 完成: task=%s, output=%s, 已导入 %d 条INSERT", task.ID, sqlPath, totalLines)
}

// ========== 表结构 ==========

// countInsertLines 统计 SQL 文件中 INSERT 语句的行数（用于进度展示）
func countInsertLines(path string) int64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 1024*1024)
	// 使用 4MB max line size 以支持超长 INSERT
	scanner.Buffer(buf, 4*1024*1024)

	var count int64
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(strings.ToUpper(line), "INSERT") {
			count++
		}
	}
	return count
}

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
