package database

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"

	"gps-archive-tool/internal/models"
)

// Connect 连接数据库
func Connect(cfg models.DBConfig) (*sqlx.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local&timeout=10s",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.DBName)

	db, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}

	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	return db, nil
}

// QueryTIDByVIN 根据车架号查询TID
func QueryTIDByVIN(db *sqlx.DB, vin string) (string, error) {
	var tid string
	// 根据实际表结构调整SQL
	query := `SELECT tid FROM vehicle_device_bind WHERE vin = ? AND is_current = 1 LIMIT 1`
	err := db.Get(&tid, query, vin)
	if err != nil {
		return "", err
	}
	return tid, nil
}

// QueryTIDByPlateNo 根据车牌号查询TID
func QueryTIDByPlateNo(db *sqlx.DB, plateNo string) (string, error) {
	var tid string
	// 先通过车牌号查VIN，再查TID
	query := `SELECT b.tid FROM vehicle_device_bind b 
	          JOIN vehicle_info v ON b.vin = v.vin 
	          WHERE v.plate_no = ? AND b.is_current = 1 LIMIT 1`
	err := db.Get(&tid, query, plateNo)
	if err != nil {
		return "", err
	}
	return tid, nil
}

// QueryBindHistory 查询TID与车辆的绑定历史
func QueryBindHistory(db *sqlx.DB, vin string) ([]models.BindRecord, error) {
	var records []models.BindRecord
	query := `SELECT tid, vin, 
	          COALESCE((SELECT plate_no FROM vehicle_info WHERE vin = b.vin), '') as plate_no,
	          DATE_FORMAT(bind_time, '%Y-%m-%d %H:%i:%s') as bind_time,
	          COALESCE(DATE_FORMAT(unbind_time, '%Y-%m-%d %H:%i:%s'), '') as unbind_time,
	              is_current 
	          FROM vehicle_device_bind b 
	          WHERE vin = ? 
	          ORDER BY bind_time DESC`
	err := db.Select(&records, query, vin)
	if err != nil {
		return nil, err
	}
	return records, nil
}

// QueryBindHistoryByTID 根据TID查询绑定历史
func QueryBindHistoryByTID(db *sqlx.DB, tid string) ([]models.BindRecord, error) {
	var records []models.BindRecord
	query := `SELECT tid, vin, 
	          COALESCE((SELECT plate_no FROM vehicle_info WHERE vin = b.vin), '') as plate_no,
	          DATE_FORMAT(bind_time, '%Y-%m-%d %H:%i:%s') as bind_time,
	          COALESCE(DATE_FORMAT(unbind_time, '%Y-%m-%d %H:%i:%s'), '') as unbind_time,
	          is_current 
	          FROM vehicle_device_bind b 
	          WHERE tid = ? 
	          ORDER BY bind_time DESC`
	err := db.Select(&records, query, tid)
	if err != nil {
		return nil, err
	}
	return records, nil
}

// EnsureTempTable 确保临时数据库中存在目标表
func EnsureTempTable(db *sqlx.DB, tableName string, schema string) error {
	// 检查表是否存在
	var count int
	err := db.Get(&count, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", tableName)
	if err != nil {
		return fmt.Errorf("检查表存在失败: %w", err)
	}

	if count == 0 {
		_, err = db.Exec(schema)
		if err != nil {
			return fmt.Errorf("创建表失败: %w", err)
		}
	}
	return nil
}

// EnsureTempTableReplace 删除并重新创建临时表（确保表结构与指定的 schema 一致）
func EnsureTempTableReplace(db *sqlx.DB, tableName string, schema string) error {
	_, err := db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS `%s`", tableName))
	if err != nil {
		return fmt.Errorf("删除旧表失败: %w", err)
	}
	_, err = db.Exec(schema)
	if err != nil {
		return fmt.Errorf("创建表失败: %w", err)
	}
	return nil
}

// BatchInsert 批量插入数据
func BatchInsert(db *sqlx.DB, tableName string, columns []string, data [][]interface{}) error {
	if len(data) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var stmt *sql.Stmt
	if len(columns) > 0 {
		// 有列名：INSERT INTO `table` (col1, col2) VALUES (?, ?)
		colStr := ""
		for i, col := range columns {
			if i > 0 {
				colStr += ", "
			}
			colStr += "`" + col + "`"
		}

		placeholders := ""
		for i := 0; i < len(columns); i++ {
			if i > 0 {
				placeholders += ", "
			}
			placeholders += "?"
		}

		stmt, err = tx.Prepare(fmt.Sprintf("INSERT INTO `%s` (%s) VALUES (%s)", tableName, colStr, placeholders))
	} else {
		// 无列名：INSERT INTO `table` VALUES (?, ?, ?)  — 从第一条数据推断列数
		colCount := len(data[0])
		if colCount == 0 {
			return fmt.Errorf("数据列数为0")
		}
		placeholders := ""
		for i := 0; i < colCount; i++ {
			if i > 0 {
				placeholders += ", "
			}
			placeholders += "?"
		}
		stmt, err = tx.Prepare(fmt.Sprintf("INSERT INTO `%s` VALUES (%s)", tableName, placeholders))
	}
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, row := range data {
		_, err = stmt.Exec(row...)
		if err != nil {
			return fmt.Errorf("插入数据失败: %w", err)
		}
	}

	return tx.Commit()
}
