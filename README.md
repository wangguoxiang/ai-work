# GPS归档数据过滤工具

## 📋 项目概述

从MySQL归档数据文件（SQL dump）中，根据TID设备号过滤GPS数据，导入临时MySQL数据库，再按TID分别导出为独立的SQL文件。

### 核心功能

1. **车辆TID查询** - 输入车架号(VIN)或车牌号，查询车辆绑定的TID设备号和绑定历史
2. **数据过滤** - 从归档文件中过滤指定TID和时间范围的GPS数据
3. **临时入库** - 过滤后的数据导入临时MySQL数据库
4. **导出拆分** - 按TID从临时库导出为独立的SQL文件（文件名: `{TID}.sql`）

## 🏗️ 项目结构

```
d:\code\ai-work\
├── backend/                    # Go后端服务
│   ├── cmd/server/main.go      # 入口文件
│   ├── internal/
│   │   ├── config/config.go    # 配置管理
│   │   ├── database/mysql.go   # MySQL数据库操作
│   │   ├── handlers/handlers.go# HTTP API处理器
│   │   ├── models/models.go    # 数据模型
│   │   └── services/
│   │       ├── archive_service.go  # 归档文件处理
│   │       ├── task_manager.go     # 任务管理
│   │       └── vehicle_service.go  # 车辆查询服务
│   ├── go.mod
│   └── config.json             # 运行时配置文件（自动生成）
├── frontend/                   # React前端
│   ├── src/
│   │   ├── components/
│   │   │   ├── VehicleQuery.tsx # 车辆查询页面
│   │   │   ├── FilterTask.tsx   # 过滤任务页面
│   │   │   ├── ConfigPanel.tsx  # 配置管理页面
│   │   │   └── TaskList.tsx     # 任务列表页面
│   │   ├── api/index.ts        # API封装
│   │   ├── App.tsx             # 主应用
│   │   └── main.tsx            # 入口
│   └── package.json
└── README.md
```

## 🚀 快速开始

### 1. 环境要求

- **Go** 1.21+
- **Node.js** 18+
- **MySQL** 5.7+（需要两个数据库：临时库 + 车辆信息库）

### 2. 启动后端

```bash
# 进入后端目录
cd backend

# 安装Go依赖
go mod tidy

# 编译运行
go run cmd/server/main.go

# 或者编译为二进制
go build -o gps-filter.exe cmd/server/main.go
./gps-filter.exe
```

后端默认运行在 **http://localhost:8080**

### 3. 启动前端

```bash
# 进入前端目录
cd frontend

# 安装依赖
npm install

# 开发模式运行
npm run dev

# 或者构建生产版本
npm run build
```

前端开发模式运行在 **http://localhost:3000**（已配置代理到后端8080端口）

## 📡 API 接口

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/health | 健康检查 |
| GET | /api/config | 获取配置 |
| PUT | /api/config | 更新配置（部分） |
| POST | /api/config | 保存完整配置 |
| POST | /api/vehicle/query | 查询单个车辆TID |
| POST | /api/vehicle/batch-query | 批量查询车辆TID |
| POST | /api/vehicle/tid-history | 查询TID绑定历史 |
| GET | /api/archive/files | 列出归档文件 |
| POST | /api/filter/start | 启动过滤任务 |
| GET | /api/filter/task/:taskId | 获取任务状态 |
| GET | /api/filter/tasks | 列出所有任务 |
| DELETE | /api/filter/task/:taskId | 删除任务 |
| GET | /api/cos/files?base_dir= | 列出COS文件（可指定目录前缀） |
| POST | /api/pipeline/create | 创建管道任务（下载→过滤→导入MySQL） |
| GET | /api/pipeline/tasks | 列出管道任务 |
| POST | /api/bindlog/query | 查询设备绑定流水 |
| POST | /api/kongche/query | 控车设备查询（SN/device id，支持分页 limit/offset） |
| GET | /api/kongche/export | 控车设备导出CSV（?sn=&device_id=） |
| POST | /api/kongche/import-csv | 导入控车 device id CSV |
| POST | /api/kongche/pipeline/create | 创建控车管道任务（按device id过滤） |

## ⚙️ 配置说明

### 数据库表结构

**车辆信息库需包含以下表：**

```sql
-- 车辆设备绑定表
CREATE TABLE vehicle_device_bind (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tid VARCHAR(64) NOT NULL COMMENT 'TID设备号',
    vin VARCHAR(32) NOT NULL COMMENT '车架号',
    bind_time DATETIME NOT NULL COMMENT '绑定时间',
    unbind_time DATETIME COMMENT '解绑时间',
    is_current TINYINT(1) DEFAULT 1 COMMENT '是否当前绑定',
    INDEX idx_vin (vin),
    INDEX idx_tid (tid)
);

-- 车辆信息表
CREATE TABLE vehicle_info (
    vin VARCHAR(32) PRIMARY KEY COMMENT '车架号',
    plate_no VARCHAR(32) COMMENT '车牌号',
    brand VARCHAR(64) COMMENT '品牌',
    model VARCHAR(64) COMMENT '型号',
    INDEX idx_plate_no (plate_no)
);
```

**临时数据库表（自动创建）：**

```sql
CREATE TABLE gps_archive_data (
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
);
```

### 归档文件格式

支持标准的MySQL SQL dump文件（含INSERT语句），以及CSV/TXT格式文件。
SQL文件中需包含 `tid` 或 `device_id` 列用于TID过滤。

## 🔧 配置项

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| temp_db | 临时MySQL数据库配置 | localhost:3306 |
| vehicle_db | 车辆信息数据库配置 | localhost:3306 |
| archive_dir | 归档数据文件目录 | ./archive |
| output_dir | 过滤结果输出目录 | ./output |
| worker_count | 并行处理线程数 | 4 |

## 📝 使用流程

1. **配置数据库** → 在"系统配置"页面设置临时数据库和车辆数据库连接信息
2. **查询车辆** → 在"车辆查询"页面输入车架号/车牌号，获取TID设备号
3. **启动过滤** → 在"过滤任务"页面输入TID列表、时间范围，点击启动
4. **查看结果** → 在"任务列表"页面查看进度，完成后在输出目录获取SQL文件
