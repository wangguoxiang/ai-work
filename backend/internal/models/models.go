package models

import "fmt"

// VehicleInfo 车辆信息查询请求
type VehicleInfo struct {
	VIN      string `json:"vin" form:"vin"`
	PlateNo  string `json:"plate_no" form:"plate_no"`
}

// VehicleQueryResult 车辆查询结果
type VehicleQueryResult struct {
	VIN        string          `json:"vin"`
	PlateNo    string          `json:"plate_no"`
	TID        string          `json:"tid"`
	Found      bool            `json:"found"`
	BindHistory []BindRecord   `json:"bind_history"`
	Error      string          `json:"error,omitempty"`
}

// BindRecord TID设备与车辆绑定记录
type BindRecord struct {
	TID        string `json:"tid"`
	VIN        string `json:"vin"`
	PlateNo    string `json:"plate_no"`
	BindTime   string `json:"bind_time"`
	UnbindTime string `json:"unbind_time,omitempty"`
	IsCurrent  bool   `json:"is_current"`
}

// BatchVehicleQueryRequest 批量查询请求
type BatchVehicleQueryRequest struct {
	Vehicles []VehicleInfo `json:"vehicles" binding:"required"`
}

// FilterTaskRequest 过滤任务请求
type FilterTaskRequest struct {
	TIDs          []string `json:"tids" binding:"required"`
	StartTime     string   `json:"start_time" binding:"required"`
	EndTime       string   `json:"end_time" binding:"required"`
	ArchiveDir    string   `json:"archive_dir" binding:"required"`
	ArchiveFile   string   `json:"archive_file"`
	OutputDir     string   `json:"output_dir" binding:"required"`
	WorkerCount   int      `json:"worker_count"`
}

// TaskStatus 任务状态
type TaskStatus struct {
	TaskID        string   `json:"task_id"`
	Status        string   `json:"status"` // running, completed, failed
	Progress      float64  `json:"progress"`
	TotalFiles    int      `json:"total_files"`
	ProcessedFiles int     `json:"processed_files"`
	TotalRecords  int64    `json:"total_records"`
	FilteredRecords int64  `json:"filtered_records"`
	ExportedRecords int64  `json:"exported_records"`
	CurrentFile   string   `json:"current_file"`
	TIDs          []string `json:"tids"`
	StartTime     string   `json:"start_time"`
	EndTime       string   `json:"end_time"`
	Error         string   `json:"error,omitempty"`
	StartAt       string   `json:"start_at"`
	Elapsed       string   `json:"elapsed"`
}

// AppConfig 应用配置
type AppConfig struct {
	TempDB        DBConfig      `json:"temp_db"`
	VehicleDB     DBConfig      `json:"vehicle_db"`
	BindLogDB     BindLogConfig `json:"bind_log_db"`
	ArchiveDir    string        `json:"archive_dir"`
	OutputDir     string        `json:"output_dir"`
	WorkerCount   int           `json:"worker_count"`
	ArchiveFile   string        `json:"archive_file"`
	WiredTypes    []string      `json:"wired_types"`
	TimezoneOffset int          `json:"timezone_offset"`
}

// DBConfig 数据库配置
type DBConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	DBName   string `json:"db_name"`
}

// DSN 生成MySQL连接字符串
func (c DBConfig) DSN() string {
	portStr := fmt.Sprintf("%d", c.Port)
	return c.User + ":" + c.Password + "@tcp(" + c.Host + ":" + portStr + ")/" + c.DBName + "?charset=utf8mb4&parseTime=True&loc=Local"
}

// DSN 生成 BindLog MySQL 连接字符串
func (c BindLogConfig) DSN() string {
	timeout := c.Timeout
	if timeout == "" {
		timeout = "10s"
	}
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=true&loc=Local&timeout=%s&allowNativePasswords=true",
		c.User, c.Password, c.Host, c.Port, c.DBName, timeout)
}

// DefaultConfig 默认配置
func DefaultConfig() AppConfig {
	return AppConfig{
		TempDB: DBConfig{
			Host:     "127.0.0.1",
			Port:     3306,
			User:     "root",
			Password: "",
			DBName:   "gps_temp",
		},
		VehicleDB: DBConfig{
			Host:     "127.0.0.1",
			Port:     3306,
			User:     "root",
			Password: "",
			DBName:   "vehicle_db",
		},
		BindLogDB: BindLogConfig{
			Host:     "127.0.0.1",
			Port:     3306,
			User:     "root",
			Password: "",
			DBName:   "qjcg",
			Table:    "t_bind_log",
			SNTable:  "t_sn",
			Timeout:  "10s",
		},
		ArchiveDir:     "./archive",
		OutputDir:      "./output",
		WorkerCount:    4,
		TimezoneOffset: 8,
		WiredTypes: []string{
			"ZJ210W", "ZJ210", "ZJ220", "ZJ220S", "ZJ220R",
			"ZJ220F(R)", "ZJ220F", "ZJ220F(D)", "IV100", "ZJ210B",
		},
	}
}

// ============ BindLog 设备绑定流水查询 ============

// BindLogConfig t_bind_log 数据库/表配置
type BindLogConfig struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	User     string   `json:"user"`
	Password string   `json:"password"`
	DBName   string   `json:"db_name"`
	Table    string   `json:"table"`
	SNTable  string   `json:"sn_table"`
	Timeout  string   `json:"timeout"`
}

// BindLogRequest 绑定日志查询请求
type BindLogRequest struct {
	Vins  []string `json:"vins"`
	Start string   `json:"start"`
	End   string   `json:"end"`
}

// BindSegment 绑定段查询结果
type BindSegment struct {
	TID        string `json:"tid"`
	SN         string `json:"sn"`
	VIN        string `json:"vin"`
	CNUM       string `json:"cnum"`
	BindTime   string `json:"bind_time"`
	UnbindTime string `json:"unbind_time,omitempty"`
	BindTS     int64  `json:"bind_ts"`
	UnbindTS   int64  `json:"unbind_ts,omitempty"`
	SNType     string `json:"sn_type"`
	IsWired    bool   `json:"is_wired"`
}

// ArchiveFileInfo 归档文件信息
type ArchiveFileInfo struct {
	FileName string `json:"file_name"`
	FilePath string `json:"file_path"`
	FileSize int64  `json:"file_size"`
}
