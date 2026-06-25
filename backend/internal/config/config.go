package config

import (
	"encoding/json"
	"os"
	"sync"

	"gps-archive-tool/internal/models"
)

var (
	appConfig models.AppConfig
	configMu  sync.RWMutex
	configPath string
)

// Init 初始化配置
func Init(cfgPath string) error {
	configPath = cfgPath
	appConfig = models.DefaultConfig()

	// 尝试从文件加载配置
	data, err := os.ReadFile(cfgPath)
	if err == nil {
		var cfg models.AppConfig
		if err := json.Unmarshal(data, &cfg); err == nil {
			configMu.Lock()
			appConfig = cfg
			configMu.Unlock()
		}
	}
	return nil
}

// Get 获取当前配置
func Get() models.AppConfig {
	configMu.RLock()
	defer configMu.RUnlock()
	return appConfig
}

// Update 更新配置
func Update(cfg models.AppConfig) error {
	configMu.Lock()
	defer configMu.Unlock()

	// 保留密码（如果新配置密码为空）
	if cfg.TempDB.Password == "" && appConfig.TempDB.Password != "" {
		cfg.TempDB.Password = appConfig.TempDB.Password
	}
	if cfg.VehicleDB.Password == "" && appConfig.VehicleDB.Password != "" {
		cfg.VehicleDB.Password = appConfig.VehicleDB.Password
	}

	appConfig = cfg
	return saveConfig()
}

// UpdatePartial 部分更新配置
func UpdatePartial(updates map[string]interface{}) error {
	configMu.Lock()
	defer configMu.Unlock()

	cfg := appConfig

	if v, ok := updates["temp_db"]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			applyDBUpdate(&cfg.TempDB, m)
		}
	}
	if v, ok := updates["vehicle_db"]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			applyDBUpdate(&cfg.VehicleDB, m)
		}
	}
	if v, ok := updates["bind_log_db"]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			applyBindLogDBUpdate(&cfg.BindLogDB, m)
		}
	}
	if v, ok := updates["wired_types"]; ok {
		if arr, ok := v.([]interface{}); ok {
			types := make([]string, len(arr))
			for i, item := range arr {
				types[i] = toString(item)
			}
			cfg.WiredTypes = types
		}
	}
	if v, ok := updates["timezone_offset"]; ok {
		cfg.TimezoneOffset = toInt(v)
	}
	if v, ok := updates["archive_dir"]; ok {
		cfg.ArchiveDir = toString(v)
	}
	if v, ok := updates["archive_file"]; ok {
		cfg.ArchiveFile = toString(v)
	}
	if v, ok := updates["output_dir"]; ok {
		cfg.OutputDir = toString(v)
	}
	if v, ok := updates["worker_count"]; ok {
		cfg.WorkerCount = toInt(v)
	}

	appConfig = cfg
	return saveConfig()
}

func applyDBUpdate(db *models.DBConfig, updates map[string]interface{}) {
	if v, ok := updates["host"]; ok {
		db.Host = toString(v)
	}
	if v, ok := updates["port"]; ok {
		db.Port = toInt(v)
	}
	if v, ok := updates["user"]; ok {
		db.User = toString(v)
	}
	if v, ok := updates["password"]; ok {
		db.Password = toString(v)
	}
	if v, ok := updates["db_name"]; ok {
		db.DBName = toString(v)
	}
}

func applyBindLogDBUpdate(cfg *models.BindLogConfig, updates map[string]interface{}) {
	if v, ok := updates["host"]; ok {
		cfg.Host = toString(v)
	}
	if v, ok := updates["port"]; ok {
		cfg.Port = toInt(v)
	}
	if v, ok := updates["user"]; ok {
		cfg.User = toString(v)
	}
	if v, ok := updates["password"]; ok {
		cfg.Password = toString(v)
	}
	if v, ok := updates["db_name"]; ok {
		cfg.DBName = toString(v)
	}
	if v, ok := updates["table"]; ok {
		cfg.Table = toString(v)
	}
	if v, ok := updates["sn_table"]; ok {
		cfg.SNTable = toString(v)
	}
	if v, ok := updates["timeout"]; ok {
		cfg.Timeout = toString(v)
	}
}

func saveConfig() error {
	data, err := json.MarshalIndent(appConfig, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0644)
}

func toString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}
