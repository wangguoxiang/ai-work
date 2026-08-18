package services

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"gps-archive-tool/internal/config"
	"gps-archive-tool/internal/models"
)

// KongCheService 控车系统设备查询服务
// 从控车数据库(默认 positioning_travel.device 表)查询设备 SN 与 device id
type KongCheService struct {
	mu sync.RWMutex
	db *sql.DB
}

// NewKongCheService 创建控车设备查询服务
func NewKongCheService() *KongCheService {
	return &KongCheService{}
}

// ensureConnected 确保数据库连接
func (s *KongCheService) ensureConnected() error {
	s.mu.RLock()
	if s.db != nil {
		s.mu.RUnlock()
		return nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db != nil {
		return nil
	}

	cfg := config.Get()
	kc := cfg.KongCheDB
	timeout := kc.Timeout
	if timeout == "" {
		timeout = "10s"
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=true&loc=Local&timeout=%s&allowNativePasswords=true",
		kc.User, kc.Password, kc.Host, kc.Port, kc.DBName, timeout)

	var err error
	s.db, err = sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("打开控车数据库连接失败: %w", err)
	}
	s.db.SetMaxOpenConns(10)
	s.db.SetMaxIdleConns(2)
	s.db.SetConnMaxLifetime(5 * time.Minute)

	if err := s.db.Ping(); err != nil {
		s.db.Close()
		s.db = nil
		return fmt.Errorf("连接控车数据库失败: %w", err)
	}
	return nil
}

// Close 关闭连接
func (s *KongCheService) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		s.db.Close()
		s.db = nil
	}
}

// Reconnect 重新连接（配置变更后调用）
func (s *KongCheService) Reconnect() error {
	s.Close()
	return s.ensureConnected()
}

var kongCheIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// QueryDevices 查询控车设备(SN / device id 模糊匹配)
// sn / deviceID 任一为空则忽略对应条件;两者都为空时返回最近 limit 条
// 返回 (设备列表, 符合条件的总数, 错误)
func (s *KongCheService) QueryDevices(req models.KongCheQueryRequest) ([]models.KongCheDevice, int, error) {
	if err := s.ensureConnected(); err != nil {
		return nil, 0, err
	}

	cfg := config.Get()
	kc := cfg.KongCheDB

	table := kc.DeviceTable
	if table == "" {
		table = "device"
	}
	snCol := kc.SNCol
	if snCol == "" {
		snCol = "sn"
	}
	idCol := kc.DeviceIDCol
	if idCol == "" {
		idCol = "id"
	}

	if !kongCheIdentRe.MatchString(table) {
		return nil, 0, fmt.Errorf("非法设备表名: %s", table)
	}
	if !kongCheIdentRe.MatchString(snCol) {
		return nil, 0, fmt.Errorf("非法SN列名: %s", snCol)
	}
	if !kongCheIdentRe.MatchString(idCol) {
		return nil, 0, fmt.Errorf("非法设备ID列名: %s", idCol)
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}

	var conds []string
	var args []interface{}
	if strings.TrimSpace(req.SN) != "" {
		conds = append(conds, snCol+" LIKE ?")
		args = append(args, "%"+strings.TrimSpace(req.SN)+"%")
	}
	if strings.TrimSpace(req.DeviceID) != "" {
		conds = append(conds, idCol+" LIKE ?")
		args = append(args, "%"+strings.TrimSpace(req.DeviceID)+"%")
	}

	// 先统计总数
	countQ := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
	if len(conds) > 0 {
		countQ += " WHERE " + strings.Join(conds, " AND ")
	}
	var total int
	if err := s.db.QueryRow(countQ, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计控车设备数量失败: %w", err)
	}

	q := fmt.Sprintf("SELECT %s, %s FROM %s", idCol, snCol, table)
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY " + idCol + " LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询控车设备失败: %w", err)
	}
	defer rows.Close()

	var res []models.KongCheDevice
	for rows.Next() {
		var d models.KongCheDevice
		if err := rows.Scan(&d.DeviceID, &d.SN); err != nil {
			return nil, 0, err
		}
		res = append(res, d)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return res, total, nil
}
