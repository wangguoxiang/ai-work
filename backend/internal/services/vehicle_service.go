package services

import (
	"fmt"
	"sync"

	"github.com/jmoiron/sqlx"

	"gps-archive-tool/internal/config"
	"gps-archive-tool/internal/database"
	"gps-archive-tool/internal/models"
)

// VehicleService 车辆查询服务
type VehicleService struct {
	db   *sqlx.DB
	dbMu sync.RWMutex
}

// NewVehicleService 创建车辆查询服务
func NewVehicleService() *VehicleService {
	return &VehicleService{}
}

// ensureConnected 确保数据库连接
func (s *VehicleService) ensureConnected() error {
	s.dbMu.RLock()
	if s.db != nil {
		s.dbMu.RUnlock()
		return nil
	}
	s.dbMu.RUnlock()

	s.dbMu.Lock()
	defer s.dbMu.Unlock()

	// 双重检查
	if s.db != nil {
		return nil
	}

	cfg := config.Get()
	var err error
	s.db, err = database.Connect(cfg.VehicleDB)
	if err != nil {
		return fmt.Errorf("连接车辆数据库失败: %w", err)
	}
	return nil
}

// Close 关闭连接
func (s *VehicleService) Close() {
	s.dbMu.Lock()
	defer s.dbMu.Unlock()
	if s.db != nil {
		s.db.Close()
		s.db = nil
	}
}

// QuerySingle 查询单个车辆
func (s *VehicleService) QuerySingle(vin, plateNo string) models.VehicleQueryResult {
	result := models.VehicleQueryResult{
		VIN:     vin,
		PlateNo: plateNo,
		Found:   false,
	}

	if err := s.ensureConnected(); err != nil {
		result.Error = err.Error()
		return result
	}

	var tid string
	var err error

	if vin != "" {
		tid, err = database.QueryTIDByVIN(s.db, vin)
	} else if plateNo != "" {
		tid, err = database.QueryTIDByPlateNo(s.db, plateNo)
	} else {
		result.Error = "请提供车架号或车牌号"
		return result
	}

	if err != nil {
		result.Error = fmt.Sprintf("未查询到设备绑定信息: %v", err)
		return result
	}

	result.TID = tid
	result.Found = true

	// 查询绑定历史
	records, err := database.QueryBindHistory(s.db, vin)
	if err == nil {
		result.BindHistory = records
	}

	return result
}

// QueryBatch 批量查询车辆
func (s *VehicleService) QueryBatch(vehicles []models.VehicleInfo) []models.VehicleQueryResult {
	var wg sync.WaitGroup
	results := make([]models.VehicleQueryResult, len(vehicles))

	for i, v := range vehicles {
		wg.Add(1)
		go func(idx int, info models.VehicleInfo) {
			defer wg.Done()
			results[idx] = s.QuerySingle(info.VIN, info.PlateNo)
		}(i, v)
	}

	wg.Wait()
	return results
}

// BatchQueryPlateNoByVINs 批量查询车牌号
func (s *VehicleService) BatchQueryPlateNoByVINs(vins []string) (map[string]string, error) {
	if len(vins) == 0 {
		return map[string]string{}, nil
	}

	if err := s.ensureConnected(); err != nil {
		return nil, err
	}

	// 去重
	vinSet := make(map[string]struct{}, len(vins))
	for _, v := range vins {
		if v != "" {
			vinSet[v] = struct{}{}
		}
	}
	if len(vinSet) == 0 {
		return map[string]string{}, nil
	}

	uniqueVins := make([]string, 0, len(vinSet))
	for v := range vinSet {
		uniqueVins = append(uniqueVins, v)
	}

	// 构建 IN 查询
	query := "SELECT vin, plate_no FROM vehicle_info WHERE vin IN (?"
	for i := 1; i < len(uniqueVins); i++ {
		query += ", ?"
	}
	query += ")"

	args := make([]interface{}, len(uniqueVins))
	for i, v := range uniqueVins {
		args[i] = v
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询车牌号失败: %w", err)
	}
	defer rows.Close()

	plateMap := make(map[string]string, len(uniqueVins))
	for rows.Next() {
		var vin, plateNo string
		if err := rows.Scan(&vin, &plateNo); err != nil {
			return nil, err
		}
		plateMap[vin] = plateNo
	}
	return plateMap, rows.Err()
}

// Reconnect 重新连接（配置变更后调用）
func (s *VehicleService) Reconnect() error {
	s.Close()
	return s.ensureConnected()
}
